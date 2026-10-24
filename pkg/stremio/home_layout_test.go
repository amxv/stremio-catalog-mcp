package stremio

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
)

func testDescriptor(manifest string, url string) Descriptor {
	return Descriptor{Manifest: json.RawMessage(manifest), TransportURL: url, Flags: json.RawMessage(`{"protected":true,"custom":12}`)}
}

func layoutFixtures(t *testing.T) ([]Descriptor, catalog.State) {
	t.Helper()
	current := []Descriptor{
		testDescriptor(`{"id":"com.linvo.cinemeta","name":"Cinemeta","resources":["catalog","meta"],"unknown":{"keep":10},"catalogs":[{"id":"top","type":"movie","name":"Popular Movies","extra":[{"name":"search","isRequired":false,"optionsLimit":2}]},{"id":"top","type":"series","name":"Popular Series","extra":[{"name":"search"}]},{"id":"imdbRating","type":"movie","name":"Featured Movies","extra":[{"name":"genre","options":["Action","Drama"]}]},{"id":"imdbRating","type":"series","name":"Featured Series","extra":[{"name":"genre"}]}]}`, "https://cinemeta.example/manifest.json"),
		testDescriptor(`{"id":"test.streaming","name":"Streaming","resources":["stream"],"privateSettings":{"something":true}}`, "https://private-streaming.example/secret/manifest.json"),
		testDescriptor(`{"id":"test.other","name":"Other Catalogs","resources":["catalog"],"catalogs":[{"id":"recent","type":"series","name":"Recent","extra":[{"name":"genre"}],"custom":"keep"},{"id":"search","type":"series","name":"Search-only","extra":[{"name":"search","isRequired":true}],"extraRequired":["search"]}]}`, "https://other.example/private/manifest.json"),
		testDescriptor(`{"id":"xyz.ashray.stremio-catalog-mcp","name":"My Catalogs","resources":["catalog"],"catalogs":[{"type":"movie","id":"row_aaa","name":"First"},{"type":"series","id":"row_bbb","name":"Second"}]}`, "https://mine.example/old/manifest.json"),
	}
	state := catalog.State{Revision: 7, Rows: []catalog.Row{
		{ID: "row_aaa", Name: "First", Type: "movie", Items: []catalog.Item{}},
		{ID: "row_bbb", Name: "Second", Type: "series", Items: []catalog.Item{}},
	}}
	return current, state
}

func TestHomeLayoutMixedBatchPreservesOtherAddonsAndIsReversible(t *testing.T) {
	original, state := layoutFixtures(t)
	backup, _ := json.Marshal(original)
	customA := RowSelector{AddonID: AddonID, Type: "movie", CatalogID: "row_aaa"}
	customB := RowSelector{AddonID: AddonID, Type: "series", CatalogID: "row_bbb"}
	popularMovie := RowSelector{AddonID: "com.linvo.cinemeta", Type: "movie", CatalogID: "top"}
	featuredMovie := RowSelector{AddonID: "com.linvo.cinemeta", Type: "movie", CatalogID: "imdbRating"}
	otherRow := RowSelector{AddonID: "test.other", Type: "series", CatalogID: "recent"}
	in := HomeUpdateInput{
		Visibility: []VisibilityChange{{Row: popularMovie, Visible: false}, {Row: otherRow, Visible: false}, {Row: customA, Visible: false}},
		RowMoves:   []RowMove{{Row: featuredMovie, Before: &popularMovie}, {Row: customB, Before: &customA}},
		AddonMoves: []AddonMove{{AddonID: AddonID, Before: "com.linvo.cinemeta"}},
	}
	plan, err := PlanHomeLayout(original, state, VisibilityState{}, in, "https://mine.example/v8/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.AccountChanged || !plan.CatalogsChanged || !plan.VisibilityChanged || plan.Catalogs.Revision != 8 {
		t.Fatalf("incomplete plan: %+v", plan)
	}
	if len(plan.Visibility.Records) != 2 || plan.Catalogs.Rows[0].ID != "row_bbb" || !plan.Catalogs.Rows[1].Hidden {
		t.Fatalf("lost custom order / hidden state: %+v", plan.Catalogs)
	}
	if descriptorID(plan.Addons[0]) != AddonID || descriptorID(plan.Addons[2]) != "test.streaming" {
		t.Fatalf("addon order lost: %+v", plan.Addons)
	}
	if plan.Addons[2].TransportURL != original[1].TransportURL || !reflect.DeepEqual(plan.Addons[2].Flags, original[1].Flags) || !reflect.DeepEqual(plan.Addons[2].Manifest, original[1].Manifest) {
		t.Fatal("unrelated streaming addon fields were modified")
	}
	if marshaled, _ := json.Marshal(original); string(marshaled) != string(backup) {
		t.Fatal("planner mutated its input descriptor")
	}
	view, err := BuildHomeLayout(plan.Addons, plan.Catalogs, plan.Visibility)
	if err != nil {
		t.Fatal(err)
	}
	if view.Addons[0].Rows[0].ID != "row_bbb" || view.Addons[0].Rows[1].Visible || view.Addons[1].Rows[0].ID != "imdbRating" || view.Addons[1].Rows[1].Visible {
		t.Fatalf("wrong preview layout: %+v", view)
	}
	out, _ := json.Marshal(view)
	if strings.Contains(string(out), "private/") || strings.Contains(string(out), "secret/") || strings.Contains(string(out), "transportUrl") {
		t.Fatal("private addon URLs exposed through get/preview")
	}

	// Restoring a row must restore the exact original filter configuration,
	// without rewriting its other catalog metadata or installed addon fields.
	restored, err := PlanHomeLayout(plan.Addons, plan.Catalogs, plan.Visibility, HomeUpdateInput{Visibility: []VisibilityChange{
		{Row: popularMovie, Visible: true}, {Row: otherRow, Visible: true}, {Row: customA, Visible: true},
	}}, "https://mine.example/v9/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Visibility.Records) != 0 || restored.Catalogs.Rows[1].Hidden {
		t.Fatal("restoration left hidden flags")
	}
	idx, _ := uniqueAddonIndex(restored.Addons, "com.linvo.cinemeta")
	_, restoredRows, _ := manifestFields(restored.Addons[idx])
	_, popular, _ := manifestFields(original[0])
	fields, _, _, _ := catalogFields(restoredRows[1]) // after the preceding catalog move
	originalFields, _, _, _ := catalogFields(popular[0])
	if !rawSame(fields["extra"], originalFields["extra"]) || !rawSame(fields["extraRequired"], originalFields["extraRequired"]) {
		t.Fatalf("did not restore original catalog filters: %s vs %s", fields["extra"], originalFields["extra"])
	}
	if !strings.Contains(string(restoredRows[1]), `"optionsLimit":2`) {
		t.Fatal("unrelated catalog fields lost")
	}
}

func TestHomeLayoutProviderSearchOnlyCannotBeUnhidden(t *testing.T) {
	addons, state := layoutFixtures(t)
	ref := RowSelector{AddonID: "test.other", Type: "series", CatalogID: "search"}
	view, err := BuildHomeLayout(addons, state, VisibilityState{})
	if err != nil {
		t.Fatal(err)
	}
	row := view.Addons[2].Rows[1]
	if row.Visible || row.CanShow || row.Visibility != "provider" {
		t.Fatalf("search-only catalog incorrectly offered for Home: %+v", row)
	}
	_, err = PlanHomeLayout(addons, state, VisibilityState{}, HomeUpdateInput{Visibility: []VisibilityChange{{Row: ref, Visible: true}}}, "")
	if err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("expected provider filter protection, received %v", err)
	}
}

func TestHomeLayoutLegacyCinemetaHiddenCanBeShown(t *testing.T) {
	addons, state := layoutFixtures(t)
	addon := &addons[0]
	_, rows, _ := manifestFields(*addon)
	fields, _, _, _ := catalogFields(rows[0])
	fields["extraRequired"] = json.RawMessage(`["search"]`)
	fields["extra"] = json.RawMessage(`[{"name":"search","isRequired":true,"optionsLimit":2}]`)
	rows[0], _ = json.Marshal(fields)
	if err := replaceCatalogs(addon, rows); err != nil {
		t.Fatal(err)
	}
	ref := RowSelector{AddonID: "com.linvo.cinemeta", Type: "movie", CatalogID: "top"}
	view, err := BuildHomeLayout(addons, state, VisibilityState{})
	if err != nil || view.Addons[0].Rows[0].Visibility != "legacy_cinemeta" || !view.Addons[0].Rows[0].CanShow {
		t.Fatalf("legacy hidden row not recognized: %+v (%v)", view, err)
	}
	plan, err := PlanHomeLayout(addons, state, VisibilityState{}, HomeUpdateInput{Visibility: []VisibilityChange{{Row: ref, Visible: true}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, now, _ := manifestFields(plan.Addons[0])
	result, _, _, _ := catalogFields(now[0])
	if rowHidden(result) || !strings.Contains(string(result["extra"]), `"optionsLimit":2`) {
		t.Fatalf("legacy unhide lost fields: %s", now[0])
	}
}

func TestHomeLayoutInvalidBatchDoesNotChangeInputs(t *testing.T) {
	addons, state := layoutFixtures(t)
	before, _ := json.Marshal(addons)
	row := RowSelector{AddonID: "com.linvo.cinemeta", Type: "movie", CatalogID: "top"}
	other := RowSelector{AddonID: "test.other", Type: "series", CatalogID: "recent"}
	for _, input := range []HomeUpdateInput{
		{RowMoves: []RowMove{{Row: row, Before: &other}}},
		{Visibility: []VisibilityChange{{Row: row, Visible: false}, {Row: row, Visible: true}}},
		{AddonMoves: []AddonMove{{AddonID: "missing", Before: "com.linvo.cinemeta"}}},
		{RowMoves: []RowMove{{Row: row}}},
	} {
		if _, err := PlanHomeLayout(addons, state, VisibilityState{}, input, ""); err == nil {
			t.Fatalf("accepted unsafe layout update: %+v", input)
		}
	}
	after, _ := json.Marshal(addons)
	if string(before) != string(after) {
		t.Fatal("invalid plans mutated the input account")
	}
}

func TestCustomHiddenRowsRemainReadableButNotAdvertised(t *testing.T) {
	_, state := layoutFixtures(t)
	state.Rows[0].Hidden = true
	manifest := BuildManifest(state)
	if len(manifest.Catalogs) != 1 || manifest.Catalogs[0].ID != "row_bbb" {
		t.Fatalf("hidden custom row leaked into manifest: %+v", manifest.Catalogs)
	}
}

func TestHomeLayoutRelativeMoveNoOp(t *testing.T) {
	addons, state := layoutFixtures(t)
	row := RowSelector{AddonID: "com.linvo.cinemeta", Type: "series", CatalogID: "top"}
	previous := RowSelector{AddonID: "com.linvo.cinemeta", Type: "movie", CatalogID: "top"}
	plan, err := PlanHomeLayout(addons, state, VisibilityState{}, HomeUpdateInput{RowMoves: []RowMove{{Row: row, After: &previous}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.AccountChanged || plan.CatalogsChanged || len(plan.Summary) > 0 {
		t.Fatalf("adjacent no-op unexpectedly changed layout: %+v", plan)
	}
}
