package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCinemetaFetchPreviewIncludesTVMetadata(t *testing.T) {
	var routes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routes = append(routes, r.URL.Path)
		switch r.URL.Path {
		case "/meta/movie/tt0371746.json":
			_, _ = w.Write([]byte(`{"meta":{"id":"tt0371746","type":"movie","name":"Iron Man","releaseInfo":"2008","runtime":"126 min","imdbRating":"7.9","description":"Tony Stark builds a suit.","poster":"https://cinemeta.example/poster.jpg","background":"https://cinemeta.example/bg.jpg","links":[{"name":"7.9","category":"imdb","url":"https://imdb.com/title/tt0371746"},{"name":"Action","category":"Genres","url":"stremio:///discover/action"},{"name":"Robert Downey Jr.","category":"Cast","url":"stremio:///search?search=Robert%20Downey%20Jr"}]}}`))
		case "/catalog/movie/top/search=Iron Man.json":
			_, _ = w.Write([]byte(`{"metas":[{"id":"tt0371746","type":"movie","name":"Iron Man","poster":"https://cinemeta.example/poster.jpg","releaseInfo":"2008"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := Cinemeta{BaseURL: srv.URL}
	ctx := context.Background()
	preview, err := c.FetchPreview(ctx, "movie", "tt0371746")
	if err != nil {
		t.Fatal(err)
	}
	if preview.ID != "tt0371746" || preview.Runtime != "126 min" || preview.IMDBRating != "7.9" || preview.Description == "" || preview.Background == "" || len(preview.Links) != 3 || preview.Links[0].Category != "imdb" {
		t.Fatalf("missing fields necessary for TV focus header: %+v", preview)
	}
	encoded, err := json.Marshal(preview)
	if err != nil || !strings.Contains(string(encoded), `"category":"imdb"`) {
		t.Fatalf("preview links not serializable: %s (%v)", encoded, err)
	}
	item, err := c.Resolve(ctx, "movie", ItemInput{Title: "Iron Man"})
	if err != nil || item.ID != "tt0371746" || item.Poster == "" || item.Year != 2008 {
		t.Fatalf("unchanged agent title resolution failed: %+v (%v)", item, err)
	}
	if len(routes) != 2 || routes[0] != "/meta/movie/tt0371746.json" || routes[1] != "/catalog/movie/top/search=Iron Man.json" {
		t.Fatalf("unexpected upstream paths: %+v", routes)
	}
}

func TestCinemetaFetchPreviewRejectsBadIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta":{"id":"tt9999999","type":"series","runtime":"42 min"}}`))
	}))
	defer srv.Close()
	c := Cinemeta{BaseURL: srv.URL}
	for _, tc := range []struct{ typ, id string }{{"movie", "tt0371746"}, {"series", "bad"}, {"tv", "tt0371746"}} {
		if _, err := c.FetchPreview(context.Background(), tc.typ, tc.id); err == nil {
			t.Fatalf("unsafe metadata accepted for %s %s", tc.typ, tc.id)
		}
	}
}
