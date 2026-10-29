package handler

import (
	"net/http"
	"os"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"github.com/amxv/stremio-catalog-mcp/pkg/server"
	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
	"github.com/amxv/stremio-catalog-mcp/pkg/tvremote"
)

// Handler exposes a stateless MCP endpoint as a Vercel Go Function.
func Handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("resource") == "tv" {
		store := &catalog.RedisStore{URL: os.Getenv("UPSTASH_REDIS_REST_URL"), Token: os.Getenv("UPSTASH_REDIS_REST_TOKEN"), Key: "stremio-catalog-mcp:v1:catalogs"}
		tvremote.PublicHandler(tvremote.FromEnv(store)).ServeHTTP(w, r)
		return
	}
	if r.URL.Query().Get("resource") != "" {
		store := &catalog.RedisStore{URL: os.Getenv("UPSTASH_REDIS_REST_URL"), Token: os.Getenv("UPSTASH_REDIS_REST_TOKEN"), Key: "stremio-catalog-mcp:v1:catalogs"}
		stremio.Handler{
			Manager:   catalog.Manager{Store: store},
			FeedToken: os.Getenv("STREMIO_FEED_TOKEN"),
			Preview:   stremio.PreviewEnricher{Resolver: catalog.Cinemeta{}, Cache: store},
		}.ServeHTTP(w, r)
		return
	}
	server.Handler().ServeHTTP(w, r)
}
