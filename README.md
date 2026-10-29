# Stremio Catalog MCP

**Organize Stremio with your AI assistant.** This self-hosted [MCP](https://modelcontextprotocol.io/) server lets ChatGPT and other AI agents manage your Stremio Home screen, custom movie and TV collections, and watch history. It can also open titles on your Android TV if you opt in.

**[Website](https://stremio-catalog-mcp.ashray.xyz/) · [Get started](https://stremio-catalog-mcp.ashray.xyz/docs/quickstart) · [All tools](https://stremio-catalog-mcp.ashray.xyz/docs/tools) · [TV remote](https://stremio-catalog-mcp.ashray.xyz/docs/tv-remote)**

## What can it do?

- **Create collections.** Make, edit, reorder, and delete custom movie or series rows. Add or remove titles, with posters and metadata such as IMDb ratings, descriptions, and cast from Cinemeta.
- **Customize Home.** Hide or show compatible catalog rows, rearrange rows within an add-on, and change the order of installed add-ons—without removing your streaming providers.
- **Manage watch history.** Check up to 50 IMDb titles in one call, see exactly which TV episodes you've watched, and mark a movie, series, or specific episodes watched or unwatched. Results avoid spoiling future episodes.
- **Open titles on Android TV (optional).** Ask your AI to open a movie, show, or exact episode in Stremio. No autoplay. One paired TV, delivery acknowledgments, and no always-running laptop or TV service.

Your AI can research recommendations and send them to Stremio. **This project doesn't supply streams or replace Stremio's existing add-ons.**

## Try asking

> “Make a Stremio collection of great sci-fi thrillers.”
>
> “Hide the default Popular rows and move my MCU collection to the top.”
>
> “Which Breaking Bad episodes have I watched?”
>
> “Open Breaking Bad season 3, episode 4 on my TV.” *(Optional TV integration)*

## Get started

Deploy your own instance using **Vercel** and **Upstash-compatible Redis**, then connect your Stremio account and AI assistant. The [agent-friendly Quickstart](https://stremio-catalog-mcp.ashray.xyz/docs/quickstart) walks through everything, or you can ask a coding agent:

> Set up [Stremio Catalog MCP](https://stremio-catalog-mcp.ashray.xyz/docs/quickstart) for me using my Vercel and Stremio accounts. Follow the self-hosting guide, use Free services where possible, and ask me for required sign-ins. Keep credentials private and help me connect the MCP to ChatGPT.

**TV control is off by default.** The MCP has five tools normally; enabling `TV_OPEN_ENABLED=true` adds only `tv_open_title`. Follow the separate [Android TV setup guide](https://stremio-catalog-mcp.ashray.xyz/docs/tv-remote) for Firebase Spark, ADB installation, and pairing. You can use the rest of the project without Firebase or an Android TV.

## Development

Written in Go, hosted on Vercel, with Redis for state and Cinemeta for title metadata. The optional Android TV companion uses Firebase Cloud Messaging.

Run `make check` for Go tests and vet. See the [developer documentation](https://stremio-catalog-mcp.ashray.xyz/docs/architecture) for architecture, deployment, and other checks.

Licensed under [Apache 2.0](LICENSE).
