package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Resolver interface {
	Resolve(context.Context, string, ItemInput) (Item, error)
}

// Cinemeta provides IDs, names and posters; this server never supplies streams.
type Cinemeta struct {
	BaseURL string
	Client  *http.Client
}

type meta struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Poster      string `json:"poster"`
	ReleaseInfo string `json:"releaseInfo"`
}

// PreviewLink is the standard Stremio metadata link. Android TV reads links
// with categories "imdb", "Genres", and "Cast" from catalog previews.
type PreviewLink struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	URL      string `json:"url,omitempty"`
}

// Preview is fetched from Cinemeta when Stremio requests a custom catalog.
// It is kept separate from the user-curated rows: agents only choose titles,
// while runtime, genres, cast, ratings, and descriptions come from Cinemeta.
type Preview struct {
	ID          string        `json:"id"`
	Type        string        `json:"type"`
	Poster      string        `json:"poster,omitempty"`
	Background  string        `json:"background,omitempty"`
	Description string        `json:"description,omitempty"`
	ReleaseInfo string        `json:"releaseInfo,omitempty"`
	Runtime     string        `json:"runtime,omitempty"`
	IMDBRating  string        `json:"imdbRating,omitempty"`
	Links       []PreviewLink `json:"links,omitempty"`
}

type PreviewResolver interface {
	FetchPreview(context.Context, string, string) (Preview, error)
}

// FetchPreview reads complete Cinemeta metadata, not the sparse search result.
// It validates the returned identity before any details reach the catalog.
func (c Cinemeta) FetchPreview(ctx context.Context, mediaType, id string) (Preview, error) {
	if !ValidType(mediaType) || !ValidIMDbID(id) {
		return Preview{}, errors.New("invalid Cinemeta preview type or IMDb ID")
	}
	var response struct {
		Meta Preview `json:"meta"`
	}
	if err := c.fetch(ctx, "/meta/"+mediaType+"/"+id+".json", &response); err != nil {
		return Preview{}, err
	}
	if response.Meta.ID != id || (response.Meta.Type != "" && response.Meta.Type != mediaType) {
		return Preview{}, fmt.Errorf("Cinemeta returned unexpected preview identity for %s", id)
	}
	return response.Meta, nil
}

func (c Cinemeta) fetch(ctx context.Context, route string, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://v3-cinemeta.strem.io"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+route, nil)
	if err != nil {
		return err
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Cinemeta returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func (c Cinemeta) Resolve(ctx context.Context, mediaType string, input ItemInput) (Item, error) {
	if !ValidType(mediaType) {
		return Item{}, errors.New("media type must be movie or series")
	}
	input.ID = strings.TrimSpace(input.ID)
	input.Title = strings.TrimSpace(input.Title)
	var m meta
	if input.ID != "" {
		if !ValidIMDbID(input.ID) {
			return Item{}, fmt.Errorf("%q is not an IMDb ID (tt...)", input.ID)
		}
		var resp struct {
			Meta meta `json:"meta"`
		}
		err := c.fetch(ctx, "/meta/"+mediaType+"/"+input.ID+".json", &resp)
		if err != nil || resp.Meta.ID == "" {
			if input.Title == "" {
				return Item{}, fmt.Errorf("unable to resolve %s in Cinemeta; provide the title as a fallback", input.ID)
			}
			// Explicit IDs remain valid for Cinemeta/installed playback addons.
			return Item{ID: input.ID, Type: mediaType, Title: input.Title, Year: input.Year}, nil
		}
		m = resp.Meta
	} else {
		if input.Title == "" {
			return Item{}, errors.New("each item requires an IMDb ID or a title")
		}
		var resp struct {
			Metas []meta `json:"metas"`
		}
		route := "/catalog/" + mediaType + "/top/search=" + url.PathEscape(input.Title) + ".json"
		if err := c.fetch(ctx, route, &resp); err != nil {
			return Item{}, fmt.Errorf("Cinemeta search for %q: %w", input.Title, err)
		}
		var matches []meta
		for _, candidate := range resp.Metas {
			if !ValidIMDbID(candidate.ID) || candidate.Type != mediaType || normalizeTitle(candidate.Name) != normalizeTitle(input.Title) {
				continue
			}
			if input.Year > 0 && yearFromRelease(candidate.ReleaseInfo) != input.Year {
				continue
			}
			matches = append(matches, candidate)
		}
		if len(matches) != 1 {
			return Item{}, fmt.Errorf("%q: found %d exact Cinemeta matches; supply its IMDb ID or release year to disambiguate", input.Title, len(matches))
		}
		m = matches[0]
	}
	if !ValidIMDbID(m.ID) || (m.Type != "" && m.Type != mediaType) {
		return Item{}, fmt.Errorf("Cinemeta returned unexpected identity for %q", input.Title)
	}
	return Item{ID: m.ID, Type: mediaType, Title: m.Name, Year: yearFromRelease(m.ReleaseInfo), Poster: m.Poster}, nil
}

func normalizeTitle(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

func yearFromRelease(s string) int {
	if len(s) < 4 {
		return 0
	}
	n, err := strconv.Atoi(s[:4])
	if err != nil || n < 1880 || n > time.Now().Year()+5 {
		return 0
	}
	return n
}

// ResolveItems limits concurrent Cinemeta requests, preserving the input order.
func ResolveItems(ctx context.Context, resolver Resolver, mediaType string, inputs []ItemInput) ([]Item, error) {
	if len(inputs) > MaxItems {
		return nil, fmt.Errorf("maximum %d items per request", MaxItems)
	}
	items := make([]Item, len(inputs))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var firstErr error
	var once sync.Once
	workerCount := 8
	if len(inputs) < workerCount {
		workerCount = len(inputs)
	}
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				item, err := resolver.Resolve(ctx, mediaType, inputs[i])
				if err != nil {
					once.Do(func() { firstErr = fmt.Errorf("item %d: %w", i+1, err) })
					continue
				}
				items[i] = item
			}
		}()
	}
	for i := range inputs {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return items, nil
}
