package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
)

type mockPosterResolver struct{}

func (mockPosterResolver) Resolve(_ context.Context, mediaType string, in catalog.ItemInput) (catalog.Item, error) {
	if !catalog.ValidIMDbID(in.ID) || strings.TrimSpace(in.Title) == "" {
		return catalog.Item{}, errors.New("test title needs IMDb ID and name")
	}
	return catalog.Item{ID: in.ID, Type: mediaType, Title: in.Title, Poster: "https://images.example/" + in.ID + ".jpg", Year: in.Year}, nil
}

func TestCatalogsGetCompactAndFullPosterViews(t *testing.T) {
	rt, _, _, _, _, done := testHomeRuntime(t)
	defer done()
	rt.Manager.Resolver = mockPosterResolver{}
	_, err := rt.Manager.Upsert(context.Background(), catalog.UpsertInput{ID: "row_alpha", Items: []catalog.ItemInput{{ID: "tt0000042", Title: "Answer", Year: 2005}}})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := rt.catalogsGet(context.Background(), CatalogsGetInput{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.InstallURL != "" || len(summary.Rows) != 2 || summary.Rows[0].TitleCount != 1 || summary.Rows[0].Titles != nil || summary.Rows[0].Position != 0 {
		t.Fatalf("default should be compact and not leak install URL: %+v", summary)
	}
	row, err := rt.catalogsGet(context.Background(), CatalogsGetInput{RowID: "row_alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Rows) != 1 || row.Rows[0].Titles == nil || len(*row.Rows[0].Titles) != 1 || (*row.Rows[0].Titles)[0].Poster != "https://images.example/tt0000042.jpg" {
		t.Fatalf("full row missing poster: %+v", row)
	}
	all, err := rt.catalogsGet(context.Background(), CatalogsGetInput{IncludeTitles: true, IncludeInstallURL: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all.InstallURL, "/stremio/fake-feed-token/manifest.json") || len(all.Rows) != 2 || all.Rows[1].Titles == nil || len(*all.Rows[1].Titles) != 0 {
		t.Fatalf("full list or explicit install URL missing: %+v", all)
	}
	if _, err = rt.catalogsGet(context.Background(), CatalogsGetInput{RowID: "row_unknown"}); err == nil {
		t.Fatal("unknown row should not be an empty successful response")
	}
}

func TestInstallURLStillWorksWithoutRedis(t *testing.T) {
	rt := Runtime{BaseURL: "https://example.com", FeedToken: "private-test-token"}
	result, err := rt.catalogsGet(context.Background(), CatalogsGetInput{IncludeInstallURL: true})
	if err != nil || result.InstallURL != "https://example.com/stremio/private-test-token/manifest.json" || result.Warning == "" || len(result.Rows) != 0 {
		t.Fatalf("installation recovery without storage broken: %+v (%v)", result, err)
	}
	if _, err := rt.catalogsGet(context.Background(), CatalogsGetInput{}); err == nil {
		t.Fatal("ordinary catalog listing must not treat storage outage as empty state")
	}
}

func TestCatalogsUpdateBatchSyncOnlyOnceAndReplay(t *testing.T) {
	rt, account, _, _, backups, done := testHomeRuntime(t)
	defer done()
	rt.Manager.Resolver = mockPosterResolver{}
	revision := int64(2)
	input := catalog.BatchInput{RequestID: "full-batch-001", ExpectedRevision: &revision, Changes: []catalog.Change{
		{Action: "create", Name: "New Movies", Type: "movie", Titles: []catalog.ItemInput{{ID: "tt0000022", Title: "A Movie"}}},
		{Action: "edit", RowID: "row_alpha", Name: "Favorites", AddTitles: []catalog.ItemInput{{ID: "tt0000023", Title: "Another Movie"}}},
		{Action: "delete", RowID: "row_beta"},
	}}
	previewInput := input
	previewInput.DryRun = true
	preview, err := rt.catalogsUpdate(context.Background(), previewInput)
	if err != nil || !preview.DryRun || preview.Saved || preview.Sync.Status != "preview" || preview.Changes[0].Catalog == nil || account.setCalls != 0 || *backups != 0 {
		t.Fatalf("unexpected preview result: %+v (%v)", preview, err)
	}
	committed, err := rt.catalogsUpdate(context.Background(), input)
	if err != nil || !committed.Saved || !committed.Changed || committed.Sync.Status != "succeeded" {
		t.Fatalf("catalog batch failed: %+v (%v)", committed, err)
	}
	if account.setCalls != 1 || *backups != 1 || committed.Revision != 3 || len(committed.Changes) != 3 || committed.Changes[0].Catalog == nil || committed.Changes[0].Catalog.Items[0].Poster == "" {
		t.Fatalf("unexpected batch write or poster: %+v set=%d backups=%d", committed, account.setCalls, *backups)
	}
	originalID := committed.Changes[0].RowID
	again, err := rt.catalogsUpdate(context.Background(), input)
	if err != nil || !again.Replayed || again.Changed || again.Changes[0].RowID != originalID || again.Revision != 3 || again.Sync.Status != "succeeded" {
		t.Fatalf("retry duplicate or failed: %+v (%v)", again, err)
	}
	stored, _ := rt.Manager.Read(context.Background())
	if len(stored.Rows) != 2 || stored.Rows[1].ID != originalID {
		t.Fatalf("retry duplicated row: %+v", stored)
	}
	resync, err := rt.catalogsUpdate(context.Background(), catalog.BatchInput{Changes: []catalog.Change{{Action: "sync"}}})
	if err != nil || resync.Changed || resync.Sync.Status != "succeeded" {
		t.Fatalf("explicit sync not supported: %+v (%v)", resync, err)
	}
}

func TestCatalogsUpdateContentOnlyAndFailedSyncRecoverable(t *testing.T) {
	rt, account, _, _, _, done := testHomeRuntime(t)
	defer done()
	rt.Manager.Resolver = mockPosterResolver{}
	update := catalog.BatchInput{Changes: []catalog.Change{{Action: "edit", RowID: "row_alpha", AddTitles: []catalog.ItemInput{{ID: "tt0000088", Title: "Test"}}}}}
	first, err := rt.catalogsUpdate(context.Background(), update)
	if err != nil || first.Sync.Status != "not_needed" || !first.Saved || account.setCalls != 0 {
		t.Fatalf("content only edit should not touch Stremio account: %+v (%v)", first, err)
	}
	create := catalog.BatchInput{RequestID: "recoverable-create-001", Changes: []catalog.Change{{Action: "create", Name: "Pending", Type: "series"}}}
	auth := rt.Account.AuthKey
	rt.Account.AuthKey = ""
	pending, err := rt.catalogsUpdate(context.Background(), create)
	if err != nil || !pending.Saved || pending.Sync.Status != "failed" || account.setCalls != 0 {
		t.Fatalf("sync outage must preserve saved catalog: %+v (%v)", pending, err)
	}
	rt.Account.AuthKey = auth
	retried, err := rt.catalogsUpdate(context.Background(), create)
	if err != nil || !retried.Replayed || retried.Sync.Status != "succeeded" || retried.Changes[0].RowID != pending.Changes[0].RowID {
		t.Fatalf("retry after sync failure did not recover: %+v (%v)", retried, err)
	}
}

func TestCatalogsUpdateReturnsPostersInStructuredJSON(t *testing.T) {
	rt, _, _, _, _, done := testHomeRuntime(t)
	defer done()
	rt.Manager.Resolver = mockPosterResolver{}
	result, err := rt.catalogsUpdate(context.Background(), catalog.BatchInput{DryRun: true, Changes: []catalog.Change{{Action: "create", Name: "Poster Demo", Type: "movie", Titles: []catalog.ItemInput{{ID: "tt0000055", Title: "Poster"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), `"poster":"https://images.example/tt0000055.jpg"`) {
		t.Fatalf("poster URL missing from update response: %s", encoded)
	}
}
