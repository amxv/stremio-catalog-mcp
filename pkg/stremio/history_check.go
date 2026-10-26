package stremio

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxBatchWatchCheck = 50

// CheckSeason reports the *exact confirmed* episodes watched in a season.
// Episode titles and future-episode plots are intentionally excluded from
// batch recommendation checks; get with imdb_id supplies watched synopses.
type CheckSeason struct {
	Season            int   `json:"season"`
	EpisodeCount      int   `json:"episode_count"`
	WatchedEpisodes   []int `json:"watched_episodes"`
	InProgressEpisode *int  `json:"in_progress_episode,omitempty"`
}

// WatchCheck describes what Stremio can verify, not what someone may have
// watched outside Stremio. Status values are watched, partially_watched,
// not_watched or unverified. The fourth state prevents false negatives when
// completion bitfields cannot be decoded or metadata cannot be fetched.
type WatchCheck struct {
	IMDbID               string           `json:"imdb_id"`
	Title                string           `json:"title,omitempty"`
	MediaType            string           `json:"media_type,omitempty"`
	Status               string           `json:"status"`
	Recorded             bool             `json:"recorded"`
	EpisodeHistory       string           `json:"episode_history,omitempty"`
	ProgressPercent      float64          `json:"progress_percent,omitempty"`
	Seasons              []CheckSeason    `json:"seasons,omitempty"`
	SpoilerSafeThrough   *EpisodePosition `json:"spoiler_safe_through,omitempty"`
	ReleasedEpisodeCount int              `json:"released_episode_count,omitempty"`
	WatchedEpisodeCount  int              `json:"watched_episode_count,omitempty"`
	UpcomingEpisodeCount int              `json:"upcoming_episode_count,omitempty"`
	UnknownReleaseCount  int              `json:"unknown_release_count,omitempty"`
	Note                 string           `json:"note,omitempty"`
}

// Check inspects only the requested IMDb IDs. One account datastoreGet
// replaces a scan over all history; Cinemeta is fetched only for matching
// recorded series, with bounded parallel requests. Results preserve input
// order and no account writes occur.
func (s WatchHistoryService) Check(ctx context.Context, ids []string) ([]WatchCheck, error) {
	if len(ids) == 0 || len(ids) > maxBatchWatchCheck {
		return nil, fmt.Errorf("check requires 1-%d IMDb IDs", maxBatchWatchCheck)
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validHistoryID(id) {
			return nil, fmt.Errorf("invalid IMDb ID %q", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate IMDb ID %q", id)
		}
		seen[id] = true
	}

	items, err := s.Account.Get(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]HistoryRecord, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}

	checks := make([]WatchCheck, len(ids))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(8, len(ids)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				id := ids[i]
				record, ok := byID[id]
				if !ok {
					checks[i] = WatchCheck{
						IMDbID: id, Status: "not_watched",
						Note: "No Stremio watch record exists. This does not prove the title was never watched elsewhere.",
					}
					continue
				}
				checks[i] = s.checkRecord(ctx, record)
			}
		}()
	}
	for i := range ids {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return checks, nil
}

func (s WatchHistoryService) checkRecord(ctx context.Context, r HistoryRecord) WatchCheck {
	check := WatchCheck{IMDbID: r.ID, Title: r.Name, MediaType: r.Type, Recorded: true}
	if IsClearedHistoryItem(r) {
		check.Recorded = false
		check.Status = "not_watched"
		check.Note = "Stremio watch history was cleared; only a removed synchronization tombstone remains."
		return check
	}
	if r.Type == "movie" {
		if r.State.TimesWatched > 0 || r.State.FlaggedWatched > 0 {
			check.Status = "watched"
		} else if r.State.TimeOffset > 0 {
			check.Status = "partially_watched"
			if r.State.Duration > 0 {
				check.ProgressPercent = min(100, 100*float64(r.State.TimeOffset)/float64(r.State.Duration))
			}
		} else if strings.TrimSpace(r.State.LastWatched) != "" {
			check.Status = "unverified"
			check.Note = "Last-watched activity exists, but no movie completion marker is stored."
		} else {
			check.Status = "not_watched"
			check.Note = "No Stremio movie completion or in-progress marker."
		}
		return check
	}

	meta, err := s.Metadata.Fetch(ctx, "series", r.ID)
	if err != nil {
		check.Status = "unverified"
		check.EpisodeHistory = "unverified"
		check.Note = "Cinemeta episode metadata is unavailable; stored watch completion cannot be verified."
		return check
	}
	ids := make([]string, len(meta.Episodes))
	for i, ep := range meta.Episodes {
		ids[i] = ep.ID
	}
	flags := make([]bool, len(ids))
	verified := r.State.Watched != ""
	check.EpisodeHistory = "unverified"
	if verified {
		flags, err = decodeWatchedEpisodes(r.State.Watched, ids)
		if err != nil {
			check.Status = "unverified"
			check.Note = "Stored episode completion bitfield cannot be safely matched to the episode list."
			return check
		}
		check.EpisodeHistory = "verified"
	}

	bySeason := make(map[int]*CheckSeason)
	now := time.Now().UTC()
	knownWatched, unknownUnwatched, anyWatched := 0, 0, 0
	for i, ep := range meta.Episodes {
		if ep.Number < 1 {
			continue
		}
		season, exists := bySeason[ep.Season]
		if !exists {
			season = &CheckSeason{Season: ep.Season, WatchedEpisodes: []int{}}
			bySeason[ep.Season] = season
		}
		season.EpisodeCount++
		if verified && flags[i] {
			season.WatchedEpisodes = append(season.WatchedEpisodes, ep.Number)
			anyWatched++
		}
		if ep.ID == r.State.VideoID && r.State.TimeOffset > 0 && (!verified || !flags[i]) {
			n := ep.Number
			season.InProgressEpisode = &n
		}
		if ep.Season == 0 {
			continue // specials do not affect whole-series completion
		}
		release, err := parseEpisodeRelease(ep.Released)
		if err != nil {
			check.UnknownReleaseCount++
			if !verified || !flags[i] {
				unknownUnwatched++
			}
		} else if release.After(now) {
			check.UpcomingEpisodeCount++
		} else {
			check.ReleasedEpisodeCount++
			if verified && flags[i] {
				knownWatched++
			}
		}
	}
	// A fully watched series has exactly the same compact shape as a fully
	// watched movie. Episode lists, completion counts and spoiler boundaries
	// add nothing for a finished title and can be very large for long series.
	// "Fully watched" covers released regular episodes; future episodes and
	// specials do not count against it.
	if verified && check.ReleasedEpisodeCount > 0 && knownWatched == check.ReleasedEpisodeCount && unknownUnwatched == 0 {
		return WatchCheck{IMDbID: r.ID, Title: r.Name, MediaType: r.Type, Status: "watched", Recorded: true}
	}
	check.WatchedEpisodeCount = anyWatched
	seasons := make([]int, 0, len(bySeason))
	for season := range bySeason {
		seasons = append(seasons, season)
	}
	sort.Ints(seasons)
	for _, season := range seasons {
		if entry := bySeason[season]; season != 0 || len(entry.WatchedEpisodes) > 0 || entry.InProgressEpisode != nil {
			check.Seasons = append(check.Seasons, *entry)
		}
	}
	if verified {
		// Reuse the same consecutive watched-episode boundary as get, without
		// returning large episode synopses or future-episode descriptions.
		check.SpoilerSafeThrough = episodeDetails(r, meta, nil).SpoilerSafeThrough
	}

	switch {
	case anyWatched > 0 || r.State.TimeOffset > 0:
		check.Status = "partially_watched"
	case !verified && (r.State.LastWatched != "" || r.State.TimesWatched > 0 || r.State.FlaggedWatched > 0):
		check.Status = "unverified"
	default:
		check.Status = "not_watched"
	}
	if !verified {
		check.Note = "Episode completion bitfield is absent; episode completion is unverified. In-progress playback, if any, does not prove completion."
	} else if check.UnknownReleaseCount > 0 {
		check.Note = "Some episodes have unverified release dates; they are not assumed to be watched or unreleased."
	} else if check.UpcomingEpisodeCount > 0 {
		check.Note = "Status compares released episodes only; upcoming episodes are excluded."
	}
	return check
}
