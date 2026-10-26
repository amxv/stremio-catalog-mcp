package stremio

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBatchCheckReturnsExactStatusAndEpisodesInOriginalOrder(t *testing.T) {
	f, svc := newHistoryFixture(t)
	past := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	future := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	f.metaData = map[string]any{
		"id": "tt1234567", "name": "A Fictional Show", "type": "series",
		"videos": []map[string]any{
			{"id": "tt1234567:1:4", "season": 1, "episode": 4, "released": future, "name": "Future spoiler"},
			{"id": "tt1234567:1:3", "season": 1, "episode": 3, "released": past, "name": "Present spoiler"},
			{"id": "tt1234567:1:1", "season": 1, "episode": 1, "released": past, "name": "Pilot"},
			{"id": "tt1234567:1:2", "season": 1, "episode": 2, "released": past, "name": "Second"},
		},
	}
	ids := []string{"tt1234567:1:1", "tt1234567:1:2", "tt1234567:1:3", "tt1234567:1:4"}
	bitmap, err := encodeWatchedEpisodes([]bool{true, true, false, false}, ids)
	if err != nil {
		t.Fatal(err)
	}
	f.set(t, "tt1234567", "series", fmt.Sprintf(`{"watched":%q,"timeOffset":12000,"video_id":"tt1234567:1:3"}`, bitmap))
	f.set(t, "tt7654321", "movie", `{"timesWatched":1}`)
	f.set(t, "tt7654322", "movie", `{"timesWatched":0,"timeOffset":2500,"duration":10000}`)
	f.set(t, "tt7654323", "movie", `{"timesWatched":0}`)
	f.set(t, "tt7654324", "movie", `{"timesWatched":0,"lastWatched":"2026-02-01T12:00:00Z"}`)
	f.mu.Lock()
	f.items["tt7654325"] = json.RawMessage(`{"_id":"tt7654325","name":"Cleared Movie","type":"movie","removed":true,"temp":true,"state":{}}`)
	f.mu.Unlock()

	requested := []string{"tt7654322", "tt9999999", "tt1234567", "tt7654321", "tt7654324", "tt7654323", "tt7654325"}
	checks, err := svc.Check(context.Background(), requested)
	if err != nil || len(checks) != len(requested) {
		t.Fatalf("check failed: %+v (%v)", checks, err)
	}
	wantStatus := []string{"partially_watched", "not_watched", "partially_watched", "watched", "unverified", "not_watched", "not_watched"}
	for i, c := range checks {
		if c.IMDbID != requested[i] || c.Status != wantStatus[i] {
			t.Fatalf("check[%d] unexpected result: %+v (expected ID %s, status %s)", i, c, requested[i], wantStatus[i])
		}
	}
	if checks[0].ProgressPercent != 25 || checks[1].Recorded || checks[6].Recorded {
		t.Fatalf("progress and recorded flags are incorrect: partial=%+v missing=%+v cleared=%+v", checks[0], checks[1], checks[6])
	}
	series := checks[2]
	if series.EpisodeHistory != "verified" {
		t.Fatalf("trusted episode bitfield must be explicitly identified: %+v", series)
	}
	if len(series.Seasons) != 1 || !reflect.DeepEqual(series.Seasons[0].WatchedEpisodes, []int{1, 2}) || series.Seasons[0].InProgressEpisode == nil || *series.Seasons[0].InProgressEpisode != 3 {
		t.Fatalf("exact watched episodes or in-progress episode missing: %+v", series)
	}
	if series.ReleasedEpisodeCount != 3 || series.UpcomingEpisodeCount != 1 || series.WatchedEpisodeCount != 2 || series.SpoilerSafeThrough == nil || series.SpoilerSafeThrough.Episode != 2 {
		t.Fatalf("wrong released-episode classification or boundary: %+v", series)
	}
	encoded, _ := json.Marshal(checks)
	if strings.Contains(string(encoded), "Future spoiler") || strings.Contains(string(encoded), "Present spoiler") {
		t.Fatalf("batch check must not disclose episode titles or plots: %s", encoded)
	}
	if f.writes != 0 {
		t.Fatal("batch read unexpectedly changed watch state")
	}
	if f.gets != 1 {
		t.Fatalf("check must use a single targeted datastoreGet, got %d requests", f.gets)
	}
}

func TestBatchCheckCountsOnlyAiredRegularEpisodes(t *testing.T) {
	f, svc := newHistoryFixture(t)
	past, future := time.Now().UTC().Add(-48*time.Hour).Format(time.RFC3339), time.Now().UTC().Add(48*time.Hour).Format(time.RFC3339)
	f.metaData = map[string]any{
		"id": "tt1234567", "name": "Current Series", "type": "series", "videos": []map[string]any{
			{"id": "tt1234567:0:1", "season": 0, "episode": 1, "released": past},
			{"id": "tt1234567:1:1", "season": 1, "episode": 1, "released": past},
			{"id": "tt1234567:1:2", "season": 1, "episode": 2, "released": past},
			{"id": "tt1234567:1:3", "season": 1, "episode": 3, "released": future},
		},
	}
	bitmap, _ := encodeWatchedEpisodes([]bool{false, true, true, false}, []string{"tt1234567:0:1", "tt1234567:1:1", "tt1234567:1:2", "tt1234567:1:3"})
	f.set(t, "tt1234567", "series", fmt.Sprintf(`{"watched":%q}`, bitmap))
	got, err := svc.Check(context.Background(), []string{"tt1234567"})
	want := WatchCheck{IMDbID: "tt1234567", Title: "A Fictional Show", MediaType: "series", Status: "watched", Recorded: true}
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("fully watched series should have the same minimal fields as a completed movie, without any episode data: got=%+v want=%+v err=%v", got, want, err)
	}
	asJSON, _ := json.Marshal(got[0])
	for _, forbidden := range []string{"watched_episodes", "seasons", "episode_history", "spoiler_safe_through", "released_episode_count", "upcoming_episode_count", "watched_episode_count", "note"} {
		if strings.Contains(string(asJSON), `"`+forbidden+`"`) {
			t.Fatalf("fully watched series must not include %q: %s", forbidden, asJSON)
		}
	}

	// Removing a watched bitfield makes episode completion unknown, not
	// proof that no episodes were watched. A partially played episode is
	// evidence of activity, but not of completion.
	f.set(t, "tt1234567", "series", `{"timeOffset":45000,"video_id":"tt1234567:1:2"}`)
	got, err = svc.Check(context.Background(), []string{"tt1234567"})
	if err != nil || got[0].Status != "partially_watched" || got[0].WatchedEpisodeCount != 0 || len(got[0].Seasons) != 1 || got[0].Seasons[0].InProgressEpisode == nil {
		t.Fatalf("uncertain completion must not become a false watched claim: %+v err=%v", got, err)
	}
	if got[0].EpisodeHistory != "unverified" {
		t.Fatal("missing watched bitfield must advertise unverified episode data")
	}
	f.set(t, "tt1234567", "series", `{"watched":"unmappable","timeOffset":45000}`)
	got, err = svc.Check(context.Background(), []string{"tt1234567"})
	if err != nil || got[0].Status != "unverified" || len(got[0].Seasons) != 0 {
		t.Fatalf("corrupt watched bitfield must be unknown: %+v err=%v", got, err)
	}
	f.metaData = map[string]any{} // metadata outage for existing series
	got, err = svc.Check(context.Background(), []string{"tt1234567", "tt7654321"})
	if err != nil || got[0].Status != "unverified" || got[1].Status != "not_watched" {
		t.Fatalf("metadata failure should not abort other requested IDs: %+v err=%v", got, err)
	}
}

func TestBatchCheckValidatesInputsBeforeReading(t *testing.T) {
	f, svc := newHistoryFixture(t)
	cases := [][]string{
		nil,
		{},
		{"tt1234567", "tt1234567"},
		{"tt1234567", "this-is-not-an-imdb-id"},
	}
	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("tt%07d", i+1)
	}
	cases = append(cases, tooMany)
	for _, ids := range cases {
		if _, err := svc.Check(context.Background(), ids); err == nil {
			t.Fatalf("invalid batch should fail before network: %v", ids)
		}
	}
	if f.writes != 0 {
		t.Fatal("input validation mutated Stremio history")
	}
}
