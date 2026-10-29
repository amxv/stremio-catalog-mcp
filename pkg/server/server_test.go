package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Test every published tool rather than only testing the metadata helper: SDK
// serialization is the contract clients will read from tools/list.
func TestMCPToolAnnotations(t *testing.T) {
	t.Setenv("MCP_API_KEY", "test-only-key")
	t.Setenv("TV_OPEN_ENABLED", "false")
	req := httptest.NewRequest(http.MethodPost, "/mcp?key=test-only-key", strings.NewReader(`{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("tools/list returned HTTP %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Result struct {
			Tools []struct {
				Name        string                     `json:"name"`
				Description string                     `json:"description"`
				Annotations map[string]json.RawMessage `json:"annotations"`
				InputSchema json.RawMessage            `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Result.Tools) != 5 {
		t.Fatalf("expected exactly 5 tools, received %d", len(response.Result.Tools))
	}
	expected := map[string]bool{
		"catalogs_get": true, "catalogs_update": true,
		"home_layout_get": true, "home_layout_update": true,
		"watch_history": true,
	}
	for _, tool := range response.Result.Tools {
		if !expected[tool.Name] {
			t.Fatalf("unexpected or duplicated tool %q", tool.Name)
		}
		delete(expected, tool.Name)
		t.Run(tool.Name, func(t *testing.T) {
			var readOnly bool
			if err := json.Unmarshal(tool.Annotations["readOnlyHint"], &readOnly); err != nil || !readOnly {
				t.Fatalf("tool %q must advertise readOnlyHint=true: %s (error: %v)", tool.Name, tool.Annotations["readOnlyHint"], err)
			}
			if _, present := tool.Annotations["openWorldHint"]; present {
				t.Fatalf("tool %q must omit openWorldHint, not set it to false", tool.Name)
			}
			if tool.Name == "home_layout_update" || tool.Name == "catalogs_update" || tool.Name == "catalogs_get" || tool.Name == "watch_history" {
				var input struct {
					Properties map[string]json.RawMessage `json:"properties"`
				}
				if err := json.Unmarshal(tool.InputSchema, &input); err != nil {
					t.Fatalf("invalid home layout input schema: %v", err)
				}
				fields := []string{"visibility", "row_moves", "addon_moves", "dry_run"}
				if tool.Name == "catalogs_update" {
					fields = []string{"changes", "dry_run", "expected_revision", "request_id"}
				} else if tool.Name == "catalogs_get" {
					fields = []string{"row_id", "include_titles", "include_install_url"}
				} else if tool.Name == "watch_history" {
					fields = []string{"action", "imdb_id", "imdb_ids", "media_type", "season", "episodes", "limit", "offset"}
				}
				for _, field := range fields {
					if len(input.Properties[field]) == 0 {
						t.Errorf("home_layout_update schema missing %s", field)
					}
				}
				if tool.Name == "watch_history" {
					for _, forbidden := range []string{"query", "year"} {
						if _, ok := input.Properties[forbidden]; ok {
							t.Errorf("watch_history must not offer title-based %s input", forbidden)
						}
					}
					for _, phrase := range []string{"IMDb", "check", "imdb_ids", "mark_watched", "mark_unwatched", "episodes"} {
						if !strings.Contains(tool.Description, phrase) {
							t.Errorf("watch_history description must explain IMDb-only batch/whole-title behavior: missing %q", phrase)
						}
					}
				}
			}
		})
	}
	if len(expected) > 0 {
		t.Fatalf("published tools missing from tools/list: %v", expected)
	}
}

func TestOptionalTVToolsRequireExplicitFlag(t *testing.T) {
	t.Setenv("MCP_API_KEY", "test-only-key")
	for _, tc := range []struct {
		enabled  string
		expected []string
	}{
		{"", []string{"catalogs_get", "catalogs_update", "home_layout_get", "home_layout_update", "watch_history"}},
		{"FALSE", []string{"catalogs_get", "catalogs_update", "home_layout_get", "home_layout_update", "watch_history"}},
		{"true", []string{"catalogs_get", "catalogs_update", "home_layout_get", "home_layout_update", "watch_history", "tv_open_title"}},
	} {
		t.Run("flag_"+tc.enabled, func(t *testing.T) {
			t.Setenv("TV_OPEN_ENABLED", tc.enabled)
			req := httptest.NewRequest(http.MethodPost, "/mcp?key=test-only-key",
				strings.NewReader(`{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Mcp-Protocol-Version", "2025-11-25")
			recorder := httptest.NewRecorder()
			Handler().ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Result struct {
					Tools []struct {
						Name        string                     `json:"name"`
						Annotations map[string]json.RawMessage `json:"annotations"`
						InputSchema struct {
							Properties map[string]json.RawMessage `json:"properties"`
						} `json:"inputSchema"`
					} `json:"tools"`
				} `json:"result"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			actual := map[string]bool{}
			for _, tool := range response.Result.Tools {
				if actual[tool.Name] {
					t.Fatalf("duplicate tool %s", tool.Name)
				}
				actual[tool.Name] = true
				if string(tool.Annotations["readOnlyHint"]) != "true" {
					t.Errorf("%s missing readonly hint", tool.Name)
				}
				if _, ok := tool.Annotations["openWorldHint"]; ok {
					t.Errorf("%s sets open-world hint", tool.Name)
				}
				if strings.HasPrefix(tool.Name, "tv_") {
					if tool.Name != "tv_open_title" {
						t.Errorf("unexpected TV MCP tool %s", tool.Name)
					}
					for _, key := range []string{"media_type", "imdb_id", "season", "episode"} {
						if len(tool.InputSchema.Properties[key]) == 0 {
							t.Errorf("%s missing %s", tool.Name, key)
						}
					}
					for _, key := range []string{"season", "episode"} {
						var field struct {
							Type []string `json:"type"`
						}
						if err := json.Unmarshal(tool.InputSchema.Properties[key], &field); err != nil {
							t.Error(err)
						} else if len(field.Type) != 2 || !strings.Contains(strings.Join(field.Type, ","), "integer") || !strings.Contains(strings.Join(field.Type, ","), "null") {
							t.Errorf("%s must be an optional integer: %v", key, field.Type)
						}
					}
					for _, key := range []string{"device_id", "code", "action", "name", "setup_key"} {
						if _, found := tool.InputSchema.Properties[key]; found {
							t.Errorf("%s must not expose administrative field %s", tool.Name, key)
						}
					}
				}
				if strings.Contains(string(tool.InputSchema.Properties["imdb_id"]), "firebase") {
					t.Error("tool schema must describe the agent's inputs, not Firebase implementation")
				}
			}
			if len(actual) != len(tc.expected) {
				t.Fatalf("expected %d tools, got %v", len(tc.expected), actual)
			}
			for _, name := range tc.expected {
				if !actual[name] {
					t.Errorf("missing %s", name)
				}
			}
		})
	}
}

func TestMCPRequiresConfiguredKey(t *testing.T) {
	t.Setenv("MCP_API_KEY", "")
	req := httptest.NewRequest(http.MethodPost, "/mcp?key=example", nil)
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without configured key, got %d", recorder.Code)
	}
}

func TestMCPRejectsMissingAndInvalidKeys(t *testing.T) {
	t.Setenv("MCP_API_KEY", "my-private-key")
	for _, path := range []string{"/mcp", "/mcp?key=wrong", "/mcp?key=my-private-key&key=wrong"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		recorder := httptest.NewRecorder()
		Handler().ServeHTTP(recorder, req)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for %q, got %d", path, recorder.Code)
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("missing no-store response for %q", path)
		}
	}
}

func TestMCPStatelessHandshakeAndCatalogTools(t *testing.T) {
	t.Setenv("MCP_API_KEY", "my-private-key")
	t.Setenv("PUBLIC_BASE_URL", "https://example.com")
	t.Setenv("STREMIO_FEED_TOKEN", "test-feed-token")
	for _, test := range []struct {
		name     string
		body     string
		contains string
	}{
		{"initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, `"name":"stremio-catalog-mcp"`},
		{"tools/list", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, `"name":"catalogs_update"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp?key=my-private-key", strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Mcp-Protocol-Version", "2025-11-25")
			recorder := httptest.NewRecorder()
			Handler().ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), test.contains) {
				t.Fatalf("unexpected response: %s", recorder.Body.String())
			}
		})
	}
}
