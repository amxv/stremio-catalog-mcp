---
title: Architecture
description: How the MCP tools, durable state, Stremio protocol and account adapter fit together.
order: 2
category: Design
summary: Cinemeta reuse, dynamic manifests and safe account synchronization.
---

## Separation of responsibilities

```text
Agent / ChatGPT                  Stremio Android TV
   |                                  |
   | POST /mcp?key=...                | GET /stremio/:feed/vN/manifest.json
   v                                  | GET /stremio/:feed/vN/catalog/series/:row.json
pkg/server  (Go MCP SDK)              v
   |                             pkg/stremio
   v                                  |
pkg/catalog  (rows + ID matching) -----+
   |                                  |
   v                                  v
Upstash Redis REST               IMDb IDs + poster previews
                                     |
                            existing Cinemeta / stream addons
```

**The recommendation system is your agent.** This service stores only catalog rows and items. It performs no model inference and requires no AI API credentials.

## Cinemeta and playback compatibility

Each catalog entry uses Stremio's standard `tt...` IMDb identifier and `movie` or `series` type. Cinemeta resolves existing metadata and episode structures from that identity. Previously installed stream providers continue to receive exactly the IDs they already support. This add-on intentionally advertises **only the `catalog` resource**, never `stream`, `subtitles`, or `meta`.

The backend can accept an IMDb ID directly or search Cinemeta for an **exact** title/year match before storing the item. Ambiguous matches are rejected rather than guessed.

## Dynamic rows

Manifest declarations contain each row's stable ID, display name, and content type. `/catalog/{type}/{id}.json` serves its current ordered list. Editing titles changes the response immediately, subject to Stremio's cache. Creating, deleting, or renaming a row also changes the manifest, which Stremio clients may have cached from installation.

For full dynamic row support, an optional account adapter uses `addonCollectionGet` then `addonCollectionSet` to merge our new descriptor into the account's installed add-on collection. **The set operation replaces the entire collection**, so our adapter preserves unrelated descriptors, flags and order. Each manifest update receives a revisioned URL so clients have a better chance of recognizing changed declarations.

Adding, replacing, clearing, or removing **titles inside an existing row** needs no account-collection write. Those changes are served directly from the catalog endpoint. We reserve account sync for new/deleted/renamed/reordered row declarations and explicit `catalog_sync` calls, reducing unnecessary calls to Stremio's account API.

**Caveat:** This Stremio account API is used by Stremio's own clients but isn't a separately supported third-party contract. Its updates aren't transactional with other devices; external simultaneous edits can race. We serialize our own syncs with an Upstash lease, reread the live account state, and verify the result, but cross-client conflicts cannot be completely eliminated. Never give this service blanket ability to remove unrelated add-ons.

Before every account-collection replacement, the Go service stores a timestamped copy of the previous descriptors in a private Redis backup list (last 10 snapshots). If the backup cannot be saved, the account write is refused. These records may contain sensitive installed add-on URLs, so they are never exposed as MCP responses or through the public catalog feed.

## Data and safety

`pkg/catalog` stores the row document with optimistic compare-and-swap using Redis Lua `EVAL`. Parallel agent writes cannot silently overwrite each other. A separate short Redis lock serializes account syncs across Vercel instances. HTTP endpoints reject missing secrets; the read-only Stremio feed has a different token from the write-capable MCP endpoint.

Only the row document is stored in Redis. The Stremio account token, if provided, is a Vercel environment variable and is **not** serialized into catalog state. The repository is Apache-2.0 licensed with credentials/configuration separated from code for eventual public release.
