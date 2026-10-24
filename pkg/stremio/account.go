package stremio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/amxv/stremio-catalog-mcp/pkg/catalog"
)

// AccountAPI encapsulates Stremio's own account addon collection API. It is
// intentionally isolated because it isn't a separately versioned public API.
type AccountAPI struct {
	BaseURL string
	AuthKey string
	Client  *http.Client
	// BeforeReplace must successfully persist a private snapshot of the current
	// account collection before the whole-collection Stremio API write proceeds.
	BeforeReplace func(context.Context, []Descriptor) error
}

type Descriptor struct {
	Manifest      json.RawMessage `json:"manifest"`
	TransportURL  string          `json:"transportUrl"`
	TransportName string          `json:"transportName,omitempty"`
	Flags         json.RawMessage `json:"flags,omitempty"`
	// Preserve fields added by Stremio or other addons when rewriting the
	// account collection. The API uses whole-collection replacement.
	extra map[string]json.RawMessage
}

func (d *Descriptor) UnmarshalJSON(data []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	type fields struct {
		Manifest      json.RawMessage `json:"manifest"`
		TransportURL  string          `json:"transportUrl"`
		TransportName string          `json:"transportName"`
		Flags         json.RawMessage `json:"flags"`
	}
	var f fields
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	d.Manifest, d.TransportURL, d.TransportName, d.Flags = f.Manifest, f.TransportURL, f.TransportName, f.Flags
	for _, key := range []string{"manifest", "transportUrl", "transportName", "flags"} {
		delete(all, key)
	}
	d.extra = all
	return nil
}

func (d Descriptor) MarshalJSON() ([]byte, error) {
	result := make(map[string]json.RawMessage, len(d.extra)+4)
	for key, value := range d.extra {
		result[key] = value
	}
	result["manifest"] = d.Manifest
	url, _ := json.Marshal(d.TransportURL)
	result["transportUrl"] = url
	if d.TransportName != "" {
		name, _ := json.Marshal(d.TransportName)
		result["transportName"] = name
	}
	if len(d.Flags) != 0 {
		result["flags"] = d.Flags
	}
	return json.Marshal(result)
}

func (a AccountAPI) call(ctx context.Context, method string, payload map[string]any) (json.RawMessage, error) {
	if a.AuthKey == "" {
		return nil, errors.New("Stremio account is not linked")
	}
	base := a.BaseURL
	if base == "" {
		base = "https://api.strem.io"
	}
	if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://127.0.0.1:") {
		return nil, errors.New("invalid Stremio API origin")
	}
	payload["type"], payload["authKey"] = method, a.AuthKey
	encoded, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/"+strings.ToLower(method[:1])+method[1:], bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Stremio account request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Stremio account API returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("invalid Stremio response: %w", err)
	}
	if body.Error != nil {
		return nil, fmt.Errorf("Stremio account API error %d: %s", body.Error.Code, body.Error.Message)
	}
	if len(body.Result) == 0 || string(body.Result) == "null" {
		return nil, errors.New("Stremio account API returned no result")
	}
	return body.Result, nil
}

func (a AccountAPI) Get(ctx context.Context) ([]Descriptor, error) {
	raw, err := a.call(ctx, "AddonCollectionGet", map[string]any{"update": true})
	if err != nil {
		return nil, err
	}
	var result struct {
		Addons []Descriptor `json:"addons"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Addons == nil {
		return nil, errors.New("unexpected Stremio addonCollectionGet response (expected result.addons)")
	}
	for _, addon := range result.Addons {
		if addon.TransportURL == "" || len(addon.Manifest) == 0 {
			return nil, errors.New("refusing to rewrite malformed Stremio addon collection")
		}
	}
	return result.Addons, nil
}

func (a AccountAPI) Set(ctx context.Context, addons []Descriptor) error {
	if len(addons) == 0 {
		return errors.New("refusing to replace Stremio account with an empty addon collection")
	}
	_, err := a.call(ctx, "AddonCollectionSet", map[string]any{"addons": addons})
	return err
}

func descriptorID(d Descriptor) string {
	var manifest struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(d.Manifest, &manifest)
	return manifest.ID
}

// Sync only replaces our own descriptor while retaining ordering, flags and
// all metadata for installed streaming addons and Cinemeta.
func (a AccountAPI) Sync(ctx context.Context, state catalog.State, manifestURL string) error {
	current, err := a.Get(ctx)
	if err != nil {
		return err
	}
	if len(current) == 0 {
		return errors.New("refusing to synchronize an empty Stremio add-on collection")
	}
	manifest, err := json.Marshal(BuildManifest(state))
	if err != nil {
		return err
	}
	position := -1
	for i, entry := range current {
		if descriptorID(entry) == AddonID {
			if position != -1 {
				return errors.New("duplicate installed instances of our addon; refusing account rewrite")
			}
			position = i
		}
	}
	updated := append([]Descriptor(nil), current...)
	if position == -1 {
		updated = append(updated, Descriptor{
			Manifest: manifest, TransportURL: manifestURL, TransportName: "http",
			Flags: json.RawMessage(`{"official":false,"protected":false}`),
		})
	} else {
		entry := updated[position]
		entry.Manifest = manifest
		entry.TransportURL = manifestURL
		entry.TransportName = "http"
		updated[position] = entry
	}
	// Abort if another client edited the account since our original read.
	// The account endpoint has no transaction/CAS, so this reduces but cannot
	// completely eliminate races with other Stremio devices.
	latest, err := a.Get(ctx)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(latest, current) {
		return errors.New("Stremio add-on collection changed while syncing; retry to avoid overwriting other addons")
	}
	if a.BeforeReplace != nil {
		if err := a.BeforeReplace(ctx, latest); err != nil {
			return fmt.Errorf("refusing Stremio write because the account backup failed: %w", err)
		}
	}
	if err := a.Set(ctx, updated); err != nil {
		return err
	}
	verified, err := a.Get(ctx)
	if err != nil {
		return fmt.Errorf("Stremio write completed but verification failed: %w", err)
	}
	var found int
	for _, entry := range verified {
		if descriptorID(entry) == AddonID {
			found++
			var actual Manifest
			expected := BuildManifest(state)
			if json.Unmarshal(entry.Manifest, &actual) != nil || !reflect.DeepEqual(actual.Catalogs, expected.Catalogs) {
				return errors.New("Stremio reported an older manifest; sync may need retry")
			}
		}
	}
	if found != 1 || len(verified) != len(updated) {
		return errors.New("Stremio addon collection unexpectedly changed after sync")
	}
	for i, entry := range verified {
		if descriptorID(entry) != AddonID && !reflect.DeepEqual(entry, current[i]) {
			return errors.New("unrelated addon descriptor differs from original collection after sync")
		}
	}
	return nil
}
