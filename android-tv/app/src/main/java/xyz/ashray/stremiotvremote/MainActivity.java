package xyz.ashray.stremiotvremote;

import android.Manifest;
import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import com.google.firebase.messaging.FirebaseMessaging;
import org.json.JSONObject;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Small setup UI built with platform widgets, without Compose or AndroidX. */
public final class MainActivity extends Activity {
    private final Handler handler = new Handler(Looper.getMainLooper());
    private final ExecutorService io = Executors.newSingleThreadExecutor();
    private RemoteConfig config;
    private EditText serverInput;
    private TextView status;
    private Button openLast;
    private boolean visible;
    private boolean polling;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        config = new RemoteConfig(this);
        String fromAdb = getIntent().getStringExtra("server_url");
        if (fromAdb != null) {
            try { config.setServer(RemoteConfig.normalizeServer(fromAdb)); }
            catch (Exception ignored) { /* User can edit server URL below. */ }
        }
        createUI();
        showCurrentState();

        if (Build.VERSION.SDK_INT >= 33 &&
                checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) !=
                        PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 1);
        }
        if (RemoteApplication.firebaseConfigured() && config.paired()) refreshToken();
    }

    private int dp(int amount) {
        return (int) (getResources().getDisplayMetrics().density * amount);
    }

    private TextView label(String text, int sp, int color) {
        TextView label = new TextView(this);
        label.setText(text);
        label.setTextSize(sp);
        label.setTextColor(color);
        label.setPadding(0, dp(8), 0, dp(8));
        return label;
    }

    private Button button(LinearLayout root, String title, View.OnClickListener click) {
        Button button = new Button(this);
        button.setText(title);
        button.setAllCaps(false);
        button.setOnClickListener(click);
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        params.topMargin = dp(12);
        root.addView(button, params);
        return button;
    }

    private void createUI() {
        ScrollView scroll = new ScrollView(this);
        scroll.setBackgroundColor(Color.rgb(18, 28, 19));
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setGravity(Gravity.CENTER_HORIZONTAL);
        root.setPadding(dp(48), dp(24), dp(48), dp(32));
        scroll.addView(root);

        root.addView(label("Stremio TV Remote", 27, Color.rgb(213, 247, 124)));
        root.addView(label(
                "Connect this TV to your Stremio Catalog MCP. Nothing runs continuously in the background.",
                16, Color.WHITE));
        root.addView(label("MCP server URL (HTTPS)", 15, Color.LTGRAY));
        serverInput = new EditText(this);
        serverInput.setSingleLine(true);
        serverInput.setHint("https://your-server.example.com");
        serverInput.setTextColor(Color.WHITE);
        serverInput.setHintTextColor(Color.GRAY);
        serverInput.setText(config.server());
        serverInput.setSelectAllOnFocus(true);
        root.addView(serverInput, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT));

        status = label("", 19, Color.WHITE);
        status.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams statusParams = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        statusParams.topMargin = dp(20);
        root.addView(status, statusParams);

        Button pair = button(root, "Start pairing", ignored -> startPairing());
        button(root, "Allow opening Stremio from the background", ignored -> openOverlaySettings());
        openLast = button(root, "Open last requested title", ignored -> openLastTitle());
        button(root, "Reset local pairing", ignored -> {
            config.resetPairing();
            showCurrentState();
        });
        root.addView(label(
                "After pairing, say 'Open Inception on my TV' in ChatGPT. " +
                "To revoke or replace this TV, open /tv/setup on your MCP server.",
                13, Color.LTGRAY));
        setContentView(scroll);
        pair.requestFocus();
    }

    private void showCurrentState() {
        if (config.paired()) {
            status.setText("Paired TV\n" + config.deviceID() +
                    "\n\nGrant background opening permission to enable one-step navigation.");
        } else if (!config.pairSession().isEmpty()) {
            status.setText("Pair code: " + config.pairCode() +
                    "\n\nAsk ChatGPT to pair this TV using that code.");
        } else if (!RemoteApplication.firebaseConfigured()) {
            status.setText("Firebase is not configured in this APK.\n" +
                    "Build with your Firebase Android google-services.json first.");
        } else {
            status.setText("Not paired.\nEnter your HTTPS MCP server and select Start pairing.");
        }
        openLast.setEnabled(!config.lastLink().isEmpty());
    }

    private void startPairing() {
        if (!RemoteApplication.firebaseConfigured()) {
            status.setText("This APK was built without Firebase configuration.");
            return;
        }
        final String server;
        try {
            server = RemoteConfig.normalizeServer(serverInput.getText().toString());
        } catch (Exception ex) {
            status.setText("Enter a valid HTTPS server origin.");
            return;
        }
        config.setServer(server);
        status.setText("Getting Firebase device token...");
        FirebaseMessaging.getInstance().getToken()
                .addOnSuccessListener(token -> io.execute(() -> {
                    try {
                        String model = Build.MODEL == null ? "Android TV" : Build.MODEL.trim();
                        if (model.isEmpty()) model = "Android TV";
                        if (model.length() > 60) model = model.substring(0, 60);
                        JSONObject result = ApiClient.post(server, "/tv/pair/start",
                                new JSONObject().put("device_name", model)
                                        .put("fcm_token", token), null);
                        String code = result.getString("code");
                        String session = result.getString("session");
                        long expiry = System.currentTimeMillis() +
                                result.optLong("expires_in_seconds", 600) * 1000;
                        runOnUiThread(() -> {
                            config.pending(code, session, expiry);
                            showCurrentState();
                            startPolling();
                        });
                    } catch (Exception ex) {
                        showError("Pairing request failed: " + ex.getMessage());
                    }
                }))
                .addOnFailureListener(error -> showError(
                        "Firebase token unavailable. Check Play Services / Firebase setup."));
    }

    private void startPolling() {
        if (!visible || polling || config.paired() || config.pairSession().isEmpty()) return;
        polling = true;
        String session = config.pairSession();
        String server = config.server();
        io.execute(() -> {
            String failure = null;
            JSONObject result = null;
            try {
                result = ApiClient.post(server, "/tv/pair/status",
                        new JSONObject().put("session", session), null);
            } catch (Exception error) {
                failure = error.getMessage();
            }
            final JSONObject response = result;
            final String message = failure;
            runOnUiThread(() -> {
                polling = false;
                if (!visible || config.paired()) return;
                if (response != null && "paired".equals(response.optString("status"))) {
                    String id = response.optString("device_id");
                    String key = response.optString("device_secret");
                    if (!id.isEmpty() && !key.isEmpty()) {
                        config.setPaired(id, key);
                        showCurrentState();
                        refreshToken();
                        return;
                    }
                }
                if (message != null) status.setText(
                        "Pair code: " + config.pairCode() +
                        "\nWaiting for MCP approval. Connection retrying...");
                if (config.pairSession().isEmpty()) {
                    showCurrentState();
                    return;
                }
                handler.postDelayed(this::startPolling, 4000);
            });
        });
    }

    private void refreshToken() {
        FirebaseMessaging.getInstance().getToken().addOnSuccessListener(token -> {
            String id = config.deviceID();
            String secret = config.deviceSecret();
            String server = config.server();
            io.execute(() -> {
                try {
                    ApiClient.post(server, "/tv/device/token",
                            new JSONObject().put("device_id", id).put("fcm_token", token),
                            secret);
                } catch (Exception ignored) {
                    // Pairing remains stored; app can retry on a future open.
                }
            });
        });
    }

    private void openOverlaySettings() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            status.setText("This Android version does not need the permission.");
            return;
        }
        if (Settings.canDrawOverlays(this)) {
            status.setText("Background opening permission already granted.");
            return;
        }
        Intent settings = new Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION,
                Uri.parse("package:" + getPackageName()));
        if (settings.resolveActivity(getPackageManager()) != null) startActivity(settings);
        else status.setText("This TV does not expose overlay settings here. " +
                "See the ADB appops fallback in the installation guide.");
    }

    private void openLastTitle() {
        String link = config.lastLink();
        if (link.isEmpty()) return;
        try {
            startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(link)));
        } catch (Exception ex) {
            status.setText("Stremio could not open this link. Check its installation.");
        }
    }

    private void showError(String message) {
        runOnUiThread(() -> { if (!isFinishing()) status.setText(message); });
    }

    @Override protected void onResume() {
        super.onResume();
        visible = true;
        if (config != null && !config.paired() && !config.pairSession().isEmpty()) {
            startPolling();
        }
    }

    @Override protected void onPause() {
        visible = false;
        handler.removeCallbacksAndMessages(null);
        super.onPause();
    }

    @Override protected void onDestroy() {
        handler.removeCallbacksAndMessages(null);
        io.shutdownNow();
        super.onDestroy();
    }
}
