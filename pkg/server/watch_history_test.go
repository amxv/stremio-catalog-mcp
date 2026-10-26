package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
		{Action: "mark_watched", IMDbID: "tt1234567", MediaType: "series"},
	} {
		if _, err := rt.watchHistory(context.Background(), input); err == nil {
			t.Fatalf("expected strict scoped/series validation for %+v", input)
		}
	}
	if writes != 2 {
		t.Fatalf("validation failures must never write: %d", writes)
	}
}
