package server

import (
	"context"
	"fmt"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
)

type CatalogsGetInput struct {
	RowID             string `json:"row_id,omitempty" jsonschema:"Optional catalog row ID; returns this row with its ordered titles and poster URLs"`
	IncludeTitles     bool   `json:"include_titles,omitempty" jsonschema:"Include all titles and poster URLs when listing multiple rows; default is a compact summary"`
	IncludeInstallURL bool   `json:"include_install_url,omitempty" jsonschema:"Include the private Stremio addon install URL, only when needed for setup or reinstall"`
}

type CatalogSummary struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Position   int             `json:"position"`
	Visible    bool            `json:"visible"`
	TitleCount int             `json:"title_count"`
	Titles     *[]catalog.Item `json:"titles,omitempty"`
}

type CatalogsGetResult struct {
	Revision   int64            `json:"revision"`
	Rows       []CatalogSummary `json:"rows"`
	InstallURL string           `json:"install_url,omitempty"`
	Warning    string           `json:"warning,omitempty"`
}

func (rt Runtime) catalogsGet(ctx context.Context, input CatalogsGetInput) (CatalogsGetResult, error) {
	result := CatalogsGetResult{Rows: []CatalogSummary{}}
	var err error
	if input.IncludeInstallURL {
		result.InstallURL, err = stremio.ManifestURL(rt.BaseURL, rt.FeedToken)
		if err != nil {
			return CatalogsGetResult{}, err
		}
	}
	state, err := rt.Manager.Read(ctx)
	if err != nil {
		// The former install_url tool was independent of Redis. Preserve that
		// recovery path for installations whose storage is not yet configured.
		if input.IncludeInstallURL && input.RowID == "" && !input.IncludeTitles {
			result.Warning = "Catalog storage unavailable; the private installation URL is still valid. Rows were not fetched."
			return result, nil
		}
		return CatalogsGetResult{}, err
	}
	result.Revision = state.Revision
	matched := input.RowID == ""
	for i, row := range state.Rows {
		if input.RowID != "" && input.RowID != row.ID {
			continue
		}
		matched = true
		entry := CatalogSummary{ID: row.ID, Name: row.Name, Type: row.Type, Position: i, Visible: !row.Hidden, TitleCount: len(row.Items)}
		if input.IncludeTitles || input.RowID != "" {
			items := append([]catalog.Item{}, row.Items...)
			entry.Titles = &items // full saved title metadata, including posters
		}
		result.Rows = append(result.Rows, entry)
	}
	if !matched {
		return CatalogsGetResult{}, fmt.Errorf("catalog %q was not found; omit row_id to list current IDs", input.RowID)
	}
	return result, nil
}

type CatalogSyncResult struct {
	Status  string `json:"status"` // preview, not_needed, succeeded, or failed
	Message string `json:"message"`
}

type CatalogsUpdateResult struct {
	DryRun          bool                   `json:"dry_run"`
	Saved           bool                   `json:"saved"`
	Changed         bool                   `json:"changed"`
	Replayed        bool                   `json:"replayed"`
	Revision        int64                  `json:"revision"`
	AppliedRevision int64                  `json:"applied_revision"`
	Changes         []catalog.ChangeResult `json:"changes"`
	Sync            CatalogSyncResult      `json:"sync"`
}

func (rt Runtime) catalogsUpdate(ctx context.Context, input catalog.BatchInput) (CatalogsUpdateResult, error) {
	batch, err := rt.Manager.ApplyBatch(ctx, input)
	if err != nil {
		return CatalogsUpdateResult{}, err
	}
	result := CatalogsUpdateResult{
		DryRun: input.DryRun, Saved: !input.DryRun, Changed: batch.Changed,
		Replayed: batch.Replayed, Revision: batch.Revision,
		AppliedRevision: batch.AppliedRevision, Changes: batch.Changes,
	}
	if input.DryRun {
		result.Sync = CatalogSyncResult{Status: "preview", Message: "Preview only; no catalog or Stremio account changes were made. Newly generated IDs are provisional."}
		return result, nil
	}
	if !batch.SyncRequired {
		result.Sync = CatalogSyncResult{Status: "not_needed", Message: "Catalog titles saved in Redis; Stremio reads the updated titles when refreshed."}
		return result, nil
	}
	status := rt.sync(ctx) // exactly one account sync for the entire batch
	if status.Synced {
		result.Sync = CatalogSyncResult{Status: "succeeded", Message: status.Message}
	} else {
		result.Sync = CatalogSyncResult{Status: "failed", Message: status.Message + " Changes already saved, so retry with the same request_id or use a sync action."}
	}
	return result, nil
}
