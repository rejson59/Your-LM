package pl.yourlm.node;

import android.app.*;
import android.content.Context;
import android.content.Intent;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.IBinder;
import android.os.PowerManager;

/**
 * Owns the native RPC worker independently from every Activity. A foreground
 * notification, CPU wake lock and high-performance Wi-Fi lock keep active
 * inference alive when the UI is closed or the display is off.
 */
public final class WorkerService extends Service
  implements Worker.StateListener {

  static final String ACTION_START = "pl.yourlm.node.worker.START";
  static final String ACTION_STOP = "pl.yourlm.node.worker.STOP";
  static final String CHANNEL = "yourlm-worker";
  static final int NOTIFICATION = 41;

  static final int IDLE = 0;
  static final int STARTING = 1;
  static final int RUNNING = 2;
  static final int STOPPING = 3;
  static final int FAILED = 4;
  static final int RECOVERING = 5;

  private static final String PREFERENCES = "yourlm-worker-service";
  private static final String DESIRED_RUNNING = "desired_running";
  private static final int MAX_NATIVE_RESTARTS = 3;
  private static final long[] RESTART_DELAYS_MS = { 1000, 5000, 15000 };

  static final class Snapshot {
    final int phase;
    final int revision;
    final Worker worker;
    final String error;

    Snapshot(int phase, int revision, Worker worker, String error) {
      this.phase = phase;
      this.revision = revision;
      this.worker = worker;
      this.error = error;
    }

    boolean active() {
      return (
        phase == STARTING ||
        phase == RUNNING ||
        phase == RECOVERING ||
        phase == STOPPING
      );
    }
  }

  private static final Object STATE_LOCK = new Object();
  private static int phase = IDLE;
  private static int revision;
  private static Worker visibleWorker;
  private static String error = "";

  private final Object workerLock = new Object();
  private Worker current;
  private long generation;
  private PowerManager.WakeLock wakeLock;
  private WifiManager.WifiLock wifiLock;
  private Thread recoveryThread;

  static Snapshot snapshot() {
    synchronized (STATE_LOCK) {
      return new Snapshot(phase, revision, visibleWorker, error);
    }
  }

  private static void publish(int next, Worker worker, String message) {
    synchronized (STATE_LOCK) {
      phase = next;
      visibleWorker = worker;
      error = message == null ? "" : message;
      revision++;
    }
  }

  private static void setDesiredRunning(Context context, boolean desired) {
    context
      .getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
      .edit()
      .putBoolean(DESIRED_RUNNING, desired)
      .commit();
  }

  private static boolean desiredRunning(Context context) {
    return context
      .getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
      .getBoolean(DESIRED_RUNNING, false);
  }

  static void requestStart(Context context) {
    setDesiredRunning(context, true);
    synchronized (STATE_LOCK) {
      if (phase == STARTING || phase == RUNNING || phase == RECOVERING) return;
      phase = STARTING;
      visibleWorker = null;
      error = "";
      revision++;
    }
    Intent intent = new Intent(context, WorkerService.class).setAction(
      ACTION_START
    );
    if (Build.VERSION.SDK_INT >= 26) context.startForegroundService(intent);
    else context.startService(intent);
  }

  static void requestStop(Context context) {
    setDesiredRunning(context, false);
    synchronized (STATE_LOCK) {
      if (phase == IDLE || phase == FAILED) {
        phase = IDLE;
        visibleWorker = null;
        error = "";
        revision++;
      } else {
        phase = STOPPING;
        revision++;
      }
    }
    context.startService(
      new Intent(context, WorkerService.class).setAction(ACTION_STOP)
    );
  }

  public void onCreate() {
    super.onCreate();
    if (Build.VERSION.SDK_INT >= 26) {
      NotificationChannel channel = new NotificationChannel(
        CHANNEL,
        "Worker YourLM",
        NotificationManager.IMPORTANCE_LOW
      );
      channel.setDescription(
        "Aktywny worker obliczeniowy działający przy wyłączonym ekranie"
      );
      channel.setShowBadge(false);
      ((NotificationManager) getSystemService(NOTIFICATION_SERVICE))
        .createNotificationChannel(channel);
    }
  }

  private PendingIntent openIntent() {
    Intent open = new Intent(this, NodeActivity.class)
      .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
    return PendingIntent.getActivity(
      this,
      0,
      open,
      PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE
    );
  }

  private PendingIntent stopIntent() {
    return PendingIntent.getService(
      this,
      1,
      new Intent(this, WorkerService.class).setAction(ACTION_STOP),
      PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE
    );
  }

  private Notification notification(int workerPhase) {
    boolean ready = workerPhase == RUNNING;
    boolean failed = workerPhase == FAILED;
    boolean recovering = workerPhase == RECOVERING || failed;
    Notification.Builder builder = Build.VERSION.SDK_INT >= 26
      ? new Notification.Builder(this, CHANNEL)
      : new Notification.Builder(this);
    return builder
      .setSmallIcon(android.R.drawable.stat_sys_upload)
      .setContentTitle(
        ready
          ? "Worker YourLM działa"
          : failed
            ? "Worker YourLM: silnik niedostępny"
            : recovering
              ? "Worker YourLM odzyskuje silnik"
              : "Worker YourLM uruchamia się"
      )
      .setContentText(
        ready
          ? "Ekran może być wyłączony. Telefon nadal pomaga laptopowi."
          : failed
            ? "Uruchomienie lub odzyskanie nie powiodło się. Dotknij, aby skopiować diagnostykę albo zatrzymać worker."
            : recovering
              ? "Silnik natywny zakończył pracę. Trwa ograniczona próba odtworzenia; diagnostyka została zachowana."
              : "Przygotowywanie silnika i prywatnego połączenia Wi-Fi…"
      )
      .setContentIntent(openIntent())
      .setOngoing(true)
      .setOnlyAlertOnce(true)
      .setCategory(Notification.CATEGORY_SERVICE)
      .addAction(0, "Zatrzymaj", stopIntent())
      .build();
  }

  private void acquireLocks() {
    if (wakeLock == null) {
      PowerManager power = (PowerManager) getSystemService(POWER_SERVICE);
      wakeLock = power.newWakeLock(
        PowerManager.PARTIAL_WAKE_LOCK,
        "YourLM:WorkerCPU"
      );
      wakeLock.setReferenceCounted(false);
    }
    if (!wakeLock.isHeld()) wakeLock.acquire();

    if (wifiLock == null) {
      WifiManager wifi = (WifiManager) getApplicationContext()
        .getSystemService(WIFI_SERVICE);
      wifiLock = wifi.createWifiLock(
        WifiManager.WIFI_MODE_FULL_HIGH_PERF,
        "YourLM:WorkerWiFi"
      );
      wifiLock.setReferenceCounted(false);
    }
    if (!wifiLock.isHeld()) wifiLock.acquire();
  }

  private void releaseLocks() {
    try {
      if (wifiLock != null && wifiLock.isHeld()) wifiLock.release();
    } catch (RuntimeException ignored) {}
    try {
      if (wakeLock != null && wakeLock.isHeld()) wakeLock.release();
    } catch (RuntimeException ignored) {}
  }

  public int onStartCommand(Intent intent, int flags, int startId) {
    String action = intent == null ? null : intent.getAction();
    if (ACTION_STOP.equals(action)) {
      setDesiredRunning(this, false);
      stopWorker();
      return START_NOT_STICKY;
    }
    if (ACTION_START.equals(action)) setDesiredRunning(this, true);
    if (!desiredRunning(this)) {
      stopWorker();
      return START_NOT_STICKY;
    }
    startForeground(NOTIFICATION, notification(STARTING));
    acquireLocks();
    if (ACTION_START.equals(action)) {
      Worker failed = null;
      synchronized (workerLock) {
        if (
          current != null &&
          !current.nativeAlive() &&
          (recoveryThread == null || !recoveryThread.isAlive())
        ) {
          generation++;
          failed = current;
          current = null;
          recoveryThread = null;
        }
      }
      if (failed != null) failed.close();
    }
    startWorker();
    return START_STICKY;
  }

  private void startWorker() {
    final long run;
    final Worker worker;
    synchronized (workerLock) {
      if (current != null) return;
      run = ++generation;
      worker = new Worker(getApplicationContext(), this);
      current = worker;
      publish(STARTING, null, "");
    }
    new Thread(
      () -> {
        String failure = "";
        try {
          worker.start();
          synchronized (workerLock) {
            if (generation != run || current != worker) return;
          }
          publish(RUNNING, worker, "");
          ((NotificationManager) getSystemService(NOTIFICATION_SERVICE))
            .notify(NOTIFICATION, notification(RUNNING));
          worker.listen();
          synchronized (workerLock) {
            if (
              generation == run &&
              current == worker &&
              desiredRunning(this)
            ) {
              failure = "Usługa nasłuchująca workera zakończyła pracę.";
            }
          }
        } catch (Exception e) {
          failure = e.getMessage() == null
            ? e.getClass().getSimpleName()
            : e.getMessage();
        } finally {
          worker.close();
          boolean ownsRun;
          synchronized (workerLock) {
            ownsRun = generation == run && current == worker;
            if (ownsRun) current = null;
          }
          if (ownsRun) {
            releaseLocks();
            if (failure.isEmpty()) {
              publish(IDLE, null, "");
              stopForeground(STOP_FOREGROUND_REMOVE);
              stopSelf();
            } else {
              publish(FAILED, worker, failure);
              ((NotificationManager) getSystemService(NOTIFICATION_SERVICE))
                .notify(NOTIFICATION, notification(FAILED));
              if (!desiredRunning(this)) {
                stopForeground(STOP_FOREGROUND_REMOVE);
                stopSelf();
              }
            }
          }
        }
      },
      "YourLM-foreground-worker"
    ).start();
  }

  public void onWorkerStateChanged(Worker worker) {
    synchronized (workerLock) {
      if (worker != current || !desiredRunning(this)) return;
      if (worker.nativeAlive()) {
        publish(RUNNING, worker, "");
        ((NotificationManager) getSystemService(NOTIFICATION_SERVICE))
          .notify(NOTIFICATION, notification(RUNNING));
        return;
      }
      if (recoveryThread != null && recoveryThread.isAlive()) return;
      final long run = generation;
      recoveryThread = new Thread(
        () -> recoverNative(worker, run),
        "YourLM-native-recovery"
      );
      recoveryThread.start();
    }
  }

  private void recoverNative(Worker worker, long run) {
    Throwable last = null;
    while (worker.restartAttempts < MAX_NATIVE_RESTARTS) {
      synchronized (workerLock) {
        if (
          current != worker ||
          generation != run ||
          !desiredRunning(this)
        ) return;
      }
      int attempt = worker.restartAttempts;
      String message =
        "Silnik natywny zakończył pracę. Próba odtworzenia " +
        (attempt + 1) +
        " z " +
        MAX_NATIVE_RESTARTS +
        ". Oryginalna diagnostyka pozostaje dostępna.";
      publish(RECOVERING, worker, message);
      ((NotificationManager) getSystemService(NOTIFICATION_SERVICE))
        .notify(NOTIFICATION, notification(RECOVERING));
      try {
        Thread.sleep(RESTART_DELAYS_MS[attempt]);
        synchronized (workerLock) {
          if (
            current != worker ||
            generation != run ||
            !desiredRunning(this)
          ) return;
        }
        worker.restartNative();
        synchronized (workerLock) {
          if (current != worker || generation != run) return;
          recoveryThread = null;
        }
        publish(RUNNING, worker, "");
        ((NotificationManager) getSystemService(NOTIFICATION_SERVICE))
          .notify(NOTIFICATION, notification(RUNNING));
        worker.watchNative();
        return;
      } catch (InterruptedException interrupted) {
        Thread.currentThread().interrupt();
        return;
      } catch (Throwable failure) {
        last = failure;
      }
    }
    String failure =
      "Nie udało się trwale odtworzyć silnika po " +
      MAX_NATIVE_RESTARTS +
      " próbach." +
      (last == null
        ? " Ostatni proces natywny również zakończył pracę; zachowano jego diagnostykę."
        : " " +
          (last.getMessage() == null
            ? last.getClass().getSimpleName()
            : last.getMessage()));
    worker.markRecoveryFailure(last == null ? new Exception(failure) : last);
    publish(FAILED, worker, failure);
    releaseLocks();
    ((NotificationManager) getSystemService(NOTIFICATION_SERVICE))
      .notify(NOTIFICATION, notification(FAILED));
    // Keep the foreground service and authenticated DIAG endpoint alive. An
    // explicit stop is still required, so the original memory/native evidence
    // cannot disappear behind an automatic terminal shutdown.
  }

  private void stopWorker() {
    Worker old;
    Thread recovery;
    synchronized (workerLock) {
      generation++;
      old = current;
      current = null;
      recovery = recoveryThread;
      recoveryThread = null;
    }
    if (recovery != null) recovery.interrupt();
    publish(IDLE, null, "");
    if (old != null) old.close();
    releaseLocks();
    stopForeground(STOP_FOREGROUND_REMOVE);
    stopSelf();
  }

  public void onDestroy() {
    Worker old;
    Thread recovery;
    boolean shouldRestore = desiredRunning(this);
    synchronized (workerLock) {
      generation++;
      old = current;
      current = null;
      recovery = recoveryThread;
      recoveryThread = null;
    }
    if (recovery != null) recovery.interrupt();
    if (old != null) old.close();
    releaseLocks();
    if (shouldRestore) publish(STARTING, null, "Android odtwarza usługę workera…");
    else if (snapshot().phase != FAILED) publish(IDLE, null, "");
    super.onDestroy();
  }

  public IBinder onBind(Intent intent) {
    return null;
  }
}
