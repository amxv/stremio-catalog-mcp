package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These are intentionally small, non-privileged examples. Replace or remove
// them when implementing your own MCP server; the HTTP transport stays intact.
func makeServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "go-mcp-template", Version: "0.1.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "echo_text",
		Description: "Echo text back. Demonstrates typed MCP tool arguments and text content.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
		Text string `json:"text" jsonschema:"The text to echo"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: input.Text}}}, nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_numbers",
		Description: "Add two numbers. Demonstrates typed MCP tool arguments and structured output.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
		A float64 `json:"a" jsonschema:"First operand"`
		B float64 `json:"b" jsonschema:"Second operand"`
	}) (*mcp.CallToolResult, struct {
		Sum float64 `json:"sum"`
	}, error) {
		return nil, struct {
			Sum float64 `json:"sum"`
		}{Sum: input.A + input.B}, nil
	})
	return s
}
