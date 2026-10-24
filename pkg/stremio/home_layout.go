package stremio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
)

// RowSelector identifies a catalog without relying on its mutable display name.
// The same catalog ID may appear for both movies and series.
type RowSelector struct {
	AddonID   string `json:"addon_id" jsonschema:"Installed addon manifest ID from home_layout_get"`
	CatalogID string `json:"catalog_id" jsonschema:"Catalog ID from home_layout_get"`
	Type      string `json:"type" jsonschema:"Catalog type from home_layout_get, such as movie or series"`
}

type VisibilityChange struct {
	Row     RowSelector `json:"row" jsonschema:"The exact row to hide or show"`
	Visible bool        `json:"visible" jsonschema:"true to show on Home, false to hide from Home"`
}

type RowMove struct {
	Row    RowSelector  `json:"row" jsonschema:"Row to move"`
	Before *RowSelector `json:"before,omitempty" jsonschema:"Place before this row of the same addon"`
	After  *RowSelector `json:"after,omitempty" jsonschema:"Place after this row of the same addon"`
}

type AddonMove struct {
	AddonID string `json:"addon_id" jsonschema:"Installed addon ID to move, keeping its catalogs together"`
	Before  string `json:"before,omitempty" jsonschema:"Place before this installed addon ID"`
	After   string `json:"after,omitempty" jsonschema:"Place after this installed addon ID"`
}

type HomeUpdateInput struct {
	Visibility []VisibilityChange `json:"visibility,omitempty" jsonschema:"Batch of catalog Home visibility changes"`
	RowMoves   []RowMove          `json:"row_moves,omitempty" jsonschema:"Move rows within their own addons; cross-addon row moves are unsupported"`
	AddonMoves []AddonMove        `json:"addon_moves,omitempty" jsonschema:"Move entire installed addons while preserving their internal catalog order"`
	DryRun     bool               `json:"dry_run,omitempty" jsonschema:"Preview and validate without writing to Stremio or Redis"`
}

type HomeRow struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Position   int    `json:"position"`
	Visible    bool   `json:"visible"`
	CanHide    bool   `json:"can_hide"`
	CanShow    bool   `json:"can_show"`
	Visibility string `json:"visibility_source,omitempty"`
	Note       string `json:"note,omitempty"`
}

type HomeAddon struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	HasStream bool      `json:"has_stream_resource"`
	Rows      []HomeRow `json:"rows"`
}

type HomeLayout struct {
	Addons []HomeAddon `json:"addons"`
	Note   string      `json:"note"`
}

// VisibilitySnapshot records only the catalog's original and applied filtering
// fields. Restoring it does not overwrite later title/name/other-field edits.
type VisibilitySnapshot struct {
	OriginalExtra    json.RawMessage `json:"original_extra,omitempty"`
	OriginalRequired json.RawMessage `json:"original_required,omitempty"`
	AppliedExtra     json.RawMessage `json:"applied_extra,omitempty"`
	AppliedRequired  json.RawMessage `json:"applied_required,omitempty"`
}

type VisibilityState struct {
	Records map[string]VisibilitySnapshot `json:"records"`
}

func DecodeVisibilityState(raw string) (VisibilityState, error) {
	state := VisibilityState{Records: map[string]VisibilitySnapshot{}}
	if raw == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return VisibilityState{}, fmt.Errorf("invalid saved visibility overrides: %w", err)
	}
	if state.Records == nil {
		state.Records = map[string]VisibilitySnapshot{}
	}
	return state, nil
}

func visibilityKey(row RowSelector) string {
	encoded, _ := json.Marshal([3]string{row.AddonID, row.Type, row.CatalogID})
	return string(encoded)
}

func rawSame(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == 0 && len(b) == 0
	}
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func catalogFields(raw json.RawMessage) (map[string]json.RawMessage, RowSelector, string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, RowSelector{}, "", errors.New("malformed addon catalog descriptor")
	}
	var id, mediaType, name string
	if json.Unmarshal(fields["id"], &id) != nil || id == "" || json.Unmarshal(fields["type"], &mediaType) != nil || mediaType == "" || json.Unmarshal(fields["name"], &name) != nil {
		return nil, RowSelector{}, "", errors.New("addon catalog is missing id, type, or name")
	}
	return fields, RowSelector{CatalogID: id, Type: mediaType}, name, nil
}

func manifestFields(d Descriptor) (map[string]json.RawMessage, []json.RawMessage, error) {
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(d.Manifest, &manifest); err != nil || manifest == nil {
		return nil, nil, errors.New("malformed addon manifest")
	}
	var rows []json.RawMessage
	if len(manifest["catalogs"]) == 0 {
		// Stream-only addons are allowed to omit catalogs entirely.
		return manifest, []json.RawMessage{}, nil
	}
	if err := json.Unmarshal(manifest["catalogs"], &rows); err != nil {
		return nil, nil, fmt.Errorf("malformed catalogs for %q: %w", descriptorID(d), err)
	}
	return manifest, rows, nil
}

func replaceCatalogs(d *Descriptor, rows []json.RawMessage) error {
	manifest, _, err := manifestFields(*d)
	if err != nil {
		return err
	}
	manifest["catalogs"], err = json.Marshal(rows)
	if err != nil {
		return err
	}
	d.Manifest, err = json.Marshal(manifest)
	return err
}

func uniqueAddonIndex(addons []Descriptor, id string) (int, error) {
	if id == "" {
		return -1, errors.New("addon_id is required")
	}
	index := -1
	for i, addon := range addons {
		if descriptorID(addon) != id {
			continue
		}
		if index != -1 {
			return -1, fmt.Errorf("addon id %q occurs more than once; cannot safely edit", id)
		}
		index = i
	}
	if index < 0 {
		return -1, fmt.Errorf("addon %q is not installed", id)
	}
	return index, nil
}

func uniqueRowIndex(rows []json.RawMessage, ref RowSelector) (int, error) {
	if ref.CatalogID == "" || ref.Type == "" {
		return -1, errors.New("catalog_id and type are required")
	}
	index := -1
	for i, raw := range rows {
		_, candidate, _, err := catalogFields(raw)
		if err != nil {
			return -1, err
		}
		if candidate.CatalogID == ref.CatalogID && candidate.Type == ref.Type {
			if index != -1 {
				return -1, fmt.Errorf("duplicate catalog %q/%q in addon %q", ref.Type, ref.CatalogID, ref.AddonID)
			}
			index = i
		}
	}
	if index < 0 {
		return -1, fmt.Errorf("catalog %q/%q not found in addon %q", ref.Type, ref.CatalogID, ref.AddonID)
	}
	return index, nil
}

func rowHidden(fields map[string]json.RawMessage) bool {
	var required []string
	if len(fields["extraRequired"]) > 0 && json.Unmarshal(fields["extraRequired"], &required) == nil && len(required) > 0 {
		return true
	}
	var extra []struct {
		IsRequired bool `json:"isRequired"`
	}
	if json.Unmarshal(fields["extra"], &extra) == nil {
		for _, e := range extra {
			if e.IsRequired {
				return true
			}
		}
	}
	return false
}

func supportedHideExtra(fields map[string]json.RawMessage) string {
	var extras []struct {
		Name       string `json:"name"`
		IsRequired bool   `json:"isRequired"`
	}
	if json.Unmarshal(fields["extra"], &extras) != nil {
		return ""
	}
	for _, option := range []string{"search", "genre"} {
		for _, extra := range extras {
			if extra.Name == option && !extra.IsRequired {
				return option
			}
		}
	}
	return ""
}

func legacyCinemetaRestore(ref RowSelector, fields map[string]json.RawMessage) bool {
	if ref.AddonID != "com.linvo.cinemeta" || (ref.CatalogID != "top" && ref.CatalogID != "imdbRating") || (ref.Type != "movie" && ref.Type != "series") {
		return false
	}
	requiredName := "search"
	if ref.CatalogID == "imdbRating" {
		requiredName = "genre"
	}
	var required []string
	if json.Unmarshal(fields["extraRequired"], &required) != nil || len(required) != 1 || required[0] != requiredName {
		return false
	}
	var extras []struct {
		Name       string `json:"name"`
		IsRequired bool   `json:"isRequired"`
	}
	if json.Unmarshal(fields["extra"], &extras) != nil {
		return false
	}
	for _, e := range extras {
		if e.Name == requiredName && e.IsRequired {
			return true
		}
	}
	return false
}

func extraArray(fields map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	var extras []map[string]json.RawMessage
	if err := json.Unmarshal(fields["extra"], &extras); err != nil {
		return nil, fmt.Errorf("invalid catalog extra: %w", err)
	}
	return extras, nil
}

func modifyVisibility(fields map[string]json.RawMessage, ref RowSelector, visible bool, records *VisibilityState) (bool, error) {
	key := visibilityKey(ref)
	if rowHidden(fields) == !visible {
		return false, nil
	}
	if !visible {
		filter := supportedHideExtra(fields)
		if filter == "" {
			return false, fmt.Errorf("catalog %q/%q cannot safely hide: no supported optional search or genre filter", ref.Type, ref.CatalogID)
		}
		original := VisibilitySnapshot{OriginalExtra: bytes.Clone(fields["extra"]), OriginalRequired: bytes.Clone(fields["extraRequired"])}
		extras, err := extraArray(fields)
		if err != nil {
			return false, err
		}
		for _, item := range extras {
			var name string
			_ = json.Unmarshal(item["name"], &name)
			if name == filter {
				item["isRequired"] = json.RawMessage(`true`)
			}
		}
		fields["extra"], _ = json.Marshal(extras)
		fields["extraRequired"], _ = json.Marshal([]string{filter})
		original.AppliedExtra, original.AppliedRequired = bytes.Clone(fields["extra"]), bytes.Clone(fields["extraRequired"])
		records.Records[key] = original
		return true, nil
	}
	if old, exists := records.Records[key]; exists {
		if !rawSame(old.AppliedExtra, fields["extra"]) || !rawSame(old.AppliedRequired, fields["extraRequired"]) {
			return false, errors.New("catalog filters changed since hiding; refusing to overwrite newer provider settings")
		}
		if len(old.OriginalExtra) == 0 {
			delete(fields, "extra")
		} else {
			fields["extra"] = bytes.Clone(old.OriginalExtra)
		}
		if len(old.OriginalRequired) == 0 {
			delete(fields, "extraRequired")
		} else {
			fields["extraRequired"] = bytes.Clone(old.OriginalRequired)
		}
		delete(records.Records, key)
		return true, nil
	}
	if !legacyCinemetaRestore(ref, fields) {
		return false, fmt.Errorf("catalog %q/%q is filtered by its provider, not hidden by this tool; refusing to change its required filters", ref.Type, ref.CatalogID)
	}
	extras, err := extraArray(fields)
	if err != nil {
		return false, err
	}
	requiredName := "search"
	if ref.CatalogID == "imdbRating" {
		requiredName = "genre"
	}
	for _, item := range extras {
		var name string
		_ = json.Unmarshal(item["name"], &name)
		if name == requiredName {
			item["isRequired"] = json.RawMessage(`false`)
		}
	}
	fields["extra"], _ = json.Marshal(extras)
	delete(fields, "extraRequired")
	return true, nil
}

func cloneDescriptors(addons []Descriptor) ([]Descriptor, error) {
	encoded, err := json.Marshal(addons)
	if err != nil {
		return nil, err
	}
	var copied []Descriptor
	err = json.Unmarshal(encoded, &copied)
	return copied, err
}

func findCustomRow(rows []catalog.Row, ref RowSelector) (int, error) {
	if ref.Type == "" || ref.CatalogID == "" {
		return -1, errors.New("catalog_id and type are required")
	}
	for i, row := range rows {
		if row.ID == ref.CatalogID && row.Type == ref.Type {
			return i, nil
		}
	}
	return -1, fmt.Errorf("custom catalog %q/%q does not exist", ref.Type, ref.CatalogID)
}

func moveIndex[T any](items []T, from, target int, after bool) []T {
	if from == target {
		return items
	}
	entry := items[from]
	items = append(items[:from], items[from+1:]...)
	if from < target {
		target--
	}
	if after {
		target++
	}
	items = append(items, entry)
	copy(items[target+1:], items[target:len(items)-1])
	items[target] = entry
	return items
}

// PlanHomeLayout performs pure validation and mutation; no network or store write.
// The server persists new states and updated account descriptors with one API write.
type HomePlan struct {
	Addons            []Descriptor
	Catalogs          catalog.State
	Visibility        VisibilityState
	AccountChanged    bool
	CatalogsChanged   bool
	VisibilityChanged bool
	Summary           []string
}

func PlanHomeLayout(current []Descriptor, source catalog.State, saved VisibilityState, input HomeUpdateInput, manifestURL string) (HomePlan, error) {
	plan := HomePlan{}
	var err error
	plan.Addons, err = cloneDescriptors(current)
	if err != nil {
		return plan, err
	}
	serialized, _ := json.Marshal(source)
	if err = json.Unmarshal(serialized, &plan.Catalogs); err != nil {
		return plan, err
	}
	serialized, _ = json.Marshal(saved)
	if err = json.Unmarshal(serialized, &plan.Visibility); err != nil {
		return plan, err
	}
	if plan.Visibility.Records == nil {
		plan.Visibility.Records = map[string]VisibilitySnapshot{}
	}
	if len(input.Visibility)+len(input.RowMoves)+len(input.AddonMoves) == 0 {
		return plan, errors.New("provide visibility, row_moves, or addon_moves")
	}
	if len(input.Visibility)+len(input.RowMoves)+len(input.AddonMoves) > 50 {
		return plan, errors.New("maximum 50 layout operations per request")
	}
	seenVisibility := map[string]bool{}
	for _, item := range input.Visibility {
		key := visibilityKey(item.Row)
		if seenVisibility[key] {
			return plan, fmt.Errorf("duplicate visibility change for %s/%s", item.Row.Type, item.Row.CatalogID)
		}
		seenVisibility[key] = true
		index, err := uniqueAddonIndex(plan.Addons, item.Row.AddonID)
		if err != nil {
			return plan, err
		}
		if item.Row.AddonID == AddonID {
			row, err := findCustomRow(plan.Catalogs.Rows, item.Row)
			if err != nil {
				return plan, err
			}
			if plan.Catalogs.Rows[row].Hidden != !item.Visible {
				plan.Catalogs.Rows[row].Hidden = !item.Visible
				plan.CatalogsChanged = true
				plan.Summary = append(plan.Summary, fmt.Sprintf("%s custom row %s", boolVerb(item.Visible), plan.Catalogs.Rows[row].Name))
			}
			continue
		}
		_, rows, err := manifestFields(plan.Addons[index])
		if err != nil {
			return plan, err
		}
		row, err := uniqueRowIndex(rows, item.Row)
		if err != nil {
			return plan, err
		}
		fields, _, name, err := catalogFields(rows[row])
		if err != nil {
			return plan, err
		}
		changed, err := modifyVisibility(fields, item.Row, item.Visible, &plan.Visibility)
		if err != nil {
			return plan, err
		}
		if changed {
			rows[row], _ = json.Marshal(fields)
			if err := replaceCatalogs(&plan.Addons[index], rows); err != nil {
				return plan, err
			}
			plan.AccountChanged, plan.VisibilityChanged = true, true
			plan.Summary = append(plan.Summary, fmt.Sprintf("%s %s row %s (%s)", boolVerb(item.Visible), item.Row.AddonID, name, item.Row.Type))
		}
	}
	for _, item := range input.RowMoves {
		if (item.Before == nil) == (item.After == nil) {
			return plan, errors.New("each row move needs exactly one of before or after")
		}
		target := item.Before
		if target == nil {
			target = item.After
		}
		if item.Row.AddonID != target.AddonID {
			return plan, fmt.Errorf("cross-addon catalog move unsupported: move the entire addon %q instead", item.Row.AddonID)
		}
		index, err := uniqueAddonIndex(plan.Addons, item.Row.AddonID)
		if err != nil {
			return plan, err
		}
		if item.Row.AddonID == AddonID {
			from, err := findCustomRow(plan.Catalogs.Rows, item.Row)
			if err != nil {
				return plan, err
			}
			to, err := findCustomRow(plan.Catalogs.Rows, *target)
			if err != nil {
				return plan, err
			}
			if from != to {
				original := append([]catalog.Row(nil), plan.Catalogs.Rows...)
				newRows := moveIndex(plan.Catalogs.Rows, from, to, item.After != nil)
				if !reflect.DeepEqual(newRows, original) {
					plan.Catalogs.Rows, plan.CatalogsChanged = newRows, true
					plan.Summary = append(plan.Summary, "Reordered custom catalog row "+item.Row.CatalogID)
				}
			}
			continue
		}
		_, rows, err := manifestFields(plan.Addons[index])
		if err != nil {
			return plan, err
		}
		from, err := uniqueRowIndex(rows, item.Row)
		if err != nil {
			return plan, err
		}
		to, err := uniqueRowIndex(rows, *target)
		if err != nil {
			return plan, err
		}
		if from != to {
			original := append([]json.RawMessage(nil), rows...)
			rows = moveIndex(rows, from, to, item.After != nil)
			if !reflect.DeepEqual(rows, original) {
				if err := replaceCatalogs(&plan.Addons[index], rows); err != nil {
					return plan, err
				}
				plan.AccountChanged = true
				plan.Summary = append(plan.Summary, "Reordered catalog "+item.Row.CatalogID+" in "+item.Row.AddonID)
			}
		}
	}
	for _, item := range input.AddonMoves {
		if (item.Before == "") == (item.After == "") {
			return plan, errors.New("each addon move needs exactly one of before or after")
		}
		toID := item.Before
		if toID == "" {
			toID = item.After
		}
		from, err := uniqueAddonIndex(plan.Addons, item.AddonID)
		if err != nil {
			return plan, err
		}
		to, err := uniqueAddonIndex(plan.Addons, toID)
		if err != nil {
			return plan, err
		}
		if from != to {
			original := append([]Descriptor(nil), plan.Addons...)
			plan.Addons = moveIndex(plan.Addons, from, to, item.After != "")
			if !reflect.DeepEqual(plan.Addons, original) {
				plan.AccountChanged = true
				plan.Summary = append(plan.Summary, "Moved addon "+item.AddonID)
			}
		}
	}
	if plan.CatalogsChanged {
		plan.Catalogs.Revision++
		if err := plan.Catalogs.Validate(); err != nil {
			return plan, err
		}
		if !strings.HasPrefix(manifestURL, "https://") {
			return plan, errors.New("cannot publish custom catalog layout without an HTTPS manifest URL")
		}
		index, err := uniqueAddonIndex(plan.Addons, AddonID)
		if err != nil {
			return plan, err
		}
		plan.Addons[index].Manifest, err = json.Marshal(BuildManifest(plan.Catalogs))
		if err != nil {
			return plan, err
		}
		plan.Addons[index].TransportURL = manifestURL
		plan.Addons[index].TransportName = "http"
		plan.AccountChanged = true
	}
	return plan, nil
}

func boolVerb(visible bool) string {
	if visible {
		return "Restored"
	}
	return "Hid"
}

func BuildHomeLayout(addons []Descriptor, state catalog.State, visibility VisibilityState) (HomeLayout, error) {
	output := HomeLayout{Addons: []HomeAddon{}, Note: "Rows from different add-ons cannot be interleaved. Addon order can affect source priority; Continue Watching is controlled by Stremio."}
	for i, d := range addons {
		var meta struct {
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Resources json.RawMessage `json:"resources"`
		}
		if err := json.Unmarshal(d.Manifest, &meta); err != nil || meta.ID == "" {
			return HomeLayout{}, errors.New("malformed installed addon manifest")
		}
		addon := HomeAddon{ID: meta.ID, Name: meta.Name, Position: i, Rows: []HomeRow{}}
		var resources []string
		if json.Unmarshal(meta.Resources, &resources) == nil {
			for _, resource := range resources {
				if resource == "stream" {
					addon.HasStream = true
				}
			}
		}
		if meta.ID == AddonID {
			for j, row := range state.Rows {
				addon.Rows = append(addon.Rows, HomeRow{ID: row.ID, Type: row.Type, Name: row.Name, Position: j, Visible: !row.Hidden, CanHide: true, CanShow: true, Visibility: "managed"})
			}
			output.Addons = append(output.Addons, addon)
			continue
		}
		_, rows, err := manifestFields(d)
		if err != nil {
			return HomeLayout{}, err
		}
		for j, raw := range rows {
			fields, ref, name, err := catalogFields(raw)
			if err != nil {
				return HomeLayout{}, err
			}
			ref.AddonID = meta.ID
			hidden := rowHidden(fields)
			_, managed := visibility.Records[visibilityKey(ref)]
			legacy := hidden && !managed && legacyCinemetaRestore(ref, fields)
			row := HomeRow{ID: ref.CatalogID, Type: ref.Type, Name: name, Position: j, Visible: !hidden, CanHide: !hidden && supportedHideExtra(fields) != "", CanShow: !hidden || managed || legacy}
			if hidden {
				switch {
				case managed:
					row.Visibility = "managed"
				case legacy:
					row.Visibility = "legacy_cinemeta"
				default:
					row.Visibility, row.Note = "provider", "Required provider filters prevent showing this row on Home; not modified by this tool"
				}
			} else if !row.CanHide {
				row.Note = "Cannot safely hide: this catalog lacks an optional search or genre filter"
			}
			addon.Rows = append(addon.Rows, row)
		}
		output.Addons = append(output.Addons, addon)
	}
	return output, nil
}
