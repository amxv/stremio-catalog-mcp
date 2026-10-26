package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// MetadataClient uses Cinemeta only to identify titles and the canonical
// episode ordering. We deliberately never expose unwatched episode synopses.
type MetadataClient struct {
	BaseURL string
	Client  *http.Client
}

type MediaMetadata struct {
	ID       string
	Name     string
	Poster   string
	Type     string
	Overview string
	Episodes []Episode
}

type Episode struct {
	ID       string
	Season   int
	Number   int
	Released string
	Name     string
	Overview string
}

func (c MetadataClient) Fetch(ctx context.Context, mediaType, imdbID string) (MediaMetadata, error) {
	if mediaType != "movie" && mediaType != "series" {
		return MediaMetadata{}, errors.New("media_type must be movie or series")
	}
	if !validHistoryID(imdbID) {
		return MediaMetadata{}, errors.New("invalid IMDb ID")
	}
	base := c.BaseURL
	if base == "" {
		base = "https://v3-cinemeta.strem.io"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && u.Hostname() == "127.0.0.1")) {
		return MediaMetadata{}, errors.New("invalid Cinemeta origin")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/meta/"+mediaType+"/"+imdbID+".json", nil)
	if err != nil {
		return MediaMetadata{}, err
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return MediaMetadata{}, fmt.Errorf("Cinemeta metadata unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return MediaMetadata{}, fmt.Errorf("Cinemeta metadata returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Meta struct {
			ID          string `json:"id"`
			Type        string `json:"type"`
			Name        string `json:"name"`
			Poster      string `json:"poster"`
			Description string `json:"description"`
			Videos      []struct {
				ID          string `json:"id"`
				Season      int    `json:"season"`
				Number      int    `json:"number"`
				Episode     int    `json:"episode"`
				Released    string `json:"released"`
				Name        string `json:"name"`
				Overview    string `json:"overview"`
				Description string `json:"description"`
			} `json:"videos"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result); err != nil {
		return MediaMetadata{}, fmt.Errorf("invalid Cinemeta metadata: %w", err)
	}
	m := result.Meta
	if m.ID != imdbID || (m.Type != "" && m.Type != mediaType) || m.Name == "" {
		return MediaMetadata{}, errors.New("Cinemeta returned mismatched title metadata")
	}
	meta := MediaMetadata{ID: m.ID, Type: mediaType, Name: m.Name, Poster: m.Poster, Overview: m.Description}
	if mediaType == "series" {
		if len(m.Videos) == 0 || len(m.Videos) > maxWatchedBits {
			return MediaMetadata{}, errors.New("series has no usable episode metadata")
		}
		ids := make(map[string]bool, len(m.Videos))
		positions := make(map[[2]int]bool, len(m.Videos))
		for _, video := range m.Videos {
			num := video.Number
			if num == 0 {
				num = video.Episode
			}
			position := [2]int{video.Season, num}
			if video.ID == "" || ids[video.ID] || positions[position] || video.Season < 0 || num < 0 {
				return MediaMetadata{}, errors.New("ambiguous Cinemeta episode list")
			}
			ids[video.ID] = true
			positions[position] = true
			overview := video.Overview
			if overview == "" {
				overview = video.Description
			}
			meta.Episodes = append(meta.Episodes, Episode{
				ID: video.ID, Season: video.Season, Number: num, Released: video.Released,
				Name: video.Name, Overview: overview,
			})
		}
		// Matches Stremio Core LibraryItemState.watched_bitfield ordering.
		sort.SliceStable(meta.Episodes, func(i, j int) bool {
			a, b := meta.Episodes[i], meta.Episodes[j]
			if a.Season != b.Season {
				return a.Season < b.Season
			}
			if a.Number != b.Number {
				return a.Number < b.Number
			}
			return a.Released < b.Released
		})
	}
	return meta, nil
}

func validHistoryID(id string) bool {
	if len(id) < 7 || len(id) > 14 || !strings.HasPrefix(id, "tt") {
		return false
	}
	for _, c := range id[2:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
