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
		{"--project", "valid", "--redis-mode", "unsupported"},
		{"--project", "valid", "--redis-rest-url", "https://redis.example"},
		{"--project", "valid", "--redis-mode", "existing", "--redis-rest-url", "https://redis.example", "--redis-rest-url-file", "/tmp/redis-url"},
		{"--project", "valid", "--redis-mode", "existing", "--upstash-key-file", "/tmp/developer-key"},
		{"--project", "valid", "--stremio-browser", "--stremio-key-file", "/tmp/session"},
		{"--project", "valid", "--stremio-browser", "--stremio-browser-session", "stremio-session"},
		{"--project", "valid", "--stremio-browser-session", "../unsafe"},
	} {
		if _, err := parseSettings(args, "/tmp/repo"); err == nil {
			t.Errorf("accepted invalid args: %v", args)
		}
	}
}

func TestExistingRedisConfigPlan(t *testing.T) {
	options, err := parseSettings([]string{"--project", "private-catalog", "--redis-mode", "existing", "--redis-rest-url", "https://db.vendor.example/api", "--stremio-browser"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if options.redisMode != "existing" || options.redisURL != "https://db.vendor.example/api" || !options.stremioBrowser {
		t.Fatalf("invalid existing Redis setup options: %+v", options)
	}
	if err := execute(context.Background(), t.TempDir(), options); err != nil {
		t.Fatal(err)
	}
}

func TestExistingRESTValidation(t *testing.T) {
	for _, address := range []string{"redis://redis.example:6379", "http://redis.example", "https://user:pass@redis.example", "https://redis.example/?token=secret", "https://redis.example/#fragment", "https://localhost"} {
		if _, err := validateRedisRESTURL(address); err == nil {
			t.Errorf("accepted invalid Redis REST endpoint: %s", address)
		}
	}
	for _, address := range []string{"https://db.upstash.io", "https://redis.vendor.example/api/"} {
		if _, err := validateRedisRESTURL(address); err != nil {
			t.Errorf("rejected valid Redis REST endpoint: %s: %v", address, err)
		}
	}
}

func TestExistingRESTProbeAndFingerprint(t *testing.T) {
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer existing-rest-token" {
			http.Error(w, "forbidden", http.StatusUnauthorized)
			return
		}
		var args []any
		if json.NewDecoder(r.Body).Decode(&args) != nil || len(args) == 0 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		requests++
		switch args[0] {
		case "PING":
			_, _ = w.Write([]byte(`{"result":"PONG"}`))
		case "EVAL":
			if len(args) < 4 || !strings.HasPrefix(args[3].(string), "stremio-catalog-mcp:setup-probe:") {
				http.Error(w, "wrong probe key", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"result":"verified"}`))
		default:
			http.Error(w, "unexpected command", http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	redis := redisREST{URL: srv.URL, Token: "existing-rest-token", Client: srv.Client()}
	if err := redis.probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("expected PING and EVAL, got %d calls", requests)
	}
	a := externalRedisID(srv.URL, redis.Token)
	if !strings.HasPrefix(a, "existing-rest:") || strings.Contains(a, redis.Token) || a == externalRedisID(srv.URL, "another-token") {
		t.Fatal("bad Redis fingerprint for setup receipt")
	}
	redis.Token = "invalid-token"
	if err := redis.probe(context.Background()); err == nil {
		t.Fatal("unexpected successful probe with an invalid token")
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

func TestProvisionWithExistingRedisNeedsNoDeveloperKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Vercel stub uses POSIX shell")
	}
	root := t.TempDir()
	bin := t.TempDir()
	log := filepath.Join(root, "vercel-calls")
	varPath := filepath.Join(root, "env-response.json")
	if err := os.WriteFile(varPath, []byte(`{"envs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stub := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$MOCK_VERCEL_CALL_LOG"
case "$1" in
  project) if [ "$2" = inspect ] && [ ! -e project-exists ]; then echo 'not found' >&2; exit 1; fi
           if [ "$2" = create ]; then touch project-exists; fi ;;
  link) mkdir -p .vercel
        printf '%s\n' '{"projectName":"my-catalog","projectId":"prj_example","orgId":"team_example"}' > .vercel/project.json ;;
  api) case "$2" in */domains*) printf '%s\n' '{"domains":[{"name":"my-catalog.vercel.app","verified":true}]}' ;;
                *) cat "$MOCK_ENV_RESPONSE" ;; esac ;;
  env) cat >/dev/null ;;
  --prod) echo deployed ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "vercel"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_VERCEL_CALL_LOG", log)
	t.Setenv("MOCK_ENV_RESPONSE", varPath)
	t.Setenv("UPSTASH_EMAIL", "")
	t.Setenv("UPSTASH_API_KEY", "")
	t.Setenv("STREMIO_AUTH_KEY", "")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var args []any
		if json.NewDecoder(r.Body).Decode(&args) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if args[0] == "PING" {
			_, _ = w.Write([]byte(`{"result":"PONG"}`))
			return
		}
		if args[0] == "EVAL" {
			_, _ = w.Write([]byte(`{"result":"verified"}`))
			return
		}
		http.Error(w, "unsupported command", http.StatusBadRequest)
	}))
	defer srv.Close()
	urlFile, tokenFile := filepath.Join(root, "endpoint.txt"), filepath.Join(root, "rest-token.txt")
	if err := os.WriteFile(urlFile, []byte(srv.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte("private-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := settings{project: "my-catalog", redisMode: "existing", redisURLFile: urlFile, redisTokenFile: tokenFile, baseURL: "https://my-catalog.vercel.app", noGit: true, apply: true, yes: true}
	noDeveloperAPI := func(_, _ string) upstashClient {
		t.Fatal("Developer API must not be used for existing Redis mode")
		return upstashClient{}
	}
	makeRedis := func(endpoint, token string) redisREST {
		return redisREST{URL: endpoint, Token: token, Client: srv.Client()}
	}
	var clipboardValue string
	clipboard := func(text string) error { clipboardValue = text; return nil }
	if err := provisionWithRedis(context.Background(), root, config, noDeveloperAPI, makeRedis, clipboard); err != nil {
		t.Fatal(err)
	}
	keys, exists, err := loadKeys(root)
	if err != nil || !exists || clipboardValue != mcpURL(keys) || keys.DatabaseID != externalRedisID(srv.URL, "private-token") {
		t.Fatal("existing Redis setup receipt or clipboard is inconsistent")
	}
	firstLog, _ := os.ReadFile(log)
	if strings.Contains(string(firstLog), "private-token") || strings.Contains(string(firstLog), keys.MCPKey) || strings.Contains(string(firstLog), srv.URL) {
		t.Fatal("Vercel command log exposed secrets")
	}
	if strings.Count(string(firstLog), "env add") != 5 {
		t.Fatalf("expected 5 private config values, log: %s", firstLog)
	}
	// Rerunning reuses keys, does not provision databases or create extra envs.
	var all struct {
		Envs []struct {
			Key    string   `json:"key"`
			Target []string `json:"target"`
		} `json:"envs"`
	}
	for _, name := range []string{"MCP_API_KEY", "STREMIO_FEED_TOKEN", "UPSTASH_REDIS_REST_URL", "UPSTASH_REDIS_REST_TOKEN", "PUBLIC_BASE_URL"} {
		all.Envs = append(all.Envs, struct {
			Key    string   `json:"key"`
			Target []string `json:"target"`
		}{name, []string{"production"}})
	}
	data, _ := json.Marshal(all)
	if err := os.WriteFile(varPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := provisionWithRedis(context.Background(), root, config, noDeveloperAPI, makeRedis, clipboard); err != nil {
		t.Fatal(err)
	}
	other, _, err := loadKeys(root)
	if err != nil || other != keys {
		t.Fatal("rerun rotated connection keys")
	}
	secondLog, _ := os.ReadFile(log)
	if strings.Count(string(secondLog), "env add") != 5 {
		t.Fatal("rerun re-created secrets")
	}
	// A different token means a different credential fingerprint; refuse rebinding.
	if err := os.WriteFile(tokenFile, []byte("different-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := provisionWithRedis(context.Background(), root, config, noDeveloperAPI, makeRedis, clipboard); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatal("silent Redis token rotation was not prevented")
	}
}

func TestAgentBrowserCapturesSessionWithoutPrinting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock Agent Browser uses POSIX shell")
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "browser-commands")
	responseFile := filepath.Join(t.TempDir(), "response.json")
	stub := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$MOCK_BROWSER_CALLS"
case "$*" in
  *--json*eval*) cat "$MOCK_BROWSER_RESPONSE" ;;
  *open*) echo 'Stremio Web opened' ;;
  *close*) echo 'Browser closed' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "agent-browser"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOCK_BROWSER_CALLS", log)
	t.Setenv("MOCK_BROWSER_RESPONSE", responseFile)
	const fakeKey = "stremio-session-secret-from-browser-fixture"
	writeResponse := func(origin string, key any) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"success": true, "data": map[string]any{"origin": origin, "result": key}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(responseFile, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeResponse("https://web.stremio.com/", fakeKey)
	got, err := browserSessionKey(context.Background(), "signed-in-test")
	if err != nil || got != fakeKey {
		t.Fatalf("signed-in session did not resolve: %v", err)
	}
	key, err := sessionViaAgentBrowser(context.Background(), settings{stremioBrowser: true})
	if err != nil || key != fakeKey {
		t.Fatalf("headed Agent Browser flow failed: %v", err)
	}
	logBytes, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logBytes), fakeKey) || !strings.Contains(string(logBytes), "--headed") || !strings.Contains(string(logBytes), "close") {
		t.Fatal("headed browser flow leaked the session or didn't close its browser")
	}
	writeResponse("https://untrusted.example/", fakeKey)
	if _, err := browserSessionKey(context.Background(), "signed-in-test"); err == nil {
		t.Fatal("accepted session value from an unrelated origin")
	}
	writeResponse("https://web.stremio.com/", nil)
	missing, err := browserSessionKey(context.Background(), "signed-in-test")
	if err != nil || missing != "" {
		t.Fatalf("unsigned-in browser should return no key: %v", err)
	}
}
