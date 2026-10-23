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
				Annotations map[string]json.RawMessage `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Result.Tools) != 7 {
		t.Fatalf("expected all 7 tools, received %d", len(response.Result.Tools))
	}
	for _, tool := range response.Result.Tools {
		t.Run(tool.Name, func(t *testing.T) {
			var readOnly bool
			if err := json.Unmarshal(tool.Annotations["readOnlyHint"], &readOnly); err != nil || !readOnly {
				t.Fatalf("tool %q must advertise readOnlyHint=true: %s (error: %v)", tool.Name, tool.Annotations["readOnlyHint"], err)
			}
			if _, present := tool.Annotations["openWorldHint"]; present {
				t.Fatalf("tool %q must omit openWorldHint, not set it to false", tool.Name)
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
		{"tools/list", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, `"name":"catalog_put"`},
		{"install URL", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"catalog_install_url","arguments":{}}}`, `"url":"https://example.com/stremio/test-feed-token/manifest.json"`},
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
