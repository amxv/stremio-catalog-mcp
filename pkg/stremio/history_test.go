package stremio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestStremioWatchedBitfieldAnchorAndRoundTrip(t *testing.T) {
	ids := []string{}
	for i := 1; i <= 9; i++ {
		ids = append(ids, fmt.Sprintf("tt2934286:1:%d", i))
	}
	// This fixture comes from stremio-core's own watched_bitfield.rs tests.
	const native = "tt2934286:1:5:5:eJyTZwAAAEAAIA=="
	flags, err := decodeWatchedEpisodes(native, ids)
	if err != nil {
		t.Fatal(err)
	}
	for i, flag := range flags {
		if flag != (i < 5) {
			t.Fatalf("S01E%d unexpectedly marked watched=%t", i+1, flag)
		}
	}
	encoded, err := encodeWatchedEpisodes(flags, ids)
	if err != nil {
		t.Fatal(err)
	}
	roundTripped, err := decodeWatchedEpisodes(encoded, ids)
	if err != nil || !reflect.DeepEqual(flags, roundTripped) {
		t.Fatalf("native watched bitmap did not round trip: %s, %v", encoded, err)
	}
	// An added special before the first season must not shift completion flags.
	withSpecial := append([]string{"tt2934286:0:1"}, ids...)
	shifted, err := decodeWatchedEpisodes(native, withSpecial)
	if err != nil {
		t.Fatal(err)
	}
	if shifted[0] || !shifted[5] || shifted[6] {
		t.Fatalf("incorrect shifted episode mapping: %v", shifted)
	}
	if _, err := decodeWatchedEpisodes("tt1234567:1:2:2:eJyTZwAAAEAAIA==", ids); err == nil {
		t.Fatal("unrecognized anchor must not silently become an unwatched show")
	}
	if _, err := decodeWatchedEpisodes("broken", ids); err == nil {
		t.Fatal("malformed bitmap must fail closed")
	}
}

type historyFixture struct {
	mu       sync.Mutex
	items    map[string]json.RawMessage
	writes   int
	metaData map[string]any
	server   *httptest.Server
}

func newHistoryFixture(t *testing.T) (*historyFixture, WatchHistoryService) {
	t.Helper()
	f := &historyFixture{items: map[string]json.RawMessage{}, metaData: map[string]any{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/meta/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": f.metaData})
			return
		}
		var request struct {
			AuthKey    string            `json:"authKey"`
			Collection string            `json:"collection"`
			IDs        []string          `json:"ids"`
			Changes    []json.RawMessage `json:"changes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.AuthKey != "fake-auth" || request.Collection != "libraryItem" {
			http.Error(w, "bad datastore request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/api/datastoreMeta":
			var entries [][]any
			for id := range f.items {
				entries = append(entries, []any{id, int64(1_798_000_000_000)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": entries})
		case "/api/datastoreGet":
			var items []json.RawMessage
			for _, id := range request.IDs {
				if item := f.items[id]; item != nil {
					items = append(items, item)
				}
			}
			if items == nil {
				items = []json.RawMessage{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": items})
		case "/api/datastorePut":
			for _, item := range request.Changes {
				var value struct {
					ID string `json:"_id"`
				}
				if json.Unmarshal(item, &value) != nil || value.ID == "" {
					http.Error(w, "missing ID", 400)
					return
				}
				f.items[value.ID] = item
				f.writes++
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"success": true}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f, WatchHistoryService{
		Account:  HistoryAccount{API: AccountAPI{BaseURL: f.server.URL, AuthKey: "fake-auth"}},
		Metadata: MetadataClient{BaseURL: f.server.URL},
	}
}

func (f *historyFixture) set(t *testing.T, id, mediaType, state string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	name := "A Fictional Show"
	if mediaType == "movie" {
		name = "A Fictional Movie"
	}
	f.items[id] = json.RawMessage(fmt.Sprintf(`{"_id":%q,"name":%q,"type":%q,"poster":"https://example.invalid/poster.jpg","_mtime":"2026-10-01T00:00:00Z","removed":false,"temp":false,"custom_field":{"preserve":true},"state":%s}`, id, name, mediaType, state))
}

func (f *historyFixture) raw(t *testing.T, id string) map[string]json.RawMessage {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(f.items[id], &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *historyFixture) episodes() {
	f.metaData = map[string]any{
		"id": "tt1234567", "type": "series", "name": "A Fictional Show", "description": "A fictional premise, without season twists.",
		"videos": []map[string]any{
			{"id": "tt1234567:2:1", "season": 2, "episode": 1, "name": "Spoiler: future twist"},
			{"id": "tt1234567:1:3", "season": 1, "episode": 3, "name": "Spoiler: the truth", "overview": "Spoiler: everything is revealed"},
			{"id": "tt1234567:1:1", "season": 1, "episode": 1, "name": "Pilot", "overview": "The characters meet."},
			{"id": "tt1234567:1:2", "season": 1, "episode": 2, "name": "Second Chapter", "description": "The mystery deepens."},
		},
	}
}

func TestWatchHistorySeriesEpisodesAreSpoilerSafe(t *testing.T) {
	f, svc := newHistoryFixture(t)
	f.episodes()
	ids := []string{"tt1234567:1:1", "tt1234567:1:2", "tt1234567:1:3", "tt1234567:2:1"}
	bits, _ := encodeWatchedEpisodes([]bool{true, true, false, true}, ids)
	f.set(t, "tt1234567", "series", fmt.Sprintf(`{"watched":%q,"video_id":"tt1234567:1:3","timeOffset":30000,"duration":100000,"timesWatched":0}`, bits))
	season := 1
	d, err := svc.Inspect(context.Background(), "tt1234567", &season)
	if err != nil {
		t.Fatal(err)
	}
	if d.EpisodeHistory != "verified" || d.SpoilerSafeThrough == nil || *d.SpoilerSafeThrough != (EpisodePosition{1, 2}) {
		t.Fatalf("spoiler boundary must be S01E02, got %+v", d)
	}
	if len(d.Seasons) != 1 || !reflect.DeepEqual(d.Seasons[0].WatchedEpisodes, []int{1, 2}) || !reflect.DeepEqual(d.Seasons[0].UnconfirmedEpisodes, []int{3}) || d.Seasons[0].InProgressEpisode == nil || *d.Seasons[0].InProgressEpisode != 3 {
		t.Fatalf("incorrect first-season watch state: %+v", d.Seasons)
	}
	if len(d.Seasons[0].WatchedEpisodeDetails) != 2 || d.Seasons[0].WatchedEpisodeDetails[0].Title != "Pilot" || d.Seasons[0].WatchedEpisodeDetails[1].Plot != "The mystery deepens." || d.Plot == "" {
		t.Fatalf("watched episode summaries missing: %+v", d.Seasons)
	}
	encoded, _ := json.Marshal(d)
	if strings.Contains(string(encoded), "Spoiler:") || strings.Contains(string(encoded), "future twist") {
		t.Fatalf("unwatched episode title or plot leaked: %s", encoded)
	}
	// Missing/stale episode bitfields must never be interpreted as "all unwatched".
	f.set(t, "tt1234567", "series", `{"video_id":"tt1234567:1:3","timeOffset":10000,"timesWatched":2}`)
	d, err = svc.Inspect(context.Background(), "tt1234567", nil)
	if err != nil || d.EpisodeHistory != "unverified" || d.SpoilerSafeThrough != nil || len(d.Seasons[0].WatchedEpisodes) != 0 {
		t.Fatalf("uncertain watch history became a confident spoiler boundary: %+v (%v)", d, err)
	}
}

func TestWatchHistoryMarkEpisodesPreservesUnrelatedState(t *testing.T) {
	f, svc := newHistoryFixture(t)
	f.episodes()
	ids := []string{"tt1234567:1:1", "tt1234567:1:2", "tt1234567:1:3", "tt1234567:2:1"}
	bits, _ := encodeWatchedEpisodes([]bool{false, false, false, true}, ids)
	f.set(t, "tt1234567", "series", fmt.Sprintf(`{"watched":%q,"video_id":"tt1234567:1:3","timeOffset":32000,"duration":92000,"customPlayback":"keep-this"}`, bits))
	d, changed, err := svc.MarkWatched(context.Background(), "tt1234567", "series", 1, []int{1, 2})
	if err != nil || !changed || d.SpoilerSafeThrough == nil || d.SpoilerSafeThrough.Episode != 2 {
		t.Fatalf("mark-watched failed: detail=%+v changed=%t err=%v", d, changed, err)
	}
	item := f.raw(t, "tt1234567")
	var state map[string]any
	_ = json.Unmarshal(item["state"], &state)
	if state["customPlayback"] != "keep-this" || state["timeOffset"] != float64(32000) || state["video_id"] != "tt1234567:1:3" || !strings.Contains(string(item["custom_field"]), `"preserve":true`) {
		t.Fatalf("marking episodes destroyed unrelated playback metadata: %+v", state)
	}
	_, changed, err = svc.MarkWatched(context.Background(), "tt1234567", "series", 1, []int{1, 2})
	if err != nil || changed || f.writes != 1 {
		t.Fatalf("repeat mark must be no-op: %t, %v, writes=%d", changed, err, f.writes)
	}
	_, _, err = svc.MarkWatched(context.Background(), "tt1234567", "series", 1, []int{500})
	if err == nil || f.writes != 1 {
		t.Fatalf("unknown episode must not alter account: %v", err)
	}
}

func TestWatchHistoryMarkUnwatchedEpisodesIsSelectiveAndIdempotent(t *testing.T) {
	f, svc := newHistoryFixture(t)
	f.episodes()
	ids := []string{"tt1234567:1:1", "tt1234567:1:2", "tt1234567:1:3", "tt1234567:2:1"}
	initial, _ := encodeWatchedEpisodes([]bool{true, true, true, true}, ids)
	f.set(t, "tt1234567", "series", fmt.Sprintf(`{"watched":%q,"video_id":"tt1234567:1:3","timeOffset":42000,"lastWatched":"2026-01-02T00:00:00Z","customPlayback":"keep-this"}`, initial))
	detail, changed, err := svc.MarkUnwatched(context.Background(), "tt1234567", "series", 1, []int{2, 3})
	if err != nil || !changed || detail.SpoilerSafeThrough == nil || *detail.SpoilerSafeThrough != (EpisodePosition{Season: 1, Episode: 1}) {
		t.Fatalf("unwatching E2/E3 should leave the boundary at E1: %+v changed=%v err=%v", detail, changed, err)
	}
	if len(detail.Seasons) != 1 || !reflect.DeepEqual(detail.Seasons[0].WatchedEpisodes, []int{1}) {
		t.Fatalf("unwatch changed the requested season unexpectedly: %+v", detail.Seasons)
	}
	fullDetail, err := svc.Inspect(context.Background(), "tt1234567", nil)
	if err != nil || len(fullDetail.Seasons) < 2 || !reflect.DeepEqual(fullDetail.Seasons[1].WatchedEpisodes, []int{1}) {
		t.Fatalf("unwatch changed another season: %+v (%v)", fullDetail.Seasons, err)
	}
	item := f.raw(t, "tt1234567")
	var state map[string]any
	_ = json.Unmarshal(item["state"], &state)
	if state["video_id"] != "tt1234567:1:3" || state["timeOffset"] != float64(42000) || state["customPlayback"] != "keep-this" || state["lastWatched"] != "2026-01-02T00:00:00Z" || !strings.Contains(string(item["custom_field"]), `"preserve":true`) {
		t.Fatalf("unwatch must preserve all other fields: %+v", state)
	}
	_, changed, err = svc.MarkUnwatched(context.Background(), "tt1234567", "series", 1, []int{2, 3})
	if err != nil || changed || f.writes != 1 {
		t.Fatalf("repeated unwatch should not write: changed=%v err=%v writes=%d", changed, err, f.writes)
	}
	_, _, err = svc.MarkUnwatched(context.Background(), "tt1234567", "series", 1, []int{99})
	if err == nil || f.writes != 1 {
		t.Fatalf("invalid episode must not write: %v", err)
	}
	// Rewatching restores only those two bits; the newly-unwatched episode
	// never accidentally alters another season.
	detail, changed, err = svc.MarkWatched(context.Background(), "tt1234567", "series", 1, []int{2, 3})
	if err != nil || !changed || len(detail.Seasons[0].WatchedEpisodes) != 3 || f.writes != 2 {
		t.Fatalf("rewatch failed: %+v changed=%v err=%v writes=%d", detail.Seasons, changed, err, f.writes)
	}
}

func TestWatchHistoryDefaultRecapIsCompactAndSeasonSelectionIsComplete(t *testing.T) {
	meta := MediaMetadata{ID: "tt1234567", Name: "A Fictional Show", Type: "series"}
	ids := make([]string, 0, 24)
	flags := make([]bool, 0, 24)
	for season := 0; season <= 2; season++ {
		for episode := 1; episode <= 8; episode++ {
			id := fmt.Sprintf("tt1234567:%d:%d", season, episode)
			meta.Episodes = append(meta.Episodes, Episode{ID: id, Season: season, Number: episode, Name: fmt.Sprintf("Episode %d", episode), Overview: "A verified plot description."})
			ids = append(ids, id)
			flags = append(flags, season > 0)
		}
	}
	bits, err := encodeWatchedEpisodes(flags, ids)
	if err != nil {
		t.Fatal(err)
	}
	r := HistoryRecord{ID: "tt1234567", Name: "A Fictional Show", Type: "series"}
	r.State.Watched = bits
	defaultResult := episodeDetails(r, meta, nil)
	if len(defaultResult.Seasons) != 2 || len(defaultResult.Seasons[0].WatchedEpisodes) != 8 || len(defaultResult.Seasons[0].WatchedEpisodeDetails) != 0 || len(defaultResult.Seasons[1].WatchedEpisodeDetails) != 8 {
		t.Fatalf("default recap should omit unviewed specials and limit synopses to last eight episodes: %+v", defaultResult.Seasons)
	}
	firstSeason := 1
	seasonResult := episodeDetails(r, meta, &firstSeason)
	if len(seasonResult.Seasons) != 1 || len(seasonResult.Seasons[0].WatchedEpisodeDetails) != 8 {
		t.Fatalf("exact season should include every watched synopsis: %+v", seasonResult.Seasons)
	}
	specials := 0
	specialsResult := episodeDetails(r, meta, &specials)
	if len(specialsResult.Seasons) != 1 || len(specialsResult.Seasons[0].UnconfirmedEpisodes) != 8 {
		t.Fatalf("explicit specials season should be available: %+v", specialsResult.Seasons)
	}
}

func TestWatchHistoryMarkUnwatchedMoviePreservesPlaybackAndNoops(t *testing.T) {
	f, svc := newHistoryFixture(t)
	f.metaData = map[string]any{"id": "tt7654321", "name": "A Fictional Film", "type": "movie", "description": "An imagined plot."}
	f.set(t, "tt7654321", "movie", `{"timesWatched":3,"flaggedWatched":1,"timeOffset":7000,"duration":30000,"lastWatched":"2026-01-02T00:00:00Z","customPlayback":"keep"}`)
	detail, changed, err := svc.MarkUnwatched(context.Background(), "tt7654321", "movie", 0, nil)
	if err != nil || !changed || detail.MovieWatched == nil || *detail.MovieWatched || !detail.MovieInProgress {
		t.Fatalf("movie status not cleared while keeping playback: %+v changed=%v err=%v", detail, changed, err)
	}
	item := f.raw(t, "tt7654321")
	var state map[string]any
	_ = json.Unmarshal(item["state"], &state)
	if state["timesWatched"] != float64(0) || state["flaggedWatched"] != float64(0) || state["timeOffset"] != float64(7000) || state["customPlayback"] != "keep" || state["lastWatched"] != "2026-01-02T00:00:00Z" || !strings.Contains(string(item["custom_field"]), `"preserve":true`) {
		t.Fatalf("movie unwatch modified unrelated state: %+v", state)
	}
	_, changed, err = svc.MarkUnwatched(context.Background(), "tt7654321", "", 0, nil)
	if err != nil || changed || f.writes != 1 {
		t.Fatalf("idempotent movie unwatch failed: %v changed=%v writes=%d", err, changed, f.writes)
	}
}

func TestWatchHistoryMarkUnwatchedMissingRecordDoesNotCreateHistory(t *testing.T) {
	f, svc := newHistoryFixture(t)
	d, changed, err := svc.MarkUnwatched(context.Background(), "tt7654321", "movie", 0, nil)
	if err != nil || changed || d.EpisodeHistory != "not_recorded" || f.writes != 0 {
		t.Fatalf("unwatching unknown movie created a record: %+v changed=%v err=%v", d, changed, err)
	}
	d, changed, err = svc.MarkUnwatched(context.Background(), "tt7654321", "series", 1, []int{1})
	if err != nil || changed || d.EpisodeHistory != "not_recorded" || f.writes != 0 {
		t.Fatalf("unwatching unknown episode created a record: %+v changed=%v err=%v", d, changed, err)
	}
}

func TestWatchHistoryNewMovieCreatesTemporaryRecordAndIsIdempotent(t *testing.T) {
	f, svc := newHistoryFixture(t)
	f.metaData = map[string]any{"id": "tt7654321", "type": "movie", "name": "A Fictional Film", "poster": "https://example.invalid/movie.jpg", "description": "A quirky fictional adventure."}
	d, changed, err := svc.MarkWatched(context.Background(), "tt7654321", "movie", 0, nil)
	if err != nil || !changed || d.MovieWatched == nil || !*d.MovieWatched || d.Plot != "A quirky fictional adventure." {
		t.Fatalf("movie mark failed: %+v changed=%t err=%v", d, changed, err)
	}
	item := f.raw(t, "tt7654321")
	if string(item["removed"]) != "true" || string(item["temp"]) != "true" {
		t.Fatalf("marking watched must not add unseen titles to the Library tab: %+v", item)
	}
	_, changed, err = svc.MarkWatched(context.Background(), "tt7654321", "movie", 0, nil)
	if err != nil || changed || f.writes != 1 {
		t.Fatalf("repeat movie mark must be no-op: %t %v", changed, err)
	}
}

func TestWatchHistorySearchFiltersLibraryOnlyTitles(t *testing.T) {
	f, svc := newHistoryFixture(t)
	f.set(t, "tt1234567", "series", `{"lastWatched":"2026-10-01T00:00:00Z","timeOffset":10,"duration":100}`)
	f.set(t, "tt7654321", "movie", `{"timesWatched":1,"lastWatched":"2026-10-01T00:00:00Z"}`)
	f.set(t, "tt1234568", "movie", `{}`) // merely saved to the Stremio library
	items, _, err := svc.Search(context.Background(), WatchSearch{Limit: 20})
	if err != nil || len(items) != 2 {
		t.Fatalf("history must not be confused with the library: %+v %v", items, err)
	}
	items, _, err = svc.Search(context.Background(), WatchSearch{Query: "fictional show"})
	if err != nil || len(items) != 1 || items[0].IMDbID != "tt1234567" {
		t.Fatalf("case-insensitive title search failed: %+v %v", items, err)
	}
}

func TestWatchHistoryRejectsCorruptEpisodeStateWithoutWriting(t *testing.T) {
	f, svc := newHistoryFixture(t)
	f.episodes()
	f.set(t, "tt1234567", "series", `{"watched":"tt9999999:1:1:1:eJyTZwAAAEAAIA=="}`)
	d, err := svc.Inspect(context.Background(), "tt1234567", nil)
	if err != nil || d.EpisodeHistory != "unverified" || d.SpoilerSafeThrough != nil {
		t.Fatalf("corrupt bitmap must result in unverified history: %+v %v", d, err)
	}
	_, _, err = svc.MarkWatched(context.Background(), "tt1234567", "series", 1, []int{1})
	if err == nil || f.writes != 0 {
		t.Fatalf("corrupt existing bitmap must not be overwritten: %v", err)
	}
}

func TestHistoryAccountMetadataAndRoundTrip(t *testing.T) {
	f, svc := newHistoryFixture(t)
	f.set(t, "tt7654321", "movie", `{"timesWatched":1}`)
	stamps, err := svc.Account.ListIDs(context.Background())
	if err != nil || len(stamps) != 1 || stamps[0].ID != "tt7654321" {
		t.Fatalf("unexpected datastoreMeta response: %+v %v", stamps, err)
	}
	r, err := svc.Account.GetOne(context.Background(), "tt7654321")
	if err != nil || r == nil || r.State.TimesWatched != 1 {
		t.Fatalf("unexpected datastoreGet response: %+v %v", r, err)
	}
	// Account read operations have no side effects.
	if f.writes != 0 {
		t.Fatal("read changed the account")
	}
}
