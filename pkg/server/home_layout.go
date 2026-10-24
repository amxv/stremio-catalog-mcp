package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
)

type HomeUpdateResult struct {
	DryRun  bool               `json:"dry_run"`
	Changed bool               `json:"changed"`
	Synced  bool               `json:"synced"`
	Summary []string           `json:"summary"`
	Message string             `json:"message"`
	Layout  stremio.HomeLayout `json:"layout"`
}

func (rt Runtime) readLayoutStores(ctx context.Context) (string, catalog.State, string, stremio.VisibilityState, error) {
	if rt.Manager.Store == nil || rt.VisibilityStore == nil {
		return "", catalog.State{}, "", stremio.VisibilityState{}, errors.New("catalog and layout storage must both be configured")
	}
	originalCatalogRaw, err := rt.Manager.Store.Load(ctx)
	if err != nil {
		return "", catalog.State{}, "", stremio.VisibilityState{}, err
	}
	state, err := catalog.ParseState(originalCatalogRaw)
	if err != nil {
		return "", catalog.State{}, "", stremio.VisibilityState{}, err
	}
	originalVisibilityRaw, err := rt.VisibilityStore.Load(ctx)
	if err != nil {
		return "", catalog.State{}, "", stremio.VisibilityState{}, err
	}
	visibility, err := stremio.DecodeVisibilityState(originalVisibilityRaw)
	return originalCatalogRaw, state, originalVisibilityRaw, visibility, err
}

func (rt Runtime) homeLayoutGet(ctx context.Context) (stremio.HomeLayout, error) {
	if rt.Account.AuthKey == "" {
		// Preserve offline custom-row positioning even for installations that
		// haven't linked a Stremio account yet.
		state, err := rt.Manager.Read(ctx)
		if err != nil {
			return stremio.HomeLayout{}, err
		}
		return offlineLayout(state)
	}
	addons, err := rt.Account.Get(ctx)
	if err != nil {
		return stremio.HomeLayout{}, err
	}
	_, state, _, visibility, err := rt.readLayoutStores(ctx)
	if err != nil {
		return stremio.HomeLayout{}, err
	}
	return stremio.BuildHomeLayout(addons, state, visibility)
}

func (rt Runtime) homeLayoutUpdate(ctx context.Context, input stremio.HomeUpdateInput) (HomeUpdateResult, error) {
	if rt.Account.AuthKey == "" {
		return rt.offlineHomeUpdate(ctx, input)
	}
	if rt.Store == nil {
		return HomeUpdateResult{}, errors.New("backup and synchronization lock storage is not configured")
	}
	if !input.DryRun {
		lockID, err := catalog.NewID()
		if err != nil {
			return HomeUpdateResult{}, err
		}
		locked, err := rt.Store.AcquireSyncLock(ctx, lockID)
		if err != nil {
			return HomeUpdateResult{}, fmt.Errorf("unable to lock Stremio account layout: %w", err)
		}
		if !locked {
			return HomeUpdateResult{}, errors.New("another Stremio account sync is in progress; retry shortly")
		}
		defer func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = rt.Store.ReleaseSyncLock(unlockCtx, lockID)
		}()
	}

	originalAddons, err := rt.Account.Get(ctx)
	if err != nil {
		return HomeUpdateResult{}, err
	}
	catalogRaw, originalCatalog, visibilityRaw, originalVisibility, err := rt.readLayoutStores(ctx)
	if err != nil {
		return HomeUpdateResult{}, err
	}
	manifestURL := ""
	if rt.BaseURL != "" && rt.FeedToken != "" {
		manifestURL, err = stremio.ManifestURL(rt.BaseURL, rt.FeedToken, originalCatalog.Revision+1)
		if err != nil {
			return HomeUpdateResult{}, err
		}
	}
	plan, err := stremio.PlanHomeLayout(originalAddons, originalCatalog, originalVisibility, input, manifestURL)
	if err != nil {
		return HomeUpdateResult{}, err
	}
	view, err := stremio.BuildHomeLayout(plan.Addons, plan.Catalogs, plan.Visibility)
	if err != nil {
		return HomeUpdateResult{}, err
	}
	visibilityChanged := !reflect.DeepEqual(plan.Visibility, originalVisibility)
	changed := plan.AccountChanged || plan.CatalogsChanged || visibilityChanged
	result := HomeUpdateResult{DryRun: input.DryRun, Changed: changed, Summary: plan.Summary, Layout: view}
	if input.DryRun {
		result.Message = "Preview only; Stremio and Redis were not modified."
		return result, nil
	}
	if !changed {
		result.Message, result.Synced = "Already in the requested layout; nothing changed.", true
		return result, nil
	}
	if !plan.AccountChanged {
		return HomeUpdateResult{}, errors.New("refusing a storage-only layout mutation without a corresponding Stremio update")
	}
	latest, err := rt.Account.Get(ctx)
	if err != nil {
		return HomeUpdateResult{}, err
	}
	if !reflect.DeepEqual(originalAddons, latest) {
		return HomeUpdateResult{}, errors.New("Stremio layout changed during planning; retry rather than overwriting another client's edits")
	}
	if rt.Account.BeforeReplace == nil {
		return HomeUpdateResult{}, errors.New("account backup is not configured; refusing to update Stremio")
	}
	if err := rt.Account.BeforeReplace(ctx, latest); err != nil {
		return HomeUpdateResult{}, fmt.Errorf("account backup failed, no changes saved: %w", err)
	}
	newVisibilityRaw := visibilityRaw
	if visibilityChanged {
		encoded, err := json.Marshal(plan.Visibility)
		if err != nil {
			return HomeUpdateResult{}, err
		}
		newVisibilityRaw = string(encoded)
		ok, err := rt.VisibilityStore.CAS(ctx, visibilityRaw, newVisibilityRaw)
		if err != nil {
			return HomeUpdateResult{}, err
		}
		if !ok {
			return HomeUpdateResult{}, errors.New("visibility settings changed concurrently; retry")
		}
	}
	newCatalogRaw := catalogRaw
	if plan.CatalogsChanged {
		encoded, err := json.Marshal(plan.Catalogs)
		if err != nil {
			return HomeUpdateResult{}, err
		}
		newCatalogRaw = string(encoded)
		ok, err := rt.Manager.Store.CAS(ctx, catalogRaw, newCatalogRaw)
		if err != nil || !ok {
			if visibilityChanged {
				rollbackOK, rollbackErr := rt.VisibilityStore.CAS(ctx, newVisibilityRaw, visibilityRaw)
				if rollbackErr != nil || !rollbackOK {
					return HomeUpdateResult{}, errors.New("catalog edit failed and visibility rollback could not be confirmed; inspect account before retrying")
				}
			}
			if err != nil {
				return HomeUpdateResult{}, err
			}
			return HomeUpdateResult{}, errors.New("custom catalogs changed concurrently; retry")
		}
	}
	rollbackStores := func() error {
		if plan.CatalogsChanged {
			ok, err := rt.Manager.Store.CAS(ctx, newCatalogRaw, catalogRaw)
			if err != nil || !ok {
				return errors.New("custom catalog state could not be rolled back")
			}
		}
		if visibilityChanged {
			ok, err := rt.VisibilityStore.CAS(ctx, newVisibilityRaw, visibilityRaw)
			if err != nil || !ok {
				return errors.New("saved visibility state could not be rolled back")
			}
		}
		return nil
	}

	setErr := rt.Account.Set(ctx, plan.Addons)
	verified, verifyErr := rt.Account.Get(ctx)
	if verifyErr == nil && reflect.DeepEqual(verified, plan.Addons) {
		result.Synced = true
		result.Message = "Layout saved and verified in Stremio. Restart or refresh Stremio devices if they still show the old cached layout."
		return result, nil
	}
	if verifyErr == nil && reflect.DeepEqual(verified, originalAddons) {
		if err := rollbackStores(); err != nil {
			return HomeUpdateResult{}, fmt.Errorf("Stremio did not update and %w; review the saved backup", err)
		}
		if setErr != nil {
			return HomeUpdateResult{}, fmt.Errorf("Stremio rejected the update; local changes rolled back: %w", setErr)
		}
		return HomeUpdateResult{}, errors.New("Stremio did not retain the update; local changes rolled back")
	}
	// An API timeout does not prove the write failed, and another client may
	// have edited the account. Keep our desired local state until reconciled.
	if verifyErr != nil {
		return HomeUpdateResult{}, fmt.Errorf("Stremio update may have applied, but verification failed: %w; account backup retained", verifyErr)
	}
	return HomeUpdateResult{}, errors.New("Stremio returned an unexpected account configuration after the write; account backup retained; no automatic rollback to avoid overwriting concurrent edits")
}
