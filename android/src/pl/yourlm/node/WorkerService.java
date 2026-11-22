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
public final class WorkerService extends Service {

  static final String ACTION_START = "pl.yourlm.node.worker.START";
  static final String ACTION_STOP = "pl.yourlm.node.worker.STOP";
  static final String CHANNEL = "yourlm-worker";
  static final int NOTIFICATION = 41;

  static final int IDLE = 0;
  static final int STARTING = 1;
  static final int RUNNING = 2;
  static final int STOPPING = 3;
  static final int FAILED = 4;

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
      return phase == STARTING || phase == RUNNING || phase == STOPPING;
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

  static void requestStart(Context context) {
    synchronized (STATE_LOCK) {
      if (phase == STARTING || phase == RUNNING) return;
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

  private Notification notification(boolean ready) {
    Notification.Builder builder = Build.VERSION.SDK_INT >= 26
      ? new Notification.Builder(this, CHANNEL)
      : new Notification.Builder(this);
    return builder
      .setSmallIcon(android.R.drawable.stat_sys_upload)
      .setContentTitle(ready ? "Worker YourLM działa" : "Worker YourLM uruchamia się")
      .setContentText(
        ready
          ? "Ekran może być wyłączony. Telefon nadal pomaga laptopowi."
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
    String action = intent == null ? ACTION_START : intent.getAction();
    if (ACTION_STOP.equals(action)) {
      stopWorker();
      return START_NOT_STICKY;
    }
    startForeground(NOTIFICATION, notification(false));
    acquireLocks();
    startWorker();
    return START_STICKY;
  }

  private void startWorker() {
    final long run;
    final Worker worker;
    synchronized (workerLock) {
      if (current != null) return;
      run = ++generation;
      worker = new Worker(getApplicationContext());
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
            .notify(NOTIFICATION, notification(true));
          worker.listen();
          synchronized (workerLock) {
            if (generation == run && current == worker) {
              failure = "Silnik workera zakończył pracę.";
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
            if (failure.isEmpty()) publish(IDLE, null, "");
            else publish(FAILED, null, failure);
            stopForeground(STOP_FOREGROUND_REMOVE);
            stopSelf();
          }
        }
      },
      "YourLM-foreground-worker"
    ).start();
  }

  private void stopWorker() {
    Worker old;
    synchronized (workerLock) {
      generation++;
      old = current;
      current = null;
    }
    publish(IDLE, null, "");
    if (old != null) old.close();
    releaseLocks();
    stopForeground(STOP_FOREGROUND_REMOVE);
    stopSelf();
  }

  public void onDestroy() {
    Worker old;
    synchronized (workerLock) {
      generation++;
      old = current;
      current = null;
    }
    if (old != null) old.close();
    releaseLocks();
    if (snapshot().phase != FAILED) publish(IDLE, null, "");
    super.onDestroy();
  }

  public IBinder onBind(Intent intent) {
    return null;
  }
}
