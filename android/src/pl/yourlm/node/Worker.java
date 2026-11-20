package pl.yourlm.node;

import android.app.ActivityManager;
import android.content.Context;
import android.os.Build;
import android.security.keystore.*;
import java.io.*;
import java.math.BigInteger;
import java.net.*;
import java.security.*;
import java.security.cert.Certificate;
import java.util.*;
import java.util.concurrent.*;
import javax.net.ssl.*;
import javax.security.auth.x500.X500Principal;
import org.json.JSONObject;

/** The experimental native RPC protocol never listens on Wi-Fi. Only this authenticated TLS bridge does. */
final class Worker implements Closeable {

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
  final String secret = hex(random(24));
  final String alias = "YourLM-session";
  Process rpc;
  SSLServerSocket server;
  String code;
  int rpcPort;
  volatile boolean closed;

  Worker(Context c) {
    ctx = c;
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

  static InetAddress lan() throws Exception {
    for (NetworkInterface nic : Collections.list(
      NetworkInterface.getNetworkInterfaces()
    )) {
      if (!nic.isUp() || !nic.getName().startsWith("wlan")) continue;
      for (InetAddress a : Collections.list(nic.getInetAddresses()))
        if (a instanceof Inet4Address && a.isSiteLocalAddress()) return a;
    }
    throw new IOException(
      "Połącz telefon z prywatną siecią Wi-Fi (adres IPv4). "
    );
  }

  ActivityManager.MemoryInfo mem() {
    ActivityManager.MemoryInfo m = new ActivityManager.MemoryInfo();
    (
      (ActivityManager) ctx.getSystemService(Context.ACTIVITY_SERVICE)
    ).getMemoryInfo(m);
    return m;
  }

  void start() throws Exception {
    InetAddress ip = lan();
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
    InetAddress loop = InetAddress.getByName("127.0.0.1");
    try (ServerSocket reserve = new ServerSocket(0, 1, loop)) {
      rpcPort = reserve.getLocalPort();
    }
    File bin = new File(
      ctx.getApplicationInfo().nativeLibraryDir,
      "libylm_rpc.so"
    );
    rpc = new ProcessBuilder(
      bin.getPath(),
      "-H",
      "127.0.0.1",
      "-p",
      "" + rpcPort,
      "-t",
      "" + Math.max(1, Runtime.getRuntime().availableProcessors() / 2)
    )
      .redirectErrorStream(true)
      .redirectOutput(new File(ctx.getCacheDir(), "worker.log"))
      .start();
    boolean ready = false;
    for (int i = 0; i < 80 && rpc.isAlive(); i++) {
      try (Socket s = new Socket(loop, rpcPort)) {
        ready = true;
        break;
      } catch (IOException e) {
        Thread.sleep(50);
      }
    }
    if (!ready) throw new IOException("Nie udało się uruchomić silnika ARM64.");
    code =
      "YLM1|" +
      ip.getHostAddress() +
      ":" +
      server.getLocalPort() +
      "|" +
      hex(MessageDigest.getInstance("SHA-256").digest(cert.getEncoded())) +
      "|" +
      secret;
  }

  void listen() throws IOException {
    new Thread(() -> {
      try {
        rpc.waitFor();
        server.close();
      } catch (Exception ignored) {}
    }).start();
    while (!closed) {
      Socket s = server.accept();
      active.add(s);
      try {
        clients.execute(() -> serve(s));
      } catch (RejectedExecutionException e) {
        drop(s);
      }
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
          auth[1].getBytes("UTF-8"),
          secret.getBytes("UTF-8")
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
        s.getOutputStream().write((info + "\n").getBytes("UTF-8"));
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
    for (Socket s : active) drop(s);
    clients.shutdownNow();
    if (rpc != null) rpc.destroyForcibly();
  }
}
