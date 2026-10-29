package xyz.ashray.stremiotvremote;

import org.json.JSONObject;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;

final class ApiClient {
    private ApiClient() {}

    static JSONObject post(String base, String route, JSONObject body, String bearer) throws Exception {
        // normalizeServer rejects everything except an HTTPS origin.
        String origin = RemoteConfig.normalizeServer(base);
        HttpURLConnection conn = (HttpURLConnection) new URL(origin + route).openConnection();
        conn.setInstanceFollowRedirects(false);
        conn.setRequestMethod("POST");
        conn.setConnectTimeout(7000);
        conn.setReadTimeout(7000);
        conn.setDoOutput(true);
        conn.setRequestProperty("Content-Type", "application/json");
        conn.setRequestProperty("Accept", "application/json");
        if (bearer != null) conn.setRequestProperty("Authorization", "Bearer " + bearer);
        try {
            byte[] bytes = body.toString().getBytes(StandardCharsets.UTF_8);
            if (bytes.length > 8192) throw new IOException("Request too large");
            try (OutputStream output = conn.getOutputStream()) { output.write(bytes); }
            int code = conn.getResponseCode();
            if (code < 200 || code >= 300) {
                throw new IOException("TV service returned HTTP " + code);
            }
            try (InputStream stream = conn.getInputStream(); ByteArrayOutputStream output = new ByteArrayOutputStream()) {
                byte[] buffer = new byte[2048];
                int read;
                while ((read = stream.read(buffer)) != -1) {
                    output.write(buffer, 0, read);
                    if (output.size() > 16384) throw new IOException("Response too large");
                }
                return new JSONObject(output.toString(StandardCharsets.UTF_8.name()));
            }
        } finally {
            conn.disconnect();
        }
    }
}
