package tmdb

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"unicode"
)

type Suggestion struct {
	Title     string `json:"title"`
	MediaType string `json:"media_type"`
	Year      int    `json:"year,omitempty"`
}

type searchPage struct {
	Results    []searchHit `json:"results"`
	TotalPages int         `json:"total_pages"`
}

// An extra page is worthwhile only for crowded, short-title searches.
// Most lookups stay at one page per media type.
func shouldSearchSecondPage(page searchPage, spec titleQuery) bool {
	if page.TotalPages < 2 || len(page.Results) < 20 || len(titleWords(spec.Name)) > 2 {
		return false
	}
	for _, hit := range page.Results {
		if rankTitle(hit, spec) <= 2 {
			return true
		}
	}
	return false
}

// TMDB's multi search can bury movies below TV shows (and vice versa).
// Searching both indexes concurrently considers the first page of each,
// rather than just the combined ranking's first 20 hits.
func (c *Client) searchBoth(ctx context.Context, spec titleQuery) ([]searchHit, error) {
	kinds := []string{"movie", "tv"}
	if spec.Kind != "" {
		kinds = []string{spec.Kind}
	}
	all := make([][]searchHit, len(kinds))
	errs := make([]error, len(kinds))
	var wg sync.WaitGroup
	for i, kind := range kinds {
		wg.Add(1)
		go func(i int, kind string) {
			defer wg.Done()
			var page searchPage
			params := url.Values{
				"query": {spec.Name}, "include_adult": {"false"}, "language": {"en-US"},
			}
			errs[i] = c.get(ctx, "/3/search/"+kind, params, &page)
			if errs[i] != nil {
				return
			}
			for j := range page.Results {
				page.Results[j].MediaType = kind
			}
			if shouldSearchSecondPage(page, spec) {
				params.Set("page", "2")
				var second searchPage
				errs[i] = c.get(ctx, "/3/search/"+kind, params, &second)
				if errs[i] != nil {
					return
				}
				for j := range second.Results {
					second.Results[j].MediaType = kind
				}
				page.Results = append(page.Results, second.Results...)
			}
			all[i] = page.Results
		}(i, kind)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			// Missing one media index is not proof of a unique title.
			return nil, err
		}
	}
	var hits []searchHit
	seen := make(map[string]bool)
	for _, part := range all {
		for _, h := range part {
			if h.ID <= 0 {
				continue
			}
			key := fmt.Sprintf("%s:%d", h.MediaType, h.ID)
			if !seen[key] {
				seen[key] = true
				hits = append(hits, h)
			}
		}
	}
	return hits, nil
}

func eligible(h searchHit, spec titleQuery) bool {
	if spec.Year != 0 {
		year := releaseYear(h.date())
		if year != spec.Year {
			// Premiere years and wide theatrical-release years can differ.
			// Keep prominent adjacent-year works visible rather than silently
			// preferring an obscure same-year adaptation (e.g. Crash).
			if (year != spec.Year-1 && year != spec.Year+1) || h.VoteCount < 1000 {
				return false
			}
		}
	}
	if spec.Country != "" {
		found := false
		for _, country := range h.OriginCountry {
			if strings.EqualFold(country, spec.Country) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if spec.Anime && !(h.OriginalLanguage == "ja" && containsInt(h.GenreIDs, 16)) {
		return false
	}
	return true
}

func containsInt(values []int, needle int) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// A title's lifetime vote count is a less volatile signal of prominence than
// popularity alone. Neither score is presented as an IMDb rating.
func prominence(h searchHit) float64 {
	return 2.5*math.Log1p(float64(h.VoteCount)) + 0.7*math.Log1p(math.Max(0, h.Popularity))
}

func sortedByProminence(hits []searchHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := prominence(hits[i]), prominence(hits[j])
		if a == b {
			return hits[i].ID < hits[j].ID
		}
		return a > b
	})
}

func sortedByRelevance(hits []searchHit, spec titleQuery) {
	if spec.Year == 0 {
		sortedByProminence(hits)
		return
	}
	sort.SliceStable(hits, func(i, j int) bool {
		score := func(hit searchHit) float64 {
			value := prominence(hit)
			if releaseYear(hit.date()) == spec.Year {
				value += 4
			}
			return value
		}
		a, b := score(hits[i]), score(hits[j])
		if a == b {
			return hits[i].ID < hits[j].ID
		}
		return a > b
	})
}

func hasNonLatinScript(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r)
	})
}

// These are match tiers, not fuzzy string similarity. A query never resolves
// merely because it has a small edit distance from another title.
func rankTitle(h searchHit, spec titleQuery) int {
	if sameTitle(spec.Name, h.displayTitle()) {
		return 0
	}
	if sameTitle(spec.Name, h.originalTitle()) {
		return 1
	}
	if relaxedKey(spec.Name) == relaxedKey(h.displayTitle()) ||
		relaxedKey(spec.Name) == relaxedKey(h.originalTitle()) {
		return 2
	}
	if shortTitle(spec.Name, h.displayTitle()) || shortTitle(spec.Name, h.originalTitle()) {
		return 3
	}
	return 100
}

func selectCandidates(hits []searchHit, spec titleQuery) []searchHit {
	tiers := make([][]searchHit, 4)
	for _, h := range hits {
		if !eligible(h, spec) {
			continue
		}
		rank := rankTitle(h, spec)
		if rank < len(tiers) {
			tiers[rank] = append(tiers[rank], h)
		}
	}
	var candidates []searchHit
	if len(tiers[0]) != 0 {
		candidates = tiers[0]
		// An original-language title can match a non-Latin query even
		// when the translated display title differs.
		// For Latin queries, a stray original_title on an unrelated item
		// should not reintroduce the Dark Nemesis false-positive.
		if hasNonLatinScript(spec.Name) {
			candidates = append(candidates, tiers[1]...)
		}
	} else if len(tiers[1]) != 0 {
		candidates = tiers[1]
	} else if len(tiers[2]) != 0 {
		candidates = tiers[2]
	} else {
		candidates = tiers[3]
	}
	sortedByRelevance(candidates, spec)
	return candidates
}

func selectFallbackCandidates(hits []searchHit, spec titleQuery, previous []searchHit) []searchHit {
	maxTriedRank := -1
	for _, hit := range previous {
		if rank := rankTitle(hit, spec); rank > maxTriedRank {
			maxTriedRank = rank
		}
	}
	var next []searchHit
	minRank := 100
	for _, hit := range hits {
		if !eligible(hit, spec) {
			continue
		}
		rank := rankTitle(hit, spec)
		if rank <= maxTriedRank || rank >= 4 {
			continue
		}
		if rank < minRank {
			minRank = rank
			next = nil
		}
		if rank == minRank {
			next = append(next, hit)
		}
	}
	sortedByRelevance(next, spec)
	return next
}

// Alternative titles are retrieved only for unresolved titles. Search already
// indexes localized and alternate names, but it doesn't include those names
// in the search result, so verify the aliases before choosing an IMDb ID.
func (c *Client) resolveAlternativeTitles(ctx context.Context, hits []searchHit, spec titleQuery) []searchHit {
	sorted := append([]searchHit(nil), hits...)
	sortedByProminence(sorted)
	// Keep this a small, bounded fallback, not a per-result N+1.
	if len(sorted) > 6 {
		sorted = sorted[:6]
	}
	matched := make([]bool, len(sorted))
	var wg sync.WaitGroup
	for i, hit := range sorted {
		if !eligible(hit, spec) {
			continue
		}
		wg.Add(1)
		go func(i int, hit searchHit) {
			defer wg.Done()
			var aliases struct {
				Titles []struct {
					Title string `json:"title"`
				} `json:"titles"`
				Results []struct {
					Title string `json:"title"`
				} `json:"results"`
			}
			if c.get(ctx, fmt.Sprintf("/3/%s/%d/alternative_titles", hit.MediaType, hit.ID), nil, &aliases) != nil {
				return
			}
			for _, alt := range aliases.Titles {
				if sameTitle(spec.Name, alt.Title) {
					matched[i] = true
					return
				}
			}
			for _, alt := range aliases.Results {
				if sameTitle(spec.Name, alt.Title) {
					matched[i] = true
					return
				}
			}
		}(i, hit)
	}
	wg.Wait()
	var results []searchHit
	for i, hit := range sorted {
		if matched[i] {
			results = append(results, hit)
		}
	}
	return results
}

// Suggestions are sourced from actual TMDB results; they are not guesses or
// spelling corrections and are never assigned an IMDb ID automatically.
func titleSuggestions(hits []searchHit, spec titleQuery) []Suggestion {
	words := titleWords(spec.Name)
	candidates := append([]searchHit(nil), hits...)
	sortedByProminence(candidates)
	var out []Suggestion
	for _, hit := range candidates {
		if len(out) >= 3 {
			break
		}
		if hit.displayTitle() == "" {
			continue
		}
		overlaps := false
		for _, w := range words {
			if len([]rune(w)) < 3 {
				continue
			}
			for _, candidateWord := range titleWords(hit.displayTitle()) {
				if w == candidateWord {
					overlaps = true
					break
				}
			}
		}
		if !overlaps {
			continue
		}
		media := hit.MediaType
		if media == "tv" {
			media = "series"
		}
		out = append(out, Suggestion{Title: hit.displayTitle(), MediaType: media, Year: releaseYear(hit.date())})
	}
	return out
}
