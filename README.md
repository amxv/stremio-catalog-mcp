# Stremio Catalog MCP

**Your AI recommends. Stremio plays.** This lightweight [Model Context Protocol](https://modelcontextprotocol.io/) server lets ChatGPT or another MCP assistant publish movie and TV recommendations to native Stremio catalog rows, then organize your Home screen.

**[Website](https://stremio-catalog-mcp.ashray.xyz/) · [Quickstart](https://stremio-catalog-mcp.ashray.xyz/docs/quickstart) · [Self-hosting guide](https://stremio-catalog-mcp.ashray.xyz/docs/deployment) · [MCP tools](https://stremio-catalog-mcp.ashray.xyz/docs/tools)**

## What it does

Ask your assistant to find something to watch, then send the results to Stremio. It can create, rename, reorder, refresh, clear, and delete custom catalog rows, and add or remove individual titles. **Home layout tools** can show/hide compatible rows from Cinemeta and other installed add-ons, reorder catalogs within each add-on, and rearrange whole add-ons. Existing Cinemeta metadata and streaming add-ons keep working; this service supplies **catalogs only**, never streams or its own recommendations.

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
- **Stremio account API:** optional account linking to add and rename rows and manage the Home layout; it is an unversioned integration and can require an app restart.

One deployment manages **one Stremio account**. Keep all generated keys, Redis tokens, and the Stremio session private. See the website's [technical reference](https://stremio-catalog-mcp.ashray.xyz/docs/architecture) for security and limitations.

## Develop

Go 1.26+, Bun for the optional docs site, and a Vercel account for hosting. Run `make check` to test the Go server; `make docs-check` and `make docs-build` validate the Astro/ZueDocs site. Credentials aren't required for tests.

Open source under [Apache-2.0](LICENSE). Self-host it on your own Vercel and Redis accounts.
