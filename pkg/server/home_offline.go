package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
)

// Stremio account linking is optional. Before linking, users could already
// reorder custom catalog rows using catalog_put(position); keep that capability
// available through home_layout_update without exposing a duplicate content API.
func offlineLayout(state catalog.State) (stremio.HomeLayout, error) {
	manifest, err := json.Marshal(stremio.BuildManifest(state))
	if err != nil {
		return stremio.HomeLayout{}, err
	}
	addons := []stremio.Descriptor{{Manifest: manifest, TransportURL: "https://unlinked.invalid/manifest.json"}}
	view, err := stremio.BuildHomeLayout(addons, state, stremio.VisibilityState{})
	if err != nil {
		return stremio.HomeLayout{}, err
	}
	view.Note = "Stremio account is not linked. Only custom rows are listed; their order and visibility can be edited, but the TV may need a manual add-on reinstall. Link the account for other installed add-ons."
	return view, nil
}

func offlineChangeOnlyOwnAddon(input stremio.HomeUpdateInput) error {
	if len(input.AddonMoves) != 0 {
		return errors.New("addon_moves requires a linked Stremio account")
	}
	for _, visibility := range input.Visibility {
		if visibility.Row.AddonID != stremio.AddonID {
			return errors.New("third-party catalog visibility requires a linked Stremio account")
		}
	}
	for _, move := range input.RowMoves {
		if move.Row.AddonID != stremio.AddonID || (move.Before != nil && move.Before.AddonID != stremio.AddonID) || (move.After != nil && move.After.AddonID != stremio.AddonID) {
			return errors.New("third-party catalog row moves require a linked Stremio account")
		}
	}
	return nil
}

func (rt Runtime) offlineHomeUpdate(ctx context.Context, input stremio.HomeUpdateInput) (HomeUpdateResult, error) {
	if err := offlineChangeOnlyOwnAddon(input); err != nil {
		return HomeUpdateResult{}, err
	}
	if rt.Manager.Store == nil {
		return HomeUpdateResult{}, errors.New("catalog storage is not configured")
	}
	raw, err := rt.Manager.Store.Load(ctx)
	if err != nil {
		return HomeUpdateResult{}, err
	}
	state, err := catalog.ParseState(raw)
	if err != nil {
		return HomeUpdateResult{}, err
	}
	originalManifest, _ := json.Marshal(stremio.BuildManifest(state))
	addons := []stremio.Descriptor{{Manifest: originalManifest, TransportURL: "https://unlinked.invalid/manifest.json"}}
	plan, err := stremio.PlanHomeLayout(addons, state, stremio.VisibilityState{}, input, "https://unlinked.invalid/manifest.json")
	if err != nil {
		return HomeUpdateResult{}, err
	}
	view, err := offlineLayout(plan.Catalogs)
	if err != nil {
		return HomeUpdateResult{}, err
	}
	result := HomeUpdateResult{DryRun: input.DryRun, Changed: plan.CatalogsChanged, Summary: plan.Summary, Layout: view}
	if input.DryRun {
		result.Message = "Preview only; no catalog changes saved. Stremio account is not linked."
		return result, nil
	}
	if !plan.CatalogsChanged {
		result.Message = "Custom rows already in requested order and visibility. Account is not linked."
		return result, nil
	}
	encoded, err := json.Marshal(plan.Catalogs)
	if err != nil {
		return HomeUpdateResult{}, err
	}
	ok, err := rt.Manager.Store.CAS(ctx, raw, string(encoded))
	if err != nil {
		return HomeUpdateResult{}, err
	}
	if !ok {
		return HomeUpdateResult{}, errors.New("catalogs changed concurrently; reload layout and retry")
	}
	result.Message = "Custom Home layout saved in Redis. Link Stremio or reinstall the catalog add-on to update the Home screen."
	return result, nil
}
