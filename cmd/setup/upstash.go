package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const upstashAPI = "https://api.upstash.com/v2/redis"

type upstashClient struct {
	base   string
	email  string
	key    string
	client *http.Client
}

type redisDatabase struct {
	ID           string `json:"database_id"`
	Name         string `json:"database_name"`
	State        string `json:"state"`
	Type         string `json:"type"`
	DatabaseType string `json:"database_type"`
	Region       string `json:"primary_region"`
	Endpoint     string `json:"endpoint"`
	RestURL      string `json:"rest_url"`
	RestToken    string `json:"rest_token"`
	TLS          bool   `json:"tls"`
	Eviction     bool   `json:"eviction"`
}

func (u upstashClient) request(ctx context.Context, method, route string, payload any, dest any) error {
	if u.email == "" || u.key == "" {
		return errors.New("Upstash account email and developer API key are required")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	base := u.base
	if base == "" {
		base = upstashAPI
	}
	req, err := http.NewRequestWithContext(ctx, method, base+route, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.email+":"+u.key)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "stremio-catalog-mcp-setup/1.0")
	client := u.client
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Upstash request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Never echo provider bodies: an upstream error can include credentials.
		if resp.StatusCode == http.StatusBadRequest {
			return errors.New("Upstash rejected the Free database request (possibly the account's Free limit); no paid plan was attempted")
		}
		return fmt.Errorf("Upstash API returned HTTP %d (check your developer API credentials and permissions)", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(dest)
}

func (u upstashClient) database(ctx context.Context, name, region string) (redisDatabase, error) {
	var all []redisDatabase
	if err := u.request(ctx, http.MethodGet, "/databases", nil, &all); err != nil {
		return redisDatabase{}, err
	}
	var matches []redisDatabase
	for _, db := range all {
		if db.Name == name {
			matches = append(matches, db)
		}
	}
	if len(matches) > 1 {
		return redisDatabase{}, fmt.Errorf("more than one Upstash database named %q; select one manually", name)
	}
	var db redisDatabase
	if len(matches) == 1 {
		if matches[0].ID == "" {
			return redisDatabase{}, errors.New("existing Upstash database has no ID")
		}
		if err := u.request(ctx, http.MethodGet, "/database/"+url.PathEscape(matches[0].ID), nil, &db); err != nil {
			return redisDatabase{}, err
		}
	} else {
		payload := map[string]any{
			"database_name":  name,
			"platform":       "aws",
			"primary_region": region,
			"read_regions":   []string{},
			"plan":           "free",
			"eviction":       false,
			"tls":            true,
		}
		if err := u.request(ctx, http.MethodPost, "/database", payload, &db); err != nil {
			return redisDatabase{}, err
		}
	}
	if db.Name != name || db.ID == "" {
		return redisDatabase{}, errors.New("Upstash returned an unexpected database identity")
	}
	if db.State != "active" || !db.TLS || db.Eviction {
		return redisDatabase{}, errors.New("Upstash database must be active, TLS-enabled, and non-evicting")
	}
	plan := db.Type
	if plan == "" {
		plan = db.DatabaseType
	}
	if plan != "" && !strings.EqualFold(plan, "free") {
		return redisDatabase{}, errors.New("refusing to use an unexpected paid Upstash database")
	}
	if db.Region != "" && db.Region != region {
		return redisDatabase{}, fmt.Errorf("existing Upstash database is in %s, not %s; choose its existing region", db.Region, region)
	}
	if _, _, err := db.credentials(); err != nil {
		return redisDatabase{}, err
	}
	return db, nil
}

func (db redisDatabase) credentials() (string, string, error) {
	endpoint := db.RestURL
	if endpoint == "" {
		endpoint = db.Endpoint
	}
	if endpoint == "" || db.RestToken == "" {
		return "", "", errors.New("Upstash did not return REST credentials; check your account's Developer API permissions")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", err
	}
	if !strings.Contains(u.Hostname(), ".") {
		u.Host += ".upstash.io"
	}
	if u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".upstash.io") || u.User != nil || u.Port() != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("Upstash returned an unexpected REST endpoint")
	}
	return strings.TrimRight(u.String(), "/"), db.RestToken, nil
}

func verifyStremioKey(ctx context.Context, sessionKey string, client *http.Client) error {
	if len(strings.TrimSpace(sessionKey)) < 20 {
		return errors.New("Stremio session key is missing or too short")
	}
	body, _ := json.Marshal(map[string]any{"type": "AddonCollectionGet", "authKey": sessionKey, "update": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.strem.io/api/addonCollectionGet", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not validate Stremio account session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Stremio session verification returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Result struct {
			Addons []json.RawMessage `json:"addons"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil || result.Result.Addons == nil {
		return errors.New("Stremio session verification returned no installed-addon collection")
	}
	return nil
}
