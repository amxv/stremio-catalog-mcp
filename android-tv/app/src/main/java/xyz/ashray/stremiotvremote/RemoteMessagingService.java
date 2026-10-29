package xyz.ashray.stremiotvremote;

import android.Manifest;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;
import com.google.firebase.messaging.FirebaseMessagingService;
import com.google.firebase.messaging.RemoteMessage;
import org.json.JSONObject;
import java.util.Map;
import java.util.regex.Pattern;

/**
 * FCM starts this component only when needed. No socket, polling timer,
 * foreground service, boot receiver, or wake lock runs while the TV is idle.
 */
public final class RemoteMessagingService extends FirebaseMessagingService {
    private static final Pattern COMMAND = Pattern.compile("[A-Za-z0-9_-]{43}");
    private static final String CHANNEL = "stremio_title";

    @Override public void onNewToken(String token) {
        super.onNewToken(token);
        RemoteConfig config = new RemoteConfig(this);
        if (!config.paired()) return;
        try {
            ApiClient.post(config.server(), "/tv/device/token",
                    new JSONObject().put("device_id", config.deviceID())
                            .put("fcm_token", token),
                    config.deviceSecret());
        } catch (Exception ignored) {
            // The next app opening refreshes the token too. No secrets in logs.
        }
    }

    @Override public void onMessageReceived(RemoteMessage message) {
        RemoteConfig config = new RemoteConfig(this);
        if (!config.paired()) return;
        Map<String, String> data = message.getData();
        if (!"open_title".equals(data.get("kind"))) return;
        String command = data.get("command_id");
        if (command == null || !COMMAND.matcher(command).matches() ||
                config.alreadyHandled(command)) return;

        String link;
        try {
            long issued = Long.parseLong(data.get("issued_at"));
            long now = System.currentTimeMillis() / 1000;
            if (Math.abs(now - issued) > 120) return;
            Integer season = data.containsKey("season") ? Integer.parseInt(data.get("season")) : null;
            Integer episode = data.containsKey("episode") ? Integer.parseInt(data.get("episode")) : null;
            link = StremioLinks.detail(data.get("media_type"), data.get("imdb_id"), season, episode);
        } catch (RuntimeException invalid) {
            return;
        }

        // Duplicate pushes must never navigate twice. Retain the link so the
        // user can open it manually even if Android blocks background launch.
        config.lastRequest(command, link);
        Intent intent = new Intent(Intent.ACTION_VIEW, Uri.parse(link));
        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        String outcome;
        if (intent.resolveActivity(getPackageManager()) == null) {
            outcome = showNotification(intent) ? "notification_posted" : "notification_unavailable";
            acknowledge(config, command, outcome);
            return;
        }

        // Android 10+ generally disallows background activity launches without
        // a qualifying exemption. A user-granted overlay permission is one on
        // Android 12. Do not actually create an overlay window.
        boolean mayLaunch = Build.VERSION.SDK_INT < Build.VERSION_CODES.Q ||
                Settings.canDrawOverlays(this);
        if (mayLaunch) {
            try {
                startActivity(intent);
                acknowledge(config, command, "launch_requested");
                return;
            } catch (RuntimeException ignored) {
                // Never claim successful navigation; show a manual action.
            }
        }
        outcome = showNotification(intent) ? "notification_posted" : "notification_unavailable";
        acknowledge(config, command, outcome);
    }

    /** Best effort: report receipt through the paired device's private credential.
     * Runs briefly on Firebase's background callback thread; no polling loop. */
    private void acknowledge(RemoteConfig config, String command, String status) {
        try {
            ApiClient.post(config.server(), "/tv/device/receipt",
                    new JSONObject().put("device_id", config.deviceID())
                            .put("command_id", command).put("status", status),
                    config.deviceSecret(), 2000);
        } catch (Exception ignored) {
            // Acknowledgment is best-effort and never blocks opening Stremio.
        }
    }

    private boolean showNotification(Intent viewIntent) {
        NotificationManager manager = getSystemService(NotificationManager.class);
        if (manager == null) return false;
        if (Build.VERSION.SDK_INT >= 33 &&
                checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)
                        != PackageManager.PERMISSION_GRANTED) return false;
        try {
            manager.createNotificationChannel(new NotificationChannel(
                    CHANNEL, "Open titles in Stremio", NotificationManager.IMPORTANCE_DEFAULT));
            PendingIntent tap = PendingIntent.getActivity(this, 1, viewIntent,
                    PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
            Notification notification = new Notification.Builder(this, CHANNEL)
                    .setSmallIcon(R.drawable.ic_remote)
                    .setContentTitle("Open title in Stremio")
                    .setContentText("Select to open the requested title on this TV")
                    .setContentIntent(tap)
                    .setAutoCancel(true)
                    .build();
            manager.notify(1001, notification);
            return true;
        } catch (RuntimeException ignored) {
            return false;
        }
    }
}
