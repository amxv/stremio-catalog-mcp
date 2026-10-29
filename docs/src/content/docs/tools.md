---
title: MCP tools
description: Five tools to manage catalogs, the Stremio Home screen, and watch history.
order: 3
category: Guides
summary: Catalogs, Home layout, and IMDb-only watch-history reads, batch checks, and updates.
---

The MCP exposes **five tools by default**. Your assistant can understand them directly from their names and descriptions:

| | Read | Update |
| --- | --- | --- |
| **Catalog contents** | `catalogs_get` | `catalogs_update` |
| **Home arrangement** | `home_layout_get` | `home_layout_update` |
| **Watch history** | `watch_history` (`get` or batch `check`) | `watch_history` (`mark_watched` or `mark_unwatched`) |

With the **optional Android TV integration** enabled (`TV_OPEN_ENABLED=true`, Firebase configured), **only one additional tool** appears: `tv_open_title` (six tools total). It opens a movie or series details page, or a **specific series episode** with optional `season` and `episode`, **without autoplay**. The model never sees pairing or device-management tools; these are handled at the separately authenticated `/tv/setup` page, which supports **one paired TV**. Delivery receipts distinguish a TV launch request, notification fallback, and unconfirmed delivery; they do **not** prove the screen rendered. With the flag disabled, the original five-tool interface remains unchanged. See [Android TV remote and ADB setup](/docs/tv-remote).

**Catalogs** manage named collections and the movies or series inside them. **Home layout** manages the order and visibility of those rows, including compatible rows from other installed add-ons. **Watch history** reads the Stremio account and updates explicit watched status; it never creates its own streaming or recommendation service.

## Watch history and spoiler-safe episode status

**All `watch_history` operations use IMDb IDs (`tt...`) exclusively.** Agents must identify the correct IMDb ID during discovery; there is no title-name lookup, `query`, or `year` parameter in this tool. This prevents mixing up remakes, similarly named shows, or movies and series with identical titles.

`watch_history({})` lists recent watched or in-progress movies and shows, omitting titles merely saved in the Library. Add `limit` (1–50, default 20) and `offset` to page through results. Every returned title includes `imdb_id`.

For one specific show, use its IMDb ID:

```json
{ "imdb_id": "tt0903747", "season": 1 }
```

The result includes `watched_episodes`, `watched_episode_details` (titles and plot descriptions of confirmed watched episodes), `unconfirmed_episodes`, any `in_progress_episode`, and `spoiler_safe_through` (the last **consecutively** confirmed episode starting at S01E01). It also includes the show's premise/overview, plus a movie's plot description when that movie is watched. An episode merely opened or partly played does **not** count as completed. Unconfirmed episodes do not include plot descriptions or titles. For all seasons, omit `season`: you get every ordinary season's complete watched episode numbers, but only the **last eight watched episode synopses in episode order** to keep the response compact. Specify a season to retrieve all of that season's watched synopses, including `season: 0` for specials.

`unconfirmed` means Stremio doesn't prove an episode is watched, **not** that it has never been watched. If Stremio has no record, the bitfield is corrupt, or its episode list cannot be matched safely, the result explicitly reports unknown/unverified status and never invents a spoiler boundary. Stremio stores one series-level `lastWatched` timestamp, not per-episode historical watch dates.

### Check a shortlist of recommendations in one call

When recommending movies or TV shows, find their IMDb IDs **first**, then make a single targeted batch request:

```json
{
  "action": "check",
  "imdb_ids": ["tt0903747", "tt6751668", "tt9999999"]
}
```

`check` accepts **1–50 unique IMDb IDs** and returns a `checks` array in the **same order** as the input. The server uses **one Stremio `datastoreGet` call** for precisely those IDs, rather than scanning every title in the account. For recorded TV shows it fetches canonical episode metadata concurrently, with a bounded worker pool, to decode the watched bitfield.

Each result includes the IMDb ID, the saved title/type when known, whether Stremio has a playback record, and a `status`.

**Fully watched TV shows return the same compact fields as fully watched movies.** There is no redundant season or episode list, no episode counts, and no spoiler boundary in the `check` result. A currently airing show counts as watched when all *released regular episodes* are complete; upcoming episodes do not prevent that status.

**Partially watched TV shows return exact episode numbers**, grouped by season in `seasons[].watched_episodes` (e.g. `[1,2,4]`), along with any `in_progress_episode`. They do **not** return episode titles or plots. The `episode_history` field indicates `verified` or `unverified`; an empty watched list in an **unverified** result is **not proof** that none were completed. For partial shows, the response may include episode counts and `spoiler_safe_through`, expressed only as season/episode numbers. Use `get` with an individual `imdb_id` only when you specifically want its detailed watched-episode synopses.

Status values are:

| `status` | Meaning |
| --- | --- |
| `watched` | Movie marked completed, or every confirmed released regular episode of a series marked watched. Upcoming episodes and season-0 specials do not prevent this status. |
| `partially_watched` | Some series episodes are marked watched or playback is in progress; for movies, an unfinished playback position is saved. |
| `not_watched` | No watch completion or in-progress playback is recorded in this Stremio account, including when a title is absent or its history was cleared. **Not proof it was never viewed elsewhere.** |
| `unverified` | Stremio has ambiguous/stale records, a corrupt watched bitfield, or inaccessible episode metadata; the server cannot safely determine completion. |

If one series' metadata is unavailable, its result is `unverified` without hiding reliable results for the other IDs. The batch action never changes viewing history. Agents can exclude `watched` items from recommendations and distinguish `partially_watched` or `unverified` items instead of guessing.

### Entire movie or series: just one tool call

When someone says they've watched a movie **or an entire TV show**, use just `action: "mark_watched"` and its IMDb ID. **Do not look up seasons or episode numbers first.** For example:

```json
{ "action": "mark_watched", "imdb_id": "tt0903747" }
```

```json
{ "action": "mark_watched", "imdb_id": "tt0816692" }
```

For series, the server finds the canonical episodes from Cinemeta and updates all **released regular episodes across all seasons in one verified Stremio write**. Future-dated episodes, those with unknown release dates, and season-0 specials are not newly marked watched. Previously saved watched markers are preserved. The response reports the number of eligible and skipped episodes, and future-episode plots/titles are not revealed. Repeating a full-show update is safe and will pick up any newly released episodes.

To clear **an entire movie or TV series** from your Stremio watch history in the same simple way:

```json
{ "action": "mark_unwatched", "imdb_id": "tt0903747" }
```

This deliberately clears **all** playback state for the title: watched episodes, movie watched flags, last played video, playback offset/duration, time watched, and last watched timestamps. It also marks the library item removed/temporary to hide it from the Library and watch-history views. The MCP re-reads the record after the write to verify that no playback state remains. Repeating the action is a no-op. Clearing a TV series does not require its episode list, so it can work even if the stored watched bitfield is corrupt or outdated.

**Stremio API limitation:** there is no supported per-item hard-delete endpoint. Stremio keeps a minimal *removed synchronization tombstone* containing the title ID and metadata. `mark_unwatched` removes the playback history, **not** the underlying tombstone from Stremio's servers. A physical erasure of a title's last server-side record cannot be guaranteed through the API.

### Correct individual episodes without affecting other episodes

For episode-specific corrections, optionally supply `season` and `episodes`. Mark a few episodes watched:

```json
{ "action": "mark_watched", "imdb_id": "tt0903747", "media_type": "series", "season": 1, "episodes": [1, 2] }
```

To reverse **specific episodes only** without changing playback position or other episodes, use `mark_unwatched` with both `season` and `episodes`:

```json
{ "action": "mark_unwatched", "imdb_id": "tt0903747", "season": 1, "episodes": [2] }
```

For an IMDb ID already recorded in Stremio, `media_type` is optional. For a new ID, the server attempts to determine movie vs series from Cinemeta; `media_type` can be supplied if necessary. **Episode-specific** updates require explicit season and episode numbers; entire-title updates require neither. New watched records are temporary history items, not additions to your saved Library.

Stremio account linking (`STREMIO_AUTH_KEY`) is required, and results reflect only that Stremio account's synced history, not other streaming apps. The service uses Stremio's unversioned datastore API and Cinemeta's canonical episode order, so behavior may depend on upstream API changes. It re-reads after writes to verify the updated status but cannot prevent concurrent changes on another Stremio device atomically.

## Inspect your catalogs

`catalogs_get({})` lists your own catalog rows with names, IDs, types, number of titles, and current visibility. The compact default avoids returning hundreds of posters when you only want to locate a row.

To see a specific row's titles, use `catalogs_get({ "row_id": "row_..." })`. **The full result includes saved poster URLs**, release years, IMDb IDs, and exact title order. Use `include_titles: true` to get titles and posters from *all* your rows.

Need to reinstall the add-on? `catalogs_get({ "include_install_url": true })` also returns its **private installation URL**. Treat that URL as a secret; ordinary reads don't return it.

If Redis is temporarily unavailable, explicitly requesting only the installation URL still works; the response includes a warning that catalog rows could not be loaded.

## Create and edit catalog contents

Use `catalogs_update` with one or more ordered changes. For example:

```json
{
  "changes": [
    {
      "action": "create",
      "name": "Witty Whodunits",
      "type": "series",
      "titles": [
        { "title": "Psych", "year": 2006 },
        { "title": "Monk", "year": 2002 }
      ]
    }
  ],
  "request_id": "witty-whodunits-001"
}
```

Your agent can use IMDb IDs (`tt...`) instead of titles. Cinemeta resolves title metadata and posters where available. IDs remain stable when you edit or rename a row.

For **editing**, provide `action: "edit"`, `row_id`, and whichever changes you need:

```json
{
  "changes": [
    {
      "action": "edit",
      "row_id": "row_...",
      "name": "Even Better Whodunits",
      "add_titles": [{ "title": "Only Murders in the Building", "year": 2021 }],
      "remove_title_ids": ["tt0491738"],
      "move_titles": [
        { "id": "tt0312172", "before": "tt11691774" }
      ]
    }
  ],
  "dry_run": true
}
```

`move_titles` moves one title relative to another title **within the same row**. Use `before` or `after` with their IMDb IDs. To replace the *entire* title list, use `replace_titles: [{...}]`. An explicit `replace_titles: []` clears the row **without deleting it**. Omitting the field leaves existing titles untouched. You cannot combine `replace_titles` with `add_titles` or `remove_title_ids` in the same change.

For deletion, use `{"action":"delete","row_id":"row_..."}`. To retry account synchronization, use `{"action":"sync"}`. These actions go inside the same `changes` array; the server validates the batch before a single Redis commit.

The update result includes changed rows and their poster URLs, so an agent can show recommendations immediately after publishing them without another fetch.

### Previews, safe retries, and sync

- `dry_run: true` validates and previews the batch without saving anything. Generated row IDs in a preview are provisional.
- `expected_revision` optionally rejects stale modifications; get the current revision with `catalogs_get`.
- `request_id` prevents duplicate creations when retrying a saved batch after a timeout. Reuse the **same ID and changes** for a retry; choose a new ID for a different batch. The server retains the most recent 64 applied request IDs.
- Results distinguish **saved** catalog changes from **Stremio account sync**. Editing only titles requires no manifest sync, while creating, renaming, or deleting a row automatically synchronizes the manifest once per batch. You can explicitly retry with `action: "sync"` if that step fails.

One deployment supports 24 custom rows, with up to 100 titles in each row.

## Organize your Home screen

Ask your agent:

> Hide Cinemeta's Popular Movie and Popular Series rows, put my custom catalog add-on before Cinemeta, and move Featured Series above Featured Movies. Show me the preview first.

`home_layout_get` returns exact IDs for all installed add-ons and their catalogs, including hidden rows. `home_layout_update` can change row visibility, reorder rows within an add-on, and move whole add-ons in one batch.

```json
{
  "visibility": [
    {
      "row": { "addon_id": "com.linvo.cinemeta", "catalog_id": "top", "type": "movie" },
      "visible": false
    },
    {
      "row": { "addon_id": "com.linvo.cinemeta", "catalog_id": "top", "type": "series" },
      "visible": false
    }
  ],
  "addon_moves": [
    { "addon_id": "xyz.ashray.stremio-catalog-mcp", "before": "com.linvo.cinemeta" }
  ],
  "dry_run": true
}
```

Set `dry_run` to `false` (or omit it) to save the changes. For a row move, use `row_moves: [{ "row": {...}, "before": {...} }]`, or `after` instead of `before`. The row and its target must belong to the **same add-on**. `addon_moves` positions the entire add-on (including all its catalog rows) before or after another add-on.

Hiding a custom row keeps its titles in Redis. Hiding a compatible third-party catalog uses an existing optional `search` or `genre` filter; the original filter settings are retained privately and restored when you show it again. Intrinsically search-only catalogs and catalogs without a compatible optional filter cannot be modified safely.

Moving a whole add-on can affect streaming source precedence. Custom rows cannot be arbitrarily interleaved with rows from another add-on. Apps already open may need a refresh or sign-in to pick up a changed manifest.

If automatic Stremio account linking isn't configured, `home_layout_get` shows your custom rows only. You can still use `home_layout_update` to reorder or hide/show those rows in Redis, but their new placement won't appear in Stremio until the add-on manifest is reinstalled or the account is linked. Managing third-party add-on rows requires account linking.

## Security and synchronization

The authenticated `?key=` URL grants write access. **The `catalogs_update`, `home_layout_update`, and `watch_history` (`mark_watched` and `mark_unwatched`) actions really do write**, even though this server advertises `readOnlyHint: true` for all tools per its compatibility convention; the annotation is not an authorization boundary.

Home-layout changes keep private backups; watch-history changes use Stremio's per-item datastore with immediate re-read verification. Stremio account syncing uses an unversioned API; a saved catalog doesn't guarantee that an already-open TV has refreshed. See [Architecture and security](/docs/architecture).
