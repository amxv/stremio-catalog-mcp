# Stremio Catalog MCP

**Your AI recommends. Stremio plays.** This lightweight [Model Context Protocol](https://modelcontextprotocol.io/) server lets ChatGPT or another MCP assistant publish movie and TV recommendations to native Stremio catalog rows, then organize your Home screen.

**[Website](https://stremio-catalog-mcp.ashray.xyz/) · [Quickstart](https://stremio-catalog-mcp.ashray.xyz/docs/quickstart) · [Self-hosting guide](https://stremio-catalog-mcp.ashray.xyz/docs/deployment) · [MCP tools](https://stremio-catalog-mcp.ashray.xyz/docs/tools)**

## What it does

Ask your assistant to find something to watch, then send the results to Stremio. It can create, rename, reorder, refresh, clear, and delete custom catalog rows, and add or remove individual titles. **Home layout tools** can show/hide compatible rows from Cinemeta and other installed add-ons, reorder catalogs within each add-on, and rearrange whole add-ons. Existing Cinemeta metadata and streaming add-ons keep working; this service supplies **catalogs only**, never streams or its own recommendations.

The **five-tool interface** stays focused: `catalogs_get`/`catalogs_update` manage your rows; `home_layout_get`/`home_layout_update` manage visibility and ordering; `watch_history` lets an agent search your Stremio watch history, check exactly which TV episodes are marked watched, and explicitly mark a movie or specific episode(s) **watched or unwatched**. One call with `action: "mark_watched"` and only the movie or TV title (or IMDb ID) marks the full title watched; for series it automatically marks all released regular episodes across every season, excluding upcoming episodes and specials. One call with `action: "mark_unwatched"` and only the title clears **all** playback history and marks the item removed. Stremio retains a removed sync tombstone because its API has no physical per-title deletion endpoint. Detailed catalog reads and update results include saved **poster URLs**. Catalog changes can be batched, previewed with `dry_run`, and retried safely using `request_id`.

**Spoiler-aware TV context:** ask "How far have I watched Breaking Bad?" and the agent can check Stremio's episode-completion bitmap instead of guessing from the last episode opened. If only S01E01-E02 are confirmed, the tool returns that boundary alongside the episodes' titles and synopses, without future-episode descriptions. Ask "I already watched episode 3" or "Actually, I haven't seen episode 2" and the same tool can update Stremio in either direction. It requires `STREMIO_AUTH_KEY` and reflects Stremio's synced account state; missing or unmappable data is reported as unknown, never assumed unwatched.

**IMDb ratings, runtimes, genres, cast, and descriptions show up automatically in the Stremio TV preview** when Cinemeta has that information. Agents still only provide titles or IMDb IDs. The Stremio catalog endpoint fetches full Cinemeta metadata and keeps a 24-hour Redis cache separate from the saved rows, including for catalogs created before this improvement. It does not install a competing metadata add-on, affect stream selection, or change the MCP tool interface. During an upstream outage, saved titles and posters still work, but some preview details may be unavailable until Cinemeta recovers.

For example: **“Hide Cinemeta's Popular and Featured rows, move my MCU row first, and place my catalog add-on above Cinemeta.”** `home_layout_get` lists the current layout; one batched `home_layout_update` call supports visibility changes, row moves, addon moves, and a no-write `dry_run` preview. Third-party rows without optional search/genre filters cannot be safely hidden. Individual rows cannot be moved across addon boundaries, although complete addons can be reordered.

## Let your agent set it up

Fork or clone this repository and give your coding agent this instruction:

> Self-host this Stremio Catalog MCP for me. Follow the [agent-led Quickstart](https://stremio-catalog-mcp.ashray.xyz/docs/quickstart). Deploy the Go API to my Vercel account, use a Free Upstash Redis database **or an existing compatible Redis REST URL/token**, securely connect my Stremio account, verify the endpoints, and copy the full authenticated MCP URL to my clipboard. Ask me only for required sign-ins, secret approvals, and the final ChatGPT plugin step. Do not print credentials or enable paid services.

The included Go helper automates most of the provisioning:

```bash
go run ./cmd/setup --project my-stremio-catalog  # Preview, no changes
go run ./cmd/setup --apply --project my-stremio-catalog
```

The helper guides you through secure credentials, provisions Free Redis, configures and deploys Vercel, and copies the MCP URL without printing its secret. Agents can use protected `--upstash-email-file`, `--upstash-key-file`, and `--stremio-key-file` arguments with `--yes` after human approval. Run `go run ./cmd/setup --copy-url` to copy your saved URL again.

Already have Redis? Use `--redis-mode existing` with `--redis-rest-url-file` and `--redis-rest-token-file`. No Upstash Developer API key is required. To connect Stremio without manually handling its session key, add `--stremio-browser`: your agent opens Stremio Web, you log in, and it securely reads the signed-in session. [Fidelius](https://fidelius.ashray.xyz/) is a useful option for securely passing tokens and private files to agents.

**The helper is optional.** The [self-hosting reference](https://stremio-catalog-mcp.ashray.xyz/docs/deployment) also walks through the entire manual Vercel + Redis setup. Review the [Go bootstrap source](https://github.com/amxv/stremio-catalog-mcp/blob/main/cmd/setup/main.go) before running it.

### Add to ChatGPT

Once your agent has copied the URL: **Plugins → Create custom MCP server → No authentication → enter a name → Add as plugin.** Paste the full URL including `?key=...`. *No authentication* means ChatGPT won't initiate a separate OAuth flow; the URL still contains a private API key.

Try: **“Find six excellent mind-bending sci-fi series and make a Stremio row called Sci-Fi Worth Watching.”** Or: **“Show me every Stremio Home row and hide the default recommendations.”** Refresh Stremio if the new layout hasn't appeared yet.

## What runs where

- **Go + Vercel:** stateless Streamable HTTP MCP endpoint and standard Stremio catalog endpoints.
- **Redis REST (Upstash-compatible):** durable, ordered rows and synchronization safeguards.
- **Cinemeta:** IMDb ID lookup, with playback handled by your existing add-ons.
- **Stremio account API:** optional account linking to add/rename rows, manage Home layout, and read/update watch status. The account API is unversioned; watch status reflects what Stremio has synced from your devices.

One deployment manages **one Stremio account**. Keep all generated keys, Redis tokens, and the Stremio session private. See the website's [technical reference](https://stremio-catalog-mcp.ashray.xyz/docs/architecture) for security and limitations.

## Develop

Go 1.26+, Bun for the optional docs site, and a Vercel account for hosting. Run `make check` to test the Go server; `make docs-check` and `make docs-build` validate the Astro/ZueDocs site. Credentials aren't required for tests.

Open source under [Apache-2.0](LICENSE). Self-host it on your own Vercel and Redis accounts.
