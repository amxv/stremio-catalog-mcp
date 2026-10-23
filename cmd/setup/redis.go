package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Existing Redis instances must expose the Upstash-compatible HTTPS REST
// protocol: JSON command arrays, bearer authentication, and Redis EVAL.
// A redis:// TCP endpoint is not interchangeable with this HTTP API.
type redisREST struct {
	URL    string
	Token  string
	Client *http.Client
}

func validateRedisRESTURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || !strings.Contains(u.Hostname(), ".") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("Redis REST endpoint must be an HTTPS URL with a hostname, without credentials, query or fragment")
	}
	if strings.HasSuffix(u.Path, "/") {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}
	return u.String(), nil
}

func (r redisREST) command(ctx context.Context, args ...any) (json.RawMessage, error) {
	endpoint, err := validateRedisRESTURL(r.URL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.Token) == "" {
		return nil, errors.New("Redis REST token is missing")
	}
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Content-Type", "application/json")
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	resp, err := client.Do(req)
	if err != nil {
		// Do not echo the URL: an endpoint may include private identifiers.
		return nil, fmt.Errorf("Redis REST connectivity check failed (%T)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Redis REST returned HTTP %d (verify the endpoint and token)", resp.StatusCode)
	}
	var body struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return nil, errors.New("Redis REST returned an invalid JSON response")
	}
	if len(body.Error) > 0 && string(body.Error) != "null" {
		return nil, errors.New("Redis REST rejected the command; an Upstash-compatible Redis REST API with Lua EVAL is required")
	}
	if len(body.Result) == 0 {
		return nil, errors.New("Redis REST returned no result")
	}
	return body.Result, nil
}

// Probe on a new, expiring key. It validates the atomic Lua operations that
// the production catalog store needs, without reading or changing any catalog.
func (r redisREST) probe(ctx context.Context) error {
	result, err := r.command(ctx, "PING")
	if err != nil {
		return err
	}
	if string(result) != `"PONG"` {
		return errors.New("Redis REST PING did not return PONG")
	}
	keySuffix, err := newToken()
	if err != nil {
		return err
	}
	const script = `redis.call('SET', KEYS[1], 'verified', 'EX', 60); local v = redis.call('GET', KEYS[1]); redis.call('DEL', KEYS[1]); return v`
	result, err = r.command(ctx, "EVAL", script, 1, "stremio-catalog-mcp:setup-probe:"+keySuffix)
	if err != nil {
		return fmt.Errorf("Redis REST Lua EVAL check failed: %w", err)
	}
	if string(result) != `"verified"` {
		return errors.New("Redis REST Lua EVAL returned an unexpected response")
	}
	return nil
}

// This identifier includes the REST token in a one-way hash; it detects a
// different Redis service or credential on subsequent setup runs, without
// recording either secret in the local setup receipt.
func externalRedisID(endpoint, token string) string {
	value := sha256.Sum256([]byte(endpoint + "\x00" + token))
	return "existing-rest:" + hex.EncodeToString(value[:16])
}
