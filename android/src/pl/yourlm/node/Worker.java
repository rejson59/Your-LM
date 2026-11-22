package pl.yourlm.node;

import android.app.ActivityManager;
import android.app.ApplicationExitInfo;
import android.content.Context;
import android.content.SharedPreferences;
import android.net.ConnectivityManager;
import android.net.NetworkCapabilities;
import android.net.wifi.WifiInfo;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.security.keystore.*;
import java.io.*;
import java.math.BigInteger;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.security.cert.Certificate;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicLong;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import javax.net.ssl.*;
import javax.security.auth.x500.X500Principal;
import org.json.JSONObject;

/**
 * The experimental llama.cpp RPC protocol stays on loopback. Wi-Fi only sees
 * the authenticated TLS bridge. Manual pairing proves a 100-bit key with HMAC
 * and pins the TLS certificate; the key itself is never sent over the LAN.
 */
final class Worker implements Closeable {

  interface StateListener {
    void onWorkerStateChanged(Worker worker);
  }

  private static final String MANUAL_ALPHABET =
    "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  final Context ctx;
  final Set<Socket> active = Collections.newSetFromMap(
    new ConcurrentHashMap<Socket, Boolean>()
  );
  final ThreadPoolExecutor clients = new ThreadPoolExecutor(
    0,
    16,
    30,
    TimeUnit.SECONDS,
    new SynchronousQueue<Runnable>()
  );
  final String manualKey;
  final String secret;
  final String alias = "YourLM-session";
  final SharedPreferences preferences;
  final StateListener stateListener;
  final File logFile;
  final File eventFile;
  final File snapshotFile;
  final Object eventLock = new Object();
  final AtomicLong bridgeConnections = new AtomicLong();
  final AtomicLong bridgeHostToNativeBytes = new AtomicLong();
  final AtomicLong bridgeNativeToHostBytes = new AtomicLong();
  File engineFile;
  Process rpc;
  SSLServerSocket server;
  ServerSocket pairing;
  String code, pairAddress, pin;
  int rpcPort;
  volatile int rpcPid;
  volatile int rpcExitCode = Integer.MIN_VALUE;
  volatile int recoveryTriggerExitCode = Integer.MIN_VALUE;
  volatile long rpcStartedAt;
  volatile long rpcStoppedAt;
  volatile long recoveryTriggerStoppedAt;
  volatile String recoveryTriggerNativeLog = "";
  volatile long lastForwardingErrorAt;
  volatile long lastSnapshotPersistedAt;
  volatile String lastForwardingError = "";
  volatile String recoveryError = "";
  volatile boolean recovering;
  volatile int restartAttempts;
  volatile int restartCount;
  volatile boolean closed;

  private static final String WORKER_BUILD = "b12562-ylm-rpc4";

  Worker(Context c) {
    this(c, null);
  }

  Worker(Context c, StateListener listener) {
    ctx = c;
    stateListener = listener;
    preferences = c.getSharedPreferences("yourlm-worker", Context.MODE_PRIVATE);
    String storedKey = preferences.getString("manual_key", "");
    if (!storedKey.matches("[ABCDEFGHJKLMNPQRSTUVWXYZ2-9]{5}(?:-[ABCDEFGHJKLMNPQRSTUVWXYZ2-9]{5}){3}")) {
      storedKey = createManualKey();
      preferences.edit().putString("manual_key", storedKey).apply();
    }
    manualKey = storedKey;
    secret = derivedSecret(manualKey.replace("-", ""));
    File diagnostics = new File(c.getFilesDir(), "diagnostics");
    diagnostics.mkdirs();
    logFile = new File(diagnostics, "worker-native.log");
    eventFile = new File(diagnostics, "worker-events.log");
    snapshotFile = new File(diagnostics, "worker-latest.json");
  }

  private void notifyStateChanged() {
    if (stateListener != null) stateListener.onWorkerStateChanged(this);
  }

  boolean nativeAlive() {
    Process process = rpc;
    return process != null && process.isAlive();
  }

  static byte[] random(int n) {
    byte[] b = new byte[n];
    new SecureRandom().nextBytes(b);
    return b;
  }

  static String hex(byte[] bytes) {
    StringBuilder s = new StringBuilder();
    for (byte b : bytes) s.append(String.format(Locale.ROOT, "%02x", b));
    return s.toString();
  }

  private static String createManualKey() {
    byte[] entropy = random(20);
    StringBuilder raw = new StringBuilder(20);
    for (byte b : entropy) raw.append(
      MANUAL_ALPHABET.charAt((b & 0xff) & 31)
    );
    return (
      raw.substring(0, 5) +
      "-" +
      raw.substring(5, 10) +
      "-" +
      raw.substring(10, 15) +
      "-" +
      raw.substring(15, 20)
    );
  }

  private static String derivedSecret(String key) {
    try {
      byte[] digest = MessageDigest.getInstance("SHA-256").digest(
        ("YourLM-auth-v2|" + key).getBytes(StandardCharsets.US_ASCII)
      );
      return hex(Arrays.copyOf(digest, 24));
    } catch (GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }

  static InetAddress lan() throws Exception {
    for (NetworkInterface nic : Collections.list(
      NetworkInterface.getNetworkInterfaces()
    )) {
      if (!nic.isUp() || !nic.getName().startsWith("wlan")) continue;
      for (InetAddress a : Collections.list(nic.getInetAddresses())) if (
        a instanceof Inet4Address && a.isSiteLocalAddress()
      ) return a;
    }
    throw new IOException(
      "Połącz oba urządzenia z tą samą prywatną siecią Wi-Fi. " +
      "Nie używaj sieci gościnnej ani danych komórkowych."
    );
  }

  ActivityManager.MemoryInfo mem() {
    ActivityManager.MemoryInfo m = new ActivityManager.MemoryInfo();
    (
      (ActivityManager) ctx.getSystemService(Context.ACTIVITY_SERVICE)
    ).getMemoryInfo(m);
    return m;
  }

  int findProcessId() {
    if (engineFile == null) return 0;
    File[] entries = new File("/proc").listFiles();
    if (entries == null) return 0;
    String executable = engineFile.getPath();
    byte[] data = new byte[4096];
    int newest = 0;
    for (File entry : entries) {
      String name = entry.getName();
      if (!name.matches("[0-9]+")) continue;
      try (InputStream input = new FileInputStream(new File(entry, "cmdline"))) {
        int count = input.read(data);
        if (count <= 0) continue;
        int end = 0;
        while (end < count && data[end] != 0) end++;
        String command = new String(data, 0, end, StandardCharsets.UTF_8);
        if (command.equals(executable) || command.endsWith("/libylm_rpc.so")) {
          newest = Math.max(newest, Integer.parseInt(name));
        }
      } catch (Exception ignored) {}
    }
    return newest;
  }

  private static long processMemoryBytes(int pid, String file, String field) {
    if (pid <= 0) return 0;
    try (
      BufferedReader reader = new BufferedReader(
        new FileReader(new File("/proc/" + pid + "/" + file))
      )
    ) {
      String prefix = field + ":";
      String line;
      while ((line = reader.readLine()) != null) {
        if (!line.startsWith(prefix)) continue;
        String[] fields = line.trim().split("\\s+");
        return fields.length >= 2 ? Long.parseLong(fields[1]) * 1024L : 0;
      }
    } catch (Exception ignored) {}
    return 0;
  }

  private static long processStatusBytes(int pid, String field) {
    return processMemoryBytes(pid, "status", field);
  }

  private static long processPssBytes(int pid) {
    return processMemoryBytes(pid, "smaps_rollup", "Pss");
  }

  private static int processId(Process process) {
    if (process == null) return 0;
    try {
      Object value = Process.class.getMethod("pid").invoke(process);
      if (value instanceof Long) return Math.toIntExact((Long) value);
    } catch (Throwable ignored) {}
    return 0;
  }

  long processResidentBytes() {
    if (rpc == null || !rpc.isAlive()) {
      rpcPid = 0;
      return 0;
    }
    if (rpcPid == 0) {
      rpcPid = processId(rpc);
      if (rpcPid == 0) rpcPid = findProcessId();
    }
    return processStatusBytes(rpcPid, "VmRSS");
  }

  private long appResidentBytes() {
    return processStatusBytes(android.os.Process.myPid(), "VmRSS");
  }

  private long combinedProcessFootprintBytes() {
    processResidentBytes();
    int appPid = android.os.Process.myPid();
    long rpcPss = processPssBytes(rpcPid);
    long appPss = processPssBytes(appPid);
    if (rpcPss > 0 && appPss > 0) return rpcPss + appPss;
    return processStatusBytes(rpcPid, "VmRSS") + processStatusBytes(appPid, "VmRSS");
  }

  /** Reject a packaging regression before Android tries to execute it. */
  static void requireAndroid64Pie(File bin) throws IOException {
    byte[] h = new byte[20];
    try (InputStream in = new FileInputStream(bin)) {
      int off = 0, n;
      while (off < h.length && (n = in.read(h, off, h.length - off)) > 0) off += n;
      if (off != h.length) throw new IOException("Plik silnika jest ucięty");
    }
    int type = (h[16] & 255) | ((h[17] & 255) << 8);
    int machine = (h[18] & 255) | ((h[19] & 255) << 8);
    boolean machineSupported = false;
    for (String abi : Build.SUPPORTED_64_BIT_ABIS) {
      if (
        ("arm64-v8a".equals(abi) && machine == 183) ||
        ("x86_64".equals(abi) && machine == 62)
      ) {
        machineSupported = true;
        break;
      }
    }
    if (
      h[0] != 0x7f ||
      h[1] != 'E' ||
      h[2] != 'L' ||
      h[3] != 'F' ||
      h[4] != 2 ||
      h[5] != 1 ||
      type != 3 ||
      !machineSupported
    ) throw new IOException(
      "Uszkodzony pakiet: silnik musi być zgodnym z urządzeniem plikiem " +
      "Android 64-bit PIE (ET_DYN)."
    );
  }

  private ProcessBuilder nativeProcessBuilder() {
    ProcessBuilder builder = new ProcessBuilder(
      engineFile.getPath(),
      "-H",
      "127.0.0.1",
      "-p",
      "" + rpcPort,
      "-t",
      "" + Math.max(1, Runtime.getRuntime().availableProcessors() - 1),
      "-c"
    )
      .redirectErrorStream(true)
      .redirectOutput(ProcessBuilder.Redirect.appendTo(logFile));
    File rpcCache = new File(ctx.getFilesDir(), "rpc-cache");
    rpcCache.mkdirs();
    builder.environment().put("HOME", ctx.getFilesDir().getPath());
    builder.environment().put("TMPDIR", ctx.getCacheDir().getPath());
    builder.environment().put("LLAMA_CACHE", rpcCache.getPath());
    builder.environment().put("GGML_RPC_DEBUG", "2");
    return builder;
  }

  private synchronized void launchNative(boolean restart) throws Exception {
    if (closed) throw new IOException("Worker został zatrzymany");
    if (restart) {
      try (FileOutputStream out = new FileOutputStream(logFile, true)) {
        out.write(
          ("\n\n===== AUTOMATYCZNE ODTWORZENIE SILNIKA " +
            restartAttempts +
            " =====\n").getBytes(StandardCharsets.UTF_8)
        );
      }
    }
    rpcPid = 0;
    rpcExitCode = Integer.MIN_VALUE;
    rpcStartedAt = System.currentTimeMillis();
    rpcStoppedAt = 0;
    lastForwardingErrorAt = 0;
    lastForwardingError = "";
    recordEvent(
      restart ? "native_restart" : "native_start",
      "Launching " + engineFile.getPath()
    );
    try {
      rpc = nativeProcessBuilder().start();
      rpcPid = processId(rpc);
      if (rpcPid == 0) rpcPid = findProcessId();
    } catch (IOException error) {
      rpcPid = 0;
      throw new IOException(failure("Android odmówił uruchomienia silnika", engineFile), error);
    }
    InetAddress loop = InetAddress.getByName("127.0.0.1");
    boolean ready = false;
    for (int attempt = 0; attempt < 200 && !closed && rpc.isAlive(); attempt++) {
      try (Socket socket = new Socket(loop, rpcPort)) {
        ready = true;
        break;
      } catch (IOException error) {
        Thread.sleep(50);
      }
    }
    if (!ready) {
      String message = failure(
        rpc.isAlive()
          ? "Silnik nie otworzył portu w ciągu 10 sekund"
          : "Silnik natywny zakończył pracę podczas startu",
        engineFile
      );
      rpc.destroyForcibly();
      rpcPid = 0;
      throw new IOException(message);
    }
    if (restart) restartCount++;
    recovering = false;
    recoveryError = "";
    if (restart) notifyStateChanged();
  }

  void start() throws Exception {
    InetAddress ip = lan();
    File bin = new File(
      ctx.getApplicationInfo().nativeLibraryDir,
      "libylm_rpc.so"
    );
    engineFile = bin;
    requireAndroid64Pie(bin);

    KeyStore ks = KeyStore.getInstance("AndroidKeyStore");
    ks.load(null);
    if (!ks.containsAlias(alias) || ks.getCertificate(alias) == null) {
      if (ks.containsAlias(alias)) ks.deleteEntry(alias);
      KeyPairGenerator gen = KeyPairGenerator.getInstance(
        "EC",
        "AndroidKeyStore"
      );
      gen.initialize(
        new KeyGenParameterSpec.Builder(
          alias,
          KeyProperties.PURPOSE_SIGN | KeyProperties.PURPOSE_VERIFY
        )
          .setDigests(
            KeyProperties.DIGEST_NONE,
            KeyProperties.DIGEST_SHA256,
            KeyProperties.DIGEST_SHA384,
            KeyProperties.DIGEST_SHA512
          )
          .setCertificateSubject(new X500Principal("CN=YourLM"))
          .setCertificateSerialNumber(BigInteger.ONE)
          .build()
      );
      gen.generateKeyPair();
    }
    final Certificate cert = ks.getCertificate(alias);
    X509ExtendedKeyManager km = new X509ExtendedKeyManager() {
      public String[] getClientAliases(String k, java.security.Principal[] i) {
        return null;
      }

      public String chooseClientAlias(
        String[] k,
        java.security.Principal[] i,
        Socket s
      ) {
        return null;
      }

      public String[] getServerAliases(String k, java.security.Principal[] i) {
        return "EC".equals(k) ? new String[] { alias } : null;
      }

      public String chooseServerAlias(
        String k,
        java.security.Principal[] i,
        Socket s
      ) {
        return "EC".equals(k) ? alias : null;
      }

      public java.security.cert.X509Certificate[] getCertificateChain(
        String a
      ) {
        return new java.security.cert.X509Certificate[] {
          (java.security.cert.X509Certificate) cert,
        };
      }

      public PrivateKey getPrivateKey(String a) {
        try {
          return (PrivateKey) ks.getKey(alias, null);
        } catch (Exception e) {
          return null;
        }
      }
    };
    SSLContext tls = SSLContext.getInstance("TLS");
    tls.init(new KeyManager[] { km }, null, new SecureRandom());
    SSLServerSocketFactory serverFactory = tls.getServerSocketFactory();
    int preferredPort = preferences.getInt("bridge_port", 0);
    try {
      server = (SSLServerSocket) serverFactory.createServerSocket(
        preferredPort >= 1024 && preferredPort <= 65535 ? preferredPort : 0,
        8,
        ip
      );
    } catch (IOException unavailablePreferredPort) {
      server = (SSLServerSocket) serverFactory.createServerSocket(0, 8, ip);
    }
    preferences
      .edit()
      .putInt("bridge_port", server.getLocalPort())
      .apply();
    server.setEnabledProtocols(new String[] { "TLSv1.2" });
    server.setSoTimeout(1000);
    pairing = new ServerSocket(0, 8, ip);
    pairAddress = ip.getHostAddress() + ":" + pairing.getLocalPort();

    InetAddress loop = InetAddress.getByName("127.0.0.1");
    try (ServerSocket reserve = new ServerSocket(0, 1, loop)) {
      rpcPort = reserve.getLocalPort();
    }
    if (logFile.length() > 8L * 1024 * 1024) {
      String preserved = logTail(logFile, 1024 * 1024);
      try (FileOutputStream out = new FileOutputStream(logFile, false)) {
        out.write(preserved.getBytes(StandardCharsets.UTF_8));
      }
    }
    try (FileOutputStream out = new FileOutputStream(logFile, true)) {
      out.write(
        ("\n\n===== NOWA USŁUGA WORKERA " +
          System.currentTimeMillis() +
          " =====\n").getBytes(StandardCharsets.UTF_8)
      );
    }
    File rpcCache = new File(ctx.getFilesDir(), "rpc-cache");
    rpcCache.mkdirs();
    removeStaleCacheParts(rpcCache);
    launchNative(false);
    pin = hex(
      MessageDigest.getInstance("SHA-256").digest(cert.getEncoded())
    );
    code =
      "YLM1|" +
      ip.getHostAddress() +
      ":" +
      server.getLocalPort() +
      "|" +
      pin +
      "|" +
      secret;
  }

  private static String exceptionDetails(Throwable error) {
    StringWriter text = new StringWriter();
    error.printStackTrace(new PrintWriter(text));
    String value = text.toString().trim();
    return value.length() <= 8000
      ? value
      : value.substring(0, 8000) + "\n…";
  }

  private void recordEvent(String kind, String detail) {
    long now = System.currentTimeMillis();
    String safe = detail == null
      ? ""
      : detail.replace('\r', ' ').trim();
    String line = now + " " + kind + " " + safe + "\n";
    synchronized (eventLock) {
      try (FileOutputStream out = new FileOutputStream(eventFile, true)) {
        out.write(line.getBytes(StandardCharsets.UTF_8));
      } catch (IOException ignored) {}
      if (eventFile.length() > 64 * 1024) {
        String tail = logTail(eventFile, 48 * 1024);
        try (FileOutputStream out = new FileOutputStream(eventFile, false)) {
          out.write(tail.getBytes(StandardCharsets.UTF_8));
          out.write('\n');
        } catch (IOException ignored) {}
      }
    }
  }

  private String workerEventTail() {
    synchronized (eventLock) {
      return logTail(eventFile, 48 * 1024);
    }
  }

  private static boolean isRoutineSocketClose(Throwable error) {
    for (Throwable current = error; current != null; current = current.getCause()) {
      if (!(current instanceof SocketException)) continue;
      String message = current.getMessage();
      if (message == null) continue;
      message = message.toLowerCase(Locale.ROOT);
      if (
        message.contains("socket closed") || message.contains("socket is closed")
      ) return true;
    }
    return false;
  }

  private void recordForwardingError(String stage, Throwable error) {
    if (
      closed || (stage.equals("native_to_host") && isRoutineSocketClose(error))
    ) return;
    long now = System.currentTimeMillis();
    String details = stage + ": " + exceptionDetails(error);
    lastForwardingErrorAt = now;
    lastForwardingError = details;
    recordEvent("forwarding_error", details);
  }

  private static String nativeExitSummary(int code) {
    if (code == Integer.MIN_VALUE) return "running_or_unknown";
    if (code < 128 || code > 255) return "exit_" + code;
    int signal = code - 128;
    String name;
    switch (signal) {
      case 6:
        name = "SIGABRT";
        break;
      case 7:
        name = "SIGBUS";
        break;
      case 9:
        name = "SIGKILL";
        break;
      case 11:
        name = "SIGSEGV";
        break;
      case 15:
        name = "SIGTERM";
        break;
      default:
        name = "signal_" + signal;
    }
    return "exit_" + code + "_" + name;
  }

  private void persistSnapshot(JSONObject report, boolean force) {
    long now = System.currentTimeMillis();
    if (!force && now - lastSnapshotPersistedAt < 15_000) return;
    lastSnapshotPersistedAt = now;
    File part = new File(snapshotFile.getParentFile(), snapshotFile.getName() + ".part");
    try (FileOutputStream out = new FileOutputStream(part, false)) {
      out.write(report.toString(2).getBytes(StandardCharsets.UTF_8));
      out.write('\n');
    } catch (Exception ignored) {
      part.delete();
      return;
    }
    if (snapshotFile.exists() && !snapshotFile.delete()) {
      part.delete();
      return;
    }
    if (!part.renameTo(snapshotFile)) part.delete();
  }

  private static void removeStaleCacheParts(File directory) {
    File[] entries = directory.listFiles();
    if (entries == null) return;
    for (File entry : entries) {
      if (entry.isDirectory()) {
        removeStaleCacheParts(entry);
      } else if (
        entry.getName().startsWith(".incoming-") &&
        entry.getName().endsWith(".part")
      ) {
        entry.delete();
      }
    }
  }

  private static void cacheStats(File entry, long[] stats) {
    if (entry == null || !entry.exists()) return;
    if (entry.isDirectory()) {
      File[] children = entry.listFiles();
      if (children != null) for (File child : children) cacheStats(child, stats);
      return;
    }
    stats[0]++;
    stats[1] += Math.max(0, entry.length());
    if (
      entry.getName().startsWith(".incoming-") &&
      entry.getName().endsWith(".part")
    ) stats[2]++;
  }

  private static String exitReason(int reason) {
    switch (reason) {
      case ApplicationExitInfo.REASON_EXIT_SELF:
        return "EXIT_SELF";
      case ApplicationExitInfo.REASON_SIGNALED:
        return "SIGNALED";
      case ApplicationExitInfo.REASON_LOW_MEMORY:
        return "LOW_MEMORY";
      case ApplicationExitInfo.REASON_CRASH:
        return "CRASH";
      case ApplicationExitInfo.REASON_CRASH_NATIVE:
        return "CRASH_NATIVE";
      case ApplicationExitInfo.REASON_ANR:
        return "ANR";
      case ApplicationExitInfo.REASON_INITIALIZATION_FAILURE:
        return "INITIALIZATION_FAILURE";
      case ApplicationExitInfo.REASON_PERMISSION_CHANGE:
        return "PERMISSION_CHANGE";
      case ApplicationExitInfo.REASON_EXCESSIVE_RESOURCE_USAGE:
        return "EXCESSIVE_RESOURCE_USAGE";
      case ApplicationExitInfo.REASON_USER_REQUESTED:
        return "USER_REQUESTED";
      case ApplicationExitInfo.REASON_USER_STOPPED:
        return "USER_STOPPED";
      case ApplicationExitInfo.REASON_DEPENDENCY_DIED:
        return "DEPENDENCY_DIED";
      case ApplicationExitInfo.REASON_OTHER:
        return "OTHER";
      default:
        return "UNKNOWN_" + reason;
    }
  }

  private org.json.JSONArray nativeExitHistory() {
    org.json.JSONArray out = new org.json.JSONArray();
    if (Build.VERSION.SDK_INT < 30) return out;
    try {
      ActivityManager manager = (ActivityManager) ctx.getSystemService(
        Context.ACTIVITY_SERVICE
      );
      List<ApplicationExitInfo> entries = manager.getHistoricalProcessExitReasons(
        null,
        0,
        8
      );
      for (ApplicationExitInfo entry : entries) {
        out.put(
          new JSONObject()
            .put("process", entry.getProcessName())
            .put("reason", exitReason(entry.getReason()))
            .put("reasonCode", entry.getReason())
            .put("status", entry.getStatus())
            .put("importance", entry.getImportance())
            .put("pssKiB", entry.getPss())
            .put("rssKiB", entry.getRss())
            .put("timestampEpochMs", entry.getTimestamp())
            .put("description", entry.getDescription())
        );
      }
    } catch (Throwable ignored) {}
    return out;
  }

  private JSONObject diagnostics() {
    return diagnostics(false);
  }

  String diagnosticReport() {
    return diagnostics(true).toString();
  }

  private synchronized JSONObject diagnostics(boolean forcePersistence) {
    ActivityManager.MemoryInfo memory = mem();
    File rpcCache = new File(ctx.getFilesDir(), "rpc-cache");
    long[] cache = new long[3];
    cacheStats(rpcCache, cache);
    long resident = processResidentBytes();
    int appPid = android.os.Process.myPid();
    boolean alive = rpc != null && rpc.isAlive();
    JSONObject out = new JSONObject();
    try {
      out
        .put("device", Build.MANUFACTURER + " " + Build.MODEL)
        .put("android", Build.VERSION.RELEASE)
        .put("sdk", Build.VERSION.SDK_INT)
        .put("workerBuild", WORKER_BUILD)
        .put("capturedAtEpochMs", System.currentTimeMillis())
        .put("appProcessPid", appPid)
        .put("rpcAlive", alive)
        .put("rpcPid", rpcPid)
        .put("rpcResidentBytes", resident)
        .put("rpcPssBytes", processPssBytes(rpcPid))
        .put("rpcPeakResidentBytes", processStatusBytes(rpcPid, "VmHWM"))
        .put("rpcAnonymousResidentBytes", processStatusBytes(rpcPid, "RssAnon"))
        .put("rpcFileResidentBytes", processStatusBytes(rpcPid, "RssFile"))
        .put("rpcSharedResidentBytes", processStatusBytes(rpcPid, "RssShmem"))
        .put("rpcSwapBytes", processStatusBytes(rpcPid, "VmSwap"))
        .put("appResidentBytes", appResidentBytes())
        .put("appPssBytes", processPssBytes(appPid))
        .put("appSwapBytes", processStatusBytes(appPid, "VmSwap"))
        .put("combinedProcessFootprintBytes", combinedProcessFootprintBytes())
        .put("processMemoryMetric", "PSS when available, otherwise RSS")
        .put("rpcStartedAtEpochMs", rpcStartedAt)
        .put("rpcStoppedAtEpochMs", rpcStoppedAt)
        .put("rpcExitSummary", nativeExitSummary(rpcExitCode))
        .put("recovering", recovering)
        .put("restartAttempts", restartAttempts)
        .put("restartCount", restartCount)
        .put(
          "recoveryError",
          recoveryError.isEmpty() ? JSONObject.NULL : recoveryError
        )
        .put("recoveryTriggerStoppedAtEpochMs", recoveryTriggerStoppedAt)
        .put(
          "recoveryTriggerExitSummary",
          nativeExitSummary(recoveryTriggerExitCode)
        )
        .put(
          "recoveryTriggerNativeLog",
          recoveryTriggerNativeLog.isEmpty()
            ? JSONObject.NULL
            : recoveryTriggerNativeLog
        )
        .put("bridgeConnections", bridgeConnections.get())
        .put("bridgeActiveSockets", active.size())
        .put("bridgeHostToNativeBytes", bridgeHostToNativeBytes.get())
        .put("bridgeNativeToHostBytes", bridgeNativeToHostBytes.get())
        .put("memoryTotalBytes", memory.totalMem)
        .put("memoryAvailableBytes", memory.availMem)
        .put("memoryLow", memory.lowMemory)
        .put("memoryLowThresholdBytes", memory.threshold)
        .put("storageAvailableBytes", ctx.getFilesDir().getUsableSpace())
        .put("storageTotalBytes", ctx.getFilesDir().getTotalSpace())
        .put("cacheFiles", cache[0])
        .put("cacheBytes", cache[1])
        .put("cachePartialFiles", cache[2])
        .put("nativeLogBytes", logFile.isFile() ? logFile.length() : 0)
        .put("nativeLog", logTail(logFile, 24000))
        .put("lastForwardingErrorAtEpochMs", lastForwardingErrorAt)
        .put(
          "lastForwardingError",
          lastForwardingError.isEmpty() ? JSONObject.NULL : lastForwardingError
        )
        .put("workerEvents", workerEventTail())
        .put("exitHistory", nativeExitHistory());
      if (rpcExitCode == Integer.MIN_VALUE) out.put("rpcExitCode", JSONObject.NULL);
      else out.put("rpcExitCode", rpcExitCode);

      WifiManager wifi = (WifiManager) ctx
        .getApplicationContext()
        .getSystemService(Context.WIFI_SERVICE);
      WifiInfo info = wifi == null ? null : wifi.getConnectionInfo();
      if (info != null) {
        out
          .put("wifiFrequencyMHz", info.getFrequency())
          .put("wifiLinkSpeedMbps", info.getLinkSpeed())
          .put("wifiRssiDbm", info.getRssi());
      }
      ConnectivityManager connectivity = (ConnectivityManager) ctx.getSystemService(
        Context.CONNECTIVITY_SERVICE
      );
      if (connectivity != null) {
        NetworkCapabilities capabilities = connectivity.getNetworkCapabilities(
          connectivity.getActiveNetwork()
        );
        if (capabilities != null) {
          out
            .put("networkDownstreamKbps", capabilities.getLinkDownstreamBandwidthKbps())
            .put("networkUpstreamKbps", capabilities.getLinkUpstreamBandwidthKbps());
        }
      }
    } catch (Exception e) {
      try {
        out.put("diagnosticError", exceptionDetails(e));
      } catch (Exception ignored) {}
    }
    persistSnapshot(out, forcePersistence);
    return out;
  }

  private String failure(String heading, File bin) {
    StringBuilder out = new StringBuilder(heading);
    if (rpc != null && !rpc.isAlive()) {
      try {
        out.append(" (kod wyjścia ").append(rpc.exitValue()).append(")");
      } catch (Exception ignored) {}
    }
    out
      .append(". ABI telefonu: ")
      .append(Arrays.toString(Build.SUPPORTED_ABIS))
      .append(". Silnik: ")
      .append(bin.length())
      .append(" B.");
    String tail = logTail(logFile, 12000);
    if (!tail.isEmpty()) out.append("\n\nLog natywny:\n").append(tail);
    else out.append("\n\nSilnik nie zapisał żadnego komunikatu.");
    return out.toString();
  }

  static String logTail(File file, int maxBytes) {
    if (file == null || !file.isFile()) return "";
    try (RandomAccessFile in = new RandomAccessFile(file, "r")) {
      long length = in.length();
      long start = Math.max(0, length - maxBytes);
      in.seek(start);
      byte[] data = new byte[(int) (length - start)];
      in.readFully(data);
      String value = new String(data, StandardCharsets.UTF_8).trim();
      return start > 0 ? "…\n" + value : value;
    } catch (IOException e) {
      return "Nie można odczytać logu: " + e.getMessage();
    }
  }

  private void startEngineWatch(final Process watched) {
    new Thread(
      () -> {
        try {
          int exitCode = watched.waitFor();
          if (closed || watched != rpc) return;
          rpcExitCode = exitCode;
          rpcPid = 0;
          rpcStoppedAt = System.currentTimeMillis();
          recordEvent("native_exit", nativeExitSummary(rpcExitCode));
          diagnostics(true);
          notifyStateChanged();
        } catch (Exception error) {
          recordEvent("native_watch_error", exceptionDetails(error));
        }
      },
      "YourLM-engine-watch"
    ).start();
  }

  synchronized boolean restartNative() throws Exception {
    if (closed) throw new IOException("Worker został zatrzymany przez użytkownika");
    if (recoveryTriggerStoppedAt == 0) {
      recoveryTriggerExitCode = rpcExitCode;
      recoveryTriggerStoppedAt = rpcStoppedAt;
      recoveryTriggerNativeLog = logTail(logFile, 24000);
    }
    recovering = true;
    restartAttempts++;
    notifyStateChanged();
    launchNative(true);
    recordEvent("native_recovered", "attempt=" + restartAttempts);
    diagnostics(true);
    return true;
  }

  void watchNative() {
    startEngineWatch(rpc);
  }

  void markRecoveryFailure(Throwable error) {
    recovering = false;
    recoveryError = exceptionDetails(error);
    recordEvent("native_recovery_failed", recoveryError);
    diagnostics(true);
    notifyStateChanged();
  }

  void listen() throws IOException {
    startEngineWatch(rpc);
    new Thread(
      () -> {
        while (!closed) {
          try {
            final Socket s = pairing.accept();
            try {
              clients.execute(() -> servePairing(s));
            } catch (RejectedExecutionException e) {
              try {
                s.close();
              } catch (IOException ignored) {}
            }
          } catch (IOException e) {
            return;
          }
        }
      },
      "YourLM-pairing"
    ).start();
    while (!closed) {
      Socket s;
      try {
        s = server.accept();
      } catch (SocketTimeoutException e) {
        // A native exit is supervised by WorkerService. Keep INFO/DIAG alive so
        // the host can retrieve the original exit evidence while recovery runs.
        continue;
      } catch (IOException e) {
        if (closed) return;
        throw new IOException(
          failure("Silnik workera nieoczekiwanie się zatrzymał", engineFile),
          e
        );
      }
      try {
        s.setTcpNoDelay(true);
        s.setReceiveBufferSize(1024 * 1024);
        s.setSendBufferSize(1024 * 1024);
      } catch (Exception ignored) {}
      active.add(s);
      try {
        clients.execute(() -> serve(s));
      } catch (RejectedExecutionException e) {
        drop(s);
      }
    }
  }

  private void servePairing(Socket s) {
    try {
      s.setSoTimeout(5000);
      InputStream input = s.getInputStream();
      ByteArrayOutputStream request = new ByteArrayOutputStream(80);
      int next = -1;
      while (
        request.size() <= 80 &&
        (next = input.read()) != -1 &&
        next != '\n'
      ) request.write(next);
      if (request.size() > 80 || next != '\n') return;
      String line = request.toString("US-ASCII");
      String[] parts = line.trim().split(" +");
      if (
        parts.length != 2 ||
        !"YLM2".equals(parts[0]) ||
        !parts[1].matches("[0-9a-fA-F]{64}")
      ) return;
      String address =
        ((InetSocketAddress) server.getLocalSocketAddress())
            .getAddress()
            .getHostAddress() +
        ":" +
        server.getLocalPort();
      Mac mac = Mac.getInstance("HmacSHA256");
      String canonical = manualKey.replace("-", "");
      mac.init(
        new SecretKeySpec(
          canonical.getBytes(StandardCharsets.US_ASCII),
          "HmacSHA256"
        )
      );
      String message = "YLM2|" + parts[1] + "|" + address + "|" + pin;
      String proof = hex(
        mac.doFinal(message.getBytes(StandardCharsets.US_ASCII))
      );
      JSONObject reply = new JSONObject()
        .put("address", address)
        .put("pin", pin)
        .put("proof", proof);
      s
        .getOutputStream()
        .write((reply.toString() + "\n").getBytes(StandardCharsets.UTF_8));
    } catch (Exception ignored) {
    } finally {
      try {
        s.close();
      } catch (IOException ignored) {}
    }
  }

  private void drop(Socket s) {
    active.remove(s);
    try {
      s.close();
    } catch (IOException ignored) {}
  }

  void serve(Socket s) {
    try {
      s.setSoTimeout(6000);
      InputStream in = s.getInputStream();
      ByteArrayOutputStream b = new ByteArrayOutputStream();
      int ch;
      while (b.size() < 80 && (ch = in.read()) != -1 && ch != '\n') b.write(ch);
      String[] auth = b.toString("US-ASCII").split(" ", 2);
      if (
        auth.length != 2 ||
        !MessageDigest.isEqual(
          auth[1].getBytes(StandardCharsets.UTF_8),
          secret.getBytes(StandardCharsets.UTF_8)
        )
      ) return;
      if (auth[0].equals("INFO")) {
        ActivityManager.MemoryInfo m = mem();
        String info = new JSONObject()
          .put("name", Build.MANUFACTURER + " " + Build.MODEL)
          .put("total", m.totalMem)
          .put("free", m.availMem)
          .put("used", Math.max(0, m.totalMem - m.availMem))
          .put("allocation", combinedProcessFootprintBytes())
          .put("cores", Runtime.getRuntime().availableProcessors())
          .put("build", WORKER_BUILD)
          .put("alive", nativeAlive())
          .put("recovering", recovering)
          .put("restartAttempts", restartAttempts)
          .put("restartCount", restartCount)
          .put(
            "recoveryError",
            recoveryError.isEmpty() ? JSONObject.NULL : recoveryError
          )
          .toString();
        s.getOutputStream().write((info + "\n").getBytes(StandardCharsets.UTF_8));
        return;
      }
      if (auth[0].equals("DIAG")) {
        OutputStream output = s.getOutputStream();
        output.write((diagnostics().toString() + "\n").getBytes(StandardCharsets.UTF_8));
        output.flush();
        return;
      }
      if (!auth[0].equals("RPC")) return;
      try {
        forwardRPC(s, in);
      } catch (Exception e) {
        recordForwardingError("host_to_native", e);
      }
    } catch (Exception ignored) {
      // Authentication and one-shot INFO/DIAG failures are not RPC failures.
    } finally {
      drop(s);
    }
  }

  private void forwardRPC(Socket remote, InputStream remoteInput) throws Exception {
    bridgeConnections.incrementAndGet();
    try (Socket local = new Socket("127.0.0.1", rpcPort)) {
      remote.setSoTimeout(0);
      local.setTcpNoDelay(true);
      local.setReceiveBufferSize(1024 * 1024);
      local.setSendBufferSize(1024 * 1024);
      remote.setTcpNoDelay(true);
      remote.getOutputStream().write('!');
      Thread back = new Thread(
        () -> {
          try {
            pump(
              local.getInputStream(),
              remote.getOutputStream(),
              bridgeNativeToHostBytes
            );
          } catch (IOException e) {
            recordForwardingError("native_to_host", e);
          } finally {
            drop(remote);
          }
        },
        "YourLM-rpc-return"
      );
      back.start();
      pump(remoteInput, local.getOutputStream(), bridgeHostToNativeBytes);
    }
  }

  static void pump(InputStream in, OutputStream out, AtomicLong transferred)
    throws IOException {
    byte[] buf = new byte[262144];
    int n;
    while ((n = in.read(buf)) != -1) {
      out.write(buf, 0, n);
      transferred.addAndGet(n);
    }
  }

  public void close() {
    closed = true;
    try {
      if (server != null) server.close();
    } catch (IOException ignored) {}
    try {
      if (pairing != null) pairing.close();
    } catch (IOException ignored) {}
    for (Socket s : active) drop(s);
    clients.shutdownNow();
    if (rpc != null) rpc.destroyForcibly();
    rpcPid = 0;
  }
}
