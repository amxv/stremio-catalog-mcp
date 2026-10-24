package server

import (
	"context"
	"strings"
	"testing"

	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
)

func TestUnlinkedAccountKeepsCustomReorderAndVisibility(t *testing.T) {
	rt, account, _, _, _, done := testHomeRuntime(t)
	defer done()
	rt.Account.AuthKey = ""
	ctx := context.Background()
	initial, err := rt.homeLayoutGet(ctx)
	if err != nil || len(initial.Addons) != 1 || len(initial.Addons[0].Rows) != 2 {
		t.Fatalf("offline layout listing failed: %+v (%v)", initial, err)
	}
	first := stremio.RowSelector{AddonID: stremio.AddonID, CatalogID: "row_alpha", Type: "movie"}
	second := stremio.RowSelector{AddonID: stremio.AddonID, CatalogID: "row_beta", Type: "series"}
	change := stremio.HomeUpdateInput{RowMoves: []stremio.RowMove{{Row: second, Before: &first}}, Visibility: []stremio.VisibilityChange{{Row: first, Visible: false}}}
	change.DryRun = true
	preview, err := rt.homeLayoutUpdate(ctx, change)
	if err != nil || !preview.Changed || preview.Synced || preview.Layout.Addons[0].Rows[0].ID != "row_beta" {
		t.Fatalf("bad offline preview: %+v (%v)", preview, err)
	}
	before, err := rt.Manager.Read(ctx)
	if err != nil || before.Revision != 2 || before.Rows[0].ID != "row_alpha" {
		t.Fatal("preview changed catalog state")
	}
	change.DryRun = false
	saved, err := rt.homeLayoutUpdate(ctx, change)
	if err != nil || !saved.Changed || saved.Synced || account.setCalls != 0 {
		t.Fatalf("offline edit wrote account or failed: %+v (%v)", saved, err)
	}
	after, err := rt.Manager.Read(ctx)
	if err != nil || after.Revision != 3 || after.Rows[0].ID != "row_beta" || !after.Rows[1].Hidden {
		t.Fatalf("offline catalog state wrong: %+v (%v)", after, err)
	}
	other := stremio.RowSelector{AddonID: "com.linvo.cinemeta", CatalogID: "top", Type: "movie"}
	if _, err := rt.homeLayoutUpdate(ctx, stremio.HomeUpdateInput{Visibility: []stremio.VisibilityChange{{Row: other, Visible: false}}}); err == nil || !strings.Contains(err.Error(), "linked") {
		t.Fatalf("third-party modification should be rejected when account unlinked: %v", err)
	}
}
