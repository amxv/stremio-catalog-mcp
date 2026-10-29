package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/time/rate"
)

type titleFixture struct {
	Movies []searchHit
	Shows  []searchHit
}

func fixtureHit(id int64, title, date string, votes int, popularity float64) searchHit {
	return searchHit{ID: id, Title: title, Name: title,
		ReleaseDate: date, FirstAirDate: date, VoteCount: votes,
		Popularity: popularity, OriginalLanguage: "en"}
}

func fixtureClient(t *testing.T, titles map[string]titleFixture, ids map[int64]string, aliases map[int64][]string) (*Client, func() map[string]int) {
	t.Helper()
	var mu sync.Mutex
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case len(parts) == 3 && parts[0] == "3" && parts[1] == "search":
			query := titles[r.URL.Query().Get("query")]
			var entries []searchHit
			if parts[2] == "movie" {
				entries = query.Movies
			} else if parts[2] == "tv" {
				entries = query.Shows
			} else {
				t.Errorf("unexpected search path: %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": entries})
		case len(parts) == 4 && parts[0] == "3" && parts[3] == "external_ids":
			id, _ := strconv.ParseInt(parts[2], 10, 64)
			_ = json.NewEncoder(w).Encode(map[string]any{"imdb_id": ids[id]})
		case len(parts) == 4 && parts[0] == "3" && parts[3] == "alternative_titles":
			id, _ := strconv.ParseInt(parts[2], 10, 64)
			var values []map[string]string
			for _, name := range aliases[id] {
				values = append(values, map[string]string{"title": name})
			}
			if parts[1] == "tv" {
				_ = json.NewEncoder(w).Encode(map[string]any{"results": values})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"titles": values})
			}
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	c := New("fixture-token", "")
	c.BaseURL, c.HTTPClient = server.URL, server.Client()
	c.limiter = rate.NewLimiter(rate.Inf, 100)
	c.slots = make(chan struct{}, 32)
	c.ratingResolver = fakeRating{ratings: map[string]string{"tt0375679": "7.7", "tt0114746": "8.0"}}
	c.ratingSlots = make(chan struct{}, 32)
	c.cache = newLookupCache()
	return c, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		snapshot := make(map[string]int, len(calls))
		for k, v := range calls {
			snapshot[k] = v
		}
		return snapshot
	}
}

func TestStressReportCandidateCoverageAndRanking(t *testing.T) {
	crash := titleFixture{Movies: []searchHit{
		fixtureHit(2018, "Crash", "2018-09-10", 20, 350),
		fixtureHit(1996, "Crash", "1996-07-17", 400, 90),
		fixtureHit(2024, "Crash", "2024-03-01", 12, 200),
		fixtureHit(2004, "Crash", "2004-05-06", 7500, 30),
	}}
	monkeys := titleFixture{
		Movies: []searchHit{fixtureHit(1995, "Twelve Monkeys", "1995-12-29", 10000, 15)},
		Shows:  []searchHit{fixtureHit(2015, "12 Monkeys", "2015-01-16", 2900, 160)},
	}
	alien := titleFixture{Movies: []searchHit{
		fixtureHit(1979, "Alien", "1979-05-25", 10000, 35),
		fixtureHit(1992, "Alien³", "1992-05-22", 3100, 150),
		fixtureHit(2017, "Alien: Covenant", "2017-05-19", 5400, 260),
	}}
	chineseMovie := fixtureHit(2000, "Crouching Tiger, Hidden Dragon", "2000-07-06", 6500, 60)
	chineseMovie.OriginalTitle, chineseMovie.OriginalLanguage = "臥虎藏龍", "zh"
	chineseSeries := fixtureHit(2001, "卧虎藏龙", "2001-01-01", 100, 80)
	chineseSeries.OriginalLanguage = "zh"
	takenSeries := fixtureHit(20170, "Taken", "2017-02-27", 160, 220)
	cases := map[string]titleFixture{
		"Crash":      crash,
		"12 Monkeys": monkeys,
		"Alien":      alien,
		"卧虎藏龙":       {Movies: []searchHit{chineseMovie}, Shows: []searchHit{chineseSeries}},
		"The Room": {Movies: []searchHit{
			fixtureHit(2019, "The Room", "2019-09-12", 300, 650),
			fixtureHit(2003, "The Room", "2003-06-27", 2900, 22),
		}},
		"Taken": {Movies: []searchHit{fixtureHit(2008, "Taken", "2008-02-27", 8900, 30)},
			Shows: []searchHit{takenSeries}},
	}
	ids := map[int64]string{
		2018: "tt0012018", 1996: "tt0115964", 2024: "tt0012024", 2004: "tt0375679",
		1995: "tt0114746", 2015: "tt3148266", 1979: "tt0078748",
		1992: "tt0103644", 2017: "tt2316204", 2000: "tt0190332", 2001: "tt1234567",
		2019: "tt1000001", 2003: "tt0368226", 2008: "tt0936501", 20170: "tt5052460",
	}
	client, calls := fixtureClient(t, cases, ids, nil)
	result, err := client.Lookup(context.Background(), Input{Titles: []string{
		"Crash", "12 Monkeys", "12 Monkeys (2015)", "Alien", "卧虎藏龙", "The Room", "Taken",
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Results
	if len(got[0].Matches) != 4 || got[0].Matches[0].IMDbID != "tt0375679" || got[0].MoreMatches {
		t.Errorf("Crash: expected all four candidates with 2004 ranked first: %+v", got[0])
	}
	if got[1].Status != "ambiguous" || len(got[1].Matches) != 2 ||
		got[1].Matches[0].IMDbID != "tt0114746" || got[1].Matches[1].IMDbID != "tt3148266" {
		t.Errorf("12 Monkeys should include film and series: %+v", got[1])
	}
	if got[2].IMDbID != "tt3148266" || got[2].Year != 2015 {
		t.Errorf("year hint should select 2015 TV series: %+v", got[2])
	}
	if got[3].IMDbID != "tt0078748" || len(got[3].Matches) != 0 {
		t.Errorf("Alien³ and sequels must not create ambiguity: %+v", got[3])
	}
	if got[4].Status != "ambiguous" || len(got[4].Matches) != 2 ||
		got[4].Matches[0].IMDbID != "tt0190332" || got[4].Matches[1].IMDbID != "tt1234567" {
		t.Errorf("Chinese original title should find the movie and series: %+v", got[4])
	}
	if got[5].Matches[0].IMDbID != "tt0368226" || got[6].Matches[0].IMDbID != "tt0936501" {
		t.Errorf("popular films should outrank newer adaptations: Room=%+v Taken=%+v", got[5], got[6])
	}
	paths := calls()
	if paths["/3/search/movie"] < 7 || paths["/3/search/tv"] < 7 || paths["/3/search/multi"] != 0 {
		t.Errorf("expected both movie and TV indexes: %+v", paths)
	}
}

func TestStressReportQualifiedAndLocalizedTitles(t *testing.T) {
	officeUS := fixtureHit(10, "The Office", "2005-03-24", 10000, 100)
	officeUS.OriginCountry = []string{"US"}
	officeUK := fixtureHit(11, "The Office", "2001-07-09", 3500, 80)
	officeUK.OriginCountry = []string{"GB"}
	shogun := fixtureHit(12, "Shōgun", "2024-02-27", 5000, 110)
	baahubali := fixtureHit(13, "Bāhubali: The Beginning", "2015-07-10", 4400, 50)
	baahubali2 := fixtureHit(14, "Bāhubali 2: The Conclusion", "2017-04-28", 4200, 60)
	cases := map[string]titleFixture{
		"The Office":                  {Shows: []searchHit{officeUS, officeUK}},
		"Shogun":                      {Shows: []searchHit{shogun}},
		"Amelie":                      {Movies: []searchHit{fixtureHit(15, "Amélie", "2001-04-25", 5000, 20)}},
		"Leon The Professional":       {Movies: []searchHit{fixtureHit(16, "Léon: The Professional", "1994-09-14", 6200, 25)}},
		"Baahubali: The Beginning":    {Movies: []searchHit{baahubali}},
		"Baahubali 2: The Conclusion": {Movies: []searchHit{baahubali2}},
		"Dr. Strangelove":             {Movies: []searchHit{fixtureHit(17, "Dr. Strangelove or: How I Learned to Stop Worrying and Love the Bomb", "1964-01-29", 8100, 40)}},
		"Intouchables":                {Movies: []searchHit{fixtureHit(18, "The Intouchables", "2011-11-02", 9000, 50)}},
		"Untranslated Local Alias":    {Movies: []searchHit{fixtureHit(19, "Runaway Train", "1985-11-15", 3000, 50)}},
		"Dune":                        {Movies: []searchHit{fixtureHit(20, "Dune", "2021-10-22", 16000, 140), fixtureHit(21, "Dune", "1984-12-14", 3100, 35)}},
		"Oldboy":                      {Movies: []searchHit{fixtureHit(22, "Oldboy", "2003-11-21", 11000, 80), fixtureHit(23, "Oldboy", "2013-11-27", 1200, 90)}},
		"Zodiac":                      {Movies: []searchHit{fixtureHit(24, "Zodiac", "2007-03-02", 8000, 50)}},
		"Avatar":                      {Movies: []searchHit{fixtureHit(25, "Avatar", "2009-12-18", 30000, 200)}},
	}
	ids := map[int64]string{}
	for id := int64(10); id <= 25; id++ {
		ids[id] = fmt.Sprintf("tt%07d", id)
	}
	client, _ := fixtureClient(t, cases, ids, map[int64][]string{19: {"Untranslated Local Alias"}})
	input := []string{
		"The Office (US)", "The Office (UK)", "Amelie", "Shogun",
		"Leon The Professional", "Baahubali: The Beginning",
		"Baahubali 2: The Conclusion", "Dr. Strangelove", "Intouchables",
		"Untranslated Local Alias", "Dune (2021)", "Oldboy (2003)",
		"Zodiac (2007)", "Avatar (2009)",
	}
	result, err := client.Lookup(context.Background(), Input{Titles: input})
	if err != nil {
		t.Fatal(err)
	}
	expected := []int64{10, 11, 15, 12, 16, 13, 14, 17, 18, 19, 20, 22, 24, 25}
	for i, id := range expected {
		item := result.Results[i]
		if item.IMDbID != ids[id] || item.Status != "" {
			t.Errorf("%q should resolve %s, got %+v", input[i], ids[id], item)
		}
	}
	if result.Results[9].Title != "Runaway Train" {
		t.Errorf("alias lookup should preserve the canonical name: %+v", result.Results[9])
	}
}

func TestSuggestionsOnlyForUnresolvedTitles(t *testing.T) {
	client, _ := fixtureClient(t, map[string]titleFixture{
		"Parks and Rec": {Shows: []searchHit{fixtureHit(1, "Parks and Recreation", "2009-04-09", 2500, 70)}},
	}, nil, nil)
	result, err := client.Lookup(context.Background(), Input{Titles: []string{"Parks and Rec", "Entirely Made Up Movie"}})
	if err != nil {
		t.Fatal(err)
	}
	item := result.Results[0]
	if item.Status != "not_found" || item.IMDbID != "" || len(item.Suggestions) != 1 ||
		item.Suggestions[0].Title != "Parks and Recreation" {
		t.Fatalf("unresolved name should return an explicit suggestion, not an IMDb ID: %+v", item)
	}
	if item = result.Results[1]; item.Status != "not_found" || len(item.Suggestions) != 0 {
		t.Errorf("nonexistent title should remain not_found: %+v", item)
	}
}

func TestCrowdedShortTitleSearchChecksSecondPage(t *testing.T) {
	var mu sync.Mutex
	secondCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/search/tv" {
			_, _ = w.Write([]byte(`{"results":[],"total_pages":1}`))
			return
		}
		if r.URL.Path == "/3/search/movie" {
			if r.URL.Query().Get("page") == "2" {
				mu.Lock()
				secondCalls++
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(searchPage{Results: []searchHit{
					fixtureHit(2004, "Crash", "2004-05-06", 7500, 20),
				}, TotalPages: 3})
				return
			}
			page := []searchHit{
				fixtureHit(1996, "Crash", "1996-07-17", 400, 90),
				fixtureHit(2018, "Crash", "2018-09-10", 20, 350),
			}
			for i := 0; i < 18; i++ {
				page = append(page, fixtureHit(int64(3000+i), fmt.Sprintf("Crash Landing %d", i), "2020-01-01", 1, 500))
			}
			_ = json.NewEncoder(w).Encode(searchPage{Results: page, TotalPages: 3})
			return
		}
		switch r.URL.Path {
		case "/3/movie/2004/external_ids":
			_, _ = w.Write([]byte(`{"imdb_id":"tt0375679"}`))
		case "/3/movie/1996/external_ids":
			_, _ = w.Write([]byte(`{"imdb_id":"tt0115964"}`))
		case "/3/movie/2018/external_ids":
			_, _ = w.Write([]byte(`{"imdb_id":"tt0012018"}`))
		default:
			t.Errorf("unrelated candidate requested: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	c := New("test", "")
	c.BaseURL, c.HTTPClient = server.URL, server.Client()
	c.limiter, c.slots = rate.NewLimiter(rate.Inf, 100), make(chan struct{}, 12)
	c.ratingResolver, c.cache = fakeRating{}, newLookupCache()
	result, err := c.Lookup(context.Background(), Input{Titles: []string{"Crash"}})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Results[0]
	if got.Status != "ambiguous" || len(got.Matches) != 3 || got.Matches[0].IMDbID != "tt0375679" {
		t.Errorf("important adaptation on page 2 should rank first: %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if secondCalls != 1 {
		t.Errorf("expected one selective extra page, got %d", secondCalls)
	}
}

func TestConcurrentOverlappingRequestsReuseWork(t *testing.T) {
	fixtures := map[string]titleFixture{"Breaking Bad": {
		Shows: []searchHit{fixtureHit(1, "Breaking Bad", "2008-01-20", 10000, 200)},
	}}
	c, calls := fixtureClient(t, fixtures, map[int64]string{1: "tt0903747"}, nil)
	// Exercise singleflight as well as the completed-result cache.
	const workers = 5
	var wg sync.WaitGroup
	errors := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := c.Lookup(context.Background(), Input{Titles: []string{"Breaking Bad"}})
			if err != nil {
				errors <- err
			} else if got.Results[0].IMDbID != "tt0903747" {
				errors <- fmt.Errorf("unexpected result: %+v", got.Results[0])
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	got := calls()
	if got["/3/search/movie"] != 1 || got["/3/search/tv"] != 1 ||
		got["/3/tv/1/external_ids"] != 1 {
		t.Errorf("five calls should share one upstream request per endpoint: %+v", got)
	}
}
