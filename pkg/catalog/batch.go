package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

const maxBatchChanges = 24
const maxResolvedTitles = 100
const maxRecentRequests = 64

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type TitleMove struct {
	ID     string `json:"id" jsonschema:"IMDb ID of the title to move"`
	Before string `json:"before,omitempty" jsonschema:"IMDb ID to place the title before; choose exactly one of before or after"`
	After  string `json:"after,omitempty" jsonschema:"IMDb ID to place the title after; choose exactly one of before or after"`
}

// Change describes one operation on the custom catalog collection.
// The action selects the relevant fields; irrelevant fields are rejected.
type Change struct {
	Action         string       `json:"action" jsonschema:"create, edit, delete, or sync"`
	RowID          string       `json:"row_id,omitempty" jsonschema:"Existing catalog ID from catalogs_get; required for edit and delete"`
	Name           string       `json:"name,omitempty" jsonschema:"Catalog name on create, or optional new name on edit"`
	Type           string       `json:"type,omitempty" jsonschema:"movie or series; required only for create"`
	Titles         []ItemInput  `json:"titles,omitempty" jsonschema:"Initial titles for create, in display order"`
	AddTitles      []ItemInput  `json:"add_titles,omitempty" jsonschema:"Add titles to an existing catalog; existing IDs get refreshed"`
	RemoveTitleIDs []string     `json:"remove_title_ids,omitempty" jsonschema:"IMDb IDs to remove from an existing catalog"`
	ReplaceTitles  *[]ItemInput `json:"replace_titles,omitempty" jsonschema:"Complete new ordered title list for edit; [] clears the catalog; omitting preserves titles"`
	MoveTitles     []TitleMove  `json:"move_titles,omitempty" jsonschema:"Reposition existing titles by IMDb ID within this catalog"`
}

type BatchInput struct {
	Changes          []Change `json:"changes" jsonschema:"One or more catalog changes, applied together in order"`
	DryRun           bool     `json:"dry_run,omitempty" jsonschema:"Validate and preview without saving anything"`
	ExpectedRevision *int64   `json:"expected_revision,omitempty" jsonschema:"Optional revision from catalogs_get to reject edits against stale state"`
	RequestID        string   `json:"request_id,omitempty" jsonschema:"Unique retry key for this batch, recommended for create; avoids duplicate catalogs if a request is retried"`
}

type ChangeReference struct {
	Action string `json:"action"`
	RowID  string `json:"row_id,omitempty"`
}

type ChangeResult struct {
	Action  string `json:"action"`
	RowID   string `json:"row_id,omitempty"`
	Catalog *Row   `json:"catalog,omitempty"`
}

// AppliedRequest lives in the same CAS-protected state as the created catalog IDs.
// Retries can therefore recover from an ambiguous HTTP response without duplicating rows.
type AppliedRequest struct {
	ID           string            `json:"id"`
	Digest       string            `json:"digest"`
	Revision     int64             `json:"revision"`
	SyncRequired bool              `json:"sync_required"`
	Results      []ChangeReference `json:"results"`
}

type BatchResult struct {
	Revision        int64          `json:"revision"`
	AppliedRevision int64          `json:"applied_revision"`
	Changed         bool           `json:"changed"`
	Replayed        bool           `json:"replayed"`
	SyncRequired    bool           `json:"sync_required"`
	Changes         []ChangeResult `json:"changes"`
}

func validateChange(c Change) error {
	if c.Action != "create" && c.Action != "edit" && c.Action != "delete" && c.Action != "sync" {
		return fmt.Errorf("action must be create, edit, delete, or sync; received %q", c.Action)
	}
	if len(c.Titles)+len(c.AddTitles)+len(c.RemoveTitleIDs)+len(c.MoveTitles) > 200 {
		return errors.New("too many title edits in one change")
	}
	switch c.Action {
	case "create":
		if c.RowID != "" || !ValidName(c.Name) || !ValidType(c.Type) || len(c.AddTitles) != 0 || len(c.RemoveTitleIDs) != 0 || c.ReplaceTitles != nil || len(c.MoveTitles) != 0 {
			return errors.New("create requires name and type (movie or series), with optional titles; other fields must be omitted")
		}
	case "edit":
		if c.RowID == "" || c.Type != "" || len(c.Titles) != 0 || (c.Name == "" && len(c.AddTitles) == 0 && len(c.RemoveTitleIDs) == 0 && c.ReplaceTitles == nil && len(c.MoveTitles) == 0) {
			return errors.New("edit requires row_id and at least one of name, add_titles, remove_title_ids, replace_titles, move_titles; omit type")
		}
		if c.Name != "" && !ValidName(c.Name) {
			return errors.New("catalog name must be 1–80 characters and single-line")
		}
		if c.ReplaceTitles != nil && (len(c.AddTitles) != 0 || len(c.RemoveTitleIDs) != 0) {
			return errors.New("replace_titles cannot be combined with add_titles or remove_title_ids; it already defines the entire title list")
		}
	case "delete", "sync":
		if c.Name != "" || c.Type != "" || len(c.Titles) != 0 || len(c.AddTitles) != 0 || len(c.RemoveTitleIDs) != 0 || c.ReplaceTitles != nil || len(c.MoveTitles) != 0 || (c.Action == "delete" && c.RowID == "") || (c.Action == "sync" && c.RowID != "") {
			return fmt.Errorf("%s accepts only %s", c.Action, map[bool]string{true: "row_id", false: "action"}[c.Action == "delete"])
		}
	}
	for _, id := range c.RemoveTitleIDs {
		if !ValidIMDbID(id) {
			return fmt.Errorf("invalid remove_title_ids IMDb ID %q", id)
		}
	}
	for _, move := range c.MoveTitles {
		if !ValidIMDbID(move.ID) || (move.Before == "") == (move.After == "") {
			return errors.New("each move_titles entry needs a valid IMDb ID and exactly one of before or after")
		}
		if move.Before != "" && !ValidIMDbID(move.Before) || move.After != "" && !ValidIMDbID(move.After) {
			return errors.New("move_titles before/after must be IMDb IDs")
		}
		if move.ID == move.Before || move.ID == move.After {
			return errors.New("a title cannot move relative to itself")
		}
	}
	return nil
}

func digestChanges(changes []Change) (string, error) {
	encoded, err := json.Marshal(changes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func rowIndex(s *State, id string) (int, error) {
	for i := range s.Rows {
		if s.Rows[i].ID == id {
			return i, nil
		}
	}
	return -1, fmt.Errorf("catalog %q not found; call catalogs_get for the current IDs", id)
}

func titleIndex(items []Item, id string) (int, error) {
	for i, item := range items {
		if item.ID == id {
			return i, nil
		}
	}
	return -1, fmt.Errorf("IMDb title %q not found in catalog", id)
}

func applyTitleMoves(items []Item, moves []TitleMove) ([]Item, error) {
	for _, move := range moves {
		from, err := titleIndex(items, move.ID)
		if err != nil {
			return nil, err
		}
		anchor := move.Before
		if anchor == "" {
			anchor = move.After
		}
		to, err := titleIndex(items, anchor)
		if err != nil {
			return nil, err
		}
		entry := items[from]
		items = append(items[:from], items[from+1:]...)
		if from < to {
			to--
		}
		if move.After != "" {
			to++
		}
		items = append(items, Item{})
		copy(items[to+1:], items[to:len(items)-1])
		items[to] = entry
	}
	return items, nil
}

func hydrateResults(s State, refs []ChangeReference) []ChangeResult {
	results := make([]ChangeResult, 0, len(refs))
	for _, ref := range refs {
		result := ChangeResult{Action: ref.Action, RowID: ref.RowID}
		for _, row := range s.Rows {
			if row.ID == ref.RowID && ref.Action != "delete" {
				copyRow := row
				result.Catalog = &copyRow
				break
			}
		}
		results = append(results, result)
	}
	return results
}

func findRequest(s State, id, digest string) (*AppliedRequest, error) {
	for _, applied := range s.RecentRequests {
		if applied.ID != id {
			continue
		}
		if applied.Digest != digest {
			return nil, fmt.Errorf("request_id %q was previously used with different changes; choose a new ID", id)
		}
		return &applied, nil
	}
	return nil, nil
}

// ApplyBatch resolves and validates all actions before one CAS. Both preview
// and failed validation leave the shared catalog state unchanged.
func (m Manager) ApplyBatch(ctx context.Context, in BatchInput) (BatchResult, error) {
	if m.Store == nil {
		return BatchResult{}, errors.New("catalog storage is not configured")
	}
	if len(in.Changes) == 0 || len(in.Changes) > maxBatchChanges {
		return BatchResult{}, fmt.Errorf("provide 1–%d changes", maxBatchChanges)
	}
	if in.RequestID != "" && !validRequestID.MatchString(in.RequestID) {
		return BatchResult{}, errors.New("request_id must be 1–128 letters, numbers, dots, hyphens, colons, or underscores and start with a letter/number")
	}
	if in.ExpectedRevision != nil && *in.ExpectedRevision < 0 {
		return BatchResult{}, errors.New("expected_revision cannot be negative")
	}
	count, syncCount := 0, 0
	for i, change := range in.Changes {
		if err := validateChange(change); err != nil {
			return BatchResult{}, fmt.Errorf("changes[%d]: %w", i, err)
		}
		if change.Action == "sync" {
			syncCount++
		}
		count += len(change.Titles) + len(change.AddTitles)
		if change.ReplaceTitles != nil {
			count += len(*change.ReplaceTitles)
		}
	}
	if count > maxResolvedTitles {
		return BatchResult{}, fmt.Errorf("maximum %d titles to resolve per batch", maxResolvedTitles)
	}
	if syncCount > 1 {
		return BatchResult{}, errors.New("at most one sync action per batch")
	}
	digest, err := digestChanges(in.Changes)
	if err != nil {
		return BatchResult{}, err
	}
	// Read first to skip external title lookups on duplicate requests.
	initialRaw, err := m.Store.Load(ctx)
	if err != nil {
		return BatchResult{}, err
	}
	initial, err := decodeState(initialRaw)
	if err != nil {
		return BatchResult{}, err
	}
	if !in.DryRun && in.RequestID != "" {
		previous, err := findRequest(initial, in.RequestID, digest)
		if err != nil {
			return BatchResult{}, err
		}
		if previous != nil {
			return BatchResult{Revision: initial.Revision, AppliedRevision: previous.Revision, Replayed: true, SyncRequired: previous.SyncRequired, Changes: hydrateResults(initial, previous.Results)}, nil
		}
	}
	if in.ExpectedRevision != nil && initial.Revision != *in.ExpectedRevision {
		return BatchResult{}, fmt.Errorf("catalog revision changed: expected %d, found %d; fetch catalogs_get again", *in.ExpectedRevision, initial.Revision)
	}

	resolved := make([][]Item, len(in.Changes))
	createdIDs := make([]string, len(in.Changes))
	for i, change := range in.Changes {
		if change.Action == "create" {
			createdIDs[i], err = NewID()
			if err != nil {
				return BatchResult{}, err
			}
		}
		var titles []ItemInput
		var mediaType string
		switch change.Action {
		case "create":
			mediaType, titles = change.Type, change.Titles
		case "edit":
			row, err := rowIndex(&initial, change.RowID)
			if err != nil {
				return BatchResult{}, fmt.Errorf("changes[%d]: %w", i, err)
			}
			mediaType = initial.Rows[row].Type
			if change.ReplaceTitles != nil {
				titles = *change.ReplaceTitles
			} else {
				titles = change.AddTitles
			}
		}
		if len(titles) > 0 {
			if m.Resolver == nil {
				return BatchResult{}, errors.New("Cinemeta resolver is unavailable")
			}
			resolved[i], err = ResolveItems(ctx, m.Resolver, mediaType, titles)
			if err != nil {
				return BatchResult{}, fmt.Errorf("changes[%d]: %w", i, err)
			}
		}
	}

	for attempt := 0; attempt < 8; attempt++ {
		raw := initialRaw
		state := initial
		if attempt > 0 {
			raw, err = m.Store.Load(ctx)
			if err != nil {
				return BatchResult{}, err
			}
			state, err = decodeState(raw)
			if err != nil {
				return BatchResult{}, err
			}
			if !in.DryRun && in.RequestID != "" {
				previous, err := findRequest(state, in.RequestID, digest)
				if err != nil {
					return BatchResult{}, err
				}
				if previous != nil {
					return BatchResult{Revision: state.Revision, AppliedRevision: previous.Revision, Replayed: true, SyncRequired: previous.SyncRequired, Changes: hydrateResults(state, previous.Results)}, nil
				}
			}
			if in.ExpectedRevision != nil && state.Revision != *in.ExpectedRevision {
				return BatchResult{}, fmt.Errorf("catalog revision changed: expected %d, found %d; fetch catalogs_get again", *in.ExpectedRevision, state.Revision)
			}
		}
		// Copy before mutation so the next CAS attempt cannot reuse mutated data.
		serialized, err := json.Marshal(state)
		if err != nil {
			return BatchResult{}, err
		}
		var updated State
		if err = json.Unmarshal(serialized, &updated); err != nil {
			return BatchResult{}, err
		}
		refs, requiresSync := make([]ChangeReference, 0, len(in.Changes)), syncCount > 0
		for i, change := range in.Changes {
			if change.Action == "sync" {
				refs = append(refs, ChangeReference{Action: "sync"})
				continue
			}
			id := change.RowID
			if change.Action == "create" {
				id = createdIDs[i]
				updated.Rows = append(updated.Rows, Row{ID: id, Name: strings.TrimSpace(change.Name), Type: change.Type, Items: append([]Item{}, resolved[i]...)})
				requiresSync = true
			} else {
				index, err := rowIndex(&updated, id)
				if err != nil {
					return BatchResult{}, fmt.Errorf("changes[%d]: %w", i, err)
				}
				if change.Action == "delete" {
					updated.Rows = append(updated.Rows[:index], updated.Rows[index+1:]...)
					requiresSync = true
				} else {
					row := &updated.Rows[index]
					if change.Name != "" && row.Name != strings.TrimSpace(change.Name) {
						row.Name = strings.TrimSpace(change.Name)
						requiresSync = true
					}
					if change.ReplaceTitles != nil {
						row.Items = append([]Item{}, resolved[i]...)
					} else {
						removing := map[string]bool{}
						for _, id := range change.RemoveTitleIDs {
							removing[id] = true
						}
						kept := make([]Item, 0, len(row.Items)+len(resolved[i]))
						for _, item := range row.Items {
							if !removing[item.ID] {
								kept = append(kept, item)
							}
						}
						byID := map[string]int{}
						for k, item := range kept {
							byID[item.ID] = k
						}
						for _, item := range resolved[i] {
							if k, exists := byID[item.ID]; exists {
								kept[k] = item
							} else {
								byID[item.ID] = len(kept)
								kept = append(kept, item)
							}
						}
						row.Items = kept
					}
					row.Items, err = applyTitleMoves(row.Items, change.MoveTitles)
					if err != nil {
						return BatchResult{}, fmt.Errorf("changes[%d]: %w", i, err)
					}
				}
			}
			refs = append(refs, ChangeReference{Action: change.Action, RowID: id})
		}
		if err := updated.Validate(); err != nil {
			return BatchResult{}, err
		}
		changed := !reflect.DeepEqual(state.Rows, updated.Rows)
		if !changed {
			return BatchResult{Revision: state.Revision, AppliedRevision: state.Revision, Changed: false, SyncRequired: requiresSync, Changes: hydrateResults(updated, refs)}, nil
		}
		updated.Revision++
		if in.DryRun {
			return BatchResult{Revision: updated.Revision, AppliedRevision: updated.Revision, Changed: true, SyncRequired: requiresSync, Changes: hydrateResults(updated, refs)}, nil
		}
		if in.RequestID != "" {
			updated.RecentRequests = append(updated.RecentRequests, AppliedRequest{ID: in.RequestID, Digest: digest, Revision: updated.Revision, SyncRequired: requiresSync, Results: refs})
			if len(updated.RecentRequests) > maxRecentRequests {
				updated.RecentRequests = updated.RecentRequests[len(updated.RecentRequests)-maxRecentRequests:]
			}
		}
		encoded, err := json.Marshal(updated)
		if err != nil {
			return BatchResult{}, err
		}
		ok, err := m.Store.CAS(ctx, raw, string(encoded))
		if err != nil {
			return BatchResult{}, err
		}
		if ok {
			return BatchResult{Revision: updated.Revision, AppliedRevision: updated.Revision, Changed: true, SyncRequired: requiresSync, Changes: hydrateResults(updated, refs)}, nil
		}
	}
	return BatchResult{}, errors.New("catalog changed concurrently after repeated retries; retry the request with the same request_id")
}
