---
title: Quickstart
description: Connect an MCP client, install the private add-on, and publish your first row.
order: 1
category: Start
summary: From a chat recommendation to a native Stremio catalog.
---

## 1. Deploy the Go service

Provision a small durable Upstash Redis database, then deploy the repository root as a Vercel Go Function. Set the five required environment variables described in [Deployment](/docs/deployment). The Stremio account session key is optional but required for automatic new-row synchronization.

The service never needs an OpenAI API key. Your MCP client provides all the recommendations.

## 2. Connect your MCP client

Connect the private Streamable HTTP URL:

```text
https://your-api-host/mcp?key=<your-MCP_API_KEY>
```

Use your actual secret only in the MCP client connection settings. The Go endpoint rejects unauthenticated requests.

## 3. Install once in Stremio

Call `catalog_install_url` and open its URL in Stremio's add-on installation dialog. Sign into the same Stremio account on your TV. The URL is private, so do not post it publicly.

If `STREMIO_AUTH_KEY` is configured, the server can also install or upgrade its own add-on automatically using Stremio's account collection API.

## 4. Publish from a chat

Ask your agent to recommend titles and send them to Stremio. It can call `catalog_put`:

```json
{
  "name": "Murder Mysteries with Comedy",
  "type": "series",
  "items": [
    { "title": "Psych", "year": 2006 },
    { "title": "Monk", "year": 2002 }
  ]
}
```

The service resolves names to IMDb IDs via Cinemeta, stores the row, and returns its stable ID. See [MCP tools](/docs/tools) for more operations. Your existing streaming add-ons handle playback when you open an item.

For newly named rows, Stremio may need an app refresh or restart after account synchronization.
