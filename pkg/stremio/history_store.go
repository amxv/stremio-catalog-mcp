package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type HistoryAccount struct {
	API AccountAPI
}

type HistoryStamp struct {
	ID         string
	ModifiedAt int64 // Unix milliseconds
}

// HistoryRecord keeps the original Stremio payload for lossless targeted
// updates. The account API's datastorePut writes complete library items.
type HistoryRecord struct {
	ID       string `json:"_id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Poster   string `json:"poster"`
	Year     string `json:"year"`
	Modified string `json:"_mtime"`
	Removed  bool   `json:"removed"`
	Temp     bool   `json:"temp"`
	State    struct {
		LastWatched    string `json:"lastWatched"`
		TimesWatched   int    `json:"timesWatched"`
		FlaggedWatched int    `json:"flaggedWatched"`
		TimeOffset     int64  `json:"timeOffset"`
		Duration       int64  `json:"duration"`
		VideoID        string `json:"video_id"`
		Watched        string `json:"watched"`
	} `json:"state"`
	raw map[string]json.RawMessage
}

func decodeHistoryRecord(data json.RawMessage) (HistoryRecord, error) {
	var r HistoryRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r.raw); err != nil {
		return r, err
	}
	if !validHistoryID(r.ID) || (r.Type != "movie" && r.Type != "series") {
		return r, errors.New("invalid Stremio history item")
	}
	return r, nil
}

// ListIDs queries datastoreMeta, which is considerably smaller than loading
// the entire library. The result includes saved but never-played titles too;
// higher layers filter those out of watch history results.
func (h HistoryAccount) ListIDs(ctx context.Context) ([]HistoryStamp, error) {
	raw, err := h.API.call(ctx, "DatastoreMeta", map[string]any{"collection": "libraryItem"})
	if err != nil {
		return nil, err
	}
	var tuples [][]json.RawMessage
	if err := json.Unmarshal(raw, &tuples); err != nil {
		return nil, fmt.Errorf("invalid Stremio history index: %w", err)
	}
	seen := make(map[string]bool, len(tuples))
	result := make([]HistoryStamp, 0, len(tuples))
	for _, tuple := range tuples {
		if len(tuple) < 2 {
			continue
		}
		var id string
		var modified int64
		if json.Unmarshal(tuple[0], &id) != nil || json.Unmarshal(tuple[1], &modified) != nil || !validHistoryID(id) || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, HistoryStamp{ID: id, ModifiedAt: modified})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ModifiedAt > result[j].ModifiedAt })
	return result, nil
}

func (h HistoryAccount) Get(ctx context.Context, ids []string) ([]HistoryRecord, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, errors.New("request 1-100 IMDb IDs per history page")
	}
	for _, id := range ids {
		if !validHistoryID(id) {
			return nil, errors.New("invalid IMDb ID")
		}
	}
	raw, err := h.API.call(ctx, "DatastoreGet", map[string]any{
		"collection": "libraryItem", "ids": ids, "all": false,
	})
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("invalid Stremio history records: %w", err)
	}
	result := make([]HistoryRecord, 0, len(items))
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	for _, item := range items {
		r, err := decodeHistoryRecord(item)
		if err != nil {
			return nil, err
		}
		if !wanted[r.ID] {
			return nil, errors.New("Stremio returned an unrelated history item")
		}
		result = append(result, r)
	}
	return result, nil
}

func (h HistoryAccount) GetOne(ctx context.Context, id string) (*HistoryRecord, error) {
	items, err := h.Get(ctx, []string{id})
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

// Put writes exactly one item and explicitly checks datastorePut's success
// response. It never alters the add-on collection or the Redis catalog rows.
func (h HistoryAccount) Put(ctx context.Context, item map[string]json.RawMessage) error {
	raw, err := h.API.call(ctx, "DatastorePut", map[string]any{
		"collection": "libraryItem", "changes": []any{item},
	})
	if err != nil {
		return err
	}
	var result struct {
		Success bool `json:"success"`
	}
	if json.Unmarshal(raw, &result) != nil || !result.Success {
		return errors.New("Stremio did not confirm the watch-history update")
	}
	return nil
}

// ClearedHistoryItem produces the strongest per-title reset supported by
// Stremio's datastore API. Stremio exposes Meta/Get/Put, not item deletion;
// keeping a removed temporary tombstone is necessary for cross-device sync.
// The entire playback state is discarded, including unknown playback fields.
func ClearedHistoryItem(r HistoryRecord, now time.Time) (map[string]json.RawMessage, error) {
	encoded, err := json.Marshal(r.raw)
	if err != nil {
		return nil, err
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &item); err != nil {
		return nil, err
	}
	for name, value := range map[string]any{
		"removed": true, "temp": true, "state": map[string]any{},
		"_mtime": now.UTC().Format(time.RFC3339Nano),
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		item[name] = raw
	}
	return item, nil
}

// IsClearedHistoryItem distinguishes a complete playback-state reset from a
// temporary item that still has watched markers. It remains a datastore
// tombstone, not physical deletion of the record from Stremio's servers.
func IsClearedHistoryItem(r HistoryRecord) bool {
	if !r.Removed || !r.Temp {
		return false
	}
	var state map[string]json.RawMessage
	return json.Unmarshal(r.raw["state"], &state) == nil && len(state) == 0
}

func copyAndPatchHistoryRecord(r *HistoryRecord, meta MediaMetadata, now time.Time, updates map[string]any) (map[string]json.RawMessage, error) {
	var item map[string]json.RawMessage
	if r != nil {
		// Reparse so modifying the copy cannot mutate the source snapshot.
		encoded, err := json.Marshal(r.raw)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &item); err != nil {
			return nil, err
		}
	} else {
		item = make(map[string]json.RawMessage)
		// This matches Stremio Core's temporary/removed history-item model.
		// Marking a title does not implicitly add it to the Library tab.
		for k, v := range map[string]any{
			"_id": meta.ID, "name": meta.Name, "type": meta.Type, "poster": meta.Poster,
			"removed": true, "temp": true, "_ctime": now.UTC().Format(time.RFC3339Nano),
			"posterShape": "poster", "behaviorHints": map[string]any{},
		} {
			b, _ := json.Marshal(v)
			item[k] = b
		}
	}
	state := make(map[string]json.RawMessage)
	if len(item["state"]) != 0 && string(item["state"]) != "null" {
		if err := json.Unmarshal(item["state"], &state); err != nil {
			return nil, errors.New("cannot safely modify malformed Stremio playback state")
		}
	}
	if state == nil {
		state = make(map[string]json.RawMessage)
	}
	for k, v := range updates {
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		state[k] = data
	}
	encodedState, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	item["state"] = encodedState
	stamp, _ := json.Marshal(now.UTC().Format(time.RFC3339Nano))
	item["_mtime"] = stamp
	return item, nil
}

func hasWatchActivity(r HistoryRecord) bool {
	if r.Type == "series" && watchedBitCount(r.State.Watched) > 0 {
		return true
	}
	return r.State.TimesWatched > 0 || r.State.FlaggedWatched > 0 || r.State.TimeOffset > 0 || strings.TrimSpace(r.State.LastWatched) != ""
}
