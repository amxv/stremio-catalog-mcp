package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

type WatchHistoryService struct {
	Account  HistoryAccount
	Metadata MetadataClient
}

type WatchSummary struct {
	IMDbID          string  `json:"imdb_id"`
	Title           string  `json:"title"`
	MediaType       string  `json:"media_type"`
	WatchStatus     string  `json:"watch_status"`
	LastActivity    string  `json:"last_activity,omitempty"`
	ProgressPercent float64 `json:"progress_percent,omitempty"`
}

type EpisodePosition struct {
	Season  int `json:"season"`
	Episode int `json:"episode"`
}

type SeasonWatchStatus struct {
	Season                int              `json:"season"`
	EpisodeCount          int              `json:"episode_count"`
	WatchedEpisodes       []int            `json:"watched_episodes"`
	WatchedEpisodeDetails []WatchedEpisode `json:"watched_episode_details,omitempty"`
	UnconfirmedEpisodes   []int            `json:"unconfirmed_episodes"`
	Completed             bool             `json:"completed"`
	// The currently playing episode is *not* automatically a watched episode.
	InProgressEpisode *int `json:"in_progress_episode,omitempty"`
}

type WatchedEpisode struct {
	Episode int    `json:"episode"`
	Title   string `json:"title,omitempty"`
	Plot    string `json:"plot,omitempty"`
}

type WatchDetail struct {
	IMDbID             string              `json:"imdb_id"`
	Title              string              `json:"title,omitempty"`
	Plot               string              `json:"plot,omitempty"`
	MediaType          string              `json:"media_type,omitempty"`
	Recorded           bool                `json:"recorded"`
	MovieWatched       *bool               `json:"movie_watched,omitempty"`
	MovieInProgress    bool                `json:"movie_in_progress,omitempty"`
	ProgressPercent    float64             `json:"progress_percent,omitempty"`
	EpisodeHistory     string              `json:"episode_history,omitempty"`
	Seasons            []SeasonWatchStatus `json:"seasons,omitempty"`
	SpoilerSafeThrough *EpisodePosition    `json:"spoiler_safe_through,omitempty"`
	LastActivity       string              `json:"last_activity,omitempty"`
	Note               string              `json:"note,omitempty"`
}

type WatchSearch struct {
	Query  string
	Limit  int
	Offset int
}

func historySummary(r HistoryRecord) WatchSummary {
	s := WatchSummary{IMDbID: r.ID, Title: r.Name, MediaType: r.Type, LastActivity: r.State.LastWatched}
	if s.LastActivity == "" {
		s.LastActivity = r.Modified
	}
	if r.State.Duration > 0 && r.State.TimeOffset > 0 {
		s.ProgressPercent = 100 * float64(r.State.TimeOffset) / float64(r.State.Duration)
		if s.ProgressPercent > 100 {
			s.ProgressPercent = 100
		}
	}
	switch {
	case r.Type == "movie" && (r.State.TimesWatched > 0 || r.State.FlaggedWatched > 0):
		s.WatchStatus = "watched"
	case r.Type == "movie" && r.State.TimeOffset == 0:
		s.WatchStatus = "not_marked_watched"
	case r.Type == "series" && watchedBitCount(r.State.Watched) > 0:
		s.WatchStatus = "some_episodes_watched"
	case r.State.TimeOffset > 0:
		s.WatchStatus = "in_progress"
	default:
		s.WatchStatus = "episode_status_unverified"
	}
	return s
}

func (s WatchHistoryService) Search(ctx context.Context, opts WatchSearch) ([]WatchSummary, int, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Limit > 50 || opts.Offset < 0 || opts.Offset > 5000 {
		return nil, 0, errors.New("limit must be 1-50 and offset must be 0-5000")
	}
	stamps, err := s.Account.ListIDs(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	wantedCount := opts.Offset + opts.Limit + 1 // discover whether another page exists
	results := make([]WatchSummary, 0, opts.Limit)
	matched := 0
	// Search all titles for a query; for a recent-list read stop as soon as
	// the requested page is filled. DatastoreMeta itself never contains titles.
	for start := 0; start < len(stamps); start += 75 {
		end := min(start+75, len(stamps))
		ids := make([]string, 0, end-start)
		for _, stamp := range stamps[start:end] {
			ids = append(ids, stamp.ID)
		}
		items, err := s.Account.Get(ctx, ids)
		if err != nil {
			return nil, 0, err
		}
		byID := make(map[string]HistoryRecord, len(items))
		for _, item := range items {
			byID[item.ID] = item
		}
		for _, stamp := range stamps[start:end] {
			item, ok := byID[stamp.ID]
			if !ok || !hasWatchActivity(item) || (query != "" && !strings.Contains(strings.ToLower(item.Name), query)) {
				continue
			}
			if matched >= opts.Offset && len(results) < opts.Limit {
				results = append(results, historySummary(item))
			}
			matched++
		}
		if query == "" && matched >= wantedCount {
			break
		}
	}
	return results, matched, nil
}

func (s WatchHistoryService) Inspect(ctx context.Context, imdbID string, season *int) (WatchDetail, error) {
	if !validHistoryID(imdbID) {
		return WatchDetail{}, errors.New("invalid IMDb ID")
	}
	r, err := s.Account.GetOne(ctx, imdbID)
	if err != nil {
		return WatchDetail{}, err
	}
	if r == nil {
		return WatchDetail{IMDbID: imdbID, EpisodeHistory: "not_recorded", Note: "No watch record exists; do not infer that this title has never been watched elsewhere."}, nil
	}
	return s.inspectRecord(ctx, *r, season)
}

func (s WatchHistoryService) inspectRecord(ctx context.Context, r HistoryRecord, season *int) (WatchDetail, error) {
	if IsClearedHistoryItem(r) {
		return clearedWatchDetail(r), nil
	}
	detail := WatchDetail{
		IMDbID: r.ID, Title: r.Name, MediaType: r.Type, Recorded: true,
		LastActivity: r.State.LastWatched,
	}
	if r.Type == "movie" {
		watched := r.State.TimesWatched > 0 || r.State.FlaggedWatched > 0
		detail.MovieWatched = &watched
		if watched {
			if meta, err := s.Metadata.Fetch(ctx, "movie", r.ID); err == nil {
				detail.Plot = meta.Overview
			}
		}
		detail.MovieInProgress = !watched && r.State.TimeOffset > 0
		if r.State.Duration > 0 && r.State.TimeOffset > 0 {
			detail.ProgressPercent = min(100, 100*float64(r.State.TimeOffset)/float64(r.State.Duration))
		}
		if !watched {
			detail.Note = "Not marked watched in Stremio; this does not prove it has never been watched."
		}
		return detail, nil
	}
	meta, err := s.Metadata.Fetch(ctx, "series", r.ID)
	if err != nil {
		// A broken metadata request must never become a false 'unwatched'.
		detail.EpisodeHistory = "unverified"
		detail.Note = "Episode list unavailable; no safe spoiler boundary can be established."
		return detail, nil
	}
	return episodeDetails(r, meta, season), nil
}

func clearedWatchDetail(r HistoryRecord) WatchDetail {
	return WatchDetail{
		IMDbID: r.ID, Title: r.Name, MediaType: r.Type,
		Recorded: false, EpisodeHistory: "cleared",
		Note: "All playback data for this title was cleared. Stremio retains a removed synchronization tombstone; its API does not expose per-title physical deletion.",
	}
}

// ForgetTitle removes ALL Stremio playback history for one movie or series,
// including episode bitfields, last-played video and position, timestamps,
// duration and unrecognized state fields. Stremio's public client protocol
// only supports datastorePut; physical deletion of individual library items
// is unavailable. A removed temporary tombstone remains for synchronization.
// This operation is intentionally separate from selective episode unwatching.
func (s WatchHistoryService) ForgetTitle(ctx context.Context, imdbID, mediaType string) (WatchDetail, bool, error) {
	if !validHistoryID(imdbID) {
		return WatchDetail{}, false, errors.New("a valid imdb_id is required to clear a title")
	}
	previous, err := s.Account.GetOne(ctx, imdbID)
	if err != nil {
		return WatchDetail{}, false, err
	}
	if previous == nil {
		return WatchDetail{IMDbID: imdbID, MediaType: mediaType, EpisodeHistory: "not_recorded", Note: "No Stremio record exists; no playback data was changed."}, false, nil
	}
	if mediaType != "" && previous.Type != mediaType {
		return WatchDetail{}, false, errors.New("media_type conflicts with Stremio's saved title")
	}
	if IsClearedHistoryItem(*previous) {
		return clearedWatchDetail(*previous), false, nil
	}
	// Stremio has no conditional update. Abort if another device changes the
	// record between the initial read and the destructive rewrite.
	latest, err := s.Account.GetOne(ctx, imdbID)
	if err != nil {
		return WatchDetail{}, false, err
	}
	if !reflect.DeepEqual(rawHistoryRecord(previous), rawHistoryRecord(latest)) {
		return WatchDetail{}, false, errors.New("Stremio watch state changed while clearing; retry to avoid overwriting new playback")
	}
	item, err := ClearedHistoryItem(*previous, time.Now())
	if err != nil {
		return WatchDetail{}, false, err
	}
	if err := s.Account.Put(ctx, item); err != nil {
		return WatchDetail{}, false, err
	}
	confirmed, err := s.Account.GetOne(ctx, imdbID)
	if err != nil {
		return WatchDetail{}, false, fmt.Errorf("Stremio write completed but verification read failed: %w", err)
	}
	if confirmed == nil {
		return WatchDetail{}, false, errors.New("Stremio did not return the removed title for verification")
	}
	if !IsClearedHistoryItem(*confirmed) || hasWatchActivity(*confirmed) {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(confirmed.raw["state"], &fields)
		fieldStatus := make([]string, 0, len(fields))
		for name, raw := range fields {
			value := strings.TrimSpace(string(raw))
			empty := value == "null" || value == "false" || value == "0" || value == `""` || value == "[]" || value == "{}"
			fieldStatus = append(fieldStatus, fmt.Sprintf("%s:empty=%t", name, empty))
		}
		sort.Strings(fieldStatus)
		return WatchDetail{}, false, fmt.Errorf("Stremio did not fully clear playback state (removed=%t, temp=%t, watch_activity=%t, field_defaults=[%s])", confirmed.Removed, confirmed.Temp, hasWatchActivity(*confirmed), strings.Join(fieldStatus, ","))
	}
	return clearedWatchDetail(*confirmed), true, nil
}

func episodeDetails(r HistoryRecord, meta MediaMetadata, season *int) WatchDetail {
	detail := WatchDetail{
		IMDbID: r.ID, Title: r.Name, MediaType: "series", Recorded: true,
		Plot:         meta.Overview,
		LastActivity: r.State.LastWatched, Seasons: []SeasonWatchStatus{},
		EpisodeHistory: "verified",
		Note:           "Only checked episodes are confirmed watched. Other episodes are unconfirmed, not proof of never watching. Stremio does not retain individual episode watch dates.",
	}
	ids := make([]string, len(meta.Episodes))
	for i, ep := range meta.Episodes {
		ids[i] = ep.ID
	}
	flags := make([]bool, len(ids))
	if r.State.Watched == "" {
		detail.EpisodeHistory = "unverified"
		detail.Note = "No episode completion bitfield is stored. The last-played episode alone is insufficient to establish what was watched."
	} else {
		decoded, err := decodeWatchedEpisodes(r.State.Watched, ids)
		if err != nil {
			detail.EpisodeHistory = "unverified"
			detail.Note = "Episode completion bitmap cannot be mapped safely to current episodes; all completion statuses are unknown."
		} else {
			flags = decoded
		}
	}
	// A show may have hundreds of special features and hundreds of watched
	// episodes. By default, return completion bits for every ordinary season
	// but synopses for only the last eight confirmed episodes in episode
	// order. Selecting a season returns every watched synopsis in that season.
	includeSynopsis := make(map[int]bool)
	if season == nil {
		remaining := 8
		if detail.EpisodeHistory == "verified" {
			for i := len(flags) - 1; i >= 0 && remaining > 0; i-- {
				if flags[i] && meta.Episodes[i].Season > 0 {
					includeSynopsis[i] = true
					remaining--
				}
			}
		}
	} else {
		for i, ep := range meta.Episodes {
			if ep.Season == *season {
				includeSynopsis[i] = true
			}
		}
	}
	bySeason := map[int]*SeasonWatchStatus{}
	for i, ep := range meta.Episodes {
		if ep.Number < 1 {
			continue
		}
		entry, exists := bySeason[ep.Season]
		if !exists {
			entry = &SeasonWatchStatus{Season: ep.Season, WatchedEpisodes: []int{}, UnconfirmedEpisodes: []int{}, WatchedEpisodeDetails: []WatchedEpisode{}}
			bySeason[ep.Season] = entry
		}
		entry.EpisodeCount++
		if flags[i] && detail.EpisodeHistory == "verified" {
			entry.WatchedEpisodes = append(entry.WatchedEpisodes, ep.Number)
			if includeSynopsis[i] {
				entry.WatchedEpisodeDetails = append(entry.WatchedEpisodeDetails, WatchedEpisode{Episode: ep.Number, Title: ep.Name, Plot: ep.Overview})
			}
		} else {
			entry.UnconfirmedEpisodes = append(entry.UnconfirmedEpisodes, ep.Number)
		}
		if ep.ID == r.State.VideoID && r.State.TimeOffset > 0 && !flags[i] {
			n := ep.Number
			entry.InProgressEpisode = &n
		}
	}
	seasonKeys := make([]int, 0, len(bySeason))
	for k := range bySeason {
		seasonKeys = append(seasonKeys, k)
	}
	sort.Ints(seasonKeys)
	for _, n := range seasonKeys {
		entry := bySeason[n]
		entry.Completed = detail.EpisodeHistory == "verified" && len(entry.WatchedEpisodes) == entry.EpisodeCount
		if season != nil && *season == n || season == nil && (n != 0 || len(entry.WatchedEpisodes) > 0) {
			detail.Seasons = append(detail.Seasons, *entry)
		}
	}
	// Calculate the boundary from *every* season, even when the caller only
	// requested one. Specials (season 0) cannot advance the spoiler boundary.
	if detail.EpisodeHistory == "verified" {
		expectedSeason, expectedEpisode := 1, 1
		for i, ep := range meta.Episodes {
			if ep.Season < 1 || ep.Number < 1 {
				continue
			}
			if ep.Season == expectedSeason+1 && ep.Number == 1 && expectedEpisode > 1 {
				expectedSeason++
				expectedEpisode = 1
			}
			if ep.Season != expectedSeason || ep.Number != expectedEpisode || !flags[i] {
				break
			}
			detail.SpoilerSafeThrough = &EpisodePosition{Season: ep.Season, Episode: ep.Number}
			expectedEpisode++
		}
	}
	return detail
}

// MarkWatched updates Stremio's own datastore. Series require explicit
// episode numbers; marking an entire show based on a casual remark is unsafe.
func (s WatchHistoryService) MarkWatched(ctx context.Context, imdbID, mediaType string, season int, episodes []int) (WatchDetail, bool, error) {
	return s.setWatched(ctx, imdbID, mediaType, season, episodes, true)
}

// MarkUnwatched reverses specific episodes without touching others. When no
// episodes are supplied, it completely clears the title's playback state and
// marks the record removed for synchronization across devices.
func (s WatchHistoryService) MarkUnwatched(ctx context.Context, imdbID, mediaType string, season int, episodes []int) (WatchDetail, bool, error) {
	if season == 0 && len(episodes) == 0 {
		return s.ForgetTitle(ctx, imdbID, mediaType)
	}
	return s.setWatched(ctx, imdbID, mediaType, season, episodes, false)
}

func (s WatchHistoryService) setWatched(ctx context.Context, imdbID, mediaType string, season int, episodes []int, watched bool) (WatchDetail, bool, error) {
	if !validHistoryID(imdbID) {
		return WatchDetail{}, false, errors.New("a valid imdb_id is required to mark watched")
	}
	previous, err := s.Account.GetOne(ctx, imdbID)
	if err != nil {
		return WatchDetail{}, false, err
	}
	if previous != nil {
		if mediaType != "" && previous.Type != mediaType {
			return WatchDetail{}, false, errors.New("media_type conflicts with Stremio's saved title")
		}
		mediaType = previous.Type
	}
	if mediaType != "movie" && mediaType != "series" {
		return WatchDetail{}, false, errors.New("media_type (movie or series) is required for a title not yet in Stremio")
	}
	if mediaType == "movie" && (len(episodes) > 0 || season != 0) {
		return WatchDetail{}, false, errors.New("movies cannot have season or episodes")
	}
	if mediaType == "series" && (season < 0 || len(episodes) == 0 || len(episodes) > 30) {
		return WatchDetail{}, false, errors.New("to update a series, supply a season and 1-30 explicit episode numbers")
	}
	// An unknown title is not a candidate for unwatching. Do not create a
	// phantom Stremio record simply to store zero watched bits.
	if !watched && previous == nil {
		return WatchDetail{IMDbID: imdbID, MediaType: mediaType, EpisodeHistory: "not_recorded", Note: "No Stremio watch record exists; no completion markers were changed."}, false, nil
	}
	currentMovieWatched := previous != nil && (previous.State.TimesWatched > 0 || previous.State.FlaggedWatched > 0)
	if mediaType == "movie" && currentMovieWatched == watched {
		d, err := s.inspectRecord(ctx, *previous, nil)
		return d, false, err
	}
	meta := MediaMetadata{ID: imdbID, Type: mediaType}
	if mediaType == "series" || previous == nil {
		meta, err = s.Metadata.Fetch(ctx, mediaType, imdbID)
		if err != nil {
			return WatchDetail{}, false, err
		}
	}
	updates := make(map[string]any)
	var newFlags []bool
	if mediaType == "movie" {
		if watched {
			updates["timesWatched"] = 1
			updates["lastWatched"] = time.Now().UTC().Format(time.RFC3339Nano)
			updates["timeOffset"] = 0
		} else {
			updates["timesWatched"] = 0
			updates["flaggedWatched"] = 0
			// Keep timeOffset, lastWatched, and other playback history intact.
		}
	} else {
		ids := make([]string, len(meta.Episodes))
		for i, ep := range meta.Episodes {
			ids[i] = ep.ID
		}
		newFlags = make([]bool, len(ids))
		if previous != nil && previous.State.Watched != "" {
			newFlags, err = decodeWatchedEpisodes(previous.State.Watched, ids)
			if err != nil {
				return WatchDetail{}, false, err // never destroy unmappable history
			}
		}
		requested := make(map[int]bool, len(episodes))
		for _, number := range episodes {
			if number < 1 || requested[number] {
				return WatchDetail{}, false, errors.New("episode numbers must be unique positive integers")
			}
			requested[number] = true
		}
		found := make(map[int]bool)
		changed := false
		for i, ep := range meta.Episodes {
			if ep.Season == season && requested[ep.Number] {
				found[ep.Number] = true
				if newFlags[i] != watched {
					newFlags[i] = watched
					changed = true
				}
			}
		}
		if len(found) != len(requested) {
			return WatchDetail{}, false, fmt.Errorf("season %d does not contain all requested episode numbers; nothing was changed", season)
		}
		if !changed {
			d := episodeDetails(*previous, meta, &season)
			return d, false, nil
		}
		encoded, err := encodeWatchedEpisodes(newFlags, ids)
		if err != nil {
			return WatchDetail{}, false, err
		}
		updates["watched"] = encoded
		// Per-episode completion has no trustworthy individual watch date.
		// Do not invent a new lastWatched time for the entire series.
	}
	// Stremio does not support optimistic writes. Re-read immediately before
	// applying the full-record update to narrow the concurrent-playback race.
	latest, err := s.Account.GetOne(ctx, imdbID)
	if err != nil {
		return WatchDetail{}, false, err
	}
	if !reflect.DeepEqual(rawHistoryRecord(previous), rawHistoryRecord(latest)) {
		return WatchDetail{}, false, errors.New("Stremio watch state changed during the update; retry to preserve playback progress")
	}
	item, err := copyAndPatchHistoryRecord(previous, meta, time.Now(), updates)
	if err != nil {
		return WatchDetail{}, false, err
	}
	if err := s.Account.Put(ctx, item); err != nil {
		return WatchDetail{}, false, err
	}
	confirmed, err := s.Account.GetOne(ctx, imdbID)
	if err != nil || confirmed == nil {
		return WatchDetail{}, false, errors.New("Stremio write submitted but could not verify the resulting watch state")
	}
	if mediaType == "movie" {
		actualWatched := confirmed.State.TimesWatched > 0 || confirmed.State.FlaggedWatched > 0
		if actualWatched != watched {
			return WatchDetail{}, false, errors.New("Stremio did not retain the requested movie watch status")
		}
		d, err := s.inspectRecord(ctx, *confirmed, nil)
		return d, true, err
	}
	ids := make([]string, len(meta.Episodes))
	for i, ep := range meta.Episodes {
		ids[i] = ep.ID
	}
	actual, err := decodeWatchedEpisodes(confirmed.State.Watched, ids)
	if err != nil || !reflect.DeepEqual(actual, newFlags) {
		return WatchDetail{}, false, errors.New("Stremio did not retain the expected watched episodes")
	}
	return episodeDetails(*confirmed, meta, &season), true, nil
}

func rawHistoryRecord(r *HistoryRecord) map[string]json.RawMessage {
	if r == nil {
		return nil
	}
	return r.raw
}
