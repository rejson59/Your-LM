package pl.yourlm.node;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageInfo;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;
import java.io.*;
import java.net.HttpURLConnection;
import java.net.URL;
import java.security.MessageDigest;
import org.json.JSONArray;
import org.json.JSONObject;

/** Checks GitHub releases and hands a downloaded APK to Android's installer. */
final class AppUpdater {

  static final String API =
    "https://api.github.com/repos/rejson59/Your-LM/releases?per_page=12";
  static final String AUTHORITY = "pl.yourlm.node.updates";

  static final class State {
    String current = "";
    String latest = "";
    String message = "Aktualizacje są sprawdzane automatycznie przez GitHub.";
    String releaseURL = "";
    String assetURL = "";
    String digest = "";
    boolean checking;
    boolean downloading;
    boolean available;
    boolean ready;
  }

  interface Listener {
    void changed();
  }

  final Activity activity;
  final Listener listener;
  final State state = new State();
  volatile boolean pendingInstall;

  AppUpdater(Activity activity, Listener listener) {
    this.activity = activity;
    this.listener = listener;
    try {
      PackageInfo info = activity
        .getPackageManager()
        .getPackageInfo(activity.getPackageName(), 0);
      state.current = info.versionName;
    } catch (Exception e) {
      state.current = "0.0.0";
    }
  }

  void notifyChanged() {
    activity.runOnUiThread(() -> listener.changed());
  }

  static int[] numbers(String version) {
    String main = version.replaceFirst("^[vV]", "").split("-", 2)[0];
    String[] values = main.split("\\.");
    int[] out = new int[] { 0, 0, 0 };
    for (int i = 0; i < out.length && i < values.length; i++) {
      try {
        out[i] = Integer.parseInt(values[i]);
      } catch (Exception ignored) {}
    }
    return out;
  }

  static String suffix(String version) {
    int separator = version.indexOf('-');
    return separator < 0 ? "" : version.substring(separator + 1);
  }

  static int trailingNumber(String value) {
    int start = value.length();
    while (start > 0 && Character.isDigit(value.charAt(start - 1))) start--;
    if (start == value.length()) return -1;
    try {
      return Integer.parseInt(value.substring(start));
    } catch (Exception ignored) {
      return -1;
    }
  }

  static String numberPrefix(String value) {
    int start = value.length();
    while (start > 0 && Character.isDigit(value.charAt(start - 1))) start--;
    return value.substring(0, start);
  }

  static boolean newer(String candidate, String current) {
    int[] a = numbers(candidate), b = numbers(current);
    for (int i = 0; i < a.length; i++) {
      if (a[i] != b[i]) return a[i] > b[i];
    }
    String candidateSuffix = suffix(candidate);
    String currentSuffix = suffix(current);
    if (candidateSuffix.equals(currentSuffix)) return false;
    if (candidateSuffix.isEmpty()) return !currentSuffix.isEmpty();
    if (currentSuffix.isEmpty()) return false;
    int candidateNumber = trailingNumber(candidateSuffix);
    int currentNumber = trailingNumber(currentSuffix);
    if (
      candidateNumber >= 0 &&
      currentNumber >= 0 &&
      numberPrefix(candidateSuffix).equals(numberPrefix(currentSuffix))
    ) return candidateNumber > currentNumber;
    return candidateSuffix.compareTo(currentSuffix) > 0;
  }

  static String readText(HttpURLConnection connection, int limit)
    throws IOException {
    try (InputStream in = connection.getInputStream()) {
      ByteArrayOutputStream out = new ByteArrayOutputStream();
      byte[] buffer = new byte[16384];
      int total = 0, n;
      while ((n = in.read(buffer)) != -1) {
        total += n;
        if (total > limit) throw new IOException("Odpowiedź jest zbyt duża");
        out.write(buffer, 0, n);
      }
      return out.toString("UTF-8");
    }
  }

  static HttpURLConnection connection(String target) throws IOException {
    HttpURLConnection connection = (HttpURLConnection) new URL(target)
      .openConnection();
    connection.setConnectTimeout(15000);
    connection.setReadTimeout(30000);
    connection.setInstanceFollowRedirects(true);
    connection.setRequestProperty("Accept", "application/vnd.github+json");
    connection.setRequestProperty("User-Agent", "YourLM-Android");
    return connection;
  }

  void check() {
    if (state.checking || state.downloading) return;
    state.checking = true;
    state.message = "Sprawdzanie nowych wydań…";
    notifyChanged();
    new Thread(
      () -> {
        try {
          HttpURLConnection http = connection(API);
          if (http.getResponseCode() != 200) throw new IOException(
            "GitHub odpowiedział kodem " + http.getResponseCode()
          );
          JSONArray releases = new JSONArray(readText(http, 3 * 1024 * 1024));
          JSONObject selected = null;
          String apk = "";
          String apkDigest = "";
          boolean newerReleaseSeen = false;
          boolean allowPrerelease = state.current.contains("-");
          for (int i = 0; i < releases.length(); i++) {
            JSONObject release = releases.getJSONObject(i);
            if (
              release.optBoolean("draft") ||
              (release.optBoolean("prerelease") && !allowPrerelease)
            ) continue;
            String tag = release.optString("tag_name");
            if (!newer(tag, state.current)) continue;
            newerReleaseSeen = true;
            JSONArray assets = release.optJSONArray("assets");
            if (assets == null) continue;
            for (int j = 0; j < assets.length(); j++) {
              JSONObject asset = assets.getJSONObject(j);
              if ("YourLM-Android-arm64.apk".equals(asset.optString("name"))) {
                String candidateURL = asset.optString("browser_download_url");
                String candidateDigest = asset.optString("digest");
                if (
                  !candidateURL.isEmpty() && candidateDigest.startsWith("sha256:")
                ) {
                  apk = candidateURL;
                  apkDigest = candidateDigest;
                }
                break;
              }
            }
            if (!apk.isEmpty()) {
              selected = release;
              break;
            }
          }
          if (selected == null) {
            state.available = false;
            state.latest = state.current;
            state.releaseURL = "";
            state.assetURL = "";
            state.digest = "";
            state.message = newerReleaseSeen
              ? "Połączono z GitHub. Nowsze wydanie nie zawiera zweryfikowanego APK dla tego telefonu."
              : "Połączono z GitHub. Wersja " + state.current + " jest obecnie najnowszym opublikowanym wydaniem.";
          } else {
            String next = selected
              .optString("tag_name")
              .replaceFirst("^[vV]", "");
            if (!next.equals(state.latest)) state.ready = false;
            state.latest = next;
            state.releaseURL = selected.optString("html_url");
            state.assetURL = apk;
            state.digest = apkDigest;
            state.available = true;
            state.message = "Połączono z GitHub. Dostępna jest wersja " + state.latest + ".";
          }
        } catch (Exception e) {
          state.message = "Nie udało się sprawdzić aktualizacji: " + e.getMessage();
        } finally {
          state.checking = false;
          notifyChanged();
        }
      },
      "YourLM-update-check"
    ).start();
  }

  File updateFile() {
    return new File(new File(activity.getCacheDir(), "updates"), ApkProvider.FILE_NAME);
  }

  static String hex(byte[] data) {
    StringBuilder out = new StringBuilder(data.length * 2);
    for (byte value : data) out.append(String.format("%02x", value & 0xff));
    return out.toString();
  }

  void downloadAndInstall() {
    if (state.downloading) return;
    if (state.ready && updateFile().isFile()) {
      requestInstall();
      return;
    }
    if (state.assetURL.isEmpty()) {
      check();
      return;
    }
    state.downloading = true;
    state.message = "Pobieranie APK " + state.latest + "…";
    notifyChanged();
    new Thread(
      () -> {
        File tmp = null;
        try {
          HttpURLConnection http = connection(state.assetURL);
          if (http.getResponseCode() != 200) throw new IOException(
            "Pobieranie zwróciło kod " + http.getResponseCode()
          );
          if (http.getContentLengthLong() > 200L * 1024 * 1024) {
            throw new IOException("APK jest zbyt duży");
          }
          File target = updateFile();
          target.getParentFile().mkdirs();
          tmp = new File(target.getParentFile(), "YourLM-update.part.apk");
          MessageDigest sha256 = MessageDigest.getInstance("SHA-256");
          try (
            InputStream in = http.getInputStream();
            OutputStream out = new FileOutputStream(tmp)
          ) {
            byte[] data = new byte[131072];
            long total = 0;
            int n;
            while ((n = in.read(data)) != -1) {
              total += n;
              if (total > 200L * 1024 * 1024) throw new IOException(
                "APK jest zbyt duży"
              );
              sha256.update(data, 0, n);
              out.write(data, 0, n);
            }
          }
          String got = hex(sha256.digest());
          if (
            state.digest.startsWith("sha256:") &&
            !got.equalsIgnoreCase(state.digest.substring(7))
          ) throw new IOException("Suma APK nie zgadza się z wydaniem GitHub");
          PackageInfo archive = activity
            .getPackageManager()
            .getPackageArchiveInfo(tmp.getAbsolutePath(), 0);
          if (
            archive == null ||
            archive.versionName == null ||
            !activity.getPackageName().equals(archive.packageName) ||
            !newer(archive.versionName, state.current)
          ) throw new IOException("Pobrany plik nie jest nowszym APK YourLM");
          if (target.exists() && !target.delete()) throw new IOException(
            "Nie można zastąpić poprzedniego pobrania"
          );
          if (!tmp.renameTo(target)) throw new IOException("Nie można zapisać APK");
          state.ready = true;
          state.message = "APK pobrany. Android poprosi o potwierdzenie aktualizacji.";
          activity.runOnUiThread(this::requestInstall);
        } catch (Exception e) {
          state.message = "Aktualizacja nie została pobrana: " + e.getMessage();
          if (tmp != null) tmp.delete();
        } finally {
          state.downloading = false;
          notifyChanged();
        }
      },
      "YourLM-update-download"
    ).start();
  }

  void requestInstall() {
    File file = updateFile();
    if (!file.isFile()) {
      state.ready = false;
      state.message = "Brak pobranego APK.";
      notifyChanged();
      return;
    }
    if (
      Build.VERSION.SDK_INT >= 26 &&
      !activity.getPackageManager().canRequestPackageInstalls()
    ) {
      pendingInstall = true;
      state.message = "Zezwól YourLM instalować aktualizacje, a potem wróć do aplikacji.";
      Intent settings = new Intent(
        Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
        Uri.parse("package:" + activity.getPackageName())
      );
      activity.startActivity(settings);
      notifyChanged();
      return;
    }
    pendingInstall = false;
    Uri uri = Uri.parse("content://" + AUTHORITY + "/" + ApkProvider.FILE_NAME);
    Intent install = new Intent(Intent.ACTION_VIEW);
    install.setDataAndType(uri, "application/vnd.android.package-archive");
    install.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK);
    activity.startActivity(install);
  }

  void resumeInstall() {
    if (
      pendingInstall &&
      (Build.VERSION.SDK_INT < 26 || activity.getPackageManager().canRequestPackageInstalls())
    ) requestInstall();
  }
}
