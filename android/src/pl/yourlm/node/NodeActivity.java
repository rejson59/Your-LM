package pl.yourlm.node;

import android.app.*;
import android.content.*;
import android.content.res.ColorStateList;
import android.database.Cursor;
import android.graphics.*;
import android.graphics.drawable.GradientDrawable;
import android.net.Uri;
import android.os.*;
import android.provider.OpenableColumns;
import android.provider.Settings;
import android.view.*;
import android.widget.*;
import io.nayuki.qrcodegen.QrCode;
import java.io.*;
import java.util.Locale;

/** Finished native launcher: choose a role first, then follow one guided flow. */
public final class NodeActivity extends Activity {

  static final int BG = 0xfff4f6f2;
  static final int SURFACE = 0xffffffff;
  static final int INK = 0xff14251e;
  static final int MUTED = 0xff637269;
  static final int GREEN = 0xff176b4b;
  static final int GREEN_DARK = 0xff0e5038;
  static final int MINT = 0xffdff5e8;
  static final int LINE = 0xffdce4dd;
  static final int RED = 0xff9a3328;
  static final int RED_BG = 0xffffebe8;
  static final int AMBER_BG = 0xfffff3d8;

  static final int HOME = 0, WORKER = 1, PHONE = 2;
  final int importRequest = 17;
  final int notificationRequest = 18;
  LinearLayout page;
  boolean busy, dead;
  int screen = HOME;
  int workerRevision = -1;
  TextView phoneStatus;
  AppUpdater updater;
  final Handler ui = new Handler(Looper.getMainLooper());
  final Runnable workerUpdates = new Runnable() {
    public void run() {
      if (dead) return;
      WorkerService.Snapshot snapshot = WorkerService.snapshot();
      if (snapshot.revision != workerRevision) {
        workerRevision = snapshot.revision;
        if (screen == WORKER) showWorker();
        else if (screen == HOME) showHome();
      }
      ui.postDelayed(this, 500);
    }
  };

  int dp(int n) {
    return (int) (getResources().getDisplayMetrics().density * n + .5f);
  }

  GradientDrawable shape(int color, int radius, int strokeColor) {
    GradientDrawable d = new GradientDrawable();
    d.setColor(color);
    d.setCornerRadius(dp(radius));
    if (strokeColor != 0) d.setStroke(dp(1), strokeColor);
    return d;
  }

  LinearLayout.LayoutParams lp(int width, int height, int top, int bottom) {
    LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(width, height);
    p.topMargin = dp(top);
    p.bottomMargin = dp(bottom);
    return p;
  }

  TextView label(String value) {
    TextView t = new TextView(this);
    t.setText(value);
    t.setTextColor(GREEN);
    t.setTextSize(12);
    t.setTypeface(android.graphics.Typeface.DEFAULT_BOLD);
    t.setLetterSpacing(.11f);
    return t;
  }

  TextView text(String value, float size, int color, boolean bold) {
    TextView t = new TextView(this);
    t.setText(value);
    t.setTextSize(size);
    t.setTextColor(color);
    t.setLineSpacing(0, 1.08f);
    if (bold) t.setTypeface(android.graphics.Typeface.DEFAULT_BOLD);
    return t;
  }

  Button button(String value, boolean primary) {
    Button b = new Button(this);
    b.setText(value);
    b.setAllCaps(false);
    b.setTextSize(15);
    b.setTypeface(android.graphics.Typeface.DEFAULT_BOLD);
    b.setTextColor(primary ? Color.WHITE : GREEN_DARK);
    b.setBackgroundTintList(
      ColorStateList.valueOf(primary ? GREEN : 0xffe8f1eb)
    );
    b.setMinHeight(dp(52));
    b.setPadding(dp(18), 0, dp(18), 0);
    return b;
  }

  LinearLayout card(int color, int stroke) {
    LinearLayout c = new LinearLayout(this);
    c.setOrientation(LinearLayout.VERTICAL);
    c.setPadding(dp(20), dp(20), dp(20), dp(20));
    c.setBackground(shape(color, 18, stroke));
    c.setElevation(dp(1));
    return c;
  }

  void resetPage() {
    page.removeAllViews();
    page.setPadding(dp(20), dp(18), dp(20), dp(34));
  }

  public void onCreate(Bundle state) {
    super.onCreate(state);
    getWindow().setStatusBarColor(BG);
    getWindow().getDecorView().setSystemUiVisibility(
      View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR
    );
    ScrollView scroll = new ScrollView(this);
    scroll.setFillViewport(true);
    scroll.setBackgroundColor(BG);
    page = new LinearLayout(this);
    page.setOrientation(LinearLayout.VERTICAL);
    scroll.addView(page, new ScrollView.LayoutParams(-1, -2));
    setContentView(scroll);
    updater = new AppUpdater(this, () -> {
      if (!dead && screen == HOME) showHome();
    });
    showHome();
    updater.check();
    ui.post(workerUpdates);
  }

  void topBar(boolean back, String section) {
    LinearLayout row = new LinearLayout(this);
    row.setGravity(Gravity.CENTER_VERTICAL);
    row.setOrientation(LinearLayout.HORIZONTAL);
    if (back) {
      Button b = button("‹", false);
      b.setTextSize(28);
      b.setContentDescription("Wróć");
      b.setMinWidth(dp(48));
      b.setOnClickListener(v -> onBackPressed());
      row.addView(b, new LinearLayout.LayoutParams(dp(48), dp(48)));
    } else {
      TextView mark = text("Y", 20, Color.WHITE, true);
      mark.setGravity(Gravity.CENTER);
      mark.setBackground(shape(GREEN_DARK, 13, 0));
      row.addView(mark, new LinearLayout.LayoutParams(dp(44), dp(44)));
    }
    LinearLayout names = new LinearLayout(this);
    names.setOrientation(LinearLayout.VERTICAL);
    names.setPadding(dp(12), 0, 0, 0);
    names.addView(text("YourLM", 20, INK, true));
    names.addView(text(section, 12, MUTED, false));
    row.addView(names, new LinearLayout.LayoutParams(0, -2, 1));
    TextView local = text("LOKALNIE", 11, GREEN_DARK, true);
    local.setGravity(Gravity.CENTER);
    local.setPadding(dp(11), dp(6), dp(11), dp(6));
    local.setBackground(shape(MINT, 20, 0));
    local.setContentDescription(
      "Model działa lokalnie. Internet jest używany tylko do aktualizacji."
    );
    local.setOnClickListener(v -> Toast.makeText(
      this,
      "Model działa lokalnie. Internet jest używany tylko do aktualizacji.",
      Toast.LENGTH_LONG
    ).show());
    row.addView(local);
    page.addView(row, lp(-1, -2, 0, 30));
  }

  void showHome() {
    screen = HOME;
    resetPage();
    topBar(false, "Prywatna AI na Twoim sprzęcie");
    page.addView(label("WYBIERZ ROLĘ TELEFONU"));
    TextView h = text("Jak chcesz użyć tego urządzenia?", 31, INK, true);
    h.setLetterSpacing(-.035f);
    page.addView(h, lp(-1, -2, 7, 8));
    page.addView(
      text(
        "Oba tryby działają lokalnie. Nic nie jest wysyłane do chmury.",
        16,
        MUTED,
        false
      ),
      lp(-1, -2, 0, 22)
    );

    WorkerService.Snapshot workerState = WorkerService.snapshot();
    LinearLayout workerCard = roleCard(
      "01",
      "Pomóż laptopowi",
      workerState.phase == WorkerService.RUNNING
        ? "Worker działa w tle"
        : workerState.phase == WorkerService.RECOVERING
          ? "Worker odtwarza silnik"
          : "Telefon jako worker",
      workerState.phase == WorkerService.RUNNING
        ? "Telefon nadal oblicza warstwy dla laptopa. Możesz wyłączyć ekran lub zamknąć ten widok."
        : workerState.phase == WorkerService.RECOVERING
          ? "Po wyjściu procesu natywnego zachowaliśmy diagnostykę i wykonujemy ograniczoną próbę odtworzenia."
          : "Laptop przechowuje model GGUF, a telefon oblicza część warstw przez prywatne Wi-Fi.",
      workerState.phase == WorkerService.RUNNING ||
        workerState.phase == WorkerService.RECOVERING
        ? "Dotknij, aby zobaczyć adres, stan lub zatrzymać"
        : "Najlepszy wybór dla Lenovo + Redmi",
      true
    );
    workerCard.setOnClickListener(v -> showWorker());
    page.addView(workerCard, lp(-1, -2, 0, 14));

    LinearLayout phoneCard = roleCard(
      "02",
      "Uruchom model tutaj",
      "Telefon jako host",
      "Zaimportuj mniejszy model GGUF i otwórz czat lub lokalne API bez laptopa.",
      "Model zajmie miejsce w pamięci telefonu",
      false
    );
    phoneCard.setOnClickListener(v -> showPhone());
    page.addView(phoneCard, lp(-1, -2, 0, 22));

    LinearLayout hardware = card(SURFACE, LINE);
    hardware.addView(label("TEN TELEFON"));
    hardware.addView(
      text(Build.MANUFACTURER + " " + Build.MODEL, 18, INK, true),
      lp(-1, -2, 5, 2)
    );
    hardware.addView(
      text(memoryLine() + "  •  " + Runtime.getRuntime().availableProcessors() + " rdzeni logicznych", 14, MUTED, false)
    );
    hardware.addView(
      text(
        "Pokazujemy fizyczną pamięć zgłaszaną przez Androida. Rozszerzenie „8+8” w pamięci flash nie jest doliczane do budżetu modelu.",
        13,
        MUTED,
        false
      ),
      lp(-1, -2, 10, 0)
    );
    page.addView(hardware, lp(-1, -2, 0, 14));
    drawUpdateCard();
  }

  void drawUpdateCard() {
    AppUpdater.State update = updater.state;
    LinearLayout card = card(
      update.available ? 0xfff0faf4 : SURFACE,
      update.available ? 0xff9fcdb4 : LINE
    );
    card.addView(label("AKTUALIZACJE APLIKACJI"));
    card.addView(
      text(
        update.available
          ? "Dostępny YourLM " + update.latest
          : "YourLM " + update.current,
        20,
        INK,
        true
      ),
      lp(-1, -2, 7, 3)
    );
    card.addView(text(update.message, 13, MUTED, false));
    Button action = button(
      update.downloading
        ? "Pobieranie…"
        : update.ready
          ? "Zainstaluj pobraną aktualizację"
          : update.available
            ? "Pobierz i zaktualizuj"
            : update.checking
              ? "Sprawdzanie…"
              : "Sprawdź aktualizacje",
      update.available || update.ready
    );
    action.setEnabled(!update.checking && !update.downloading);
    action.setOnClickListener(v -> {
      if (update.available || update.ready) updater.downloadAndInstall();
      else updater.check();
    });
    card.addView(action, lp(-1, dp(52), 14, 0));
    if (!update.releaseURL.isEmpty()) {
      Button notes = button("Zobacz informacje o wydaniu", false);
      notes.setOnClickListener(v -> startActivity(
        new Intent(Intent.ACTION_VIEW, Uri.parse(update.releaseURL))
      ));
      card.addView(notes, lp(-1, dp(48), 8, 0));
    }
    page.addView(card);
  }

  LinearLayout roleCard(
    String number,
    String eyebrow,
    String title,
    String body,
    String foot,
    boolean recommended
  ) {
    LinearLayout c = card(recommended ? 0xfff7fcf8 : SURFACE, recommended ? 0xff9fcdb4 : LINE);
    LinearLayout head = new LinearLayout(this);
    head.setGravity(Gravity.CENTER_VERTICAL);
    TextView num = text(number, 13, GREEN_DARK, true);
    num.setGravity(Gravity.CENTER);
    num.setBackground(shape(MINT, 12, 0));
    head.addView(num, new LinearLayout.LayoutParams(dp(42), dp(42)));
    TextView e = text(eyebrow.toUpperCase(Locale.ROOT), 12, GREEN, true);
    e.setPadding(dp(12), 0, 0, 0);
    head.addView(e, new LinearLayout.LayoutParams(0, -2, 1));
    if (recommended) {
      TextView badge = text("POLECANE", 10, GREEN_DARK, true);
      badge.setPadding(dp(9), dp(5), dp(9), dp(5));
      badge.setBackground(shape(MINT, 14, 0));
      head.addView(badge);
    }
    c.addView(head);
    c.addView(text(title, 23, INK, true), lp(-1, -2, 17, 5));
    c.addView(text(body, 15, MUTED, false));
    TextView footer = text(foot + "   →", 13, GREEN_DARK, true);
    c.addView(footer, lp(-1, -2, 17, 0));
    c.setClickable(true);
    c.setFocusable(true);
    return c;
  }

  String memoryLine() {
    ActivityManager.MemoryInfo m = new ActivityManager.MemoryInfo();
    ((ActivityManager) getSystemService(ACTIVITY_SERVICE)).getMemoryInfo(m);
    return String.format(
      Locale.getDefault(),
      "%.1f GiB fizyczne • %.1f GiB system i aplikacje • %.1f GiB dostępne teraz",
      m.totalMem / 1073741824.0,
      (m.totalMem - m.availMem) / 1073741824.0,
      m.availMem / 1073741824.0
    );
  }

  void showWorker() {
    screen = WORKER;
    resetPage();
    topBar(true, "Telefon jako worker");
    page.addView(label("LENOVO + REDMI / TA SAMA SIEĆ WI-FI"));
    TextView h = text("Dodaj moc telefonu do laptopa", 29, INK, true);
    h.setLetterSpacing(-.03f);
    page.addView(h, lp(-1, -2, 7, 7));
    page.addView(
      text(
        "Plik GGUF zostaje na laptopie. Telefon może zachować prywatną pamięć podręczną przydzielonych wag. Jeśli kabel z autoryzowanym debugowaniem ADB pozostanie podłączony, Windows porówna szyfrowane USB z Wi-Fi i wybierze szybsze.",
        15,
        MUTED,
        false
      ),
      lp(-1, -2, 0, 18)
    );

    WorkerService.Snapshot snapshot = WorkerService.snapshot();
    workerRevision = snapshot.revision;
    if (
      (snapshot.phase == WorkerService.RUNNING ||
        snapshot.phase == WorkerService.RECOVERING) &&
      snapshot.worker != null
    ) {
      drawWorkerRunning(snapshot);
    } else {
      drawWorkerStopped(snapshot);
    }
  }

  void drawWorkerStopped(WorkerService.Snapshot snapshot) {
    boolean failed = snapshot.phase == WorkerService.FAILED;
    boolean starting = snapshot.phase == WorkerService.STARTING;
    boolean stopping = snapshot.phase == WorkerService.STOPPING;
    LinearLayout status = card(
      failed ? RED_BG : SURFACE,
      failed ? 0xffffb7ae : LINE
    );
    String heading = failed
      ? "Nie udało się uruchomić workera"
      : starting
        ? "Uruchamianie workera w tle…"
        : stopping
          ? "Zatrzymywanie workera…"
          : "Worker jest wyłączony";
    status.addView(text(heading, 20, failed ? RED : INK, true));
    status.addView(
      text(
        failed
          ? snapshot.error
          : starting
            ? "Przygotowujemy silnik, blokadę CPU i szybkie połączenie Wi-Fi."
            : stopping
              ? "Kończymy aktywne połączenia i zwalniamy zasoby telefonu."
              : memoryLine(),
        14,
        failed ? 0xff74281f : MUTED,
        false
      ),
      lp(-1, -2, 7, 0)
    );
    if (failed && !snapshot.error.isEmpty()) {
      Button copyLog = button("Kopiuj diagnostykę", false);
      copyLog.setOnClickListener(
        v ->
          copy(
            "Diagnostyka YourLM",
            snapshot.error +
            (snapshot.worker == null
              ? ""
              : "\n\n" + snapshot.worker.diagnosticReport())
          )
      );
      status.addView(copyLog, lp(-1, dp(50), 14, 0));
    }
    page.addView(status, lp(-1, -2, 0, 14));
    addMemoryTools();

    LinearLayout steps = card(SURFACE, LINE);
    steps.addView(label("TRZY KROKI"));
    addStep(steps, "1", "Połącz laptop i telefon z tym samym prywatnym Wi-Fi.");
    addStep(steps, "2", "Uruchom worker. Opcjonalnie pozostaw kabel ADB — host sam zmierzy USB i Wi-Fi.");
    addStep(steps, "3", "Na laptopie wybierz „Dodaj telefon” i przepisz adres oraz kod.");
    page.addView(steps, lp(-1, -2, 0, 14));

    Button start = button(starting ? "Uruchamianie…" : "Uruchom worker w tle", true);
    start.setEnabled(!starting && !stopping);
    start.setOnClickListener(v -> startWorker());
    page.addView(start, lp(-1, dp(56), 0, 10));
    page.addView(
      text(
        "Stałe powiadomienie pozwala zatrzymać worker. Android utrzyma CPU i Wi-Fi podczas obliczeń przy wyłączonym ekranie.",
        12,
        MUTED,
        false
      )
    );
  }

  void addMemoryTools() {
    LinearLayout memory = card(SURFACE, LINE);
    memory.addView(label("PRZYGOTUJ PAMIĘĆ TELEFONU"));
    memory.addView(
      text("Zamknij ciężkie aplikacje przed dużym modelem", 19, INK, true),
      lp(-1, -2, 7, 3)
    );
    memory.addView(
      text(
        memoryLine() +
        ". Android bez roota nie pozwala YourLM niezawodnie zamknąć cudzych aplikacji. Użyj systemowego widoku aplikacji, zakończ gry, aparat i ciężkie przeglądarki, ale nie wymuszaj zatrzymania YourLM.",
        13,
        MUTED,
        false
      )
    );
    Button settings = button("Otwórz systemową listę aplikacji", false);
    settings.setOnClickListener(
      v -> {
        try {
          startActivity(new Intent(Settings.ACTION_MANAGE_APPLICATIONS_SETTINGS));
        } catch (Exception unavailable) {
          startActivity(new Intent(Settings.ACTION_SETTINGS));
        }
      }
    );
    memory.addView(settings, lp(-1, dp(50), 13, 0));
    Button refresh = button("Odśwież pomiar RAM", false);
    refresh.setOnClickListener(v -> {
      if (screen == PHONE) showPhone();
      else showWorker();
    });
    memory.addView(refresh, lp(-1, dp(48), 8, 0));
    page.addView(memory, lp(-1, -2, 0, 14));
  }

  void addStep(LinearLayout parent, String number, String value) {
    LinearLayout row = new LinearLayout(this);
    row.setGravity(Gravity.TOP);
    TextView n = text(number, 12, GREEN_DARK, true);
    n.setGravity(Gravity.CENTER);
    n.setBackground(shape(MINT, 20, 0));
    row.addView(n, new LinearLayout.LayoutParams(dp(30), dp(30)));
    TextView body = text(value, 14, INK, false);
    body.setPadding(dp(12), dp(4), 0, 0);
    row.addView(body, new LinearLayout.LayoutParams(0, -2, 1));
    parent.addView(row, lp(-1, -2, 14, 0));
  }

  void drawWorkerRunning(WorkerService.Snapshot snapshot) {
    Worker w = snapshot.worker;
    boolean recovering = snapshot.phase == WorkerService.RECOVERING;
    LinearLayout ready = card(recovering ? 0xff72520d : GREEN_DARK, 0);
    TextView online = text(
      recovering ? "●  ODTWARZANIE SILNIKA" : "●  WORKER GOTOWY",
      12,
      recovering ? 0xffffe09b : 0xffb9f3d2,
      true
    );
    online.setLetterSpacing(.08f);
    ready.addView(online);
    ready.addView(
      text(
        recovering
          ? "Połączenie diagnostyczne działa — trwa ograniczony restart"
          : "Działa w tle — ekran może być wyłączony",
        23,
        Color.WHITE,
        true
      ),
      lp(-1, -2, 8, 3)
    );
    ready.addView(
      text(
        recovering && !snapshot.error.isEmpty()
          ? snapshot.error
          : memoryLine() +
            (w.restartCount > 0
              ? " · odzyskania silnika: " + w.restartCount
              : ""),
        13,
        recovering ? 0xffffe6ac : 0xffc8ded3,
        false
      )
    );
    page.addView(ready, lp(-1, -2, 0, 14));
    addMemoryTools();

    LinearLayout manual = card(SURFACE, 0xff9fcdb4);
    manual.addView(label("NA LAPTOPIE: KLASTER → DODAJ TELEFON"));
    manual.addView(
      text("Przepisz te dwa pola", 22, INK, true),
      lp(-1, -2, 8, 3)
    );
    manual.addView(
      text(
        "Nie potrzebujesz kamery. Kod nie opuszcza Twojej sieci i pozostaje ważny po odtworzeniu usługi, dopóki nie wyczyścisz danych aplikacji.",
        14,
        MUTED,
        false
      )
    );
    manual.addView(text("ADRES PAROWANIA", 11, MUTED, true), lp(-1, -2, 18, 3));
    manual.addView(valueBox(w.pairAddress, false));
    Button copyAddress = button("Kopiuj adres", false);
    copyAddress.setOnClickListener(v -> copy("Adres parowania YourLM", w.pairAddress));
    manual.addView(copyAddress, lp(-1, dp(48), 8, 0));
    manual.addView(text("KOD PAROWANIA", 11, MUTED, true), lp(-1, -2, 18, 3));
    manual.addView(valueBox(w.manualKey, true));
    Button copyCode = button("Kopiuj kod", false);
    copyCode.setOnClickListener(v -> copy("Kod parowania YourLM", w.manualKey));
    manual.addView(copyCode, lp(-1, dp(48), 8, 0));
    page.addView(manual, lp(-1, -2, 0, 14));

    LinearLayout qrCard = card(SURFACE, LINE);
    qrCard.addView(label("ALTERNATYWA / QR"));
    qrCard.addView(
      text("Możesz zeskanować ten QR kamerą laptopa", 17, INK, true),
      lp(-1, -2, 7, 2)
    );
    qrCard.addView(
      text("Skaner znajduje się w oknie „Dodaj telefon” na hoście.", 13, MUTED, false)
    );
    ImageView image = new ImageView(this);
    image.setAdjustViewBounds(true);
    image.setContentDescription("Kod QR parowania workera");
    image.setImageBitmap(qrBitmap(w.code));
    LinearLayout.LayoutParams imageLp = lp(-1, dp(270), 14, 4);
    imageLp.gravity = Gravity.CENTER_HORIZONTAL;
    qrCard.addView(image, imageLp);
    page.addView(qrCard, lp(-1, -2, 0, 14));

    Button stop = button("Zatrzymaj worker", false);
    stop.setTextColor(RED);
    stop.setOnClickListener(v -> stopWorker(true));
    page.addView(stop, lp(-1, dp(54), 0, 0));
  }

  TextView valueBox(String value, boolean large) {
    TextView t = text(value, large ? 18 : 17, INK, true);
    t.setTypeface(android.graphics.Typeface.MONOSPACE, android.graphics.Typeface.BOLD);
    t.setTextIsSelectable(true);
    t.setGravity(Gravity.CENTER);
    t.setPadding(dp(10), dp(14), dp(10), dp(14));
    t.setBackground(shape(0xfff0f5f1, 11, LINE));
    return t;
  }

  Bitmap qrBitmap(String value) {
    QrCode q = QrCode.encodeText(value, QrCode.Ecc.MEDIUM);
    int scale = 6, size = (q.size + 8) * scale;
    Bitmap image = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888);
    Canvas canvas = new Canvas(image);
    canvas.drawColor(Color.WHITE);
    Paint p = new Paint();
    p.setColor(INK);
    for (int y = 0; y < q.size; y++) for (int x = 0; x < q.size; x++) if (
      q.getModule(x, y)
    ) canvas.drawRect(
      (x + 4) * scale,
      (y + 4) * scale,
      (x + 5) * scale,
      (y + 5) * scale,
      p
    );
    return image;
  }

  void startWorker() {
    if (busy || WorkerService.snapshot().active()) return;
    if (
      Build.VERSION.SDK_INT >= 33 &&
      checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) !=
        android.content.pm.PackageManager.PERMISSION_GRANTED
    ) {
      requestPermissions(
        new String[] { android.Manifest.permission.POST_NOTIFICATIONS },
        notificationRequest
      );
      return;
    }
    WorkerService.requestStart(getApplicationContext());
    showWorker();
  }

  public void onRequestPermissionsResult(
    int requestCode,
    String[] permissions,
    int[] results
  ) {
    super.onRequestPermissionsResult(requestCode, permissions, results);
    if (requestCode == notificationRequest) {
      // Android still permits a foreground task when notification permission is
      // declined, but granting it exposes the persistent one-tap stop action.
      WorkerService.requestStart(getApplicationContext());
      showWorker();
    }
  }

  void stopWorker(boolean redraw) {
    WorkerService.requestStop(getApplicationContext());
    if (redraw && !dead) showWorker();
  }

  void showPhone() {
    screen = PHONE;
    resetPage();
    topBar(true, "Model na telefonie");
    page.addView(label("SAMODZIELNY HOST / GGUF"));
    TextView h = text("Czat i API bez laptopa", 29, INK, true);
    h.setLetterSpacing(-.03f);
    page.addView(h, lp(-1, -2, 7, 7));
    page.addView(
      text(
        "Najpierw dodaj plik GGUF do prywatnej biblioteki aplikacji, potem otwórz konsolę.",
        15,
        MUTED,
        false
      ),
      lp(-1, -2, 0, 18)
    );
    addMemoryTools();

    LinearLayout library = card(SURFACE, LINE);
    library.addView(label("1 / BIBLIOTEKA MODELI"));
    File[] files = new File(getFilesDir(), "models").listFiles(
      f -> f.isFile() && f.getName().toLowerCase(Locale.ROOT).endsWith(".gguf")
    );
    int count = files == null ? 0 : files.length;
    library.addView(
      text(
        count == 0 ? "Brak modeli" : (count == 1 ? "1 model gotowy" : count + " modele gotowe"),
        21,
        INK,
        true
      ),
      lp(-1, -2, 7, 2)
    );
    library.addView(
      text(
        "Import tworzy prywatną kopię. Potrzebujesz wolnego miejsca na cały plik; na pierwszy test wybierz 3–4B Q4_K_M.",
        14,
        MUTED,
        false
      )
    );
    Button add = button(busy ? "Kopiowanie…" : "Dodaj plik GGUF", false);
    add.setEnabled(!busy && !WorkerService.snapshot().active());
    add.setOnClickListener(v -> chooseModel());
    library.addView(add, lp(-1, dp(52), 14, 0));
    page.addView(library, lp(-1, -2, 0, 14));

    LinearLayout console = card(0xfff7fcf8, 0xff9fcdb4);
    console.addView(label("2 / KONSOLA HOSTA"));
    console.addView(text("Wybierz model, uruchom czat lub API", 20, INK, true), lp(-1, -2, 7, 3));
    console.addView(
      text(
        "Konsola działa wyłącznie wewnątrz aplikacji. Może też sparować inne urządzenie jako worker.",
        14,
        MUTED,
        false
      )
    );
    Button open = button("Otwórz konsolę", true);
    open.setEnabled(!busy && !WorkerService.snapshot().active());
    open.setOnClickListener(v -> startActivity(new Intent(this, HostActivity.class)));
    console.addView(open, lp(-1, dp(54), 14, 0));
    page.addView(console, lp(-1, -2, 0, 14));

    phoneStatus = text("", 13, GREEN_DARK, false);
    page.addView(phoneStatus);
  }

  void chooseModel() {
    if (busy || WorkerService.snapshot().active()) return;
    Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
    i.addCategory(Intent.CATEGORY_OPENABLE);
    i.setType("*/*");
    startActivityForResult(i, importRequest);
  }

  void importModel(Uri uri) {
    new Thread(
      () -> {
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
          ) throw new IOException("Wybierz plik z rozszerzeniem .gguf");
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
            if (in == null) throw new IOException("Nie można otworzyć pliku");
            byte[] data = new byte[131072];
            int n;
            while ((n = in.read(data)) != -1) {
              if (dead) throw new IOException("Anulowano");
              out.write(data, 0, n);
            }
          }
          if (!tmp.renameTo(target)) throw new IOException("Błąd zapisu pliku");
          runOnUiThread(() -> {
            busy = false;
            showPhone();
            phoneStatus.setText("✓ Model dodany do prywatnej biblioteki.");
          });
        } catch (Exception e) {
          final String message = e.getMessage();
          runOnUiThread(() -> {
            busy = false;
            showPhone();
            phoneStatus.setText("Import nieudany: " + message);
            phoneStatus.setTextColor(RED);
          });
        } finally {
          if (tmp != null) tmp.delete();
        }
      },
      "YourLM-import"
    ).start();
  }

  protected void onActivityResult(int req, int result, Intent data) {
    super.onActivityResult(req, result, data);
    if (
      req == importRequest &&
      result == RESULT_OK &&
      data != null &&
      data.getData() != null
    ) {
      busy = true;
      showPhone();
      phoneStatus.setText("Kopiowanie modelu do prywatnej biblioteki…");
      importModel(data.getData());
    }
  }

  void copy(String label, String value) {
    ((android.content.ClipboardManager) getSystemService(CLIPBOARD_SERVICE))
      .setPrimaryClip(ClipData.newPlainText(label, value));
    Toast.makeText(this, "Skopiowano", Toast.LENGTH_SHORT).show();
  }

  public void onBackPressed() {
    if (screen != HOME) {
      // Leaving this screen must not stop an active foreground worker.
      showHome();
      return;
    }
    super.onBackPressed();
  }

  protected void onResume() {
    super.onResume();
    if (updater != null) updater.resumeInstall();
  }

  protected void onDestroy() {
    dead = true;
    ui.removeCallbacks(workerUpdates);
    // WorkerService owns the native process; Activity destruction is only UI.
    super.onDestroy();
  }
}
