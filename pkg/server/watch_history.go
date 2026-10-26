package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
)

// A single tool handles natural history requests as well as deliberate watched
// updates. The default read needs no action or other parameters.
type WatchHistoryInput struct {
	Action    string `json:"action,omitempty" jsonschema:"get (default); mark_watched requires exact episodes for series; mark_unwatched with only a title clears ALL playback history, while season plus episodes selectively unwatches those episodes"`
	Query     string `json:"query,omitempty" jsonschema:"Search history by movie/series title. For marking, accepts a title with media_type to resolve a unique IMDb ID"`
	IMDbID    string `json:"imdb_id,omitempty" jsonschema:"Exact IMDb tt... ID; for get, returns episode-level progress of a series"`
	MediaType string `json:"media_type,omitempty" jsonschema:"movie or series; required for marking a title not already in Stremio"`
	Year      int    `json:"year,omitempty" jsonschema:"Optional release year when resolving a title to mark watched"`
	Season    *int   `json:"season,omitempty" jsonschema:"Filter get by season, or select exact episodes for a series update; omit for full-title mark_unwatched history reset"`
	Episodes  []int  `json:"episodes,omitempty" jsonschema:"Explicit episode numbers for scoped updates, e.g. [1,2]; omit with season to reset ALL playback history for a movie or entire series with mark_unwatched"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum history results, 1-50 (default 20)"`
	Offset    int    `json:"offset,omitempty" jsonschema:"Skip this many matching history results for pagination"`
}

type WatchHistoryResult struct {
	Action  string                 `json:"action"`
	Items   []stremio.WatchSummary `json:"items,omitempty"`
	Detail  *stremio.WatchDetail   `json:"detail,omitempty"`
	Count   int                    `json:"count,omitempty"`
	HasMore bool                   `json:"has_more,omitempty"`
	Changed bool                   `json:"changed,omitempty"`
	Message string                 `json:"message,omitempty"`
}

func (rt Runtime) watchHistory(ctx context.Context, input WatchHistoryInput) (WatchHistoryResult, error) {
	if rt.Account.AuthKey == "" {
		return WatchHistoryResult{}, errors.New("Stremio account not linked; configure STREMIO_AUTH_KEY to use watch history")
	}
	svc := stremio.WatchHistoryService{Account: stremio.HistoryAccount{API: rt.Account}, Metadata: rt.HistoryMetadata}
	if input.Action == "" {
		input.Action = "get"
	}
	switch input.Action {
	case "get":
		if len(input.Episodes) != 0 || input.Year != 0 {
			return WatchHistoryResult{}, errors.New("episodes and year are only valid for mark_watched")
		}
		if input.IMDbID != "" {
			if input.Query != "" || input.Limit != 0 || input.Offset != 0 {
				return WatchHistoryResult{}, errors.New("for an exact imdb_id lookup, omit query, limit and offset")
			}
			detail, err := svc.Inspect(ctx, input.IMDbID, input.Season)
			return WatchHistoryResult{Action: "get", Detail: &detail}, err
		}
		if input.Season != nil {
			return WatchHistoryResult{}, errors.New("season requires an exact imdb_id")
		}
		items, count, err := svc.Search(ctx, stremio.WatchSearch{Query: input.Query, Limit: input.Limit, Offset: input.Offset})
		if err != nil {
			return WatchHistoryResult{}, err
		}
		if len(items) == 1 && count == 1 && input.Offset == 0 && strings.EqualFold(strings.TrimSpace(items[0].Title), strings.TrimSpace(input.Query)) {
			detail, err := svc.Inspect(ctx, items[0].IMDbID, nil)
			return WatchHistoryResult{Action: "get", Detail: &detail}, err
		}
		return WatchHistoryResult{Action: "get", Items: items, Count: len(items), HasMore: count > input.Offset+len(items)}, nil
	case "mark_watched", "mark_unwatched":
		if input.Limit != 0 || input.Offset != 0 {
			return WatchHistoryResult{}, errors.New("limit and offset are only valid for history searches")
		}
		id := input.IMDbID
		if id == "" && input.Query != "" {
			if input.MediaType != "movie" && input.MediaType != "series" {
				return WatchHistoryResult{}, errors.New("media_type is required to resolve a title for a watch-status update")
			}
			resolved, err := catalog.Cinemeta{}.Resolve(ctx, input.MediaType, catalog.ItemInput{Title: input.Query, Year: input.Year})
			if err != nil {
				return WatchHistoryResult{}, fmt.Errorf("cannot uniquely resolve title for watch-status update: %w", err)
			}
			id = resolved.ID
		}
		if id == "" {
			return WatchHistoryResult{}, errors.New("provide imdb_id or query and media_type to update watch status")
		}
		if input.Season == nil && len(input.Episodes) > 0 {
			return WatchHistoryResult{}, errors.New("episode updates require an explicit season")
		}
		if input.Season != nil && len(input.Episodes) == 0 {
			return WatchHistoryResult{}, errors.New("season requires explicitly selected episodes; omit both to clear an entire title with mark_unwatched")
		}
		if input.Action == "mark_watched" && input.MediaType == "series" && (input.Season == nil || len(input.Episodes) == 0) {
			return WatchHistoryResult{}, errors.New("mark_watched on a series requires an explicit season and episodes list")
		}
		season := 0
		if input.Season != nil {
			season = *input.Season
		}
		if input.MediaType == "movie" && input.Season != nil {
			return WatchHistoryResult{}, errors.New("movies cannot have season or episodes")
		}
		var detail stremio.WatchDetail
		var changed bool
		var err error
		clearEntireTitle := input.Action == "mark_unwatched" && input.Season == nil && len(input.Episodes) == 0
		if clearEntireTitle {
			detail, changed, err = svc.ForgetTitle(ctx, id, input.MediaType)
		} else if input.Action == "mark_unwatched" {
			detail, changed, err = svc.MarkUnwatched(ctx, id, input.MediaType, season, input.Episodes)
		} else {
			detail, changed, err = svc.MarkWatched(ctx, id, input.MediaType, season, input.Episodes)
		}
		if err != nil {
			return WatchHistoryResult{}, err
		}
		if clearEntireTitle {
			message := "No new playback data was cleared; the title was already reset or has no Stremio record."
			if changed {
				message = "Cleared all playback history and removed the title from the Stremio library; Stremio retains a removed synchronization tombstone (per-title physical deletion is unsupported). Verified by reading back."
			}
			return WatchHistoryResult{Action: input.Action, Changed: changed, Detail: &detail, Message: message}, nil
		}
		status := "watched"
		if input.Action == "mark_unwatched" {
			status = "unwatched"
		}
		message := "No Stremio watched markers changed; the requested title/episodes are already " + status + " or not recorded."
		if changed {
			message = "Marked " + status + " in Stremio; update verified."
		}
		return WatchHistoryResult{Action: input.Action, Changed: changed, Detail: &detail, Message: message}, nil
	default:
		return WatchHistoryResult{}, errors.New("action must be get, mark_watched, or mark_unwatched")
	}
}
