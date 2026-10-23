# Stremio Catalog MCP

**Send curated movie and TV recommendations from your AI assistant straight to Stremio.**

This is a small, catalog-only remote MCP server and Stremio add-on. Your assistant researches and selects titles using its own tools; this service creates and manages home-screen rows. It does **not** call a language model, collect taste profiles, provide video streams, or replace Cinemeta. Existing streaming add-ons continue to provide playback through the same IMDb IDs.

> **Status:** private, single-account foundation, organized for eventual open-source and multi-account support. Stremio's account collection API is not a separately versioned public API and may change.

## Features

- Create **movie or series** rows with your own names, using IMDb IDs or unambiguous titles resolved by Cinemeta.
- Append, deduplicate, replace and remove selected titles; rename, reorder or clear existing rows.
- Delete a row entirely, preserving every other installed Stremio add-on.
- Serve native Stremio manifests and catalog feeds with Cinemeta-compatible IDs.
- Optionally synchronize row changes into your Stremio account so names and new rows appear on signed-in devices.
- Run as a single Vercel Go Function with durable catalog storage in Upstash Redis REST.

## MCP tools

| Tool | Operation |
| --- | --- |
| `catalog_list` | Get row IDs, names, types, order, and item IDs. |
| `catalog_put` | Create, rename, move, replace, or append to a row. |
| `catalog_remove_titles` | Remove selected IMDb IDs from a row. |
| `catalog_clear` | Empty a row without deleting its name or ID. |
| `catalog_delete` | Remove an entire row. |
| `catalog_sync` | Retry syncing rows into the Stremio account. |
| `catalog_install_url` | Return the private URL for one-time installation. |

Example `catalog_put` call:

```json
{
  "name": "Whodunits with Comedy",
  "type": "series",
  "items": [
    { "title": "Psych", "year": 2006 },
    { "title": "Monk", "year": 2002 },
    { "title": "Only Murders in the Building", "year": 2021 }
  ]
}
```

On subsequent calls, pass the returned `id` with `mode: "append"` (default) or `mode: "replace"`. An empty replacement clears the row. To move a row, pass `position` (zero-based). Movie and series items live in separate rows, as required by Stremio's catalog schema.

## Deployment

Built from [amxv/go-mcp-template](https://github.com/amxv/go-mcp-template). Deploy the repository root as a Vercel Go Function, and `docs/` as a separate Astro/ZueDocs Vercel project.

### Private environment variables (API project only)

| Variable | Purpose |
| --- | --- |
| `MCP_API_KEY` | Long random secret authenticating remote MCP requests. |
| `PUBLIC_BASE_URL` | HTTPS origin, e.g. `https://catalog.example.com`. |
| `STREMIO_FEED_TOKEN` | Independent URL-safe secret for the Stremio add-on feed. |
| `UPSTASH_REDIS_REST_URL` | Upstash Redis HTTPS REST endpoint. |
| `UPSTASH_REDIS_REST_TOKEN` | Redis REST authentication token. |
| `STREMIO_AUTH_KEY` | **Optional:** Stremio session token, enabling automatic row install/rename/delete across signed-in devices. |

Never reuse the MCP key as the feed key. Neither belongs in source control or public docs. Never send a Stremio account password to this service. Without `STREMIO_AUTH_KEY`, MCP writes are saved and existing rows update, but newly declared rows require manually reinstalling the latest manifest.

**MCP:** `https://your-host/mcp?key=<MCP_API_KEY>`.

**Stremio:** `https://your-host/stremio/<STREMIO_FEED_TOKEN>/manifest.json` (or use the `catalog_install_url` tool). The separate feed token is necessarily embedded in the add-on URL because Stremio does not send custom authorization headers.

Stremio account sync uses its own `addonCollectionGet` and `addonCollectionSet` methods. Each update reads the latest collection, replaces **only our own descriptor**, and verifies the new manifest. Unrelated entries, including Cinemeta and playback providers, are preserved. Versioned URLs help clients notice newly created or renamed rows. A TV app restart may still be necessary; the Stremio protocol has no real-time push API.

## Local development

Go 1.26+ and Bun (for documentation) are required:

```sh
make check
make docs-install
make docs-check
make docs-build
```

The Go tests run without credentials or a live account. Production requires durable Redis; it never silently stores catalogs in process memory. HTTP uses stateless JSON-RPC Streamable HTTP and fail-closed key validation.

## Design and references

- [Stremio add-on protocol](https://github.com/Stremio/stremio-addon-sdk/blob/master/docs/protocol.md): manifests and catalogs.
- [Cinemeta compatibility](https://github.com/Stremio/stremio-addon-sdk/blob/master/docs/advanced.md#understanding-cinemeta): reuse IMDb IDs with other providers.
- [Stremio account API client](https://github.com/Stremio/stremio-api-client): `addonCollectionGet` / `addonCollectionSet`.
- [Stremio core API requests](https://github.com/Stremio/stremio-core/blob/main/src/types/api/request.rs): account request shapes.

`pkg/catalog` owns row state, optimistic concurrency, and title resolution; `pkg/stremio` owns the catalog protocol and account adapter; `pkg/server` defines the MCP surface; `api/` is the Vercel function. A future multi-user version can add tenant isolation and account authorization without replacing these packages.

Licensed under Apache-2.0. The repository is currently private and can be made public without a code/license migration.
