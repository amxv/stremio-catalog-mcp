package server

import (
	"context"
	"os"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Runtime struct {
	Manager   catalog.Manager
	Account   stremio.AccountAPI
	Store     *catalog.RedisStore
	BaseURL   string
	FeedToken string
}

func runtimeFromEnv() Runtime {
	store := &catalog.RedisStore{URL: os.Getenv("UPSTASH_REDIS_REST_URL"), Token: os.Getenv("UPSTASH_REDIS_REST_TOKEN"), Key: "stremio-catalog-mcp:v1:catalogs"}
	return Runtime{
		Manager: catalog.Manager{Store: store, Resolver: catalog.Cinemeta{}},
		Account: stremio.AccountAPI{AuthKey: os.Getenv("STREMIO_AUTH_KEY")},
		Store:   store, BaseURL: os.Getenv("PUBLIC_BASE_URL"), FeedToken: os.Getenv("STREMIO_FEED_TOKEN"),
	}
}

type SyncStatus struct {
	Synced  bool   `json:"synced"`
	Message string `json:"message"`
}

func contentOnlyStatus() SyncStatus {
	return SyncStatus{Message: "Catalog titles saved; no account manifest update was necessary. Existing Stremio rows will fetch updated contents when refreshed."}
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
			return SyncStatus{Message: "Saved; another account sync is running. Call catalog_sync again shortly."}
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

type RowResult struct {
	Catalog catalog.Row `json:"catalog"`
	Status  SyncStatus  `json:"status"`
}

func serverFor(rt Runtime) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "stremio-catalog-mcp", Version: "0.1.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{Name: "catalog_list", Description: "List your Stremio catalog rows, IDs, and titles. Call before editing existing rows."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, catalog.State, error) {
			state, err := rt.Manager.Read(ctx)
			return nil, state, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "catalog_put", Description: "Create, rename, reorder, replace, or append to a Stremio catalog row. Supply movie/series and IMDb IDs or unambiguous titles. IDs stay stable across edits. Use mode=replace and items=[] to clear. Existing playback addons supply streams."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in catalog.UpsertInput) (*mcp.CallToolResult, RowResult, error) {
			row, err := rt.Manager.Upsert(ctx, in)
			if err != nil {
				return nil, RowResult{}, err
			}
			if in.ID != "" && in.Name == "" && in.Position == nil {
				return nil, RowResult{Catalog: row, Status: contentOnlyStatus()}, nil
			}
			return nil, RowResult{Catalog: row, Status: rt.sync(ctx)}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "catalog_remove_titles", Description: "Remove individual titles by IMDb ID from an existing row, preserving other titles and their order."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
			RowID string   `json:"row_id" jsonschema:"Existing row ID"`
			IDs   []string `json:"ids" jsonschema:"IMDb tt... IDs to remove"`
		}) (*mcp.CallToolResult, RowResult, error) {
			row, err := rt.Manager.RemoveItems(ctx, in.RowID, in.IDs)
			if err != nil {
				return nil, RowResult{}, err
			}
			return nil, RowResult{Catalog: row, Status: contentOnlyStatus()}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "catalog_clear", Description: "Remove all titles from one row while keeping its name, ID and position on the Stremio home screen."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
			RowID string `json:"row_id" jsonschema:"Row ID to empty"`
		}) (*mcp.CallToolResult, RowResult, error) {
			row, err := rt.Manager.Upsert(ctx, catalog.UpsertInput{ID: in.RowID, Mode: "replace"})
			if err != nil {
				return nil, RowResult{}, err
			}
			return nil, RowResult{Catalog: row, Status: contentOnlyStatus()}, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "catalog_delete", Description: "Permanently remove a catalog row and its contents from our add-on manifest. Does not touch other addons."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
			RowID string `json:"row_id" jsonschema:"Row ID to delete"`
		}) (*mcp.CallToolResult, SyncStatus, error) {
			if err := rt.Manager.Delete(ctx, in.RowID); err != nil {
				return nil, SyncStatus{}, err
			}
			return nil, rt.sync(ctx), nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "catalog_sync", Description: "Retry synchronizing our catalog manifest to your linked Stremio account. Preserves all installed streaming addons and Cinemeta."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, SyncStatus, error) {
			return nil, rt.sync(ctx), nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "catalog_install_url", Description: "Get your private Stremio add-on manifest URL to install once in Stremio. Treat this URL as a secret."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct {
			URL string `json:"url"`
		}, error) {
			url, err := stremio.ManifestURL(rt.BaseURL, rt.FeedToken)
			return nil, struct {
				URL string `json:"url"`
			}{URL: url}, err
		})
	return s
}
