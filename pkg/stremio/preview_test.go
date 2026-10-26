package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
)

type fakePreviewResolver struct {
	mu     sync.Mutex
	calls  map[string]int
	failID string
}

func (f *fakePreviewResolver) FetchPreview(_ context.Context, typ, id string) (catalog.Preview, error) {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	f.calls[id]++
	f.mu.Unlock()
	if f.failID == id {
		return catalog.Preview{}, errors.New("Cinemeta unavailable")
	}
	return catalog.Preview{ID: id, Type: typ, ReleaseInfo: "2022", Runtime: "55 min", Description: "From Cinemeta", IMDBRating: "8.2", Links: []catalog.PreviewLink{{Name: "8.2", Category: "imdb", URL: "https://imdb.com/title/" + id}, {Name: "Drama", Category: "Genres"}}}, nil
}

func (f *fakePreviewResolver) callsFor(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

type fakePreviewCache struct {
	mu    sync.Mutex
	raw   map[string]string
	loads int
	saves int
	fail  bool
}

func (c *fakePreviewCache) LoadPreviewCache(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loads++
	if c.fail {
		return "", errors.New("Redis unavailable")
	}
	return c.raw[key], nil
}

func (c *fakePreviewCache) SavePreviewCache(_ context.Context, key, raw string, ttl int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("Redis unavailable")
	}
	if ttl != 86400 {
		return errors.New("incorrect preview expiration")
	}
	if c.raw == nil {
		c.raw = make(map[string]string)
	}
	c.raw[key] = raw
	c.saves++
	return nil
}

func TestPreviewEnrichmentIsAutomaticCachedAndPreservesOrdering(t *testing.T) {
	resolver := &fakePreviewResolver{}
	cache := &fakePreviewCache{}
	enricher := PreviewEnricher{Resolver: resolver, Cache: cache}
	row := catalog.Row{ID: "row_test", Type: "movie", Items: []catalog.Item{
		{ID: "tt0371746", Type: "movie", Title: "Iron Man", Year: 2008, Poster: "https://saved.example/ironman.jpg"},
		{ID: "tt4154796", Type: "movie", Title: "Avengers: Endgame", Year: 2019, Poster: "https://saved.example/endgame.jpg"},
	}}
	first := enricher.RowMetadata(context.Background(), row)
	if len(first) != 2 || cache.saves != 1 || resolver.callsFor("tt0371746") != 1 || resolver.callsFor("tt4154796") != 1 {
		t.Fatalf("first request did not resolve metadata and save cache: %+v", first)
	}
	second := enricher.RowMetadata(context.Background(), row)
	if !reflect.DeepEqual(first, second) || resolver.callsFor("tt0371746") != 1 || resolver.callsFor("tt4154796") != 1 {
		t.Fatalf("cache should avoid repeat Cinemeta requests: %+v", second)
	}
	row.Items = append(row.Items, catalog.Item{ID: "tt0800369", Title: "Thor", Type: "movie"})
	third := enricher.RowMetadata(context.Background(), row)
	if len(third) != 3 || resolver.callsFor("tt0371746") != 1 || resolver.callsFor("tt0800369") != 1 {
		t.Fatalf("new title should be enriched without re-resolving others: %+v", third)
	}
	preview := buildPreview(row.Items[0], first["tt0371746"])
	if preview["poster"] != "https://saved.example/ironman.jpg" || preview["releaseInfo"] != "2022" || preview["runtime"] != "55 min" || preview["imdbRating"] != "8.2" || preview["description"] != "From Cinemeta" {
		t.Fatalf("wrong preview fields or saved poster overwritten: %+v", preview)
	}
	links, ok := preview["links"].([]catalog.PreviewLink)
	if !ok || len(links) != 2 || links[0].Category != "imdb" {
		t.Fatalf("IMDb and genre links missing: %+v", preview)
	}
}

func TestCatalogProtocolShowsCinemetaMetadataWithoutChangingMCPState(t *testing.T) {
	store := &catalog.MemoryStore{}
	row := catalog.Row{ID: "row_unchanged", Name: "Movies", Type: "movie", Items: []catalog.Item{{ID: "tt0371746", Type: "movie", Title: "Iron Man", Year: 2008, Poster: "https://saved.example/poster.jpg"}}}
	state := catalog.State{Revision: 12, Rows: []catalog.Row{row}}
	raw, _ := json.Marshal(state)
	if _, err := store.CAS(context.Background(), "", string(raw)); err != nil {
		t.Fatal(err)
	}
	resolver := &fakePreviewResolver{}
	h := Handler{Manager: catalog.Manager{Store: store}, FeedToken: "test-token", Preview: PreviewEnricher{Resolver: resolver, Cache: &fakePreviewCache{}}}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/stremio/test-token/v12/catalog/movie/row_unchanged.json", nil))
	if rec.Code != 200 {
		t.Fatalf("catalog returned HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var data struct {
		Metas []map[string]any `json:"metas"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil || len(data.Metas) != 1 {
		t.Fatalf("invalid Stremio response: %+v (%v)", data, err)
	}
	meta := data.Metas[0]
	if meta["id"] != "tt0371746" || meta["name"] != "Iron Man" || meta["runtime"] != "55 min" || meta["description"] != "From Cinemeta" || meta["imdbRating"] != "8.2" {
		t.Fatalf("enriched catalog response missing TV header fields: %+v", meta)
	}
	links, ok := meta["links"].([]any)
	if !ok || len(links) != 2 || links[0].(map[string]any)["category"] != "imdb" {
		t.Fatalf("IMDb link absent from catalog metadata: %+v", meta)
	}
	after, _ := store.Load(context.Background())
	if after != string(raw) {
		t.Fatal("enrichment mutated catalog state or agent-facing schema")
	}
}

func TestCatalogGracefulFallbackWhenCinemetaOrCacheUnavailable(t *testing.T) {
	r := &fakePreviewResolver{failID: "tt0371746"}
	row := catalog.Row{ID: "row_failure", Type: "movie", Items: []catalog.Item{{ID: "tt0371746", Type: "movie", Title: "Iron Man", Year: 2008, Poster: "https://saved.example/poster.jpg"}}}
	meta := (PreviewEnricher{Resolver: r, Cache: &fakePreviewCache{fail: true}}).RowMetadata(context.Background(), row)
	if len(meta) != 0 {
		t.Fatalf("upstream failure should omit enrichment: %+v", meta)
	}
	result := buildPreview(row.Items[0], meta["tt0371746"])
	if result["poster"] != "https://saved.example/poster.jpg" || result["releaseInfo"] != "2008" || result["id"] != "tt0371746" {
		t.Fatalf("upstream outage lost original catalog data: %+v", result)
	}
}
