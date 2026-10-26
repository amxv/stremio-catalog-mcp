package server

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
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
	s := mcp.NewServer(&mcp.Implementation{Name: "stremio-catalog-mcp", Version: "0.5.0"}, nil)

	mcp.AddTool(s, catalogTool("catalogs_get", "Read custom catalog rows. With no parameters return compact row summaries; pass row_id for one row's ordered titles including saved poster URLs, or include_titles=true for every row's titles. include_install_url=true returns the private addon URL only when explicitly requested. Use home_layout_get for third-party addon rows."),
		func(ctx context.Context, _ *mcp.CallToolRequest, input CatalogsGetInput) (*mcp.CallToolResult, CatalogsGetResult, error) {
			result, err := rt.catalogsGet(ctx, input)
			return nil, result, err
		})

	mcp.AddTool(s, catalogTool("catalogs_update", "Batch changes to custom catalog CONTENT: actions create (name, type, optional titles), edit (row_id plus name/add_titles/remove_title_ids/replace_titles/move_titles), delete (row_id), sync (retry Stremio account synchronization). replace_titles=[] explicitly clears a row; omit it to preserve titles. For row position or visibility use home_layout_update. Optional dry_run preview, expected_revision and request_id for safe retries. Returns changed rows with posters and separate save/sync status."),
		func(ctx context.Context, _ *mcp.CallToolRequest, input catalog.BatchInput) (*mcp.CallToolResult, CatalogsUpdateResult, error) {
			result, err := rt.catalogsUpdate(ctx, input)
			return nil, result, err
		})

	mcp.AddTool(s, catalogTool("home_layout_get", "Read the live Stremio Home layout: installed addon order, all catalogs including hidden rows, and supported visibility controls. Excludes private addon URLs. If the account is not linked, shows custom rows only."),
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, stremio.HomeLayout, error) {
			view, err := rt.homeLayoutGet(ctx)
			return nil, view, err
		})

	mcp.AddTool(s, catalogTool("home_layout_update", "Batch hide/show Home rows, reorder rows within any installed addon, and move complete addons. Provide exact IDs from home_layout_get. Use dry_run=true to preview. Preserves stream addons and backs up the account before saving."),
		func(ctx context.Context, _ *mcp.CallToolRequest, input stremio.HomeUpdateInput) (*mcp.CallToolResult, HomeUpdateResult, error) {
			result, err := rt.homeLayoutUpdate(ctx, input)
			return nil, result, err
		})

	mcp.AddTool(s, catalogTool("watch_history", "Read or edit live Stremio watch history in ONE call per title, no episode lookup required. Default action=get lists history or inspects an imdb_id. To fully watch ANY movie or TV show: action=mark_watched plus just query='Title' or imdb_id='tt...'; for series this checks all RELEASED regular episodes across all seasons, skipping future/unknown-release episodes and specials, in a single verified account write. To fully unwatch/remove a movie or TV show: action=mark_unwatched with just query or imdb_id; this clears all playback history and marks its record removed (Stremio retains a sync tombstone). To change only specific series episodes, supply season and episodes=[1,2] alongside either action. For exact saved title query, media_type is optional; ambiguous titles may need media_type/year or imdb_id. Read responses include watched episodes, plots of watched episodes and spoiler_safe_through. Never infer watched episodes from partial playback."),
		func(ctx context.Context, _ *mcp.CallToolRequest, input WatchHistoryInput) (*mcp.CallToolResult, WatchHistoryResult, error) {
			result, err := rt.watchHistory(ctx, input)
			return nil, result, err
		})
	return s
}
