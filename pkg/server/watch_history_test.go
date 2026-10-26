package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
)

func TestWatchHistoryWholeTitleUnwatchRequiresNoEpisodeList(t *testing.T) {
	var mu sync.Mutex
	items := map[string]json.RawMessage{
		"tt1234567": json.RawMessage(`{"_id":"tt1234567","name":"Sample Series","type":"series","removed":false,"temp":false,"state":{"watched":"corrupt","timeOffset":1000,"video_id":"tt1234567:1:1"}}`),
		"tt7654321": json.RawMessage(`{"_id":"tt7654321","name":"Sample Movie","type":"movie","removed":false,"temp":false,"state":{"timesWatched":1,"lastWatched":"2026-01-01T00:00:00Z"}}`),
	}
	writes := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			AuthKey string            `json:"authKey"`
			IDs     []string          `json:"ids"`
			Changes []json.RawMessage `json:"changes"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.AuthKey != "test-key" {
			http.Error(w, "invalid auth", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/datastoreGet":
			result := []json.RawMessage{}
			for _, id := range request.IDs {
				if record := items[id]; record != nil {
					result = append(result, record)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
		case "/api/datastorePut":
			for _, record := range request.Changes {
				var v struct {
					ID string `json:"_id"`
				}
				if json.Unmarshal(record, &v) != nil || v.ID == "" {
					http.Error(w, "bad item", http.StatusBadRequest)
					return
				}
				items[v.ID] = record
				writes++
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"success": true}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	rt := Runtime{Account: stremio.AccountAPI{BaseURL: api.URL, AuthKey: "test-key"}}
	for _, id := range []string{"tt1234567", "tt7654321"} {
		result, err := rt.watchHistory(context.Background(), WatchHistoryInput{Action: "mark_unwatched", IMDbID: id})
		if err != nil || !result.Changed || result.Detail == nil || result.Detail.Recorded || !strings.Contains(result.Message, "tombstone") {
			t.Fatalf("full-title unwatch failed for %s: %+v err=%v", id, result, err)
		}
		var item map[string]json.RawMessage
		if err := json.Unmarshal(items[id], &item); err != nil {
			t.Fatal(err)
		}
		if string(item["state"]) != "{}" || string(item["removed"]) != "true" || string(item["temp"]) != "true" {
			t.Fatalf("title %s retained playback state: %+v", id, item)
		}
		again, err := rt.watchHistory(context.Background(), WatchHistoryInput{Action: "mark_unwatched", IMDbID: id})
		if err != nil || again.Changed {
			t.Fatalf("second unwatch should be a no-op: %+v err=%v", again, err)
		}
	}
	if writes != 2 {
		t.Fatalf("expected exactly two writes, got %d", writes)
	}
	season := 1
	for _, input := range []WatchHistoryInput{
		{Action: "mark_unwatched", IMDbID: "tt1234567", Season: &season},
		{Action: "mark_unwatched", IMDbID: "tt1234567", Episodes: []int{1}},
		{Action: "mark_watched", IMDbID: "tt1234567", Season: &season},
	} {
		if _, err := rt.watchHistory(context.Background(), input); err == nil {
			t.Fatalf("expected strict scoped/series validation for %+v", input)
		}
	}
	if writes != 2 {
		t.Fatalf("validation failures must never write: %d", writes)
	}
}

func TestWatchHistoryOneCallWithTitleMarksWholeSeriesAndResolvesUnknownMovie(t *testing.T) {
	past := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	future := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	seriesID, movieID, newMovieID := "tt1234567", "tt7654321", "tt7654322"
	saved := map[string]json.RawMessage{
		seriesID: json.RawMessage(`{"_id":"tt1234567","name":"A Fictional Series","type":"series","removed":false,"temp":false,"state":{"video_id":"tt1234567:1:1","timeOffset":1000}}`),
	}
	writes := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/meta/series/"+seriesID+".json" {
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"id": seriesID, "type": "series", "name": "A Fictional Series", "videos": []map[string]any{
				{"id": seriesID + ":1:1", "season": 1, "episode": 1, "released": past},
				{"id": seriesID + ":1:2", "season": 1, "episode": 2, "released": past},
				{"id": seriesID + ":1:3", "season": 1, "episode": 3, "released": future},
			}}})
			return
		}
		if r.URL.Path == "/meta/movie/"+movieID+".json" {
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"id": movieID, "name": "A Fictional Movie", "type": "movie"}})
			return
		}
		if r.URL.Path == "/meta/movie/"+newMovieID+".json" {
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"id": newMovieID, "name": "New Movie", "type": "movie"}})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/catalog/") {
			results := []map[string]string{}
			if strings.HasPrefix(r.URL.Path, "/catalog/movie/") && strings.Contains(r.URL.Path, "A Fictional Movie") {
				results = append(results, map[string]string{"id": movieID, "type": "movie", "name": "A Fictional Movie"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"metas": results})
			return
		}
		var req struct {
			AuthKey string            `json:"authKey"`
			IDs     []string          `json:"ids"`
			Changes []json.RawMessage `json:"changes"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.AuthKey != "test-key" {
			http.Error(w, "invalid auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/datastoreMeta":
			index := [][]any{}
			for id := range saved {
				index = append(index, []any{id, int64(1798000000000)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": index})
		case "/api/datastoreGet":
			items := []json.RawMessage{}
			for _, id := range req.IDs {
				if raw := saved[id]; raw != nil {
					items = append(items, raw)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": items})
		case "/api/datastorePut":
			for _, item := range req.Changes {
				var value struct {
					ID string `json:"_id"`
				}
				if json.Unmarshal(item, &value) != nil || value.ID == "" {
					http.Error(w, "missing ID", http.StatusBadRequest)
					return
				}
				saved[value.ID] = item
				writes++
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"success": true}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	rt := Runtime{Account: stremio.AccountAPI{BaseURL: api.URL, AuthKey: "test-key"}, HistoryMetadata: stremio.MetadataClient{BaseURL: api.URL}}

	series, err := rt.watchHistory(context.Background(), WatchHistoryInput{Action: "mark_watched", Query: "A Fictional Series"})
	if err != nil || !series.Changed || series.Detail == nil || series.Detail.EpisodeHistory != "verified" || len(series.Detail.Seasons) != 1 || len(series.Detail.Seasons[0].WatchedEpisodes) != 2 || !strings.Contains(series.Message, "one Stremio write") || writes != 1 {
		t.Fatalf("title-only full-series update must require only one tool call and one write: %+v err=%v writes=%d", series, err, writes)
	}
	again, err := rt.watchHistory(context.Background(), WatchHistoryInput{Action: "mark_watched", IMDbID: seriesID})
	if err != nil || again.Changed || writes != 1 {
		t.Fatalf("repeat full-series update must be idempotent: %+v err=%v writes=%d", again, err, writes)
	}
	movie, err := rt.watchHistory(context.Background(), WatchHistoryInput{Action: "mark_watched", Query: "A Fictional Movie"})
	if err != nil || !movie.Changed || movie.Detail == nil || movie.Detail.MovieWatched == nil || !*movie.Detail.MovieWatched || writes != 2 {
		t.Fatalf("unknown movie title should resolve uniquely without media_type: %+v err=%v writes=%d", movie, err, writes)
	}
	newMovie, err := rt.watchHistory(context.Background(), WatchHistoryInput{Action: "mark_watched", IMDbID: newMovieID})
	if err != nil || !newMovie.Changed || newMovie.Detail == nil || newMovie.Detail.MovieWatched == nil || !*newMovie.Detail.MovieWatched || writes != 3 {
		t.Fatalf("unknown IMDb ID should auto-resolve movie type in one call: %+v err=%v writes=%d", newMovie, err, writes)
	}
	cleared, err := rt.watchHistory(context.Background(), WatchHistoryInput{Action: "mark_unwatched", Query: "A Fictional Series"})
	if err != nil || !cleared.Changed || cleared.Detail == nil || cleared.Detail.Recorded || writes != 4 {
		t.Fatalf("title-only unwatch must clear the series: %+v err=%v writes=%d", cleared, err, writes)
	}
}
