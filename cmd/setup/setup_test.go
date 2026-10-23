package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestDryRunDoesNotWriteFiles(t *testing.T) {
	root := t.TempDir()
	options, err := parseSettings([]string{"--project", "my-catalog"}, root)
	if err != nil {
		t.Fatal(err)
	}
	if options.apply || options.project != "my-catalog" || options.database != "my-catalog-redis" || options.baseURL != "https://my-catalog.vercel.app" {
		t.Fatalf("unexpected defaults: %+v", options)
	}
	if err := execute(context.Background(), root, options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".local-setup")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created a secret directory: %v", err)
	}
}

func TestConfigRejectsBadNamesAndOrigins(t *testing.T) {
	for _, args := range [][]string{
		{"--project", "My Project"},
		{"--project", "valid", "--database", "a/b"},
		{"--project", "valid", "--region", "../bad"},
		{"--project", "valid", "--base-url", "http://unsafe.example"},
		{"--project", "valid", "--base-url", "https://host.example/path"},
		{"--project", "valid", "--base-url", "https://user:password@host.example"},
	} {
		if _, err := parseSettings(args, "/tmp/repo"); err == nil {
			t.Errorf("accepted invalid args: %v", args)
		}
	}
}

func TestPrivateConnectionKeysAndNoOverwrite(t *testing.T) {
	root := t.TempDir()
	api, _ := newToken()
	feed, _ := newToken()
	keys := connectionKeys{Project: "catalog", DatabaseID: "db-example", BaseURL: "https://catalog.vercel.app", MCPKey: api, StremioKey: feed}
	if err := saveKeys(root, keys); err != nil {
		t.Fatal(err)
	}
	loaded, exists, err := loadKeys(root)
	if err != nil || !exists || loaded != keys {
		t.Fatalf("saved keys did not roundtrip: %+v, %v", loaded, err)
	}
	info, err := os.Stat(privatePath(root))
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("credentials file is not owner-only: %v, %v", info.Mode(), err)
	}
	if err := saveKeys(root, keys); err == nil {
		t.Fatal("overwrote local connection keys")
	}
	if !strings.Contains(mcpURL(keys), "/mcp?key="+api) {
		t.Fatal("wrong MCP URL")
	}
}

func TestCredentialsFilePermissions(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "api-key")
	if err := os.WriteFile(file, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateValue(file, "FAKE_SETUP_API_KEY", "test secret", false); err == nil {
		t.Fatal("accepted a world-readable secret file")
	}
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readPrivateValue(file, "FAKE_SETUP_API_KEY", "test secret", false)
	if err != nil || got != "secret" {
		t.Fatalf("private secret file not read correctly: %q, %v", got, err)
	}
}

type upstashFixture struct {
	server   *httptest.Server
	mu       sync.Mutex
	created  int
	lastBody map[string]any
	listed   []redisDatabase
	plan     string
}

func newUpstashFixture(t *testing.T) *upstashFixture {
	t.Helper()
	f := &upstashFixture{plan: "free"}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Basic dGVzdEBleGFtcGxlLmNvbTpzZWNyZXQ=" {
			http.Error(w, "auth missing", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/databases":
			_ = json.NewEncoder(w).Encode(f.listed)
		case r.Method == http.MethodPost && r.URL.Path == "/database":
			f.created++
			if err := json.NewDecoder(r.Body).Decode(&f.lastBody); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(redisDatabase{ID: "db-123", Name: "demo-redis", State: "active", Type: f.plan, TLS: true, Endpoint: "demo-redis.upstash.io", RestToken: "redis-rest-secret", Region: "us-east-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/database/db-123":
			_ = json.NewEncoder(w).Encode(redisDatabase{ID: "db-123", Name: "demo-redis", State: "active", Type: f.plan, TLS: true, Endpoint: "demo-redis.upstash.io", RestToken: "redis-rest-secret", Region: "us-east-1"})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *upstashFixture) client() upstashClient {
	return upstashClient{base: f.server.URL, email: "test@example.com", key: "secret", client: f.server.Client()}
}

func TestUpstashCreatesOnlyFreeDatabase(t *testing.T) {
	f := newUpstashFixture(t)
	db, err := f.client().database(context.Background(), "demo-redis", "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	if f.created != 1 || f.lastBody["plan"] != "free" || f.lastBody["tls"] != true || f.lastBody["eviction"] != false {
		t.Fatalf("unexpected Upstash provision parameters: %+v", f.lastBody)
	}
	url, token, err := db.credentials()
	if err != nil || url != "https://demo-redis.upstash.io" || token != "redis-rest-secret" {
		t.Fatalf("unexpected REST connection (not printed): err=%v, url=%s", err, url)
	}
}

func TestUpstashReusesExistingDatabase(t *testing.T) {
	f := newUpstashFixture(t)
	f.listed = []redisDatabase{{ID: "db-123", Name: "demo-redis"}}
	if _, err := f.client().database(context.Background(), "demo-redis", "us-east-1"); err != nil {
		t.Fatal(err)
	}
	if f.created != 0 {
		t.Fatal("duplicated an existing database")
	}
}

func TestUpstashRefusesUnexpectedPaidDatabase(t *testing.T) {
	f := newUpstashFixture(t)
	f.plan = "Pay as You Go"
	if _, err := f.client().database(context.Background(), "demo-redis", "us-east-1"); err == nil {
		t.Fatal("allowed an unexpected paid plan")
	}
}

func TestUpstashCredentialsNeverPointToOtherHosts(t *testing.T) {
	for _, endpoint := range []string{"https://evil.example", "http://demo.upstash.io", "https://user@demo.upstash.io", "https://demo.upstash.io/path", "https://demo.upstash.io:1234"} {
		if _, _, err := (redisDatabase{Endpoint: endpoint, RestToken: "token"}).credentials(); err == nil {
			t.Errorf("accepted unexpected endpoint: %s", endpoint)
		}
	}
}

func TestProvisionWithMockedVercelAndUpstashIsIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Vercel CLI stub uses POSIX shell")
	}
	root := t.TempDir()
	bin := t.TempDir()
	projectMarker := filepath.Join(root, "mock-vercel-project-exists")
	callLog := filepath.Join(root, "mock-vercel-calls")
	envResponse := filepath.Join(root, "mock-vercel-env.json")
	if err := os.WriteFile(envResponse, []byte(`{"envs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$MOCK_VERCEL_CALL_LOG"
case "$1" in
  project)
    if [ "$2" = inspect ] && [ ! -f "$MOCK_PROJECT_MARKER" ]; then
      echo 'Error: There is no project for "mock-catalog"' >&2
      exit 1
    fi
    if [ "$2" = create ]; then touch "$MOCK_PROJECT_MARKER"; fi
    ;;
  link)
    mkdir -p .vercel
    printf '%s\n' '{"projectName":"mock-catalog","projectId":"prj_mock","orgId":"team_mock"}' > .vercel/project.json
    ;;
  api)
    case "$2" in
      */domains*) printf '%s\n' '{"domains":[{"name":"mock-catalog.vercel.app","verified":true}]}' ;;
      *) cat "$MOCK_VERCEL_ENV_RESPONSE" ;;
    esac
    ;;
  env)
    cat >/dev/null
    ;;
  --prod)
    echo 'Production deployment complete.'
    ;;
  *) echo "unexpected Vercel CLI action" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "vercel"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_VERCEL_CALL_LOG", callLog)
	t.Setenv("MOCK_PROJECT_MARKER", projectMarker)
	t.Setenv("MOCK_VERCEL_ENV_RESPONSE", envResponse)
	t.Setenv("UPSTASH_EMAIL", "test@example.com")
	t.Setenv("UPSTASH_API_KEY", "secret")
	fixture := newUpstashFixture(t)
	settings := settings{
		project: "mock-catalog", database: "demo-redis", region: "us-east-1",
		baseURL: "https://mock-catalog.vercel.app", noGit: true, apply: true, yes: true,
	}
	var copied string
	upstash := func(email, key string) upstashClient {
		if email != "test@example.com" || key != "secret" {
			t.Fatalf("unexpected Upstash credential source")
		}
		return fixture.client()
	}
	clipboard := func(value string) error { copied = value; return nil }
	if err := provisionWith(context.Background(), root, settings, upstash, clipboard); err != nil {
		t.Fatal(err)
	}
	keys, found, err := loadKeys(root)
	if err != nil || !found || keys.Project != "mock-catalog" || copied != mcpURL(keys) {
		t.Fatalf("local receipt/clipboard mismatch: found=%v, err=%v", found, err)
	}
	if fixture.created != 1 {
		t.Fatal("database was not provisioned exactly once")
	}
	data, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), keys.MCPKey) || strings.Contains(string(data), keys.StremioKey) || strings.Contains(string(data), "redis-rest-secret") {
		t.Fatal("mock deployment logged a credential")
	}
	if strings.Count(string(data), "env add") != 5 {
		t.Fatalf("expected 5 production variables, calls: %s", data)
	}

	// Second run: the project and database already exist, all env vars are
	// configured and local connection keys must be reused without rotation.
	fixture.listed = []redisDatabase{{ID: "db-123", Name: "demo-redis"}}
	existing := struct {
		Envs []struct {
			Key    string   `json:"key"`
			Target []string `json:"target"`
		} `json:"envs"`
	}{}
	for _, key := range []string{"MCP_API_KEY", "STREMIO_FEED_TOKEN", "UPSTASH_REDIS_REST_URL", "UPSTASH_REDIS_REST_TOKEN", "PUBLIC_BASE_URL"} {
		existing.Envs = append(existing.Envs, struct {
			Key    string   `json:"key"`
			Target []string `json:"target"`
		}{key, []string{"production"}})
	}
	encoded, _ := json.Marshal(existing)
	if err := os.WriteFile(envResponse, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := provisionWith(context.Background(), root, settings, upstash, clipboard); err != nil {
		t.Fatal(err)
	}
	after, _, err := loadKeys(root)
	if err != nil || after != keys || fixture.created != 1 {
		t.Fatal("a rerun rotated tokens or created a second database")
	}
	data, err = os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "env add") != 5 || strings.Count(string(data), "project create") != 1 {
		t.Fatal("repeated provisioning during idempotent rerun")
	}
}
