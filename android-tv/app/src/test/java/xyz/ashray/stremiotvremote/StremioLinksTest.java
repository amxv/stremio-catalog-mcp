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

    @Test public void opensExactSeriesEpisodeWithoutAutoplay() {
        assertEquals("stremio:///detail/series/tt0903747/tt0903747:3:4",
                StremioLinks.detail("series", "tt0903747", 3, 4));
        assertEquals("stremio:///detail/series/tt0903747/tt0903747:0:1",
                StremioLinks.detail("series", "tt0903747", 0, 1));
    }

    @Test public void rejectsIncompleteOrInvalidEpisodeSelection() {
        for (Integer[] values : new Integer[][]{
                {1, null}, {null, 1}, {-1, 1}, {2, 0}, {101, 1}, {1, 1001}}) {
            try {
                StremioLinks.detail("series", "tt0903747", values[0], values[1]);
                fail("Accepted invalid season/episode combination");
            } catch (IllegalArgumentException expected) { /* correct */ }
        }
        try {
            StremioLinks.detail("movie", "tt1375666", 1, 1);
            fail("Allowed movie with series episode");
        } catch (IllegalArgumentException expected) { /* correct */ }
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
