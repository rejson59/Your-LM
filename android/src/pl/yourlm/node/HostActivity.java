package pl.yourlm.node;

import android.app.Activity;
import android.net.Uri;
import android.os.Bundle;
import android.view.WindowManager;
import android.webkit.*;
import android.widget.*;
import java.io.*;

/** Same host console as Windows, served only on a random loopback port. */
public final class HostActivity extends Activity {

  PermissionRequest pending;
  volatile Process host;
  volatile boolean closed;
  WebView view;

  public void onCreate(Bundle state) {
    super.onCreate(state);
    TextView waiting = new TextView(this);
    waiting.setText("YourLM\n\nUruchamianie prywatnej konsoli…");
    waiting.setTextSize(20);
    waiting.setTextColor(0xff14251e);
    waiting.setGravity(android.view.Gravity.CENTER);
    waiting.setPadding(48, 48, 48, 48);
    waiting.setBackgroundColor(0xfff4f6f2);
    setContentView(waiting);
    getWindow().setStatusBarColor(0xfff4f6f2);
    getWindow().getDecorView().setSystemUiVisibility(android.view.View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR);
    getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
    new Thread(() -> {
      try {
        File libs = new File(getApplicationInfo().nativeLibraryDir);
        File controller = new File(libs, "libylm_host.so");
        File engine = new File(libs, "libylm_server.so");
        Worker.requireAndroid64Pie(controller);
        Worker.requireAndroid64Pie(engine);
        File hostLog = new File(getCacheDir(), "host.log");
        if (hostLog.exists()) hostLog.delete();
        ProcessBuilder pb = new ProcessBuilder(
          controller.getPath(),
          "-no-browser"
        );
        pb.environment().put("YOURLM_HOME", getFilesDir().getPath());
        pb.environment().put("YOURLM_ENGINE", engine.getPath());
        pb.environment().put("HOME", getFilesDir().getPath());
        pb.environment().put("TMPDIR", getCacheDir().getPath());
        try {
          pb.environment().put("YOURLM_LAN", Worker.lan().getHostAddress());
        } catch (Exception ignored) {}
        pb.directory(getFilesDir());
        pb.redirectError(hostLog);
        Process p = pb.start();
        host = p;
        if (closed) {
          p.destroy();
          return;
        }
        String url = new BufferedReader(
          new InputStreamReader(p.getInputStream(), "UTF-8")
        ).readLine();
        if (
          url == null ||
          !url.matches("http://127\\.0\\.0\\.1:[0-9]+/#([a-f0-9]{48})")
        ) {
          String exit = "";
          if (!p.isAlive()) {
            try {
              exit = " (kod wyjścia " + p.exitValue() + ")";
            } catch (Exception ignored) {}
          }
          String log = Worker.logTail(hostLog, 12000);
          throw new IOException(
            "Host nie zwrócił adresu konsoli" +
            exit +
            (log.isEmpty() ? ". Brak komunikatu w logu." : ".\n\nLog natywny:\n" + log)
          );
        }
        runOnUiThread(() -> {
          if (closed) return;
          view = new WebView(this);
          WebSettings settings = view.getSettings();
          settings.setJavaScriptEnabled(true);
          settings.setDomStorageEnabled(true);
          settings.setAllowFileAccess(false);
          settings.setAllowContentAccess(false);
          settings.setAllowFileAccessFromFileURLs(false);
          settings.setAllowUniversalAccessFromFileURLs(false);
          settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
          settings.setSafeBrowsingEnabled(true);
          view.setWebChromeClient(
            new WebChromeClient() {
              public void onPermissionRequest(PermissionRequest r) {
                if (
                  !r
                    .getOrigin()
                    .getAuthority()
                    .equals(Uri.parse(url).getAuthority()) ||
                  !"http".equals(r.getOrigin().getScheme()) ||
                  r.getResources().length != 1 ||
                  !PermissionRequest.RESOURCE_VIDEO_CAPTURE.equals(
                    r.getResources()[0]
                  )
                ) {
                  r.deny();
                  return;
                }
                if (
                  checkSelfPermission(android.Manifest.permission.CAMERA) ==
                  android.content.pm.PackageManager.PERMISSION_GRANTED
                ) r.grant(new String[] {
                  PermissionRequest.RESOURCE_VIDEO_CAPTURE,
                });
                else {
                  pending = r;
                  requestPermissions(
                    new String[] { android.Manifest.permission.CAMERA },
                    21
                  );
                }
              }

              public void onPermissionRequestCanceled(PermissionRequest r) {
                if (r == pending) pending = null;
              }
            }
          );
          view.setWebViewClient(
            new WebViewClient() {
              public WebResourceResponse shouldInterceptRequest(
                WebView v,
                WebResourceRequest req
              ) {
                Uri target = req.getUrl();
                if (
                  "http".equals(target.getScheme()) &&
                  Uri.parse(url).getAuthority().equals(target.getAuthority())
                ) return super.shouldInterceptRequest(v, req);
                return new WebResourceResponse(
                  "text/plain",
                  "UTF-8",
                  403,
                  "Blocked",
                  java.util.Collections.emptyMap(),
                  new ByteArrayInputStream(new byte[0])
                );
              }

              public boolean shouldOverrideUrlLoading(
                WebView v,
                WebResourceRequest req
              ) {
                return (
                  !Uri.parse(url)
                    .getAuthority()
                    .equals(req.getUrl().getAuthority()) ||
                  !"http".equals(req.getUrl().getScheme())
                );
              }
            }
          );
          setContentView(view);
          view.loadUrl(url);
        });
        p.waitFor();
        if (!closed) throw new IOException("Proces hosta został zatrzymany");
      } catch (Exception e) {
        runOnUiThread(() -> {
          if (!closed) {
            waiting.setText("Nie udało się uruchomić hosta: " + e.getMessage());
            setContentView(waiting);
          }
        });
      }
    }).start();
  }

  public void onRequestPermissionsResult(int id, String[] names, int[] grants) {
    super.onRequestPermissionsResult(id, names, grants);
    if (id == 21 && pending != null) {
      if (
        grants.length > 0 &&
        grants[0] == android.content.pm.PackageManager.PERMISSION_GRANTED
      ) pending.grant(new String[] {
        PermissionRequest.RESOURCE_VIDEO_CAPTURE,
      });
      else pending.deny();
      pending = null;
    }
  }

  public void onBackPressed() {
    new android.app.AlertDialog.Builder(this)
      .setMessage("Zamknąć hosta i zatrzymać model?")
      .setNegativeButton("Wróć", null)
      .setPositiveButton("Zatrzymaj", (d, i) -> finish())
      .show();
  }

  protected void onDestroy() {
    closed = true;
    if (host != null) host.destroy();
    if (view != null) view.destroy();
    super.onDestroy();
  }
}
