// Package tmdb provides a deliberately small, read-only title-to-IMDb lookup.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"golang.org/x/time/rate"
)

const (
	maxTitles      = 50
	maxMatches     = 6
	maxCandidates  = 12
	maxDescription = 180
)

// Shared across warm function invocations to keep requests below TMDB's
// approximate 40 requests/second ceiling. A separate deployment instance can
// have its own limiter, so 429 responses must still be respected.
var (
	sharedLimiter      = rate.NewLimiter(27, 10)
	sharedSlots        = make(chan struct{}, 12)
	sharedClient       = &http.Client{Timeout: 5 * time.Second}
	sharedRatingSlots  = make(chan struct{}, 12)
	sharedRatingClient = &http.Client{Timeout: 1500 * time.Millisecond}
)

type Client struct {
	readToken string
	apiKey    string
	// BaseURL and HTTPClient can be replaced by tests. Never accept either
	// value from MCP tool arguments.
	BaseURL        string
	HTTPClient     *http.Client
	limiter        *rate.Limiter
	slots          chan struct{}
	ratingResolver catalog.PreviewResolver
	ratingSlots    chan struct{}
	cache          *lookupCache
}

func New(readToken, apiKey string) *Client {
	readToken, apiKey = strings.TrimSpace(readToken), strings.TrimSpace(apiKey)
	if readToken == "" && apiKey == "" {
		return nil
	}
	return &Client{
		readToken: readToken, apiKey: apiKey,
		BaseURL: "https://api.themoviedb.org", HTTPClient: sharedClient,
		limiter: sharedLimiter, slots: sharedSlots,
		ratingResolver: catalog.Cinemeta{Client: sharedRatingClient},
		ratingSlots:    sharedRatingSlots,
		cache:          sharedLookupCache,
	}
}

func FromEnv() *Client {
	return New(os.Getenv("TMDB_READ_ACCESS_TOKEN"), os.Getenv("TMDB_API_KEY"))
}

type Input struct {
	Titles []string `json:"titles" jsonschema:"1-50 movie or TV series titles to resolve to IMDb IDs"`
}

type Match struct {
	IMDbID      string   `json:"imdb_id,omitempty"`
	Title       string   `json:"title,omitempty"`
	MediaType   string   `json:"media_type,omitempty"`
	Year        int      `json:"year,omitempty"`
	ReleaseDate string   `json:"release_date,omitempty"`
	Language    string   `json:"language,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	IMDbRating  float64  `json:"imdb_rating,omitempty"`
	Description string   `json:"description,omitempty"`
}

type Item struct {
	Query string `json:"query"`
	Match
	Matches     []Match      `json:"matches,omitempty"`
	Suggestions []Suggestion `json:"suggestions,omitempty"`
	MoreMatches bool         `json:"more_matches,omitempty"`
	Status      string       `json:"status,omitempty"`
	Error       string       `json:"error,omitempty"`
}

type Result struct {
	Results []Item `json:"results"`
}

type searchHit struct {
	ID               int64    `json:"id"`
	MediaType        string   `json:"media_type"`
	Title            string   `json:"title"`
	OriginalTitle    string   `json:"original_title"`
	Name             string   `json:"name"`
	OriginalName     string   `json:"original_name"`
	ReleaseDate      string   `json:"release_date"`
	FirstAirDate     string   `json:"first_air_date"`
	Overview         string   `json:"overview"`
	Popularity       float64  `json:"popularity"`
	VoteCount        int      `json:"vote_count"`
	OriginCountry    []string `json:"origin_country"`
	OriginalLanguage string   `json:"original_language"`
	GenreIDs         []int    `json:"genre_ids"`
}

func (h searchHit) displayTitle() string {
	if h.MediaType == "tv" {
		return h.Name
	}
	return h.Title
}

func (h searchHit) date() string {
	if h.MediaType == "tv" {
		return h.FirstAirDate
	}
	return h.ReleaseDate
}

func (h searchHit) originalTitle() string {
	if h.MediaType == "tv" {
		return h.OriginalName
	}
	return h.OriginalTitle
}

// Lookup performs independent queries concurrently, then resolves the IMDb
// IDs of candidate titles without a batch-wide search/details barrier.
func (c *Client) Lookup(ctx context.Context, input Input) (Result, error) {
	if c == nil {
		return Result{}, errors.New("TMDB lookup is not configured")
	}
	if len(input.Titles) == 0 || len(input.Titles) > maxTitles {
		return Result{}, fmt.Errorf("titles must contain 1-%d entries", maxTitles)
	}
	result := Result{Results: make([]Item, len(input.Titles))}
	first := make(map[string]int, len(input.Titles))
	duplicates := make(map[int]int)
	var work []int
	for i, raw := range input.Titles {
		name := strings.TrimSpace(raw)
		if name == "" || utf8.RuneCountInString(name) > 150 || normalize(name) == "" {
			return Result{}, fmt.Errorf("titles[%d] must be a nonempty title of at most 150 characters", i)
		}
		result.Results[i].Query = name
		key := normalize(name)
		if j, found := first[key]; found {
			duplicates[i] = j
			continue
		}
		first[key] = i
		work = append(work, i)
	}

	var wg sync.WaitGroup
	for _, i := range work {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result.Results[i] = c.lookupOne(ctx, result.Results[i].Query)
		}(i)
	}
	wg.Wait()
	for i, from := range duplicates {
		result.Results[i] = result.Results[from]
		result.Results[i].Query = strings.TrimSpace(input.Titles[i])
	}
	return result, nil
}

func (c *Client) lookupOne(ctx context.Context, query string) Item {
	spec := parseTitleQuery(query)
	if c.cache != nil {
		return c.cache.lookup(ctx, spec, query, func() Item {
			return c.resolveOne(ctx, query, spec)
		})
	}
	return c.resolveOne(ctx, query, spec)
}

func (c *Client) resolveOne(ctx context.Context, query string, spec titleQuery) Item {
	item := Item{Query: query}
	hits, err := c.searchBoth(ctx, spec)
	if err != nil {
		item.Status, item.Error = "error", safeError(err)
		return item
	}
	candidates := selectCandidates(hits, spec)
	if len(candidates) == 0 {
		candidates = c.resolveAlternativeTitles(ctx, hits, spec)
	}
	if len(candidates) == 0 {
		item.Status = "not_found"
		item.Suggestions = titleSuggestions(hits, spec)
		return item
	}
	// Rank before limiting, and inspect more candidates than the six
	// results displayed. Missing IMDb IDs must not consume a result slot.
	item.MoreMatches = len(candidates) > maxCandidates
	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}

	matches := make([]Match, len(candidates))
	failed := make([]bool, len(candidates))
	var wg sync.WaitGroup
	for i, candidate := range candidates {
		wg.Add(1)
		go func(i int, candidate searchHit) {
			defer wg.Done()
			var ids struct {
				IMDbID string `json:"imdb_id"`
			}
			endpoint := fmt.Sprintf("/3/%s/%d/external_ids", candidate.MediaType, candidate.ID)
			if err := c.get(ctx, endpoint, nil, &ids); err != nil {
				failed[i] = true
				return
			}
			if !validIMDbID(ids.IMDbID) {
				return
			}
			mediaType := candidate.MediaType
			if mediaType == "tv" {
				mediaType = "series"
			}
			matches[i] = Match{
				IMDbID: ids.IMDbID, Title: candidate.displayTitle(),
				MediaType: mediaType, Year: releaseYear(candidate.date()),
				ReleaseDate: candidate.date(), Language: candidate.OriginalLanguage,
				Genres:      genres(candidate.MediaType, candidate.GenreIDs),
				Description: shorten(candidate.Overview),
			}
		}(i, candidate)
	}
	wg.Wait()
	seenIDs := make(map[string]bool)
	byIMDbID := make(map[string]searchHit, len(candidates))
	for i, match := range matches {
		if failed[i] {
			item.Status, item.Error = "error", "could not verify all TMDB matches; retry this title"
			return item
		}
		if match.IMDbID != "" && !seenIDs[match.IMDbID] {
			seenIDs[match.IMDbID] = true
			byIMDbID[match.IMDbID] = candidates[i]
			item.Matches = append(item.Matches, match)
		}
	}
	if len(item.Matches) > maxMatches {
		item.MoreMatches = true
		item.Matches = item.Matches[:maxMatches]
	}
	// Ratings and metadata are best-effort. Do these requests only for the
	// final results; discarded candidates should not cost Cinemeta calls.
	var enrich sync.WaitGroup
	for i := range item.Matches {
		enrich.Add(1)
		go func(i int) {
			defer enrich.Done()
			c.enrichFromCinemeta(ctx, &item.Matches[i])
			if item.Matches[i].Year == 0 && item.Matches[i].MediaType == "series" {
				c.enrichMissingTVYear(ctx, byIMDbID[item.Matches[i].IMDbID], &item.Matches[i])
			}
		}(i)
	}
	enrich.Wait()
	if len(item.Matches) == 0 {
		item.Status = "not_found" // TMDB titles may lack IMDb IDs.
		item.Suggestions = titleSuggestions(hits, spec)
	} else if len(item.Matches) == 1 && !item.MoreMatches {
		// Some TMDB entries lack IMDb IDs. They are not competing IMDb matches.
		item.Match = item.Matches[0]
		// A unique match needs only a release year, not a detailed synopsis/date.
		item.ReleaseDate, item.Description = "", ""
		item.Matches = nil
	} else {
		// Conflicting or unresolved candidates must not look like a certain ID.
		item.Status = "ambiguous"
	}
	return item
}

// A small number of TV results have no first-air date in TMDB's search
// response or Cinemeta. Only those entries trigger a TMDB details request.
func (c *Client) enrichMissingTVYear(ctx context.Context, candidate searchHit, match *Match) {
	if candidate.ID <= 0 {
		return
	}
	var details struct {
		FirstAirDate     string `json:"first_air_date"`
		OriginalLanguage string `json:"original_language"`
		Overview         string `json:"overview"`
		Genres           []struct {
			Name string `json:"name"`
		} `json:"genres"`
	}
	if err := c.get(ctx, fmt.Sprintf("/3/tv/%d", candidate.ID), nil, &details); err != nil {
		return
	}
	match.Year = releaseYear(details.FirstAirDate)
	if match.ReleaseDate == "" {
		match.ReleaseDate = details.FirstAirDate
	}
	if match.Language == "" {
		match.Language = details.OriginalLanguage
	}
	if match.Description == "" {
		match.Description = shorten(details.Overview)
	}
	if len(match.Genres) == 0 {
		for _, genre := range details.Genres {
			if genre.Name != "" && len(match.Genres) < 4 {
				match.Genres = append(match.Genres, genre.Name)
			}
		}
	}
}

// Cinemeta is already used for IMDb ratings elsewhere in this repository.
// Ratings are best-effort and never prevent an otherwise verified IMDb ID.
func (c *Client) enrichFromCinemeta(ctx context.Context, match *Match) {
	if c.ratingResolver == nil {
		return
	}
	select {
	case c.ratingSlots <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-c.ratingSlots }()
	ratingCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	preview, err := c.ratingResolver.FetchPreview(ratingCtx, match.MediaType, match.IMDbID)
	if err != nil {
		return
	}
	if match.Year == 0 {
		match.Year = releaseYear(preview.ReleaseInfo)
	}
	if len(match.Genres) == 0 {
		for _, link := range preview.Links {
			if link.Category == "Genres" && link.Name != "" && len(match.Genres) < 4 {
				match.Genres = append(match.Genres, link.Name)
			}
		}
	}
	rating, err := strconv.ParseFloat(preview.IMDBRating, 64)
	if err == nil && rating > 0 && rating <= 10 {
		match.IMDbRating = rating
	}
}

func releaseYear(date string) int {
	if len(date) < 4 {
		return 0
	}
	year, err := strconv.Atoi(date[:4])
	if err != nil || year < 1870 || year > time.Now().Year()+5 {
		return 0
	}
	return year
}

func validIMDbID(id string) bool {
	if len(id) < 4 || !strings.HasPrefix(id, "tt") {
		return false
	}
	for _, ch := range id[2:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func shorten(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= maxDescription {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxDescription-1])) + "…"
}

type statusError int

func (e statusError) Error() string { return fmt.Sprintf("TMDB HTTP %d", e) }

func safeError(err error) string {
	var status statusError
	if errors.As(err, &status) {
		if status == http.StatusTooManyRequests {
			return "TMDB rate limited this request; retry later"
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return "TMDB authentication failed; check the configured credential"
		}
	}
	return "TMDB request failed; retry later"
}

// get retries only rate limits and transient server errors. Never propagate
// raw HTTP/URL errors to tool output: API-key auth puts a secret in the URL.
func (c *Client) get(ctx context.Context, endpoint string, params url.Values, target any) error {
	base := strings.TrimRight(c.BaseURL, "/")
	u, err := url.Parse(base + endpoint)
	if err != nil {
		return err
	}
	q := u.Query()
	for key, values := range params {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	if c.readToken == "" {
		q.Set("api_key", c.apiKey)
	}
	u.RawQuery = q.Encode()

	for attempt := 0; attempt < 3; attempt++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			c.release()
			return err
		}
		req.Header.Set("Accept", "application/json")
		if c.readToken != "" {
			req.Header.Set("Authorization", "Bearer "+c.readToken)
		}
		resp, err := c.HTTPClient.Do(req)
		c.release()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusOK {
			err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target)
			resp.Body.Close()
			return err
		}
		resp.Body.Close()
		status := statusError(resp.StatusCode)
		if attempt == 2 || (resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500) {
			return status
		}
		pause := time.Duration(250*(1<<attempt)) * time.Millisecond
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
			pause = time.Duration(seconds) * time.Second
		} else if date, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			pause = time.Until(date)
		}
		if pause > 3*time.Second {
			pause = 3 * time.Second
		}
		if pause > 0 {
			timer := time.NewTimer(pause)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
	}
	return errors.New("TMDB request exhausted retries")
}

func (c *Client) wait(ctx context.Context) error {
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := c.limiter.Wait(ctx); err != nil {
		c.release()
		return err
	}
	return nil
}

func (c *Client) release() { <-c.slots }
