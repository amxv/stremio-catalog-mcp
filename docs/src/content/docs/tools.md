---
title: MCP tools
description: Manage rows and their titles directly from ChatGPT or any MCP client.
order: 2
category: Tools
summary: Create, rename, append, remove, replace, clear, delete and synchronize catalogs.
---

## `catalog_put`

The primary write operation. Create a new row when `id` is absent, or update an existing row using the ID returned by `catalog_list`.

```json
{
  "name": "Cozy Mysteries",
  "type": "series",
  "items": [
    { "title": "Psych", "year": 2006 },
    { "id": "tt0312172", "title": "Monk" }
  ]
}
```

- `id`: optional existing row ID (`row_...`). Without one a new row is created.
- `name`: displayed row name; required for creation, optional for editing.
- `type`: `movie` or `series`; required for creation. Row types are immutable.
- `items`: title objects, each with an IMDb `id` or an exact `title` and optional `year`.
- `mode`: `append` (default; deduplicates) or `replace` (replaces the entire list).
- `position`: optional zero-based position among this add-on's rows.

Titles are resolved through Cinemeta. If a title is ambiguous or missing, the tool returns an error and asks for an IMDb ID. The agent can obtain IMDb IDs using its usual web research; this service does not try to choose films on its own.

## `catalog_list`

Returns current rows in order, including stable row IDs and the saved titles/IMDb IDs. Call it before modifying an existing row.

## `catalog_remove_titles`

Supply `row_id` and an `ids` array of IMDb IDs. Other titles remain in the same order.

## `catalog_clear`

Supply `row_id` to remove all titles while keeping the row. Equivalently, use `catalog_put` with that `id`, `mode: "replace"`, and `items: []`.

## `catalog_delete`

Supply `row_id` to delete an entire row and remove its declaration from the installed manifest. It does not uninstall the add-on or affect unrelated streaming providers.

## `catalog_sync`

Retry Stremio account synchronization. Useful if another agent was already syncing, the account API briefly failed, or a new row was created before the account was linked. Returns a status indicating whether synchronization was verified. Saving data is **not** the same as proving it has appeared on the TV.

## `catalog_install_url`

Returns the private add-on manifest URL for first-time installation. Keep it secret: whoever possesses it can read your catalog rows, but cannot use it to write via MCP.

All write tools return explicit sync status so the agent can tell the difference between a saved update and an account-visible one. Changes to the screen may take effect only after Stremio refreshes.
