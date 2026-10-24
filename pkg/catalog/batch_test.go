package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type posterResolver struct{}

func (posterResolver) Resolve(_ context.Context, mediaType string, in ItemInput) (Item, error) {
	if !ValidIMDbID(in.ID) || in.Title == "" {
		return Item{}, errors.New("test resolver requires id and title")
	}
	return Item{ID: in.ID, Type: mediaType, Title: in.Title, Year: in.Year, Poster: "https://images.example/poster/" + in.ID + ".jpg"}, nil
}

func seedBatch(t *testing.T) (Manager, *MemoryStore, string) {
	t.Helper()
	store := &MemoryStore{}
	state := State{Revision: 12, Rows: []Row{
		{ID: "row_first", Name: "Movies", Type: "movie", Items: []Item{
			{ID: "tt0000001", Title: "First", Type: "movie", Poster: "https://images.example/first.jpg"},
			{ID: "tt0000002", Title: "Second", Type: "movie", Poster: "https://images.example/second.jpg"},
			{ID: "tt0000003", Title: "Third", Type: "movie", Poster: "https://images.example/third.jpg"},
		}},
		{ID: "row_hidden", Name: "Hidden Shows", Type: "series", Items: []Item{}, Hidden: true},
	}}
	encoded, _ := json.Marshal(state)
	if ok, err := store.CAS(context.Background(), "", string(encoded)); err != nil || !ok {
		t.Fatal(err)
	}
	return Manager{Store: store, Resolver: posterResolver{}}, store, string(encoded)
}

func TestBatchReplacesMovesCreatesDeletesAndRetainsPosters(t *testing.T) {
	m, _, _ := seedBatch(t)
	ctx := context.Background()
	input := BatchInput{Changes: []Change{
		{Action: "create", Name: "New", Type: "series", Titles: []ItemInput{{ID: "tt0000004", Title: "Four", Year: 2004}}},
		{Action: "edit", RowID: "row_first", Name: "Renamed", RemoveTitleIDs: []string{"tt0000002"},
			AddTitles:  []ItemInput{{ID: "tt0000005", Title: "Fifth", Year: 2005}},
			MoveTitles: []TitleMove{{ID: "tt0000003", Before: "tt0000001"}, {ID: "tt0000005", After: "tt0000003"}}},
		{Action: "edit", RowID: "row_hidden", ReplaceTitles: &[]ItemInput{{ID: "tt0000006", Title: "Sixth", Year: 2006}}},
	}}
	got, err := m.ApplyBatch(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Changed || !got.SyncRequired || got.Revision != 13 || len(got.Changes) != 3 {
		t.Fatalf("batch result: %+v", got)
	}
	created := got.Changes[0].Catalog
	if created == nil || len(created.Items) != 1 || created.Items[0].Poster != "https://images.example/poster/tt0000004.jpg" {
		t.Fatalf("missing resolved poster or created row: %+v", created)
	}
	renamed := got.Changes[1].Catalog
	if renamed == nil || renamed.Name != "Renamed" || len(renamed.Items) != 3 || renamed.Items[0].ID != "tt0000003" || renamed.Items[1].ID != "tt0000005" || renamed.Items[2].ID != "tt0000001" || renamed.Items[2].Poster != "https://images.example/first.jpg" {
		t.Fatalf("wrong title moves or posters: %+v", renamed)
	}
	s, err := m.Read(ctx)
	if err != nil || len(s.Rows) != 3 || s.Rows[1].Hidden != true || s.Rows[1].Items[0].Poster == "" {
		t.Fatalf("lost hidden row or metadata: %+v %v", s, err)
	}
	cleared, err := m.ApplyBatch(ctx, BatchInput{Changes: []Change{
		{Action: "edit", RowID: created.ID, ReplaceTitles: &[]ItemInput{}},
		{Action: "delete", RowID: "row_hidden"},
	}})
	if err != nil || !cleared.SyncRequired || cleared.Revision != 14 {
		t.Fatalf("clear/delete failed: %+v %v", cleared, err)
	}
	if cleared.Changes[0].Catalog == nil || len(cleared.Changes[0].Catalog.Items) != 0 || cleared.Changes[1].Catalog != nil {
		t.Fatalf("clear should keep row, delete should remove it: %+v", cleared)
	}
	s, _ = m.Read(ctx)
	if len(s.Rows) != 2 || s.Rows[1].ID != created.ID {
		t.Fatalf("wrong final rows: %+v", s.Rows)
	}
}

func TestBatchDryRunAndLateValidationFailureNeverWrite(t *testing.T) {
	m, store, before := seedBatch(t)
	ctx := context.Background()
	prev := int64(12)
	input := BatchInput{DryRun: true, RequestID: "batch-preview", ExpectedRevision: &prev, Changes: []Change{
		{Action: "create", Name: "Something", Type: "movie", Titles: []ItemInput{{ID: "tt0000011", Title: "Test"}}},
		{Action: "edit", RowID: "row_first", MoveTitles: []TitleMove{{ID: "tt0000002", After: "tt0000003"}}},
	}}
	preview, err := m.ApplyBatch(ctx, input)
	if err != nil || !preview.Changed || preview.Revision != 13 || len(preview.Changes) != 2 {
		t.Fatalf("dry-run did not compute expected result: %+v (%v)", preview, err)
	}
	raw, _ := store.Load(ctx)
	if raw != before {
		t.Fatal("dry-run wrote catalog state")
	}
	input.DryRun = false
	input.Changes = append(input.Changes, Change{Action: "edit", RowID: "row_missing", Name: "Invalid"})
	if _, err = m.ApplyBatch(ctx, input); err == nil || !strings.Contains(err.Error(), "changes[2]") {
		t.Fatalf("expected late batch validation error, received %v", err)
	}
	raw, _ = store.Load(ctx)
	if raw != before {
		t.Fatal("partial write after failed batch")
	}
}

func TestBatchRequestIDIdempotencyRevisionAndReplay(t *testing.T) {
	m, _, _ := seedBatch(t)
	ctx := context.Background()
	revision := int64(12)
	input := BatchInput{RequestID: "create-harmless-001", ExpectedRevision: &revision, Changes: []Change{
		{Action: "create", Name: "History", Type: "movie", Titles: []ItemInput{{ID: "tt0000031", Title: "History"}}},
	}}
	first, err := m.ApplyBatch(ctx, input)
	if err != nil || !first.Changed || first.Replayed || first.Revision != 13 {
		t.Fatalf("initial create failed: %+v (%v)", first, err)
	}
	again, err := m.ApplyBatch(ctx, input)
	if err != nil || !again.Replayed || again.Changed || again.Changes[0].RowID != first.Changes[0].RowID || again.AppliedRevision != 13 {
		t.Fatalf("duplicate request must replay without creating another row: %+v (%v)", again, err)
	}
	s, _ := m.Read(ctx)
	if len(s.Rows) != 3 || s.Revision != 13 || len(s.RecentRequests) != 1 {
		t.Fatalf("retry mutated catalog state: %+v", s)
	}
	changed := input
	changed.Changes = []Change{{Action: "create", Name: "Different", Type: "movie"}}
	if _, err = m.ApplyBatch(ctx, changed); err == nil || !strings.Contains(err.Error(), "previously used") {
		t.Fatalf("request_id reused with different payload: %v", err)
	}
	stale := int64(12)
	if _, err = m.ApplyBatch(ctx, BatchInput{ExpectedRevision: &stale, Changes: []Change{{Action: "delete", RowID: "row_hidden"}}}); err == nil || !strings.Contains(err.Error(), "expected 12") {
		t.Fatalf("expected stale revision rejection, got %v", err)
	}
	next, err := m.ApplyBatch(ctx, BatchInput{Changes: []Change{{Action: "edit", RowID: "row_first", Name: "Updated"}}})
	if err != nil || next.Revision != 14 {
		t.Fatalf("subsequent update: %+v (%v)", next, err)
	}
	replayed, err := m.ApplyBatch(ctx, input)
	if err != nil || !replayed.Replayed || replayed.Revision != 14 || replayed.AppliedRevision != 13 {
		t.Fatalf("replay after later edit: %+v (%v)", replayed, err)
	}
}

func TestBatchNoOpAndSyncOnlyDoNotBumpRevision(t *testing.T) {
	m, _, _ := seedBatch(t)
	ctx := context.Background()
	noOp, err := m.ApplyBatch(ctx, BatchInput{Changes: []Change{{Action: "edit", RowID: "row_first", Name: "Movies"}}})
	if err != nil || noOp.Changed || noOp.SyncRequired || noOp.Revision != 12 {
		t.Fatalf("no-op should not save or sync: %+v (%v)", noOp, err)
	}
	syncResult, err := m.ApplyBatch(ctx, BatchInput{Changes: []Change{{Action: "sync"}}})
	if err != nil || syncResult.Changed || !syncResult.SyncRequired || syncResult.Revision != 12 {
		t.Fatalf("sync-only should leave Redis unchanged: %+v (%v)", syncResult, err)
	}
}

func TestBatchRejectsUnsafeInputs(t *testing.T) {
	m, store, before := seedBatch(t)
	ctx := context.Background()
	for _, in := range []BatchInput{
		{Changes: []Change{{Action: "edit", RowID: "row_first", ReplaceTitles: &[]ItemInput{}, AddTitles: []ItemInput{{ID: "tt0000001", Title: "Old"}}}}},
		{Changes: []Change{{Action: "edit", RowID: "row_first", MoveTitles: []TitleMove{{ID: "tt0000001", Before: "tt9999999"}}}}},
		{Changes: []Change{{Action: "edit", RowID: "row_first", RemoveTitleIDs: []string{"nm1234567"}}}},
		{Changes: []Change{{Action: "create", Name: "", Type: "movie"}}},
		{Changes: []Change{{Action: "sync", RowID: "row_first"}}},
		{Changes: []Change{{Action: "delete", RowID: "row_first", AddTitles: []ItemInput{{ID: "tt0000001", Title: "Bad"}}}}},
		{Changes: []Change{{Action: "oops"}}},
		{Changes: []Change{{Action: "sync"}, {Action: "sync"}}},
		{Changes: []Change{{Action: "create", Name: "Wrong", Type: "movie", Titles: []ItemInput{{ID: "tt0000001", Title: "Duplicate"}, {ID: "tt0000001", Title: "Duplicate"}}}}},
		{Changes: []Change{{Action: "edit", RowID: "row_first", MoveTitles: []TitleMove{{ID: "tt0000001", Before: "tt0000001"}}}}},
		{RequestID: " invalid id", Changes: []Change{{Action: "sync"}}},
	} {
		if _, err := m.ApplyBatch(ctx, in); err == nil {
			t.Errorf("accepted invalid input: %+v", in)
		}
	}
	raw, _ := store.Load(ctx)
	if !reflect.DeepEqual(raw, before) {
		t.Fatal("invalid inputs changed saved catalog state")
	}
}

func TestBatchConcurrentSameRequestIDCreatesExactlyOnce(t *testing.T) {
	m, _, _ := seedBatch(t)
	ctx := context.Background()
	in := BatchInput{RequestID: "parallel-create-01", Changes: []Change{{Action: "create", Name: "Concurrent", Type: "movie"}}}
	var wg sync.WaitGroup
	results := make([]BatchResult, 10)
	errs := make([]error, len(results))
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = m.ApplyBatch(ctx, in)
		}(i)
	}
	wg.Wait()
	var id string
	for i, result := range results {
		if errs[i] != nil || len(result.Changes) != 1 {
			t.Fatalf("concurrent batch %d failed: %+v %v", i, result, errs[i])
		}
		if id == "" {
			id = result.Changes[0].RowID
		}
		if result.Changes[0].RowID != id {
			t.Fatalf("multiple IDs created by same request_id: %q vs %q", result.Changes[0].RowID, id)
		}
	}
	s, _ := m.Read(ctx)
	if len(s.Rows) != 3 || s.Revision != 13 {
		t.Fatalf("concurrent retries duplicated a row: %+v", s)
	}
}
