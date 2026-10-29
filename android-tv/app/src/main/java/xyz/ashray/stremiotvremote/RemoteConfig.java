package xyz.ashray.stremiotvremote;

import android.content.Context;
import android.content.SharedPreferences;
import java.net.URI;
import java.net.URISyntaxException;

final class RemoteConfig {
    private static final String FILE = "remote_config";
    private final SharedPreferences preferences;

    RemoteConfig(Context context) {
        preferences = context.getSharedPreferences(FILE, Context.MODE_PRIVATE);
    }

    String server() {
        return preferences.getString("server", BuildConfig.DEFAULT_SERVER_URL);
    }

    void setServer(String server) {
        preferences.edit().putString("server", server).apply();
    }

    String deviceID() { return preferences.getString("device_id", ""); }
    String deviceSecret() { return preferences.getString("device_secret", ""); }
    boolean paired() { return !deviceID().isEmpty() && !deviceSecret().isEmpty(); }

    void setPaired(String id, String secret) {
        preferences.edit()
                .putString("device_id", id)
                .putString("device_secret", secret)
                .remove("pair_session")
                .remove("pair_code")
                .remove("pair_expires")
                .apply();
    }

    void resetPairing() {
        preferences.edit().remove("device_id").remove("device_secret")
                .remove("pair_session").remove("pair_code")
                .remove("pair_expires").apply();
    }

    void pending(String code, String session, long untilMillis) {
        preferences.edit().putString("pair_code", code)
                .putString("pair_session", session)
                .putLong("pair_expires", untilMillis).apply();
    }

    String pairCode() { return preferences.getString("pair_code", ""); }
    String pairSession() {
        if (System.currentTimeMillis() > preferences.getLong("pair_expires", 0)) return "";
        return preferences.getString("pair_session", "");
    }

    void lastRequest(String commandID, String link) {
        preferences.edit().putString("last_command", commandID)
                .putString("last_link", link).apply();
    }
    boolean alreadyHandled(String commandID) {
        return commandID.equals(preferences.getString("last_command", ""));
    }
    String lastLink() { return preferences.getString("last_link", ""); }

    static String normalizeServer(String entered) throws URISyntaxException {
        URI uri = new URI(entered.trim());
        if (!"https".equals(uri.getScheme()) || uri.getHost() == null ||
                uri.getUserInfo() != null || uri.getRawQuery() != null ||
                uri.getRawFragment() != null ||
                !(uri.getRawPath().isEmpty() || "/".equals(uri.getRawPath()))) {
            throw new URISyntaxException(entered, "Expected an HTTPS server root, e.g. https://your-mcp.example.com");
        }
        return entered.trim().replaceAll("/+$", "");
    }
}
