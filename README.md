# Stremio Catalog MCP

**Your AI recommends. Stremio plays.** This lightweight [Model Context Protocol](https://modelcontextprotocol.io/) server lets ChatGPT or another MCP assistant publish movie and TV recommendations directly to named rows on your Stremio home screen.

**[Website](https://stremio-catalog-mcp.ashray.xyz/) · [Quickstart](https://stremio-catalog-mcp.ashray.xyz/docs/quickstart) · [Self-hosting guide](https://stremio-catalog-mcp.ashray.xyz/docs/deployment) · [MCP tools](https://stremio-catalog-mcp.ashray.xyz/docs/tools)**

## What it does

Ask your assistant to find something to watch, then send the results to Stremio. It can create, rename, reorder, refresh, clear, and delete catalog rows, and add or remove individual titles. Existing Cinemeta metadata and streaming add-ons keep working; this service supplies **catalogs only**, never streams or its own recommendations.

## Let your agent set it up

Fork or clone this repository and give your coding agent this instruction:

> Self-host this Stremio Catalog MCP for me. Follow the [agent-led Quickstart](https://stremio-catalog-mcp.ashray.xyz/docs/quickstart). Deploy the Go API to my Vercel account, create a dedicated Free Upstash Redis database, securely connect my Stremio account, verify the endpoints, and copy the full authenticated MCP URL to my clipboard. Ask me only for required sign-ins, secret approvals, and the final ChatGPT plugin step. Do not print credentials or enable paid services.

The included Go helper automates most of the provisioning:

```bash
go run ./cmd/setup --project my-stremio-catalog  # Preview, no changes
go run ./cmd/setup --apply --project my-stremio-catalog
```

The helper guides you through secure credentials, provisions Free Redis, configures and deploys Vercel, and copies the MCP URL without printing its secret. Agents can use protected `--upstash-email-file`, `--upstash-key-file`, and `--stremio-key-file` arguments with `--yes` after human approval. Run `go run ./cmd/setup --copy-url` to copy your saved URL again.

### Add to ChatGPT

Once your agent has copied the URL: **Plugins → Create custom MCP server → No authentication → enter a name → Add as plugin.** Paste the full URL including `?key=...`. *No authentication* means ChatGPT won't initiate a separate OAuth flow; the URL still contains a private API key.

Try: **“Find six excellent mind-bending sci-fi series and make a Stremio row called Sci-Fi Worth Watching.”** Refresh Stremio if the new row hasn't appeared yet.

## What runs where

- **Go + Vercel:** stateless Streamable HTTP MCP endpoint and standard Stremio catalog endpoints.
- **Upstash Redis REST:** durable, ordered rows and synchronization safeguards.
- **Cinemeta:** IMDb ID lookup, with playback handled by your existing add-ons.
- **Stremio account API:** optional account linking to add and rename rows automatically; it is an unversioned integration and can require an app restart.

One deployment manages **one Stremio account**. Keep all generated keys, Redis tokens, and the Stremio session private. See the website's [technical reference](https://stremio-catalog-mcp.ashray.xyz/docs/architecture) for security and limitations.

## Develop

Go 1.26+, Bun for the optional docs site, and a Vercel account for hosting. Run `make check` to test the Go server; `make docs-check` and `make docs-build` validate the Astro/ZueDocs site. Credentials aren't required for tests.

Licensed under [Apache-2.0](LICENSE). Designed for self-hosting and a future public release.
