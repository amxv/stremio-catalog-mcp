package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exercise the actual public, schema-decoded MCP tool surface, not just Go methods.
func TestFourToolCatalogLifecycleOverMCP(t *testing.T) {
	rt, account, _, _, _, done := testHomeRuntime(t)
	defer done()
	rt.Manager.Resolver = mockPosterResolver{}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return serverFor(rt) }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	server := httptest.NewServer(handler)
	defer server.Close()
	call := func(id int, tool string, args any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
		req, _ := http.NewRequest(http.MethodPost, server.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Mcp-Protocol-Version", "2025-11-25")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var data struct {
			Result struct {
				IsError    bool             `json:"isError"`
				Structured map[string]any   `json:"structuredContent"`
				Content    []map[string]any `json:"content"`
			} `json:"result"`
			Error any `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || data.Error != nil || data.Result.IsError {
			t.Fatalf("tool %q returned status %d, error=%v, content=%v", tool, response.StatusCode, data.Error, data.Result.Content)
		}
		if data.Result.Structured == nil {
			t.Fatalf("missing structured result from %s", tool)
		}
		return data.Result.Structured
	}
	base := call(1, "catalogs_get", map[string]any{})
	if len(base["rows"].([]any)) != 2 {
		t.Fatalf("expected existing 2 rows: %+v", base)
	}
	create := map[string]any{"changes": []any{map[string]any{"action": "create", "name": "Smoke Test", "type": "movie", "titles": []any{map[string]any{"id": "tt0000404", "title": "Test Movie"}}}}, "request_id": "mcp-qa-123"}
	preview := map[string]any{"changes": create["changes"], "request_id": "mcp-qa-123", "dry_run": true}
	planned := call(2, "catalogs_update", preview)
	if planned["dry_run"] != true || planned["saved"] != false || account.setCalls != 0 {
		t.Fatalf("preview committed changes: %+v", planned)
	}
	written := call(3, "catalogs_update", create)
	if written["saved"] != true || written["changed"] != true {
		t.Fatalf("create not saved: %+v", written)
	}
	results := written["changes"].([]any)
	first := results[0].(map[string]any)
	createdID := first["row_id"].(string)
	row := first["catalog"].(map[string]any)
	items := row["items"].([]any)
	if items[0].(map[string]any)["poster"] != "https://images.example/tt0000404.jpg" {
		t.Fatalf("poster URL missing: %+v", row)
	}
	replay := call(4, "catalogs_update", create)
	if replay["replayed"] != true || replay["changed"] != false {
		t.Fatalf("idempotent replay not recognized: %+v", replay)
	}
	found := call(5, "catalogs_get", map[string]any{"row_id": createdID})
	detail := found["rows"].([]any)[0].(map[string]any)
	titles := detail["titles"].([]any)
	if len(titles) != 1 || titles[0].(map[string]any)["poster"] != "https://images.example/tt0000404.jpg" {
		t.Fatalf("detailed posters not returned: %+v", detail)
	}
	clear := call(6, "catalogs_update", map[string]any{"changes": []any{map[string]any{"action": "edit", "row_id": createdID, "replace_titles": []any{}}}})
	if clear["changed"] != true {
		t.Fatalf("clearing row failed: %+v", clear)
	}
	afterClear := call(7, "catalogs_get", map[string]any{"row_id": createdID})
	if got := len(afterClear["rows"].([]any)[0].(map[string]any)["titles"].([]any)); got != 0 {
		t.Fatal("clear left titles", got)
	}
	removed := call(8, "catalogs_update", map[string]any{"changes": []any{map[string]any{"action": "delete", "row_id": createdID}}})
	if removed["sync"].(map[string]any)["status"] != "succeeded" {
		t.Fatal("delete not synced:", fmt.Sprint(removed))
	}
	final := call(9, "catalogs_get", map[string]any{})
	if len(final["rows"].([]any)) != 2 {
		t.Fatalf("temporary row not cleaned up: %+v", final)
	}
}
