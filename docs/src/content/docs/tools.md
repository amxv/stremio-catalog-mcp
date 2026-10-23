---
title: MCP tools
description: The seven tools your AI assistant can use to manage your Stremio catalogs.
order: 3
category: Guides
summary: What your assistant can read, create, edit, clear, delete, and synchronize.
---

Your assistant sees **seven tools**. You normally won't need to call them yourself.

| Tool | What it does |
| --- | --- |
| `catalog_list` | Lists your rows, their IDs, and the titles in each row. |
| `catalog_put` | Creates, renames, reorders, appends to, or replaces a row. |
| `catalog_remove_titles` | Removes specific titles while leaving the other titles alone. |
| `catalog_clear` | Empties a row, keeping its name and position. |
| `catalog_delete` | Removes a row entirely. |
| `catalog_sync` | Retries syncing the catalog manifest to your Stremio account. |
| `catalog_install_url` | Returns the private add-on URL for installation in Stremio. |

## Creating or editing a row

`catalog_put` is the main tool. For example, your assistant can publish:

```json
{
  "name": "Witty Whodunits",
  "type": "series",
  "items": [
    { "title": "Psych", "year": 2006 },
    { "title": "Monk", "year": 2002 }
  ]
}
```

The tool resolves names through Cinemeta. It prefers an unambiguous exact match and accepts IMDb IDs such as `tt0491738` if a search is ambiguous. The assistant can research the correct ID using its regular web tools.

To edit the same row later, it passes the returned `id` (starting with `row_`). `mode: "append"` adds new titles without duplicates; `mode: "replace"` replaces the entire ordered list. `position` moves a row among this add-on's rows, starting at zero.

`catalog_remove_titles` accepts `row_id` and a list of IMDb IDs. `catalog_clear` removes all titles but keeps the row. `catalog_delete` removes the row itself. The limits are 24 rows per deployment and 100 titles per row.

## What a successful response means

Write results distinguish a catalog saved in Redis from a **manifest synchronized with Stremio**. Adding or removing individual titles doesn't require a manifest update; creating, renaming, reordering, or deleting rows does.

The `synced` status confirms the account API accepted the manifest. It **doesn't** guarantee that an already-open TV app has refreshed the screen.

For security, treat `catalog_put`, `catalog_remove_titles`, `catalog_clear`, `catalog_delete`, and `catalog_sync` as **write-capable operations**, regardless of any advisory discovery annotations in the MCP schema. The authenticated `?key=` URL grants write access.

Technical details: [Architecture and security](/docs/architecture).
