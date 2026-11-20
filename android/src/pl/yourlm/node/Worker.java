package pl.yourlm.node;

import android.app.ActivityManager;
import android.content.Context;
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
  final String manualKey = createManualKey();
  final String secret = derivedSecret(manualKey.replace("-", ""));
  final String alias = "YourLM-session";
  final File logFile;
  File engineFile;
  Process rpc;
  SSLServerSocket server;
  ServerSocket pairing;
  String code, pairAddress, pin;
  int rpcPort;
  volatile boolean closed;

  Worker(Context c) {
    ctx = c;
    logFile = new File(c.getCacheDir(), "worker.log");
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

  /** Reject a packaging regression before Android tries to execute it. */
  static void requireArm64Pie(File bin) throws IOException {
    byte[] h = new byte[20];
    try (InputStream in = new FileInputStream(bin)) {
      int off = 0, n;
      while (off < h.length && (n = in.read(h, off, h.length - off)) > 0) off += n;
      if (off != h.length) throw new IOException("Plik silnika jest ucięty");
    }
    int type = (h[16] & 255) | ((h[17] & 255) << 8);
    int machine = (h[18] & 255) | ((h[19] & 255) << 8);
    if (
      h[0] != 0x7f ||
      h[1] != 'E' ||
      h[2] != 'L' ||
      h[3] != 'F' ||
      h[4] != 2 ||
      h[5] != 1 ||
      type != 3 ||
      machine != 183
    ) throw new IOException(
      "Uszkodzony pakiet: silnik musi być plikiem Android ARM64 PIE (ET_DYN)."
    );
  }

  void start() throws Exception {
    InetAddress ip = lan();
    File bin = new File(
      ctx.getApplicationInfo().nativeLibraryDir,
      "libylm_rpc.so"
    );
    engineFile = bin;
    requireArm64Pie(bin);

    KeyStore ks = KeyStore.getInstance("AndroidKeyStore");
    ks.load(null);
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
    Certificate cert = ks.getCertificate(alias);
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
    server = (SSLServerSocket) tls
      .getServerSocketFactory()
      .createServerSocket(0, 8, ip);
    server.setEnabledProtocols(new String[] { "TLSv1.2" });
    pairing = new ServerSocket(0, 8, ip);
    pairAddress = ip.getHostAddress() + ":" + pairing.getLocalPort();

    InetAddress loop = InetAddress.getByName("127.0.0.1");
    try (ServerSocket reserve = new ServerSocket(0, 1, loop)) {
      rpcPort = reserve.getLocalPort();
    }
    if (logFile.exists()) logFile.delete();
    ProcessBuilder builder = new ProcessBuilder(
      bin.getPath(),
      "-H",
      "127.0.0.1",
      "-p",
      "" + rpcPort,
      "-t",
      "" + Math.max(1, Runtime.getRuntime().availableProcessors() / 2)
    )
      .redirectErrorStream(true)
      .redirectOutput(logFile);
    builder.environment().put("HOME", ctx.getFilesDir().getPath());
    builder.environment().put("TMPDIR", ctx.getCacheDir().getPath());
    try {
      rpc = builder.start();
    } catch (IOException e) {
      throw new IOException(failure("Android odmówił uruchomienia silnika", bin), e);
    }
    boolean ready = false;
    for (int i = 0; i < 200 && rpc.isAlive(); i++) {
      try (Socket s = new Socket(loop, rpcPort)) {
        ready = true;
        break;
      } catch (IOException e) {
        Thread.sleep(50);
      }
    }
    if (!ready) {
      String message = failure(
        rpc.isAlive()
          ? "Silnik nie otworzył portu w ciągu 10 sekund"
          : "Silnik ARM64 zakończył pracę podczas startu",
        bin
      );
      rpc.destroyForcibly();
      throw new IOException(message);
    }
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
      long start = Math.max(0, in.length() - maxBytes);
      in.seek(start);
      byte[] data = new byte[(int) (in.length() - start)];
      in.readFully(data);
      String value = new String(data, StandardCharsets.UTF_8).trim();
      return start > 0 ? "…\n" + value : value;
    } catch (IOException e) {
      return "Nie można odczytać logu: " + e.getMessage();
    }
  }

  void listen() throws IOException {
    new Thread(
      () -> {
        try {
          rpc.waitFor();
          if (server != null) server.close();
          if (pairing != null) pairing.close();
        } catch (Exception ignored) {}
      },
      "YourLM-engine-watch"
    ).start();
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
      } catch (IOException e) {
        if (closed) return;
        throw new IOException(
          failure("Silnik workera nieoczekiwanie się zatrzymał", engineFile),
          e
        );
      }
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
          .put("cores", Runtime.getRuntime().availableProcessors())
          .put("build", "b12562")
          .toString();
        s.getOutputStream().write((info + "\n").getBytes(StandardCharsets.UTF_8));
        return;
      }
      if (!auth[0].equals("RPC")) return;
      try (Socket local = new Socket("127.0.0.1", rpcPort)) {
        s.setSoTimeout(0);
        local.setTcpNoDelay(true);
        s.setTcpNoDelay(true);
        s.getOutputStream().write('!');
        Thread back = new Thread(() -> {
          try {
            pump(local.getInputStream(), s.getOutputStream());
          } catch (IOException ignored) {
          } finally {
            drop(s);
          }
        });
        back.start();
        pump(in, local.getOutputStream());
      }
    } catch (Exception ignored) {
    } finally {
      drop(s);
    }
  }

  static void pump(InputStream in, OutputStream out) throws IOException {
    byte[] buf = new byte[32768];
    int n;
    while ((n = in.read(buf)) != -1) out.write(buf, 0, n);
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
  }
}
