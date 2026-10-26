package stremio

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
)

// PreviewCache is an optional, separate Redis cache: storing metadata does not
// mutate a user's curated rows or require new arguments in the MCP tool API.
type PreviewCache interface {
	LoadPreviewCache(context.Context, string) (string, error)
	SavePreviewCache(context.Context, string, string, int) error
}

// PreviewEnricher supplies the fields Android TV renders on focus. Cinemeta is
// the only metadata source; playback continues to use the user's other addons.
type PreviewEnricher struct {
	Resolver catalog.PreviewResolver
	Cache    PreviewCache
}

const previewTTL = 24 * time.Hour
const maxPreviewWorkers = 12

// RowMetadata tolerates failures: if Cinemeta or Redis is unavailable, Stremio
// still receives the stored titles and posters instead of a blank catalog.
func (e PreviewEnricher) RowMetadata(ctx context.Context, row catalog.Row) map[string]catalog.Preview {
	result := make(map[string]catalog.Preview, len(row.Items))
	if e.Resolver == nil || len(row.Items) == 0 {
		return result
	}
	if e.Cache != nil {
		if raw, err := e.Cache.LoadPreviewCache(ctx, row.ID); err == nil && raw != "" {
			var cached map[string]catalog.Preview
			if json.Unmarshal([]byte(raw), &cached) == nil {
				for _, item := range row.Items {
					if value, exists := cached[item.ID]; exists && value.ID == item.ID && (value.Type == "" || value.Type == row.Type) {
						result[item.ID] = value
					}
				}
			}
		}
	}

	var missing []catalog.Item
	for _, item := range row.Items {
		if _, ok := result[item.ID]; !ok {
			missing = append(missing, item)
		}
	}
	if len(missing) == 0 {
		return result
	}

	// Bound total enrichment latency during upstream outages. Missing entries
	// are retried at the next catalog fetch; successfully fetched ones are cached.
	fetchCtx, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	jobs := make(chan catalog.Item)
	var wg sync.WaitGroup
	var mu sync.Mutex
	workers := min(maxPreviewWorkers, len(missing))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				preview, err := e.Resolver.FetchPreview(fetchCtx, row.Type, item.ID)
				if err != nil || preview.ID != item.ID {
					continue
				}
				mu.Lock()
				result[item.ID] = preview
				mu.Unlock()
			}
		}()
	}
	for _, item := range missing {
		if fetchCtx.Err() != nil {
			break
		}
		jobs <- item
	}
	close(jobs)
	wg.Wait()

	if e.Cache != nil && len(result) != 0 {
		// Cache only the IDs still present in this row. New/reordered titles keep
		// stable row identity, and ratings refresh when the TTL expires.
		if encoded, err := json.Marshal(result); err == nil {
			_ = e.Cache.SavePreviewCache(ctx, row.ID, string(encoded), int(previewTTL.Seconds()))
		}
	}
	return result
}

// buildPreview prefers saved catalog identity, title and poster, preserving
// the original ordering while augmenting the fields the TV header displays.
func buildPreview(item catalog.Item, enriched catalog.Preview) map[string]any {
	out := map[string]any{"id": item.ID, "type": item.Type, "name": item.Title}
	poster := item.Poster
	if poster == "" {
		poster = enriched.Poster
	}
	if poster != "" {
		out["poster"] = poster
	}
	releaseInfo := enriched.ReleaseInfo
	if releaseInfo == "" && item.Year > 0 {
		releaseInfo = strconv.Itoa(item.Year)
	}
	if releaseInfo != "" {
		out["releaseInfo"] = releaseInfo
	}
	if enriched.Runtime != "" {
		out["runtime"] = enriched.Runtime
	}
	if enriched.Description != "" {
		out["description"] = enriched.Description
	}
	if enriched.Background != "" {
		out["background"] = enriched.Background
	}
	if enriched.IMDBRating != "" {
		out["imdbRating"] = enriched.IMDBRating
	}
	if len(enriched.Links) > 0 {
		out["links"] = enriched.Links
	}
	return out
}
