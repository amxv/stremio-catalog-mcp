# Go MCP Template agent instructions

- This repository is an MCP server template, **not a CLI**. Keep deployment through Vercel rather than npm or GitHub Releases.
- Use the official Go MCP SDK and **stateless Streamable HTTP** so Vercel functions don't require persistent sessions.
- The only Go Function entrypoint is `api/mcp.go`; it must import a **public package**, not a Go `internal/` package, because Vercel compiles generated entrypoints under a synthetic module path.
- Authentication is via one `?key=` parameter matching the `MCP_API_KEY` environment variable. Missing configuration fails closed. Never log full authenticated URLs.
- `pkg/server/tools.go` contains working examples: one text tool and one structured-output tool. Remove or replace them when implementing an actual service.
- `make check` runs Go tests and vet. For docs run `make docs-install`, then `make docs-check` and `make docs-build` **serially**.
- Root deployment goes to one Vercel project. ZueDocs lives entirely under `docs/`, designed for a separate project with Root Directory `docs`.
- Do not copy API keys, credentials or user-specific domains into this template.

### Docs deployment

The Docs GitHub Actions workflow deploys production only for the watched site paths; Vercel Git auto-deployments and `ignoreCommand` stay disabled. Keep the project-scoped `VERCEL_TOKEN` Actions secret and ID variables configured. Check Astro locally; builds run in CI.
