package pl.yourlm.node;

import android.app.*;
import android.content.*;
import android.database.Cursor;
import android.graphics.*;
import android.net.Uri;
import android.os.*;
import android.provider.OpenableColumns;
import android.view.*;
import android.widget.*;
import io.nayuki.qrcodegen.QrCode;
import java.io.*;
import java.util.Locale;

public final class NodeActivity extends Activity {

  final int ink = 0xff101d19,
    mint = 0xff98ecc1,
    muted = 0xffa8bab1;
  TextView status, memory;
  Button toggle, copy;
  ImageView qr;
  Worker worker;
  boolean busy, dead;
  LinearLayout layout;

  int dp(int n) {
    return (int) (getResources().getDisplayMetrics().density * n + .5f);
  }

  TextView text(String s, int sp, int color) {
    TextView t = new TextView(this);
    t.setText(s);
    t.setTextSize(sp);
    t.setTextColor(color);
    t.setPadding(0, dp(9), 0, dp(9));
    layout.addView(t);
    return t;
  }

  Button button(String s) {
    Button b = new Button(this);
    b.setText(s);
    b.setAllCaps(false);
    layout.addView(b);
    return b;
  }

  public void onCreate(Bundle b) {
    super.onCreate(b);
    ScrollView scroll = new ScrollView(this);
    scroll.setBackgroundColor(ink);
    layout = new LinearLayout(this);
    layout.setOrientation(1);
    layout.setPadding(dp(24), dp(22), dp(24), dp(24));
    scroll.addView(layout);
    setContentView(scroll);
    text("YOUR LM   /   NODE 01", 13, mint).setLetterSpacing(.14f);
    text("Węzeł obliczeniowy", 31, -1);
    text(Build.MANUFACTURER + "  /  " + Build.MODEL, 16, muted);
    memory = text("", 15, mint);
    updateMemory();
    text(
      "Udostępnij CPU w prywatnej sieci Wi-Fi. Model GGUF wybierasz na laptopie; nie kopiuj go na telefon.",
      16,
      muted
    );
    toggle = button("Uruchom worker");
    toggle.setOnClickListener(v -> {
      if (worker == null) startNode();
      else stopNode();
    });
    status = text("Wyłączony · brak otwartych połączeń", 15, mint);
    qr = new ImageView(this);
    layout.addView(qr, new LinearLayout.LayoutParams(-1, dp(260)));
    qr.setVisibility(View.GONE);
    copy = button("Kopiuj kod parowania");
    copy.setVisibility(View.GONE);
    copy.setOnClickListener(v -> {
      if (worker != null) {
        (
          (android.content.ClipboardManager) getSystemService(CLIPBOARD_SERVICE)
        ).setPrimaryClip(
          ClipData.newPlainText("YourLM — prywatny kod sesji", worker.code)
        );
        Toast.makeText(
          this,
          "Skopiowano prywatny kod sesji",
          Toast.LENGTH_SHORT
        ).show();
      }
    });
    text("HOST / SAMODZIELNY CHAT", 13, mint);
    text(
      "Aby uruchomić model na telefonie, zaimportuj GGUF do prywatnej biblioteki. Potrzebne jest miejsce na drugą kopię pliku.",
      15,
      muted
    );
    button("Importuj plik GGUF").setOnClickListener(v -> {
      if (busy || worker != null) {
        Toast.makeText(
          this,
          "Zatrzymaj worker lub poczekaj na import",
          0
        ).show();
        return;
      }
      Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
      i.addCategory(Intent.CATEGORY_OPENABLE);
      i.setType("*/*");
      startActivityForResult(i, 17);
    });
    button("Otwórz konsolę hosta").setOnClickListener(v -> {
      if (worker == null && !busy) startActivity(
        new Intent(this, HostActivity.class)
      );
      else Toast.makeText(
        this,
        "Zatrzymaj worker lub poczekaj na import",
        0
      ).show();
    });
  }

  void importModel(Uri uri) {
    new Thread(() -> {
      File tmp = null;
      try {
        String name = "model.gguf";
        try (
          Cursor cur = getContentResolver().query(uri, null, null, null, null)
        ) {
          if (cur != null && cur.moveToFirst()) {
            int col = cur.getColumnIndex(OpenableColumns.DISPLAY_NAME);
            if (col >= 0) name = cur.getString(col);
          }
        }
        if (
          name == null || !name.toLowerCase(Locale.ROOT).endsWith(".gguf")
        ) throw new IOException("Wybierz plik .gguf");
        File dir = new File(getFilesDir(), "models");
        dir.mkdirs();
        File target = new File(dir, new File(name).getName());
        if (target.exists()) throw new IOException(
          "Model o tej nazwie już istnieje"
        );
        tmp = File.createTempFile("import-", ".part", dir);
        try (
          InputStream in = getContentResolver().openInputStream(uri);
          OutputStream out = new FileOutputStream(tmp)
        ) {
          byte[] b = new byte[131072];
          int n;
          while ((n = in.read(b)) != -1) {
            if (dead) throw new IOException("Anulowano");
            out.write(b, 0, n);
          }
        }
        if (!tmp.renameTo(target)) throw new IOException("Błąd zapisu");
        runOnUiThread(() ->
          status.setText(
            "Model dodany. Otwórz konsolę i wybierz go z biblioteki."
          )
        );
      } catch (Exception e) {
        runOnUiThread(() -> status.setText("Import: " + e.getMessage()));
      } finally {
        if (tmp != null) tmp.delete();
        runOnUiThread(() -> {
          busy = false;
          toggle.setEnabled(true);
        });
      }
    }).start();
  }

  protected void onActivityResult(int req, int result, Intent data) {
    super.onActivityResult(req, result, data);
    if (
      req == 17 && result == RESULT_OK && data != null && data.getData() != null
    ) {
      busy = true;
      toggle.setEnabled(false);
      status.setText("Kopiowanie do prywatnej biblioteki…");
      importModel(data.getData());
    }
  }

  void updateMemory() {
    ActivityManager.MemoryInfo m = new ActivityManager.MemoryInfo();
    ((ActivityManager) getSystemService(ACTIVITY_SERVICE)).getMemoryInfo(m);
    memory.setText(
      String.format(
        Locale.getDefault(),
        "%.1f GiB RAM  ·  %.1f GiB dostępne",
        m.totalMem / 1073741824.,
        m.availMem / 1073741824.
      )
    );
  }

  void startNode() {
    if (busy) return;
    busy = true;
    toggle.setEnabled(false);
    status.setText("Uruchamianie silnika…");
    Worker w = new Worker(this);
    worker = w;
    new Thread(() -> {
      try {
        w.start();
        QrCode q = QrCode.encodeText(w.code, QrCode.Ecc.MEDIUM);
        int s = (q.size + 8) * 6;
        Bitmap image = Bitmap.createBitmap(s, s, Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(image);
        canvas.drawColor(-1);
        Paint p = new Paint();
        p.setColor(0xff101d19);
        for (int y = 0; y < q.size; y++) for (int x = 0; x < q.size; x++) if (
          q.getModule(x, y)
        ) canvas.drawRect(
          (x + 4) * 6,
          (y + 4) * 6,
          (x + 5) * 6,
          (y + 5) * 6,
          p
        );
        runOnUiThread(() -> {
          if (dead || worker != w) {
            w.close();
            return;
          }
          busy = false;
          toggle.setEnabled(true);
          toggle.setText("Zatrzymaj worker");
          getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
          status.setText(
            "Gotowy · zeskanuj kod w panelu hosta. Pozostaw aplikację na ekranie. Nie udostępniaj kodu innym."
          );
          qr.setImageBitmap(image);
          qr.setVisibility(View.VISIBLE);
          copy.setVisibility(View.VISIBLE);
          updateMemory();
        });
        w.listen();
      } catch (Exception e) {
        runOnUiThread(() -> {
          if (worker == w && !dead) {
            stopNode();
            status.setText("Worker zatrzymany: " + e.getMessage());
          }
        });
      } finally {
        w.close();
      }
    }).start();
  }

  void stopNode() {
    Worker old = worker;
    worker = null;
    if (old != null) old.close();
    busy = false;
    toggle.setEnabled(true);
    toggle.setText("Uruchom worker");
    qr.setImageDrawable(null);
    qr.setVisibility(View.GONE);
    copy.setVisibility(View.GONE);
    status.setText("Wyłączony · kod sesji wygasł");
    getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
    updateMemory();
  }

  protected void onDestroy() {
    dead = true;
    stopNode();
    super.onDestroy();
  }
}
