---
title: Quickstart
description: Clone the Go MCP template, run its sample tools, and deploy it to Vercel.
order: 1
category: Start
summary: Template setup, sample tools, credentials, and local checks.
---

## Create and clone

Create a private repository using [Go MCP Template](https://github.com/amxv/go-mcp-template/generate), then clone it. Update the Go module path in `go.mod` and the import in `api/mcp.go`.

Run the backend checks:

```bash
make check
```

The server uses the official Go MCP SDK. The implementation lives in `pkg/server/tools.go` and exposes **two runnable tools**:

- `echo_text`: `{ "text": "hello" }` returns text `hello`.
- `add_numbers`: `{ "a": 2, "b": 3 }` returns `{ "sum": 5 }` as structured MCP output.

## Production endpoint

Deploy the repository root to Vercel, selecting **Other** as the framework. Configure the Production secret `MCP_API_KEY` with a long randomly generated value.

```text
https://your-domain.example/mcp?key=<your-private-key>
```

This is a **stateless Streamable HTTP** MCP server. You don't need a session database. The server denies all requests if `MCP_API_KEY` is missing. Treat the full URL as a secret; query keys can appear in proxy logs and copied URLs.

## Local documentation

```bash
make docs-install
make docs-dev
```

The bundled ZueDocs site lives entirely under `docs/` and can be deployed as a second Vercel project.
