package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
	"github.com/amxv/stremio-catalog-mcp/pkg/stremio"
)

type fakeHomeAccount struct {
	mu       sync.Mutex
	addons   []stremio.Descriptor
	setCalls int
	getCalls int
}

func (a *fakeHomeAccount) handler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type    string               `json:"type"`
		AuthKey string               `json:"authKey"`
		Addons  []stremio.Descriptor `json:"addons"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AuthKey != "unit-test-stremio-key" {
		http.Error(w, "bad auth", http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch req.Type {
	case "AddonCollectionGet":
		a.getCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"addons": a.addons}})
	case "AddonCollectionSet":
		a.setCalls++
		a.addons = req.Addons
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"success": true}})
	default:
		http.Error(w, "bad method", http.StatusBadRequest)
	}
}

func testHomeRuntime(t *testing.T) (Runtime, *fakeHomeAccount, *catalog.MemoryStore, *catalog.MemoryStore, *int, func()) {
	t.Helper()
	state := catalog.State{Revision: 2, Rows: []catalog.Row{
		{ID: "row_alpha", Type: "movie", Name: "First", Items: []catalog.Item{}},
		{ID: "row_beta", Type: "series", Name: "Second", Items: []catalog.Item{}},
	}}
	account := &fakeHomeAccount{addons: []stremio.Descriptor{
		{Manifest: json.RawMessage(`{"id":"com.linvo.cinemeta","name":"Cinemeta","resources":["catalog"],"catalogs":[{"id":"top","type":"movie","name":"Popular Movies","extra":[{"name":"search","isRequired":false}]}]}`), TransportURL: "https://cinemeta.example/manifest.json"},
		{Manifest: json.RawMessage(`{"id":"test.stream","name":"Playback","resources":["stream"]}`), TransportURL: "https://stream.example/secret/manifest.json"},
		{Manifest: json.RawMessage(`{"id":"xyz.ashray.stremio-catalog-mcp","name":"My Catalogs","resources":["catalog"],"catalogs":[{"id":"row_alpha","type":"movie","name":"First"},{"id":"row_beta","type":"series","name":"Second"}]}`), TransportURL: "https://example.com/my/manifest.json"},
	}}
	accountServer := httptest.NewServer(http.HandlerFunc(account.handler))
	lockServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var values []json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&values); err != nil || len(values) == 0 {
			http.Error(w, "invalid command", 400)
			return
		}
		var command string
		_ = json.Unmarshal(values[0], &command)
		switch command {
		case "SET":
			_, _ = w.Write([]byte(`{"result":"OK"}`))
		case "EVAL":
			_, _ = w.Write([]byte(`{"result":1}`))
		default:
			http.Error(w, "unexpected Redis command", 400)
		}
	}))
	managerStore, visibilityStore := &catalog.MemoryStore{}, &catalog.MemoryStore{}
	encoded, _ := json.Marshal(state)
	if _, err := managerStore.CAS(context.Background(), "", string(encoded)); err != nil {
		t.Fatal(err)
	}
	backupCount := new(int)
	rt := Runtime{
		Manager: catalog.Manager{Store: managerStore},
		Account: stremio.AccountAPI{BaseURL: accountServer.URL, AuthKey: "unit-test-stremio-key", BeforeReplace: func(_ context.Context, _ []stremio.Descriptor) error {
			*backupCount++
			return nil
		}},
		Store:           &catalog.RedisStore{URL: lockServer.URL, Token: "test-redis", Key: "test-layout", Client: lockServer.Client()},
		VisibilityStore: visibilityStore,
		BaseURL:         "https://mycatalog.example", FeedToken: "fake-feed-token",
	}
	return rt, account, managerStore, visibilityStore, backupCount, func() { accountServer.Close(); lockServer.Close() }
}

func TestHomeLayoutDryRunAndCommit(t *testing.T) {
	rt, account, managerStore, visibilityStore, backups, done := testHomeRuntime(t)
	defer done()
	row := stremio.RowSelector{AddonID: "com.linvo.cinemeta", CatalogID: "top", Type: "movie"}
	other := stremio.RowSelector{AddonID: stremio.AddonID, CatalogID: "row_beta", Type: "series"}
	first := stremio.RowSelector{AddonID: stremio.AddonID, CatalogID: "row_alpha", Type: "movie"}
	input := stremio.HomeUpdateInput{
		Visibility: []stremio.VisibilityChange{{Row: row, Visible: false}, {Row: first, Visible: false}},
		RowMoves:   []stremio.RowMove{{Row: other, Before: &first}},
		AddonMoves: []stremio.AddonMove{{AddonID: stremio.AddonID, Before: "com.linvo.cinemeta"}},
		DryRun:     true,
	}
	preview, err := rt.homeLayoutUpdate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.DryRun || !preview.Changed || preview.Synced || account.setCalls != 0 || *backups != 0 {
		t.Fatalf("preview wrote account or reported sync: %+v", preview)
	}
	initial, _ := rt.Manager.Read(context.Background())
	if initial.Revision != 2 || initial.Rows[0].Hidden {
		t.Fatal("dry-run mutated Redis catalog state")
	}
	if raw, _ := visibilityStore.Load(context.Background()); raw != "" {
		t.Fatal("dry-run mutated visibility state")
	}
	input.DryRun = false
	result, err := rt.homeLayoutUpdate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Synced || result.DryRun || *backups != 1 || account.setCalls != 1 {
		t.Fatalf("update failed to save and verify: %+v", result)
	}
	view, err := rt.homeLayoutGet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Addons[0].ID != stremio.AddonID || view.Addons[0].Rows[0].ID != "row_beta" || view.Addons[0].Rows[1].Visible || view.Addons[1].Rows[0].Visible || !view.Addons[1].Rows[0].CanShow {
		t.Fatalf("wrong saved layout: %+v", view)
	}
	output, _ := json.Marshal(result)
	if strings.Contains(string(output), "stream.example") || strings.Contains(string(output), "transportUrl") {
		t.Fatal("private streaming provider URL leaked from layout tool")
	}
	changed, err := rt.Manager.Read(context.Background())
	if err != nil || changed.Revision != 3 || changed.Rows[0].ID != "row_beta" {
		t.Fatal("custom row order not durable")
	}
	if raw, _ := managerStore.Load(context.Background()); raw == "" {
		t.Fatal("catalog state not persisted")
	}
	if raw, _ := visibilityStore.Load(context.Background()); !strings.Contains(raw, "original_extra") {
		t.Fatal("restoration metadata was not saved")
	}
	restore, err := rt.homeLayoutUpdate(context.Background(), stremio.HomeUpdateInput{Visibility: []stremio.VisibilityChange{{Row: row, Visible: true}, {Row: first, Visible: true}}})
	if err != nil || !restore.Synced {
		t.Fatalf("restore failed: %+v (%v)", restore, err)
	}
	restored, _ := rt.homeLayoutGet(context.Background())
	if !restored.Addons[0].Rows[1].Visible || !restored.Addons[1].Rows[0].Visible {
		t.Fatal("restored rows not visible")
	}
}

func TestHomeLayoutBackupFailureMakesNoChanges(t *testing.T) {
	rt, account, manager, overrides, _, done := testHomeRuntime(t)
	defer done()
	rt.Account.BeforeReplace = func(context.Context, []stremio.Descriptor) error { return errors.New("simulated backup outage") }
	beforeCatalog, _ := manager.Load(context.Background())
	beforeOverride, _ := overrides.Load(context.Background())
	row := stremio.RowSelector{AddonID: "com.linvo.cinemeta", CatalogID: "top", Type: "movie"}
	_, err := rt.homeLayoutUpdate(context.Background(), stremio.HomeUpdateInput{Visibility: []stremio.VisibilityChange{{Row: row, Visible: false}}})
	if err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("missing required backup abort: %v", err)
	}
	afterCatalog, _ := manager.Load(context.Background())
	afterOverride, _ := overrides.Load(context.Background())
	if account.setCalls != 0 || !reflect.DeepEqual(beforeCatalog, afterCatalog) || !reflect.DeepEqual(beforeOverride, afterOverride) {
		t.Fatal("backup failure modified account or Redis")
	}
}
