# Stremio Catalog MCP agent instructions

- This repository is a catalog-only MCP server and Stremio addon, **not a CLI**. Keep the Go API and ZueDocs deployment on Vercel, not Cloudflare Workers.
- Use the official Go MCP SDK and **stateless Streamable HTTP** so Vercel functions don't require persistent sessions.
- The only Go Function entrypoint is `api/mcp.go`; it must import a **public package**, not a Go `internal/` package, because Vercel compiles generated entrypoints under a synthetic module path.
- Authentication is via one `?key=` parameter matching the `MCP_API_KEY` environment variable. Missing configuration fails closed. Never log full authenticated URLs.
- `pkg/server/tools.go` exposes real catalog management tools; do not add a recommendation engine, tasting/ratings database, streaming sources, or duplicate metadata provider.
- `pkg/catalog` owns durable state in dedicated Upstash Redis via HTTPS; use optimistic compare-and-swap for all edits. Never reuse another application's Redis database.
- `pkg/stremio` provides catalog endpoints and the optional account-sync adapter. Preserve **all fields** of unrelated add-on descriptors when using `addonCollectionSet`, and never alter or uninstall Cinemeta or streaming providers.
- Keep `STREMIO_AUTH_KEY`, `MCP_API_KEY`, `STREMIO_FEED_TOKEN`, and Redis tokens out of source, tests, logs and docs. Production variables live in the Go Vercel project only.
- Account syncing uses an undocumented Stremio API, so tests must cover catalog visibility and preservation of unrelated descriptors before changing it.
- `make check` runs Go tests and vet. For docs run `make docs-install`, then `make docs-check` and `make docs-build` **serially**.
- Root deployment goes to one Vercel project. ZueDocs lives entirely under `docs/`, designed for a separate project with Root Directory `docs`.
- This repository is initially private, but design code and examples for a future Apache-2.0 public release. Do not hardcode user-specific domains, IDs, credentials or account data.

### Docs deployment

The Docs GitHub Actions workflow deploys production only for the watched site paths; Vercel Git auto-deployments and `ignoreCommand` stay disabled. Keep the project-scoped `VERCEL_TOKEN` Actions secret and ID variables configured. Check Astro locally; builds run in CI.
