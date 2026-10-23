---
title: Deployment
description: Deploy the Go function and independent ZueDocs site to Vercel.
order: 3
category: Operations
summary: Redis storage, private credentials, account sync and docs deployment.
---

## 1. Upstash Redis

Create a dedicated TLS-enabled Redis database, ideally with eviction disabled. Collect its **REST URL** and **REST token**. We store only catalog rows there, not the Stremio account session key. Redis is accessed over HTTPS to avoid persistent serverless TCP connections.

## 2. API Vercel project

Import this GitHub repository as a Vercel project with root directory `/`, Framework Preset **Other**, and production branch `main`.

Configure these private environment variables in the **API project only**:

| Variable | Required | Description |
| --- | --- | --- |
| `MCP_API_KEY` | Yes | Random secret for MCP write access |
| `STREMIO_FEED_TOKEN` | Yes | Different URL-safe secret for Stremio read access |
| `PUBLIC_BASE_URL` | Yes | HTTPS origin of the production deployment |
| `UPSTASH_REDIS_REST_URL` | Yes | Redis REST endpoint |
| `UPSTASH_REDIS_REST_TOKEN` | Yes | Redis REST token |
| `STREMIO_AUTH_KEY` | No | Stremio session key for automatic row manifest updates |

Root `vercel.json` routes `/mcp` and `/stremio/...` into the **same Go Function**. MCP is protected by `MCP_API_KEY`, while the catalog feed uses only `STREMIO_FEED_TOKEN`.

Do not provide passwords to this service. If enabling automatic account sync, provide a Stremio account session token through a secure private Vercel variable, never through an MCP tool call, GitHub issue, or repository file.

## 3. Stremio account sync

With `STREMIO_AUTH_KEY`, mutations can install or update only our catalog manifest in the signed-in Stremio account. The adapter reads the live installed collection, preserves every unrelated descriptor, and updates its own descriptor. Without this key, existing row content updates normally, but new named rows require manual manifest reload in Stremio.

The account API is used by official Stremio clients but isn't a separately supported third-party integration. A TV app restart may be required to display changed rows; this is not a real-time push interface.

## 4. Documentation project

Create a second Vercel project on the same GitHub repository with **Root Directory `docs`** and Framework Preset Astro. Git auto-deployments are disabled for the docs project; the `Docs` GitHub Action path-filters docs changes, builds a static site, and publishes its prebuilt output. Configure the workflow's `VERCEL_TOKEN` secret and `VERCEL_ORG_ID` and `VERCEL_PROJECT_ID` variables for that docs project.

The docs project never needs MCP, Redis or Stremio credentials.

## Verify

```sh
make check
make docs-install
make docs-check
make docs-build
```

Run the last two documentation commands sequentially. During a live smoke test, validate that unauthorized MCP requests return 401, the private manifest returns a valid Stremio schema, and the account adapter preserves unrelated Cinemeta and streaming descriptors.
