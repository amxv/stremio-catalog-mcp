package server

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
	"github.com/amxv/stremio-catalog-mcp/pkg/tmdb"
	"github.com/amxv/stremio-catalog-mcp/pkg/tvremote"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Runtime struct {
	Manager         catalog.Manager
	Account         stremio.AccountAPI
	HistoryMetadata stremio.MetadataClient
	Store           *catalog.RedisStore
	VisibilityStore catalog.Store
	BaseURL         string
	FeedToken       string
	TV              *tvremote.Service
	TMDB            *tmdb.Client
}

func runtimeFromEnv() Runtime {
	store := &catalog.RedisStore{URL: os.Getenv("UPSTASH_REDIS_REST_URL"), Token: os.Getenv("UPSTASH_REDIS_REST_TOKEN"), Key: "stremio-catalog-mcp:v1:catalogs"}
	visibilityStore := &catalog.RedisStore{URL: store.URL, Token: store.Token, Key: store.Key + ":home-visibility"}
	account := stremio.AccountAPI{AuthKey: os.Getenv("STREMIO_AUTH_KEY")}
	account.BeforeReplace = func(ctx context.Context, addons []stremio.Descriptor) error {
		raw, err := json.Marshal(addons)
		if err != nil {
			return err
		}
		return store.SaveAddonBackup(ctx, raw)
	}
	return Runtime{
		Manager: catalog.Manager{Store: store, Resolver: catalog.Cinemeta{}},
		Account: account,
		Store:   store, VisibilityStore: visibilityStore, BaseURL: os.Getenv("PUBLIC_BASE_URL"), FeedToken: os.Getenv("STREMIO_FEED_TOKEN"),
		TV:   tvremote.FromEnv(store),
		TMDB: tmdb.FromEnv(),
	}
}

type SyncStatus struct {
	Synced  bool   `json:"synced"`
	Message string `json:"message"`
}

func (rt Runtime) sync(ctx context.Context) SyncStatus {
	if rt.Account.AuthKey == "" {
		return SyncStatus{Message: "Saved. Stremio account not linked; install the addon URL manually or configure STREMIO_AUTH_KEY to enable automatic row syncing."}
	}
	if rt.Store != nil {
		lockToken, err := catalog.NewID()
		if err != nil {
			return SyncStatus{Message: "Saved but sync lock failed"}
		}
		locked, err := rt.Store.AcquireSyncLock(ctx, lockToken)
		if err != nil {
			return SyncStatus{Message: "Saved but sync lock unavailable: " + err.Error()}
		}
		if !locked {
			return SyncStatus{Message: "Saved; another account sync is running. Retry with catalogs_update using a sync action."}
		}
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = rt.Store.ReleaseSyncLock(releaseCtx, lockToken)
		}()
	}
	state, err := rt.Manager.Read(ctx)
	if err != nil {
		return SyncStatus{Message: "Saved but state could not be read for sync: " + err.Error()}
	}
	url, err := stremio.ManifestURL(rt.BaseURL, rt.FeedToken, state.Revision)
	if err != nil {
		return SyncStatus{Message: "Saved but cannot sync: " + err.Error()}
	}
	if err := rt.Account.Sync(ctx, state, url); err != nil {
		return SyncStatus{Message: "Saved but Stremio account sync failed: " + err.Error()}
	}
	return SyncStatus{Synced: true, Message: "Catalog manifest synced to Stremio. Restart or refresh the TV app if changes are not immediately visible."}
}

func makeServer() *mcp.Server { return serverFor(runtimeFromEnv()) }

// catalogTool centralizes the MCP metadata contract. These annotations are
// advisory discovery hints, not authorization controls: catalog write tools
// still modify state and require the configured MCP API key.
func catalogTool(name, description string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
			// Do not set OpenWorldHint on any tool.
		},
	}
}

func serverFor(rt Runtime) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "stremio-catalog-mcp", Version: "0.8.0"}, nil)

	mcp.AddTool(s, catalogTool("catalogs_get", "Read custom catalog rows. Default: compact summaries. row_id returns one row's ordered titles/posters; include_titles reads all titles; include_install_url returns the private add-on URL only when requested. Use home_layout_get for other add-ons' rows."),
		func(ctx context.Context, _ *mcp.CallToolRequest, input CatalogsGetInput) (*mcp.CallToolResult, CatalogsGetResult, error) {
			result, err := rt.catalogsGet(ctx, input)
			return nil, result, err
		})

	mcp.AddTool(s, catalogTool("catalogs_update", "Batch create/edit/delete/sync custom catalog rows with changes[]. Edits support names, add_titles, remove_title_ids, replace_titles (empty clears), and move_titles. dry_run previews; expected_revision rejects conflicts; request_id deduplicates retries. Returns saved rows, posters, and sync status."),
		func(ctx context.Context, _ *mcp.CallToolRequest, input catalog.BatchInput) (*mcp.CallToolResult, CatalogsUpdateResult, error) {
			result, err := rt.catalogsUpdate(ctx, input)
			return nil, result, err
		})

	mcp.AddTool(s, catalogTool("home_layout_get", "Read installed add-on order, Home catalog rows (including hidden), and supported visibility controls. Excludes private add-on URLs."),
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, stremio.HomeLayout, error) {
			view, err := rt.homeLayoutGet(ctx)
			return nil, view, err
		})

	mcp.AddTool(s, catalogTool("home_layout_update", "Batch Home row visibility/order and installed add-on order. Use IDs from home_layout_get; row moves stay within their add-on. dry_run previews without writing. Preserves streaming add-ons and backs up account state."),
		func(ctx context.Context, _ *mcp.CallToolRequest, input stremio.HomeUpdateInput) (*mcp.CallToolResult, HomeUpdateResult, error) {
			result, err := rt.homeLayoutUpdate(ctx, input)
			return nil, result, err
		})

	mcp.AddTool(s, catalogTool("watch_history", "Read or update synced Stremio watch history using IMDb tt... IDs only (never title queries). get (default): recent items or one title with spoiler boundary; check: 1–50 imdb_ids in one read, returning watched/partially_watched/not_watched/unverified. Partially watched series include exact watched episode numbers; fully watched titles omit episode lists. Missing records do not prove never watched. mark_watched marks a movie or all released regular series episodes; mark_unwatched clears a title's playback state but retains Stremio's sync tombstone. Supply both season and episodes to edit specific episodes; other titles stay untouched."),
		func(ctx context.Context, _ *mcp.CallToolRequest, input WatchHistoryInput) (*mcp.CallToolResult, WatchHistoryResult, error) {
			result, err := rt.watchHistory(ctx, input)
			return nil, result, err
		})
	if rt.TV != nil {
		mcp.AddTool(s, catalogTool("tv_open_title", "Open an IMDb movie or series details page on the paired TV, without autoplay. Provide media_type and imdb_id; to select a series episode, also provide season (0 for specials) and episode (1+). Omit both for the series overview. Returns TV receipt status, not proof the screen changed."),
			func(ctx context.Context, _ *mcp.CallToolRequest, input tvremote.OpenInput) (*mcp.CallToolResult, tvremote.OpenResult, error) {
				output, err := rt.TV.OpenTitle(ctx, input)
				return nil, output, err
			})
	}
	if rt.TMDB != nil {
		mcp.AddTool(s, catalogTool("title_lookup", "Resolve 1-50 movie or TV title names to IMDb IDs in one batch. Returns title, year, language, genres and IMDb rating when available; same-name matches also include media type, date and brief plot for disambiguation. Missing or uncertain matches are explicit. Requires a configured TMDB credential."),
			func(ctx context.Context, _ *mcp.CallToolRequest, input tmdb.Input) (*mcp.CallToolResult, tmdb.Result, error) {
				output, err := rt.TMDB.Lookup(ctx, input)
				return nil, output, err
			})
	}
	return s
}
