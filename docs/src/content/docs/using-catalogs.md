---
title: Using your catalogs
description: What to ask your AI assistant, how to keep rows fresh, and what to expect in Stremio.
order: 2
category: Guides
summary: Create playlists for any mood, then edit them by chatting naturally.
---

Once connected, you don't need to use API calls or type movie IDs. **Just talk to your assistant.** It researches and chooses the recommendations, then sends them to your Stremio account.

## Make a row for any mood

> Find ten clever mysteries with a little comedy and an IMDb rating above 7. Send them to Stremio as a row called *Witty Whodunits*.

> Find the best science-fiction movies from the 2010s that aren't superhero movies. Make a catalog called *Sci-Fi Essentials*.

> Look up the strongest new thrillers this month and make me a row of the ones worth watching.

The assistant can use its ordinary search, memory, and recommendation tools. **This MCP doesn't choose titles or call an AI model.** It only publishes the final list.

## Change a list by talking

> Add *Black Mirror* to my sci-fi row.

> Remove *Dark*. Move *Severance* to the top and rename the row *Mind-Bending TV*.

> Clear my *Tonight's Picks* row and replace it with five good movies under two hours.

Your assistant can list all saved rows, append or remove individual titles, replace an entire list, rename or reorder a row, clear it without deleting it, or delete it completely.

## What shows up on the TV

Each row contains **movies or TV series**, not a mixture. If you ask for both, your assistant should create separate rows. Titles use IMDb IDs compatible with Cinemeta and your installed streaming add-ons.

Changes to the **titles inside an existing row** generally appear when Stremio fetches that catalog again. New rows, renamed rows, and deleted rows require the account-sync integration or a manual add-on reinstall. Because Stremio caches data, you may need to refresh or restart the app.

The MCP does **not** control playback, install streaming providers, track your watch history, or guarantee a title is available from a particular streaming service.

## Can I automate recommendations?

You can ask a scheduled AI task to research picks and update a stable *Tonight's Picks* row. Whether that task can call the MCP without extra approval depends on your assistant's scheduling, plugin, and permission settings. The Stremio server does not run its own recommendation scheduler.

See [all seven MCP tools](/docs/tools) for the exact operations your assistant can use.
