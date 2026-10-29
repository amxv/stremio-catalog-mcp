---
title: MCP tools
description: All seven Stremio MCP tools, with input schemas, requests, and sample responses.
order: 3
category: Guides
summary: A practical reference for catalogs, Home layout, watch history, title lookup, and Android TV.
---

The server has **five built-in tools** and **two optional tools**. With TMDB lookup and Android TV enabled, your agent sees all **seven**.

| Tool | What it does | Availability |
| --- | --- | --- |
| `title_lookup` | Find IMDb IDs and key title metadata in batches | TMDB credential configured |
| `catalogs_get` | List your custom catalog rows or inspect their titles | Always |
| `catalogs_update` | Create, edit, remove, or sync custom catalogs | Always |
| `home_layout_get` | Read Home rows, visibility, and installed add-ons | Always |
| `home_layout_update` | Hide, show, or reorder Home rows and add-ons | Always |
| `watch_history` | Read, batch-check, or update watched status | Always; requires linked Stremio account to use |
| `tv_open_title` | Open a title or episode on a paired TV | Android TV integration enabled |

All examples below show **tool arguments**, not full MCP JSON-RPC envelopes. Responses are illustrative and may omit unrelated optional fields.

## 1. `title_lookup`: find the right IMDb ID

Use this when a user names a movie or show and the agent needs its exact IMDb ID. It searches movies and series together, with multiple titles handled concurrently.

**Enable:** Set `TMDB_READ_ACCESS_TOKEN` (preferred) or `TMDB_API_KEY` in the **Go API project's** environment. Without a credential, the tool isn't advertised.

**Input schema**

```ts
{
  titles: string[]; // 1-50 movie or TV title names
}
```

**Call**

```json
{
  "titles": ["Breaking Bad", "Dune", "A Totally Unknown Title"]
}
```

**Example response**

```json
{
  "results": [
    {
      "query": "Breaking Bad",
      "imdb_id": "tt0903747",
      "title": "Breaking Bad",
      "media_type": "series",
      "year": 2008,
      "language": "en",
      "genres": ["Drama", "Crime"],
      "imdb_rating": 9.5
    },
    {
      "query": "Dune",
      "status": "ambiguous",
      "matches": [
        {
          "imdb_id": "tt1160419",
          "title": "Dune",
          "media_type": "movie",
          "year": 2021,
          "release_date": "2021-09-15",
          "language": "en",
          "genres": ["Science Fiction", "Adventure"],
          "imdb_rating": 8.0,
          "description": "Paul Atreides journeys to the most dangerous planet..."
        },
        {
          "imdb_id": "tt0087182",
          "title": "Dune",
          "media_type": "movie",
          "year": 1984,
          "release_date": "1984-12-14",
          "language": "en",
          "genres": ["Science Fiction", "Adventure"],
          "imdb_rating": 6.3,
          "description": "A young man becomes involved in a struggle for..."
        }
      ]
    },
    {
      "query": "A Totally Unknown Title",
      "status": "not_found"
    }
  ]
}
```

One clear match returns a **flat result** with the IMDb ID, name, type, year, original language, genres, and available IMDb rating. Competing exact-name titles return **up to six ranked candidates** with release dates and short descriptions, so the agent can choose or ask the user. The response preserves input order; failures are reported per title rather than guessed. `more_matches: true` signals that additional candidates may exist beyond the six returned.

You can disambiguate titles using a year or qualifier, for example `Dune (2021)`, `The Office (US)`, `Oldboy (2003)`, or `12 Monkeys (series)`. A prominent candidate one year away may also appear when premiere and theatrical release years differ, rather than silently selecting a less relevant exact-year title. Matching ignores accents and common punctuation differences, including `Amelie` versus `Amélie`. Shortened and translated names are considered when applicable, but misspellings are **not** silently corrected. When no exact match is found, optional `suggestions` are explicitly unverified alternatives.

Languages use ISO codes such as `en`. Genres come from TMDB. **IMDb ratings come from Cinemeta** when available; TMDB's different vote score is never substituted. The rating field is omitted if it can't be verified. Descriptions and exact dates are omitted for an unambiguous title to save tokens.

## 2. `catalogs_get`: read your collections

**Input schema**

```ts
{
  row_id?: string;              // Detailed view of one row
  include_titles?: boolean;     // Include every row's titles and poster URLs
  include_install_url?: boolean; // Return private Stremio add-on URL
}
```

**Call: list rows**

```json
{}
```

**Example response**

```json
{
  "revision": 8,
  "rows": [
    {
      "id": "row_example",
      "name": "Weekend Sci-Fi",
      "type": "movie",
      "position": 0,
      "visible": true,
      "title_count": 2
    }
  ]
}
```

**Call: read a specific row**

```json
{
  "row_id": "row_example"
}
```

That response includes an ordered `titles` array with IMDb IDs, names, years, types, and poster URLs. Use `include_titles` to read **all** rows in detail. The add-on installation URL is private and returned only when `include_install_url` is requested.

## 3. `catalogs_update`: create and edit collections

All changes are validated and saved together. Use IMDb IDs from `title_lookup` when available; title/year lookup through Cinemeta also works.

**Input schema (simplified)**

```ts
type Title = { id?: string; title?: string; year?: number };
type Change =
  | { action: "create"; name: string; type: "movie" | "series"; titles?: Title[] }
  | { action: "edit"; row_id: string; name?: string;
      add_titles?: Title[]; remove_title_ids?: string[];
      replace_titles?: Title[];
      move_titles?: { id: string; before?: string; after?: string }[] }
  | { action: "delete"; row_id: string }
  | { action: "sync" };

{
  changes: Change[];          // 1-24 operations, at most 100 resolved titles
  dry_run?: boolean;          // Preview without saving
  expected_revision?: number; // Reject edits if the revision has changed
  request_id?: string;        // Idempotency key for safe retries
}
```

**Call: create a series collection**

```json
{
  "changes": [
    {
      "action": "create",
      "name": "Great TV Dramas",
      "type": "series",
      "titles": [
        { "id": "tt0903747", "title": "Breaking Bad" },
        { "id": "tt1632701", "title": "Suits" }
      ]
    }
  ],
  "request_id": "great-tv-dramas-v1"
}
```

**Example response (selected fields)**

```json
{
  "dry_run": false,
  "saved": true,
  "changed": true,
  "replayed": false,
  "revision": 9,
  "applied_revision": 9,
  "changes": [
    {
      "action": "create",
      "row_id": "row_example",
      "catalog": {
        "id": "row_example",
        "name": "Great TV Dramas",
        "type": "series",
        "items": [
          { "id": "tt0903747", "type": "series", "title": "Breaking Bad", "year": 2008 }
        ]
      }
    }
  ],
  "sync": { "status": "succeeded", "message": "Catalog manifest synced to Stremio." }
}
```

**Call: edit or clear a row**

```json
{
  "changes": [
    {
      "action": "edit",
      "row_id": "row_example",
      "add_titles": [{ "id": "tt0944947", "title": "Game of Thrones" }],
      "remove_title_ids": ["tt1632701"]
    }
  ],
  "dry_run": true
}
```

To clear a row without deleting it, use `replace_titles` with an empty array. Use `delete` to remove the row; use `sync` to retry account synchronization. `dry_run` previews changes; `expected_revision` protects against stale edits; reusing the same `request_id` and changes avoids creating duplicates on retries.

**Limits:** Up to 24 custom rows, each with 100 titles. Creating, renaming, or deleting rows may require a Stremio manifest sync; the response reports whether data was saved and whether sync succeeded separately.

## 4. `home_layout_get`: inspect your Home screen

Use this before changing Home layout so you have the exact installed add-on and catalog IDs.

**Input schema**

```ts
{} // No arguments
```

**Call**

```json
{}
```

**Example response (shortened)**

```json
{
  "addons": [
    {
      "id": "com.linvo.cinemeta",
      "name": "Cinemeta",
      "position": 0,
      "has_stream_resource": false,
      "rows": [
        {
          "id": "top",
          "type": "series",
          "name": "Popular Series",
          "position": 0,
          "visible": true,
          "can_hide": true,
          "can_show": false
        }
      ]
    }
  ],
  "note": ""
}
```

Hidden rows are included. Add-on streaming credentials and private URLs are not exposed. Without Stremio account linking, only your custom rows are available.

## 5. `home_layout_update`: hide or move Home rows

**Input schema (simplified)**

```ts
type Row = { addon_id: string; catalog_id: string; type: string };

{
  visibility?: { row: Row; visible: boolean }[];
  row_moves?: { row: Row; before?: Row; after?: Row }[];
  addon_moves?: { addon_id: string; before?: string; after?: string }[];
  dry_run?: boolean;
}
```

**Call: preview hiding a row**

```json
{
  "visibility": [
    {
      "row": {
        "addon_id": "com.linvo.cinemeta",
        "catalog_id": "top",
        "type": "series"
      },
      "visible": false
    }
  ],
  "dry_run": true
}
```

**Example response (selected fields)**

```json
{
  "dry_run": true,
  "changed": true,
  "synced": false,
  "summary": ["Hide Popular Series"],
  "message": "Preview only; Stremio and Redis were not modified."
}
```

Remove `dry_run` to apply. To reorder, supply `row_moves` for rows **within the same add-on** or `addon_moves` for entire installed add-ons. Both use a `before` or `after` target. Only compatible third-party rows can be hidden; unsupported rows are refused rather than altered. Other add-ons and streaming resources remain installed. The full response also includes the resulting `layout`.

## 6. `watch_history`: read or update viewing history

All watch-history operations use **IMDb IDs**, never text title searches. First resolve a title using `title_lookup` or another source.

**Input schema**

```ts
{
  action?: "get" | "check" | "mark_watched" | "mark_unwatched";
  imdb_id?: string;                 // One title: get or mark
  imdb_ids?: string[];              // 1-50 unique IDs for check
  media_type?: "movie" | "series";  // Optional when type is known
  season?: number;                  // With episodes for a scoped update
  episodes?: number[];              // Episode numbers for a scoped update
  limit?: number;                   // Recent-history listing, 1-50
  offset?: number;                  // Recent-history pagination
}
```

**Call: list recent activity**

```json
{}
```

**Call: check several titles at once**

```json
{
  "action": "check",
  "imdb_ids": ["tt0903747", "tt0816692"]
}
```

**Example batch response (illustrative)**

```json
{
  "action": "check",
  "count": 2,
  "checks": [
    {
      "imdb_id": "tt0903747",
      "title": "Breaking Bad",
      "media_type": "series",
      "recorded": true,
      "status": "partially_watched",
      "episode_history": "verified",
      "seasons": [
        { "season": 1, "episode_count": 7, "watched_episodes": [1, 2, 3] }
      ]
    },
    {
      "imdb_id": "tt0816692",
      "title": "Interstellar",
      "media_type": "movie",
      "recorded": true,
      "status": "watched"
    }
  ]
}
```

**Call: inspect one show, including confirmed watched episodes**

```json
{
  "action": "get",
  "imdb_id": "tt0903747",
  "season": 1
}
```

**Call: mark a whole show watched**

```json
{
  "action": "mark_watched",
  "imdb_id": "tt0903747"
}
```

**Call: correct specific episodes**

```json
{
  "action": "mark_unwatched",
  "imdb_id": "tt0903747",
  "season": 1,
  "episodes": [2]
}
```

The four possible batch-check statuses are `watched`, `partially_watched`, `not_watched`, and `unverified`. For incomplete shows, only confirmed **episode numbers** are included; unreached episodes' titles and plots are never returned. Unknown or missing records **do not prove** a title was never watched.

Whole-series `mark_watched` marks all **released regular episodes** across seasons in one verified write, excluding future releases and specials. Whole-title `mark_unwatched` clears playback history, but Stremio retains a small removed sync record (tombstone); it cannot be physically deleted through this API. Always obtain explicit user intent before changing history.

**Requires:** `STREMIO_AUTH_KEY` in the API project. Account changes are verified by reading back, but the upstream Stremio account API is undocumented.

## 7. `tv_open_title`: open Stremio on Android TV

**Enable:** `TV_OPEN_ENABLED=true`, Firebase configuration, and one paired TV. Follow the [TV setup guide](/docs/tv-remote). With the flag disabled, this tool does not appear.

**Input schema**

```ts
{
  media_type: "movie" | "series";
  imdb_id: string;
  season?: number; // Omit for series overview; 0 for specials
  episode?: number; // Omit for series overview; must accompany season
}
```

**Call: open an episode**

```json
{
  "media_type": "series",
  "imdb_id": "tt0903747",
  "season": 3,
  "episode": 4
}
```

**Example response**

```json
{
  "status": "accepted_by_fcm",
  "delivery_status": "launch_requested",
  "command_id": "example-command-id",
  "device_name": "Living Room TV",
  "imdb_id": "tt0903747",
  "media_type": "series",
  "season": 3,
  "episode": 4,
  "note": "TV received the command and requested Stremio to open. On-screen navigation is not confirmed."
}
```

Omit `season` and `episode` to open a series overview or a movie. **Nothing autoplays.** Delivery may report `launch_requested`, `notification_posted`, `notification_unavailable`, or `not_confirmed`. Even a launch receipt **does not prove** Stremio visibly changed screens. Device pairing is managed privately at `/tv/setup`, not through MCP tools.

## Typical agent workflow

1. Resolve a list of names with `title_lookup` (when enabled).
2. Check their IMDb IDs in one `watch_history` batch.
3. Create a catalog with `catalogs_update` or adjust the Home screen with `home_layout_update`.
4. Optionally open a selected movie or episode with `tv_open_title`.

Your MCP connection URL contains a private `MCP_API_KEY`. Keep it confidential. For compatibility, the server advertises `readOnlyHint: true` on every tool, **including tools that really modify data**. Treat it as discovery metadata, not a security or approval boundary.

For installation and environment variables, see [Quickstart](/docs/quickstart) and [Deployment](/docs/deployment). For edge cases and account safety, see [Architecture and security](/docs/architecture).
