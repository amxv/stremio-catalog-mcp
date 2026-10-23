package catalog

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeResolver struct{}

func (fakeResolver) Resolve(_ context.Context, mediaType string, in ItemInput) (Item, error) {
	if !ValidIMDbID(in.ID) || in.Title == "" {
		return Item{}, errors.New("test resolver: invalid media")
	}
	return Item{ID: in.ID, Type: mediaType, Title: in.Title}, nil
}

func TestCatalogLifecycle(t *testing.T) {
	m := Manager{Store: &MemoryStore{}, Resolver: fakeResolver{}}
	ctx := context.Background()
	a, err := m.Upsert(ctx, UpsertInput{Name: "Whodunits", Type: "series", Items: []ItemInput{{ID: "tt0491738", Title: "Psych"}}})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || len(a.Items) != 1 || a.Items[0].Title != "Psych" {
		t.Fatalf("invalid initial row: %+v", a)
	}
	b, err := m.Upsert(ctx, UpsertInput{Name: "Movies", Type: "movie"})
	if err != nil {
		t.Fatal(err)
	}
	position := 0
	a, err = m.Upsert(ctx, UpsertInput{ID: a.ID, Name: "Murder and Comedy", Mode: "append", Position: &position, Items: []ItemInput{{ID: "tt0491738", Title: "Psych"}, {ID: "tt0312172", Title: "Monk"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Items) != 2 || a.Name != "Murder and Comedy" {
		t.Fatalf("rename or append failed: %+v", a)
	}
	a, err = m.RemoveItems(ctx, a.ID, []string{"tt0491738"})
	if err != nil || len(a.Items) != 1 || a.Items[0].ID != "tt0312172" {
		t.Fatalf("remove failed: %+v %v", a, err)
	}
	a, err = m.Upsert(ctx, UpsertInput{ID: a.ID, Mode: "replace"})
	if err != nil || len(a.Items) != 0 {
		t.Fatalf("clear failed: %+v %v", a, err)
	}
	if err := m.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	s, err := m.Read(ctx)
	if err != nil || len(s.Rows) != 1 || s.Rows[0].ID != a.ID || s.Revision != 6 {
		t.Fatalf("unexpected final state: %+v %v", s, err)
	}
}

func TestConcurrentRowWrites(t *testing.T) {
	m := Manager{Store: &MemoryStore{}, Resolver: fakeResolver{}}
	ctx := context.Background()
	var wg sync.WaitGroup
	var errs []error
	var mu sync.Mutex
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := m.Upsert(ctx, UpsertInput{Name: "Parallel", Type: "movie"})
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent updates failed: %v", errs)
	}
	s, err := m.Read(ctx)
	if err != nil || len(s.Rows) != 8 || s.Revision != 8 {
		t.Fatalf("lost concurrent write: %+v %v", s, err)
	}
}

func TestInputValidation(t *testing.T) {
	m := Manager{Store: &MemoryStore{}, Resolver: fakeResolver{}}
	for _, request := range []UpsertInput{
		{Name: "unknown", Type: "tv"},
		{Name: "bad\nname", Type: "series"},
		{Name: "test", Type: "movie", Items: []ItemInput{{ID: "nm123456", Title: "bad"}}},
		{Name: "test", Type: "series", Mode: "something"},
	} {
		if _, err := m.Upsert(context.Background(), request); err == nil {
			t.Errorf("accepted invalid request: %+v", request)
		}
	}
}
