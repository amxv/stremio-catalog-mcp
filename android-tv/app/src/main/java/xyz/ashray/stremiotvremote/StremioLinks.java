package xyz.ashray.stremiotvremote;

import java.util.regex.Pattern;

/** Pure Java validation; untrusted push data never becomes an arbitrary intent. */
public final class StremioLinks {
    private static final Pattern IMDB = Pattern.compile("tt[0-9]{5,12}");

    private StremioLinks() {}

    public static String detail(String type, String id) {
        if (!"movie".equals(type) && !"series".equals(type)) {
            throw new IllegalArgumentException("Unsupported media type");
        }
        if (id == null || !IMDB.matcher(id).matches()) {
            throw new IllegalArgumentException("Invalid IMDb ID");
        }
        // For series, blank videoId opens the show's detail/episode overview.
        // No autoPlay parameter is ever supplied.
        String videoID = "movie".equals(type) ? id : "";
        return "stremio:///detail/" + type + "/" + id + "/" + videoID;
    }
}
