---
title: Architecture
description: Understand the Go Function, auth middleware, stateless MCP transport and sample tools.
order: 2
category: Design
summary: What to change when implementing a new MCP service.
---

## Request flow

```text
ChatGPT or another MCP client
       │
       ▼ POST /mcp?key=...
Vercel Go Function        api/mcp.go
       │
       ▼
Authentication + HTTP    pkg/server/server.go
       │
       ▼
Stateless MCP server     pkg/server/tools.go
       ├─ echo_text
       └─ add_numbers
```

The code uses the official `github.com/modelcontextprotocol/go-sdk/mcp` implementation. Streamable HTTP is **stateless** with JSON responses, so any Vercel function instance can handle any request.

The `MCP_API_KEY` is loaded from the runtime environment and checked in constant time. Missing configuration returns HTTP 503; an absent or incorrect key returns HTTP 401. Responses include no-store and no-referrer headers.

## Adding a tool

In `pkg/server/tools.go`, use `mcp.AddTool` with a typed Go struct for input. The Go SDK generates a JSON Schema from the struct and validates arguments before calling your handler.

`echo_text` demonstrates returning `mcp.TextContent`; `add_numbers` demonstrates returning a typed Go value that becomes `structuredContent` automatically.

**Important for Vercel:** Go Functions compile under a generated handler package. Import application code from a public Go package such as `pkg/server`, not directly from a Go `internal/` package. The generated handler can't satisfy Go's internal-import restriction, even though a local Go build can.

## Boundaries

Keep the entrypoint small, authentication separate, and real application logic in small Go packages. Don't expose new tools until they are implemented and covered by tests. Neither Redis nor CLI release machinery is necessary for the basic MCP transport.
