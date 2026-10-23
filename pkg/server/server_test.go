package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestMCPStatelessHandshakeAndSampleTools(t *testing.T) {
	t.Setenv("MCP_API_KEY", "my-private-key")
	for _, test := range []struct {
		name     string
		body     string
		contains string
	}{
		{"initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, `"name":"go-mcp-template"`},
		{"tools/list", `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, `"name":"echo_text"`},
		{"echo", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo_text","arguments":{"text":"hello"}}}`, `"text":"hello"`},
		{"add", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"add_numbers","arguments":{"a":2.5,"b":3}}}`, `"sum":5.5`},
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
