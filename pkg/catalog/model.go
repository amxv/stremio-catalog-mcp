package catalog

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxRows  = 24
	MaxItems = 100
)

var imdbID = regexp.MustCompile(`^tt[0-9]{5,12}$`)

type Item struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	Poster string `json:"poster,omitempty"`
}

// Row IDs do not change when the user renames or reorders a row.
type Row struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Items []Item `json:"items"`
}

type State struct {
	Revision int64 `json:"revision"`
	Rows     []Row `json:"rows"`
}

type ItemInput struct {
	ID    string `json:"id,omitempty" jsonschema:"IMDb ID (tt...), or omit to resolve by title"`
	Title string `json:"title,omitempty" jsonschema:"Movie or series title, used for Cinemeta lookup"`
	Year  int    `json:"year,omitempty" jsonschema:"Release year to disambiguate matching titles"`
}

type UpsertInput struct {
	ID       string      `json:"id,omitempty" jsonschema:"Existing row ID to update; omit to create a new row"`
	Name     string      `json:"name,omitempty" jsonschema:"Displayed Stremio row name; omit to leave unchanged"`
	Type     string      `json:"type,omitempty" jsonschema:"movie or series; required for new rows"`
	Items    []ItemInput `json:"items,omitempty" jsonschema:"Items to append or replace; an empty array with mode replace clears the row"`
	Mode     string      `json:"mode,omitempty" jsonschema:"append (default) or replace, to control how items are applied"`
	Position *int        `json:"position,omitempty" jsonschema:"Optional zero-based position among this add-on's rows"`
}

func ValidType(t string) bool { return t == "movie" || t == "series" }

func ValidIMDbID(id string) bool { return imdbID.MatchString(id) }

func ValidName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && utf8.RuneCountInString(name) <= 80 && !strings.ContainsAny(name, "\r\n\x00")
}

func NewID() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "row_" + hex.EncodeToString(b[:]), nil
}

func ValidateItems(items []Item, rowType string) error {
	if len(items) > MaxItems {
		return fmt.Errorf("maximum %d titles per row", MaxItems)
	}
	seen := map[string]bool{}
	for _, item := range items {
		if !ValidIMDbID(item.ID) || item.Type != rowType || strings.TrimSpace(item.Title) == "" {
			return errors.New("invalid or mixed-type IMDb item")
		}
		if seen[item.ID] {
			return fmt.Errorf("duplicate IMDb ID %s", item.ID)
		}
		seen[item.ID] = true
	}
	return nil
}

func (s State) Validate() error {
	if len(s.Rows) > MaxRows {
		return fmt.Errorf("maximum %d catalog rows", MaxRows)
	}
	seen := map[string]bool{}
	for _, row := range s.Rows {
		if !strings.HasPrefix(row.ID, "row_") || seen[row.ID] || !ValidName(row.Name) || !ValidType(row.Type) {
			return fmt.Errorf("invalid row %q", row.ID)
		}
		seen[row.ID] = true
		if err := ValidateItems(row.Items, row.Type); err != nil {
			return fmt.Errorf("row %s: %w", row.ID, err)
		}
	}
	return nil
}
