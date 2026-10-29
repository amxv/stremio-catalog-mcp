package xyz.ashray.stremiotvremote;

import org.junit.Test;
import static org.junit.Assert.*;

public final class StremioLinksTest {
    @Test public void opensMovieDetailsWithoutAutoplay() {
        assertEquals("stremio:///detail/movie/tt1375666/tt1375666",
                StremioLinks.detail("movie", "tt1375666"));
    }

    @Test public void opensSeriesOverviewWithoutChoosingAnEpisode() {
        assertEquals("stremio:///detail/series/tt0903747/",
                StremioLinks.detail("series", "tt0903747"));
    }

    @Test public void rejectsUnexpectedRoutesAndIds() {
        for (String bad : new String[]{"", "http://example.com", "tt123/../../../", "tt0"}) {
            try {
                StremioLinks.detail("movie", bad);
                fail("Accepted " + bad);
            } catch (IllegalArgumentException expected) { /* correct */ }
        }
        try {
            StremioLinks.detail("play", "tt1375666");
            fail("Non-details command accepted");
        } catch (IllegalArgumentException expected) { /* correct */ }
    }
}
