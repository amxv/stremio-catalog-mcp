# Go MCP Template

A minimal, production-ready **Go MCP server template** that deploys as a Vercel serverless function. It uses the official Model Context Protocol Go SDK, stateless Streamable HTTP, simple API-key authentication, and an optional bundled ZueDocs site.

**This is infrastructure, not a CLI.** There is no npm package, executable shim, release workflow, or version-tag pipeline. Vercel deploys the service from the `main` branch.

## Included

- `api/mcp.go`: minimal Vercel Go Function entrypoint.
- `pkg/server/server.go`: fail-closed `?key=` authentication and stateless MCP Streamable HTTP.
- `pkg/server/tools.go`: two fully working sample tools, **`echo_text`** (text result) and **`add_numbers`** (structured JSON result).
- `pkg/server/server_test.go`: end-to-end HTTP JSON-RPC tests for authentication, initialization, tools/list, and tools/call.
- `docs/`: Astro + ZueDocs documentation workspace that can deploy independently to another Vercel project.
- `vercel.json`: production-friendly `/mcp` rewrite, Go Function settings, and `main` deployment routing.

## Start a new project

1. [Create a repository from this template](https://github.com/amxv/go-mcp-template/generate) and clone it.
2. Replace `github.com/amxv/go-mcp-template` in `go.mod` and `api/mcp.go` with your new module path.
3. Rename the MCP implementation in `pkg/server/tools.go` and replace the two sample handlers with your real tools.
4. Set up a Vercel project with the repository root as its Root Directory, Framework Preset **Other**, and Git auto-deployments on `main`.
5. Set a long random secret in the Vercel **Production** environment as `MCP_API_KEY`.
6. Connect a ChatGPT or other MCP client to `https://your-api-domain.example/mcp?key=<your-secret>`.

For a custom domain, attach it to Vercel and configure the DNS record Vercel provides. Keep DNS-only mode during ownership verification.

**Security:** Query parameters can be logged by intermediaries. Treat the authenticated URL as a secret, never commit keys, and do not paste it into public docs or logs. The server fails closed when `MCP_API_KEY` is unset.

## Development

Requirements: Go 1.26+ and Bun (only for the docs).

```sh
make check
make docs-install
make docs-check
make docs-build
```

The sample tools can be tested entirely offline. For local HTTP testing, link the API project with Vercel and run `vercel dev` with `MCP_API_KEY` configured.

## Editing the MCP tool surface

Handlers use `mcp.AddTool` with typed inputs and optional typed outputs. Tools are defined in `pkg/server/tools.go`; the public entrypoint and authentication don't need to change.

Use **`echo_text`** for the unstructured text response example and **`add_numbers`** for the JSON schema/structured output example. Delete or rename these when adapting the template; don't ship irrelevant samples in a production service.

## Docs site

The docs app is self-contained under `docs/`. Link it to a **separate** Vercel project with Root Directory `docs`, Framework Preset Astro, and its own domain. It doesn't need MCP credentials and shouldn't receive them.

Documentation: [`docs/src/content/docs`](docs/src/content/docs) and [`docs/src/data/docs.ts`](docs/src/data/docs.ts).

Licensed under Apache-2.0.
