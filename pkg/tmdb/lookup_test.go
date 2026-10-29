package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"golang.org/x/time/rate"
)

type fakeRating struct {
	ratings map[string]string
}

func (f fakeRating) FetchPreview(_ context.Context, mediaType, id string) (catalog.Preview, error) {
	return catalog.Preview{ID: id, Type: mediaType, IMDBRating: f.ratings[id]}, nil
}

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("test-read-token", "")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()
	c.limiter = rate.NewLimiter(rate.Inf, 100)
	c.slots = make(chan struct{}, 8)
	c.ratingResolver = fakeRating{ratings: map[string]string{
		"tt0903747": "9.5", "tt1392202": "6.2", "tt1632701": "8.4", "tt0000313": "4.8",
	}}
	c.ratingSlots = make(chan struct{}, 8)
	return c
}

func TestLookupBatchMatchesAndKeepsSmallResults(t *testing.T) {
	var mu sync.Mutex
	searches := make(map[string]int)
	active, maxActive := 0, 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-read-token" {
			t.Errorf("missing bearer authorization")
		}
		if r.URL.Query().Has("api_key") {
			t.Error("API key should not appear when token is configured")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/3/search/multi" {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			searches[r.URL.Query().Get("query")]++
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			switch r.URL.Query().Get("query") {
			case "Breaking Bad":
				fmt.Fprint(w, `{"results":[{"id":1396,"media_type":"tv","name":"Breaking Bad","first_air_date":"2008-01-20","popularity":99,"original_language":"en","genre_ids":[18,80],"overview":"Walter White..."},{"id":12,"media_type":"person","name":"Breaking Bad"}]}`)
			case "Spiderman 3":
				fmt.Fprint(w, `{"results":[{"id":559,"media_type":"movie","title":"Spider-Man 3","release_date":"2007-05-01","original_language":"en","genre_ids":[28,12],"popularity":75}]}`)
			case "Suits":
				fmt.Fprint(w, `{"results":[{"id":37680,"media_type":"tv","name":"Suits","first_air_date":"2011-06-23","original_language":"en","genre_ids":[18,35],"overview":"Lawyers in New York","popularity":90},{"id":313,"media_type":"movie","title":"Suits","release_date":"1999-04-03","original_language":"fr","genre_ids":[35],"overview":"A film about suits","popularity":15},{"id":900,"media_type":"tv","name":"Suits: LA","popularity":95}]}`)
			default:
				fmt.Fprint(w, `{"results":[{"id":99,"media_type":"movie","title":"Something Else"}]}`)
			}
			return
		}
		ids := map[string]string{
			"/3/tv/1396/external_ids":   `{"imdb_id":"tt0903747"}`,
			"/3/movie/559/external_ids": `{"imdb_id":"tt1392202"}`,
			"/3/tv/37680/external_ids":  `{"imdb_id":"tt1632701"}`,
			"/3/movie/313/external_ids": `{"imdb_id":"tt0000313"}`,
		}
		if body, ok := ids[r.URL.Path]; ok {
			fmt.Fprint(w, body)
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		http.NotFound(w, r)
	})

	result, err := client.Lookup(context.Background(), Input{Titles: []string{
		"Breaking Bad", "Spiderman 3", "Suits", "  Breaking Bad ", "Nothing here",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if maxActive <= 1 {
		t.Fatalf("expected parallel searches, observed %d concurrent", maxActive)
	}
	if searches["Breaking Bad"] != 1 {
		t.Errorf("duplicate input should share search: %v", searches)
	}
	if len(result.Results) != 5 {
		t.Fatalf("result count mismatch: %+v", result)
	}
	if result.Results[0].IMDbID != "tt0903747" || result.Results[1].IMDbID != "tt1392202" {
		t.Fatalf("incorrect unique title matches: %+v", result.Results[:2])
	}
	if result.Results[3].IMDbID != "tt0903747" || result.Results[3].Query != "Breaking Bad" {
		t.Errorf("input-order duplicate mapping wrong: %+v", result.Results[3])
	}
	if result.Results[4].Status != "not_found" {
		t.Errorf("unknown title must not be guessed: %+v", result.Results[4])
	}
	if len(result.Results[2].Matches) != 2 || result.Results[2].Matches[0].MediaType != "series" || result.Results[2].Matches[1].MediaType != "movie" {
		t.Fatalf("ambiguity missing exact movie+series matches: %+v", result.Results[2])
	}
	if result.Results[2].Status != "ambiguous" || result.Results[2].MoreMatches {
		t.Fatalf("ambiguous result should be explicit, without false truncation: %+v", result.Results[2])
	}
	if result.Results[2].Matches[0].ReleaseDate != "2011-06-23" || result.Results[2].Matches[1].Description != "A film about suits" {
		t.Fatalf("ambiguity lacks minimal metadata: %+v", result.Results[2])
	}
	if result.Results[0].Title != "Breaking Bad" || result.Results[0].Year != 2008 ||
		result.Results[0].Language != "en" || result.Results[0].IMDbRating != 9.5 ||
		strings.Join(result.Results[0].Genres, ",") != "Drama,Crime" {
		t.Fatalf("unique result missing requested metadata: %+v", result.Results[0])
	}
	if result.Results[2].Matches[1].Year != 1999 || result.Results[2].Matches[1].Language != "fr" ||
		strings.Join(result.Results[2].Matches[1].Genres, ",") != "Comedy" ||
		result.Results[2].Matches[1].IMDbRating != 4.8 {
		t.Fatalf("ambiguous result missing requested metadata: %+v", result.Results[2].Matches[1])
	}
	encoded, err := json.Marshal(result.Results[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "description") || strings.Contains(string(encoded), "release_date") ||
		strings.Contains(string(encoded), "matches") || !strings.Contains(string(encoded), `"imdb_rating":9.5`) {
		t.Errorf("unique match should return compact useful metadata without extras, got %s", encoded)
	}
}

func TestLookupPrefersDisplayedTitleOverUnrelatedOriginalTitle(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/multi":
			fmt.Fprint(w, `{"results":[{"media_type":"movie","id":100,"title":"Dark Nemesis","original_title":"The Dark Knight","popularity":1000},{"media_type":"movie","id":200,"title":"The Dark Knight","popularity":100,"release_date":"2008-07-18","original_language":"en","genre_ids":[28,80]}]}`)
		case "/3/movie/200/external_ids":
			fmt.Fprint(w, `{"imdb_id":"tt0468569"}`)
		default:
			t.Errorf("irrelevant title should not trigger an IMDb lookup: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	result, err := c.Lookup(context.Background(), Input{Titles: []string{"The Dark Knight"}})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Results[0]
	if got.IMDbID != "tt0468569" || got.Title != "The Dark Knight" || got.Status != "" || len(got.Matches) != 0 || got.MoreMatches {
		t.Fatalf("should resolve the displayed-title match without Dark Nemesis: %+v", got)
	}
}

func TestLookupOneVerifiedIMDbIDIsNotAmbiguous(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/multi":
			fmt.Fprint(w, `{"results":[{"media_type":"movie","id":1,"title":"Inception","release_date":"2010-07-16","original_language":"en","genre_ids":[28,878]},{"media_type":"movie","id":2,"title":"Inception","release_date":"2010-01-01"}]}`)
		case "/3/movie/1/external_ids":
			fmt.Fprint(w, `{"imdb_id":"tt1375666"}`)
		case "/3/movie/2/external_ids":
			fmt.Fprint(w, `{"imdb_id":null}`)
		default:
			t.Errorf("unexpected TMDB request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	result, err := c.Lookup(context.Background(), Input{Titles: []string{"Inception"}})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Results[0]
	if got.IMDbID != "tt1375666" || got.Title != "Inception" || got.Year != 2010 || got.Status != "" || len(got.Matches) != 0 {
		t.Fatalf("only verified IMDb ID should return a flat, resolved result: %+v", got)
	}
}

func TestLookupFallsBackToOriginalNameWhenNoDisplayedTitleMatches(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/multi":
			fmt.Fprint(w, `{"results":[{"media_type":"movie","id":10,"title":"Amélie","original_title":"Le Fabuleux Destin d'Amélie Poulain","release_date":"2001-04-25"}]}`)
		case "/3/movie/10/external_ids":
			fmt.Fprint(w, `{"imdb_id":"tt0211915"}`)
		default:
			t.Errorf("unexpected TMDB request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	result, err := c.Lookup(context.Background(), Input{Titles: []string{"Le Fabuleux Destin d'Amélie Poulain"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Results[0]; got.IMDbID != "tt0211915" || got.Title != "Amélie" || got.Status != "" {
		t.Fatalf("original title should remain a fallback: %+v", got)
	}
}

func TestLookupPreservesGenuineSameNameAmbiguity(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/multi":
			fmt.Fprint(w, `{"results":[{"media_type":"movie","id":1,"title":"The Girl with the Dragon Tattoo","release_date":"2011-12-14","original_language":"en"},{"media_type":"movie","id":2,"title":"The Girl with the Dragon Tattoo","release_date":"2009-02-27","original_language":"sv"},{"media_type":"tv","id":3,"name":"The Girl with the Dragon Tattoo","original_language":"en"}]}`)
		case "/3/movie/1/external_ids":
			fmt.Fprint(w, `{"imdb_id":"tt1568346"}`)
		case "/3/movie/2/external_ids":
			fmt.Fprint(w, `{"imdb_id":"tt1132620"}`)
		case "/3/tv/3/external_ids":
			fmt.Fprint(w, `{"imdb_id":"tt12401288"}`)
		default:
			t.Errorf("unexpected TMDB request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	result, err := c.Lookup(context.Background(), Input{Titles: []string{"The Girl with the Dragon Tattoo"}})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Results[0]
	if got.Status != "ambiguous" || len(got.Matches) != 3 || got.IMDbID != "" {
		t.Fatalf("three distinct exact matches must remain ambiguous: %+v", got)
	}
	if got.Matches[0].IMDbID != "tt1568346" || got.Matches[1].IMDbID != "tt1132620" || got.Matches[2].IMDbID != "tt12401288" {
		t.Fatalf("wrong or missing match IDs: %+v", got.Matches)
	}
}

func TestLookupNeverClaimsSingleMatchWhenOtherCandidateFails(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/search/multi":
			fmt.Fprint(w, `{"results":[{"media_type":"tv","id":1,"name":"Suits"},{"media_type":"movie","id":2,"title":"Suits"}]}`)
		case "/3/tv/1/external_ids":
			fmt.Fprint(w, `{"imdb_id":"tt1234"}`)
		default:
			http.Error(w, "temporary failure", http.StatusBadGateway)
		}
	})
	result, err := c.Lookup(context.Background(), Input{Titles: []string{"Suits"}})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Results[0]
	if got.IMDbID != "" || got.Status != "error" || got.Error == "" {
		t.Fatalf("partial candidate failure must be reported, not guessed: %+v", got)
	}
}

func TestLookupSignalsTruncatedAmbiguity(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/search/multi" {
			fmt.Fprint(w, `{"results":[{"media_type":"movie","id":1,"title":"Dune","popularity":10},{"media_type":"movie","id":2,"title":"Dune","popularity":9},{"media_type":"movie","id":3,"title":"Dune","popularity":8},{"media_type":"movie","id":4,"title":"Dune","popularity":7}]}`)
			return
		}
		var id string
		switch r.URL.Path {
		case "/3/movie/1/external_ids":
			id = "tt11111"
		case "/3/movie/2/external_ids":
			id = "tt22222"
		case "/3/movie/3/external_ids":
			id = "tt33333"
		default:
			t.Errorf("unnecessary fourth candidate fetch: %s", r.URL.Path)
		}
		fmt.Fprintf(w, `{"imdb_id":%q}`, id)
	})
	result, err := c.Lookup(context.Background(), Input{Titles: []string{"Dune"}})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Results[0]
	if got.Status != "ambiguous" || !got.MoreMatches || len(got.Matches) != 3 || got.IMDbID != "" {
		t.Fatalf("truncated matches must not claim certainty: %+v", got)
	}
}

func TestLookupRateLimitRetryAndSecretSafety(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	const apiKey = "private-super-secret-key"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != apiKey || r.Header.Get("Authorization") != "" {
			t.Errorf("API-key fallback was not used")
		}
		if r.URL.Path == "/3/search/multi" {
			fmt.Fprint(w, `{"results":[{"media_type":"tv","id":1,"name":"Example"}]}`)
			return
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "limit exceeded", http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"imdb_id":"tt1234567"}`)
	})
	c.readToken = ""
	c.apiKey = apiKey
	result, err := c.Lookup(context.Background(), Input{Titles: []string{"Example"}})
	if err != nil || result.Results[0].IMDbID != "tt1234567" || calls != 2 {
		t.Fatalf("429 did not retry: result=%+v error=%v calls=%d", result, err, calls)
	}

	c.HTTPClient = &http.Client{Transport: brokenTransport{}}
	failed, err := c.Lookup(context.Background(), Input{Titles: []string{"Example"}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(failed)
	if strings.Contains(string(encoded), apiKey) || failed.Results[0].Status != "error" {
		t.Fatalf("error leaked API key or failed to disclose error: %s", encoded)
	}
}

type brokenTransport struct{}

func (brokenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("transport failed at URL with private-super-secret-key")
}

func TestLookupValidatesInputAndTruncatesOverview(t *testing.T) {
	c := New("token", "")
	for _, input := range []Input{{}, {Titles: []string{" "}}, {Titles: []string{strings.Repeat("x", 151)}}, {Titles: make([]string, 51)}} {
		if _, err := c.Lookup(context.Background(), input); err == nil {
			t.Fatalf("invalid input accepted: %+v", input)
		}
	}
	if got := shorten(strings.Repeat("é", 220)); len([]rune(got)) > maxDescription {
		t.Errorf("overview not truncated by rune count: %d", len([]rune(got)))
	}
	if !validIMDbID("tt0903747") || validIMDbID("nm0903747") || validIMDbID("tt0x") {
		t.Error("IMDb ID validation failed")
	}
}
