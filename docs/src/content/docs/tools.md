---
title: MCP tools
description: The nine tools your AI assistant can use to manage your Stremio catalogs and Home layout.
order: 3
category: Guides
summary: What your assistant can read, create, edit, arrange, hide, restore, and synchronize.
---

Your assistant sees **nine tools**. You normally won't need to call them yourself.

| Tool | What it does |
| --- | --- |
| `catalog_list` | Lists your rows, their IDs, and the titles in each row. |
| `catalog_put` | Creates, renames, reorders, appends to, or replaces a row. |
| `catalog_remove_titles` | Removes specific titles while leaving the other titles alone. |
| `catalog_clear` | Empties a row, keeping its name and position. |
| `catalog_delete` | Removes a row entirely. |
| `catalog_sync` | Retries syncing the catalog manifest to your Stremio account. |
| `catalog_install_url` | Returns the private add-on URL for installation in Stremio. |
| `home_layout_get` | Lists all installed add-ons and catalogs (including hidden rows), their current order, and visibility capabilities. |
| `home_layout_update` | Changes multiple row visibilities, catalog positions, and installed add-on positions in one request. Supports a no-write preview. |

## Arrange your Home screen

Try asking:

> Hide Cinemeta's Popular Movie and Popular Series rows, put my custom catalog add-on before Cinemeta, and move Featured Series above Featured Movies. Show me the preview first.

The assistant uses `home_layout_get` to obtain exact identifiers. It can then send one batch to `home_layout_update`, with `dry_run: true` first if you want a preview:

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

Set `dry_run` to `false` (or omit it) to save the changes. For a row move, provide `row_moves: [{ "row": {...}, "before": {...} }]` or use `after` instead of `before`. The `row` and target must belong to the **same add-on**. `addon_moves` positions the entire add-on (and all its rows) before or after another installed add-on.

Hiding a custom row keeps the titles and ID stored in Redis. Hiding a third-party catalog uses a supported optional `search` or `genre` filter; the original filter settings are saved privately and restored when you unhide it. Catalogs that are intrinsically search-only or have no usable optional filter aren't modified. Cinemeta's familiar Popular and Featured rows can also be restored if you hid them with the older Stremio workaround before installing this feature.

Moving an add-on can affect streaming source precedence. Custom rows cannot be arbitrarily interleaved with rows from a different add-on. The Home layout is held in your Stremio account, and already-open apps may need to reload or sign in again to refresh a cached manifest.

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

For security, treat `catalog_put`, `catalog_remove_titles`, `catalog_clear`, `catalog_delete`, `catalog_sync`, and `home_layout_update` as **write-capable operations**, regardless of any advisory discovery annotations in the MCP schema. The authenticated `?key=` URL grants write access.

Technical details: [Architecture and security](/docs/architecture).
