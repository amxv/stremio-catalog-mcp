package server

import (
	"context"
	"errors"

	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
)

// A single tool handles natural history requests as well as deliberate watched
// updates. The default read needs no action or other parameters.
type WatchHistoryInput struct {
	Action    string   `json:"action,omitempty" jsonschema:"get (default), check (batch IMDb IDs), mark_watched, or mark_unwatched. All lookups and updates accept ONLY IMDb IDs, never title names"`
	IMDbID    string   `json:"imdb_id,omitempty" jsonschema:"Single IMDb tt... ID for get or mark_watched/mark_unwatched. Omit on check"`
	IMDbIDs   []string `json:"imdb_ids,omitempty" jsonschema:"For action=check only: array of 1-50 unique IMDb tt... IDs. Returns status and exact watched episodes for each ID in the same order, without scanning history"`
	MediaType string   `json:"media_type,omitempty" jsonschema:"movie or series; optional when the IMDb ID already has a record. For a new ID, the MCP can determine type from Cinemeta"`
	Season    *int     `json:"season,omitempty" jsonschema:"On get filter by season; on mark_watched/unwatched include with episodes for specific episode changes. Omit both for whole-title updates"`
	Episodes  []int    `json:"episodes,omitempty" jsonschema:"Explicit numbers e.g. [1,2] when updating specific series episodes; omit both episodes and season to mark all released episodes watched or to wipe entire title history"`
	Limit     int      `json:"limit,omitempty" jsonschema:"Maximum history results, 1-50 (default 20)"`
	Offset    int      `json:"offset,omitempty" jsonschema:"Skip this many matching history results for pagination"`
}

type WatchHistoryResult struct {
	Action  string                 `json:"action"`
	Items   []stremio.WatchSummary `json:"items,omitempty"`
	Detail  *stremio.WatchDetail   `json:"detail,omitempty"`
	Checks  []stremio.WatchCheck   `json:"checks,omitempty"`
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
		if len(input.Episodes) != 0 || len(input.IMDbIDs) != 0 || input.MediaType != "" {
			return WatchHistoryResult{}, errors.New("get accepts only imdb_id, optional season, or limit and offset for history listing")
		}
		if input.IMDbID != "" {
			if input.Limit != 0 || input.Offset != 0 {
				return WatchHistoryResult{}, errors.New("for an exact imdb_id lookup, omit limit and offset")
			}
			detail, err := svc.Inspect(ctx, input.IMDbID, input.Season)
			return WatchHistoryResult{Action: "get", Detail: &detail}, err
		}
		if input.Season != nil {
			return WatchHistoryResult{}, errors.New("season requires an exact imdb_id")
		}
		items, count, err := svc.Search(ctx, stremio.WatchSearch{Limit: input.Limit, Offset: input.Offset})
		if err != nil {
			return WatchHistoryResult{}, err
		}
		return WatchHistoryResult{Action: "get", Items: items, Count: len(items), HasMore: count > input.Offset+len(items)}, nil
	case "check":
		if input.IMDbID != "" || input.MediaType != "" || input.Season != nil || len(input.Episodes) != 0 || input.Limit != 0 || input.Offset != 0 {
			return WatchHistoryResult{}, errors.New("check accepts only imdb_ids; omit imdb_id, media_type, season, episodes, limit and offset")
		}
		checks, err := svc.Check(ctx, input.IMDbIDs)
		if err != nil {
			return WatchHistoryResult{}, err
		}
		return WatchHistoryResult{Action: "check", Checks: checks, Count: len(checks)}, nil
	case "mark_watched", "mark_unwatched":
		if input.Limit != 0 || input.Offset != 0 || len(input.IMDbIDs) != 0 {
			return WatchHistoryResult{}, errors.New("mark_watched and mark_unwatched accept one imdb_id; imdb_ids is only for check")
		}
		if input.Season == nil && len(input.Episodes) > 0 {
			return WatchHistoryResult{}, errors.New("episode updates require an explicit season")
		}
		if input.Season != nil && len(input.Episodes) == 0 {
			return WatchHistoryResult{}, errors.New("season requires explicit episodes; omit both for a whole-title update")
		}
		id, resolvedType, err := resolveWatchID(ctx, svc, input)
		if err != nil {
			return WatchHistoryResult{}, err
		}
		season := 0
		if input.Season != nil {
			season = *input.Season
		}
		if resolvedType == "movie" && input.Season != nil {
			return WatchHistoryResult{}, errors.New("movies cannot have season or episodes")
		}
		var detail stremio.WatchDetail
		var changed bool
		clearEntireTitle := input.Action == "mark_unwatched" && input.Season == nil && len(input.Episodes) == 0
		if clearEntireTitle {
			detail, changed, err = svc.ForgetTitle(ctx, id, resolvedType)
		} else if input.Action == "mark_unwatched" {
			detail, changed, err = svc.MarkUnwatched(ctx, id, resolvedType, season, input.Episodes)
		} else {
			detail, changed, err = svc.MarkWatched(ctx, id, resolvedType, season, input.Episodes)
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
		if input.Action == "mark_watched" && input.Season == nil && resolvedType == "series" {
			message = "Already marked all released regular episodes watched; no write needed. " + detail.Note
			if changed {
				message = "All released regular episodes marked watched across every season in one Stremio write; update verified. " + detail.Note
			}
		}
		return WatchHistoryResult{Action: input.Action, Changed: changed, Detail: &detail, Message: message}, nil
	default:
		return WatchHistoryResult{}, errors.New("action must be get, check, mark_watched, or mark_unwatched")
	}
}

// resolveWatchID accepts only IMDb IDs. Existing library records establish
// the media type; unknown IDs can be checked against Cinemeta without guessing
// a title. This does not scan history or perform title searches.
func resolveWatchID(ctx context.Context, svc stremio.WatchHistoryService, input WatchHistoryInput) (string, string, error) {
	if input.MediaType != "" && input.MediaType != "movie" && input.MediaType != "series" {
		return "", "", errors.New("media_type must be movie or series")
	}
	if input.IMDbID == "" {
		return "", "", errors.New("imdb_id (tt...) is required; titles are not accepted")
	}
	if input.MediaType == "" && input.Action == "mark_watched" {
		existing, err := svc.Account.GetOne(ctx, input.IMDbID)
		if err != nil {
			return "", "", err
		}
		if existing != nil {
			return input.IMDbID, existing.Type, nil
		}
		movie, movieErr := svc.Metadata.Fetch(ctx, "movie", input.IMDbID)
		series, seriesErr := svc.Metadata.Fetch(ctx, "series", input.IMDbID)
		switch {
		case movieErr == nil && seriesErr != nil:
			return movie.ID, movie.Type, nil
		case seriesErr == nil && movieErr != nil:
			return series.ID, series.Type, nil
		case movieErr == nil && seriesErr == nil:
			return "", "", errors.New("IMDb ID resolves as both a movie and a series; supply media_type")
		default:
			return "", "", errors.New("IMDb ID is not recorded and its media_type could not be verified; provide media_type")
		}
	}
	return input.IMDbID, input.MediaType, nil
}
