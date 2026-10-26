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
	Action    string `json:"action,omitempty" jsonschema:"get (default), mark_watched, mark_unwatched. With just a title/ID, mark_watched completes every released regular episode or one movie, and mark_unwatched clears all history for that title; specify season and episodes only for selective episode changes"`
	Query     string `json:"query,omitempty" jsonschema:"Search history or identify a movie/series to mark watched or unwatched. Exact existing history titles work without media_type; use media_type or year to disambiguate"`
	IMDbID    string `json:"imdb_id,omitempty" jsonschema:"Exact IMDb tt... ID; for get, returns episode-level progress of a series"`
	MediaType string `json:"media_type,omitempty" jsonschema:"movie or series, optional for exact existing titles and IMDb IDs; use for ambiguous or unrecorded titles"`
	Year      int    `json:"year,omitempty" jsonschema:"Optional release year when resolving a title to mark watched"`
	Season    *int   `json:"season,omitempty" jsonschema:"On get filter by season; on mark_watched/unwatched include with episodes for specific episode changes. Omit both for whole-title updates"`
	Episodes  []int  `json:"episodes,omitempty" jsonschema:"Explicit numbers e.g. [1,2] when updating specific series episodes; omit both episodes and season to mark all released episodes watched or to wipe entire title history"`
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
		if input.Season == nil && len(input.Episodes) > 0 {
			return WatchHistoryResult{}, errors.New("episode updates require an explicit season")
		}
		if input.Season != nil && len(input.Episodes) == 0 {
			return WatchHistoryResult{}, errors.New("season requires explicit episodes; omit both for a whole-title update")
		}
		id, resolvedType, err := resolveWatchSelection(ctx, svc, input)
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
		return WatchHistoryResult{}, errors.New("action must be get, mark_watched, or mark_unwatched")
	}
}

// resolveWatchSelection prefers an exact Stremio history title to avoid
// forcing agents to first look up an IMDb ID or guess its media type.
// Otherwise, Cinemeta must find exactly one canonical title to write.
func resolveWatchSelection(ctx context.Context, svc stremio.WatchHistoryService, input WatchHistoryInput) (string, string, error) {
	if input.MediaType != "" && input.MediaType != "movie" && input.MediaType != "series" {
		return "", "", errors.New("media_type must be movie or series")
	}
	if input.IMDbID != "" {
		if strings.TrimSpace(input.Query) != "" || input.Year != 0 {
			return "", "", errors.New("supply either imdb_id or query/year, not both")
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
	title := strings.TrimSpace(input.Query)
	if title == "" {
		return "", "", errors.New("provide an IMDb ID or title to update watch status")
	}
	if input.Year == 0 {
		matches, _, err := svc.Search(ctx, stremio.WatchSearch{Query: title, Limit: 50})
		if err != nil {
			return "", "", fmt.Errorf("cannot search saved Stremio history for title: %w", err)
		}
		var exact []stremio.WatchSummary
		for _, item := range matches {
			if strings.EqualFold(strings.TrimSpace(item.Title), title) && (input.MediaType == "" || item.MediaType == input.MediaType) {
				exact = append(exact, item)
			}
		}
		if len(exact) == 1 {
			return exact[0].IMDbID, exact[0].MediaType, nil
		}
		if len(exact) > 1 {
			return "", "", errors.New("multiple exact Stremio history titles matched; supply media_type, year, or IMDb ID")
		}
	}
	resolver := catalog.Cinemeta{BaseURL: svc.Metadata.BaseURL, Client: svc.Metadata.Client}
	if input.MediaType != "" {
		item, err := resolver.Resolve(ctx, input.MediaType, catalog.ItemInput{Title: title, Year: input.Year})
		if err != nil {
			return "", "", fmt.Errorf("cannot uniquely resolve title for watch-status update: %w", err)
		}
		return item.ID, item.Type, nil
	}
	movie, movieErr := resolver.Resolve(ctx, "movie", catalog.ItemInput{Title: title, Year: input.Year})
	series, seriesErr := resolver.Resolve(ctx, "series", catalog.ItemInput{Title: title, Year: input.Year})
	switch {
	case movieErr == nil && seriesErr == nil:
		return "", "", errors.New("title matches both a movie and a series; provide media_type or IMDb ID")
	case movieErr == nil:
		return movie.ID, movie.Type, nil
	case seriesErr == nil:
		return series.ID, series.Type, nil
	default:
		return "", "", fmt.Errorf("cannot resolve an exact movie or series title %q; provide IMDb ID or media_type/year to disambiguate", title)
	}
}
