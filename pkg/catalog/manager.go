package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Manager struct {
	Store    Store
	Resolver Resolver
}

func (m Manager) Read(ctx context.Context) (State, error) {
	if m.Store == nil {
		return State{}, errors.New("catalog storage is not configured")
	}
	raw, err := m.Store.Load(ctx)
	if err != nil {
		return State{}, err
	}
	return decodeState(raw)
}

func decodeState(raw string) (State, error) {
	state := State{Rows: []Row{}}
	if raw == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return State{}, err
	}
	if state.Rows == nil {
		state.Rows = []Row{}
	}
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

// ParseState validates a stored snapshot for coordinated catalog and layout edits.
func ParseState(raw string) (State, error) { return decodeState(raw) }

func (m Manager) mutate(ctx context.Context, change func(*State) error) (State, error) {
	if m.Store == nil {
		return State{}, errors.New("catalog storage is not configured")
	}
	for attempt := 0; attempt < 8; attempt++ {
		raw, err := m.Store.Load(ctx)
		if err != nil {
			return State{}, err
		}
		state, err := decodeState(raw)
		if err != nil {
			return State{}, err
		}
		if err = change(&state); err != nil {
			return State{}, err
		}
		state.Revision++
		if err = state.Validate(); err != nil {
			return State{}, err
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			return State{}, err
		}
		ok, err := m.Store.CAS(ctx, raw, string(encoded))
		if err != nil {
			return State{}, err
		}
		if ok {
			return state, nil
		}
	}
	return State{}, errors.New("catalog changed concurrently; retry the request")
}

func findRow(s State, id string) int {
	for i, row := range s.Rows {
		if row.ID == id {
			return i
		}
	}
	return -1
}

func (m Manager) Upsert(ctx context.Context, in UpsertInput) (Row, error) {
	if in.Mode == "" {
		in.Mode = "append"
	}
	if in.Mode != "append" && in.Mode != "replace" {
		return Row{}, errors.New("mode must be append or replace")
	}
	if in.Name != "" && !ValidName(in.Name) {
		return Row{}, errors.New("row name must be 1-80 characters and single-line")
	}
	if in.Type != "" && !ValidType(in.Type) {
		return Row{}, errors.New("type must be movie or series")
	}
	if in.ID == "" && (!ValidType(in.Type) || !ValidName(in.Name)) {
		return Row{}, errors.New("new rows require a name and type (movie or series)")
	}
	if in.Position != nil && (*in.Position < 0 || *in.Position >= MaxRows) {
		return Row{}, errors.New("position is out of bounds")
	}
	initial, err := m.Read(ctx)
	if err != nil {
		return Row{}, err
	}
	rowType := in.Type
	if in.ID != "" {
		idx := findRow(initial, in.ID)
		if idx < 0 {
			return Row{}, fmt.Errorf("unknown catalog row %s", in.ID)
		}
		if rowType == "" {
			rowType = initial.Rows[idx].Type
		}
	}
	var resolved []Item
	if len(in.Items) > 0 {
		if m.Resolver == nil {
			return Row{}, errors.New("Cinemeta resolver is unavailable")
		}
		resolved, err = ResolveItems(ctx, m.Resolver, rowType, in.Items)
		if err != nil {
			return Row{}, err
		}
	}
	rowID := in.ID
	if rowID == "" {
		rowID, err = NewID()
		if err != nil {
			return Row{}, err
		}
	}
	var result Row
	_, err = m.mutate(ctx, func(state *State) error {
		idx := findRow(*state, rowID)
		if in.ID == "" {
			if len(state.Rows) >= MaxRows {
				return fmt.Errorf("maximum %d catalog rows", MaxRows)
			}
			state.Rows = append(state.Rows, Row{ID: rowID, Name: strings.TrimSpace(in.Name), Type: rowType, Items: []Item{}})
			idx = len(state.Rows) - 1
		} else if idx < 0 {
			return errors.New("row was deleted during this update")
		}
		row := &state.Rows[idx]
		if in.Type != "" && in.Type != row.Type {
			return errors.New("cannot change a row's movie/series type; create another row")
		}
		if in.Name != "" {
			row.Name = strings.TrimSpace(in.Name)
		}
		if in.Mode == "replace" {
			row.Items = []Item{}
		}
		byID := make(map[string]int, len(row.Items))
		for i, item := range row.Items {
			byID[item.ID] = i
		}
		for _, item := range resolved {
			if i, exists := byID[item.ID]; exists {
				row.Items[i] = item
			} else {
				byID[item.ID] = len(row.Items)
				row.Items = append(row.Items, item)
			}
		}
		result = *row
		if in.Position != nil {
			position := *in.Position
			if position >= len(state.Rows) {
				position = len(state.Rows) - 1
			}
			moving := state.Rows[idx]
			state.Rows = append(state.Rows[:idx], state.Rows[idx+1:]...)
			state.Rows = append(state.Rows, Row{})
			copy(state.Rows[position+1:], state.Rows[position:])
			state.Rows[position] = moving
		}
		return nil
	})
	return result, err
}

func (m Manager) RemoveItems(ctx context.Context, rowID string, ids []string) (Row, error) {
	if rowID == "" || len(ids) == 0 {
		return Row{}, errors.New("row ID and at least one IMDb ID required")
	}
	toRemove := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !ValidIMDbID(id) {
			return Row{}, fmt.Errorf("invalid IMDb ID %q", id)
		}
		toRemove[id] = true
	}
	var row Row
	_, err := m.mutate(ctx, func(s *State) error {
		i := findRow(*s, rowID)
		if i == -1 {
			return errors.New("row not found")
		}
		keep := make([]Item, 0, len(s.Rows[i].Items))
		for _, item := range s.Rows[i].Items {
			if !toRemove[item.ID] {
				keep = append(keep, item)
			}
		}
		s.Rows[i].Items = keep
		row = s.Rows[i]
		return nil
	})
	return row, err
}

func (m Manager) Delete(ctx context.Context, rowID string) error {
	_, err := m.mutate(ctx, func(s *State) error {
		i := findRow(*s, rowID)
		if i == -1 {
			return errors.New("row not found")
		}
		s.Rows = append(s.Rows[:i], s.Rows[i+1:]...)
		return nil
	})
	return err
}
