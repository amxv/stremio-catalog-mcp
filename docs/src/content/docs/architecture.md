---
title: Architecture & security
description: Catalog protocol, account syncing, persistence, and the limits of a single-account deployment.
order: 5
category: Reference
summary: How this small bridge works without replacing Cinemeta or streaming providers.
---

## One small bridge

```text
ChatGPT / your MCP agent ── POST /mcp?key=... ──┐
                                                 │
                                           Vercel Go API
                                                 │
                                  Upstash-compatible Redis REST
                                                 │
Stremio on the TV ── GET /stremio/<feed>/... ───┘
                  ↳ Existing Cinemeta + streaming add-ons
```

The assistant does all the **research and recommendations** using its normal tools. This service just persists and serves the resulting catalog rows. No LLM runs inside the server.

It advertises Stremio's **catalog** resource, not `meta`, `stream`, or `subtitles`. Each item uses a standard IMDb `tt...` ID with `movie` or `series` type, so Stremio's existing Cinemeta metadata and streaming providers can handle the same titles. Cinemeta search is used only to resolve supplied titles and posters, rejecting ambiguous matches.

When the TV requests a custom catalog, the server also reads each title's full Cinemeta metadata and includes standard Stremio preview fields such as `runtime`, `description`, `background`, `releaseInfo`, `imdbRating`, and `links`. Android TV uses `links` with category `imdb` for its rating icon and `Genres`/`Cast` for the adjacent information. A separate 24-hour Redis cache keyed by catalog row avoids repeating Cinemeta requests on normal browsing. Cache misses fetch only missing titles in parallel, bounded by a request timeout. Failures fall back to the originally saved title/year/poster without altering the user's row, revision, or MCP tool arguments. This also upgrades already-existing rows on their next fetch.

## Updating titles versus rows

Rows have stable IDs, names, type, position, and ordered titles. The Go MCP endpoint validates tool calls and uses Redis compare-and-swap, so concurrent updates don't silently overwrite each other. It supports up to 24 rows and 100 titles per row.

**Editing titles** changes the live catalog endpoint. **Creating, renaming, or deleting rows** also changes the add-on manifest. Moving a title inside its row doesn't need a manifest sync. Use `home_layout_update` to change the order or visibility of the catalog rows themselves. Stremio may retain an older manifest until the account add-on collection syncs or the app refreshes.

`catalogs_get` gives a compact summary by default; a requested row returns full titles and saved poster URLs. `catalogs_update` validates a batch before committing its content changes with one Redis compare-and-swap. A recent `request_id` is recorded in that same state so retries don't duplicate newly created rows, and `expected_revision` can reject stale edits. When a batch changes the manifest, the server attempts one account sync and reports save status separately from sync status.

For automatic sync, an optional `STREMIO_AUTH_KEY` lets the server call Stremio's `addonCollectionGet` and `addonCollectionSet`. The latter **replaces the account's entire add-on collection**, so the adapter:

1. Fetches the current collection and preserves every unrelated descriptor, including Cinemeta and streaming add-ons.
2. Serializes this service's sync requests with a Redis lease and checks for changes from other clients before writing.
3. Saves a private Redis backup before every account replacement (up to 10 recent snapshots), aborting the write if the backup fails.
4. Replaces only its own manifest, then reads back the collection to verify synchronization.

### Home layout updates

`home_layout_get` reads the live installed add-on collection, including catalog IDs and visibility, but never returns private configured add-on URLs. `home_layout_update` accepts a batch of relative moves and row visibility changes, validates them as a whole, and makes one Stremio account replacement. It reuses the account sync lease, private pre-write backup, and read-after-write verification.

When the Stremio account is not linked, the Home layout tools fall back to listing and editing custom rows only, retaining offline row positioning rather than forcing account linking. Those edits are saved through Redis CAS but cannot synchronize other add-ons or update the installed Home screen until the account is connected.

Normal catalog synchronization still leaves other add-ons alone. With an **explicit Home layout request**, the adapter may reorder selected third-party catalog entries, change selected filter requirements, and reorder installed add-on descriptors. Other fields and streaming resources stay intact. A row cannot be moved to a different add-on without proxying its catalog; this MCP doesn't do that.

Our custom rows store their hidden status and order durably alongside their titles. Other add-ons' safely hidden catalogs use an existing optional `search` or `genre` filter. Their pre-hide filter settings are privately retained in Redis (not exposed through MCP) for exact restoration. Search-only catalogs and catalogs with no compatible optional filter are not overridden. A legacy Cinemeta Popular/Featured hide can be reversed.

Home layout operations that also change custom rows span Stremio and Redis, so they cannot be truly atomic across the two services. The implementation performs optimistic comparisons, attempts rollback if Stremio clearly rejects the write, and reports ambiguous outcomes rather than risking an unsafe overwrite.

This is **not** a transaction across multiple Stremio devices. Changes made simultaneously from a different Stremio client can still race. The account API is used by Stremio clients but is **not a stable, versioned third-party contract**, so compatibility can change.

## Security model

- `MCP_API_KEY`: private write-capable URL credential (`?key=`). Never share it publicly.
- `STREMIO_FEED_TOKEN`: separate read-only feed token embedded in Stremio's catalog URL; anyone with that URL can read the catalog contents, not modify them through MCP.
- Redis REST credentials: private Vercel production variables; the server accepts existing Upstash-compatible HTTPS Redis REST endpoints, not arbitrary Redis TCP URLs. Durable state isn't tied to a Vercel function instance.
- `STREMIO_AUTH_KEY`: sensitive Stremio session, used only for the account API and never returned through MCP tools.
- Private account backups: may include other installed add-ons' configured URLs, so they aren't exposed through the feed or MCP.

**Tool annotations aren't permissions.** Some clients will see `readOnlyHint: true`, but several tools really do edit or delete rows. Treat these as write-capable operations and rely on the actual API key and the user's approval policy, not metadata hints.

Each deployment currently serves **one Stremio account**. For multiple people, deploy separate instances with separate credentials and Redis storage. Watch history is read from Stremio's datastore; episode completion uses its anchored compressed bitfield and canonical Cinemeta episode ordering. `mark_watched` / `mark_unwatched` update only the targeted library item through datastorePut, preserving unrelated episode completion bits, current playback position, and unknown fields, then verify a subsequent read. Scheduled recommendations, cross-app history imports, playback controls, and multi-tenant account management are outside this service's scope.

## Code map and upstream references

- `api/mcp.go`: Vercel Go Function for MCP and Stremio routes.
- `pkg/server`: official Go MCP SDK, key validation, and tools.
- `pkg/catalog`: validation, Cinemeta lookup, ordered rows and Redis storage.
- `pkg/stremio`: native add-on protocol and the account-sync adapter.
- `cmd/setup`: optional self-hosting bootstrap with Free Upstash provisioning **or existing Redis REST credentials**, Agent Browser sign-in, and a secure clipboard handoff.

Read the [Stremio add-on protocol](https://github.com/Stremio/stremio-addon-sdk/blob/master/docs/protocol.md), [Cinemeta documentation](https://github.com/Stremio/stremio-addon-sdk/blob/master/docs/advanced.md#understanding-cinemeta), and [Stremio API client](https://github.com/Stremio/stremio-api-client) for upstream contracts.
