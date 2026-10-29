package xyz.ashray.stremiotvremote;

import java.util.regex.Pattern;

/** Pure Java validation; untrusted push data never becomes an arbitrary intent. */
public final class StremioLinks {
    private static final Pattern IMDB = Pattern.compile("tt[0-9]{5,12}");

    private StremioLinks() {}

    public static String detail(String type, String id) {
        return detail(type, id, null, null);
    }

    /** Select a series episode without autoplay, or omit both numbers for its overview. */
    public static String detail(String type, String id, Integer season, Integer episode) {
        if (!"movie".equals(type) && !"series".equals(type)) {
            throw new IllegalArgumentException("Unsupported media type");
        }
        if (id == null || !IMDB.matcher(id).matches()) {
            throw new IllegalArgumentException("Invalid IMDb ID");
        }
        if ((season == null) != (episode == null) ||
            (season != null && !"series".equals(type))) {
            throw new IllegalArgumentException("Only series can select an episode; provide both numbers");
        }
        // For series, blank videoId opens the show's detail/episode overview.
        // No autoPlay parameter is ever supplied.
        String videoID = "movie".equals(type) ? id : "";
        if (season != null) {
            if (season < 0 || season > 100 || episode < 1 || episode > 1000) {
                throw new IllegalArgumentException("Invalid season or episode number");
            }
            videoID = id + ":" + season + ":" + episode;
        }
        return "stremio:///detail/" + type + "/" + id + "/" + videoID;
    }
}
