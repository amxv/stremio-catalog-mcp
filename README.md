# Stremio Catalog MCP

Manage [Stremio](https://www.stremio.com/) with ChatGPT or any MCP-capable agent. Create your own movie and TV collections, arrange the Home screen, and check or update watch history by chatting.

**[Website](https://stremio-catalog-mcp.ashray.xyz/) · [Quickstart](https://stremio-catalog-mcp.ashray.xyz/docs/quickstart) · [MCP tools and examples](https://stremio-catalog-mcp.ashray.xyz/docs/tools)**

## What it does

- **Custom catalogs:** Create, edit, reorder, and remove collections of movies or shows, complete with posters.
- **Home layout:** Hide unwanted rows and reorder catalogs or installed add-ons without uninstalling streaming providers.
- **Watch history:** Check up to 50 IMDb IDs at once, see watched episodes without spoilers, or mark movies and episodes watched or unwatched.
- **Title lookup (optional):** Resolve up to 50 movie and TV names to IMDb IDs using TMDB. Results include year, language, genres, and IMDb ratings when available, with up to six ranked matches for ambiguous titles.
- **Android TV remote (optional):** Open a movie, show, or specific episode in Stremio on your paired TV, without autoplay.

Your agent handles discovery and recommendations. This server handles Stremio. **It doesn't provide streams or replace your installed add-ons.**

## Ask your agent

> “Make a sci-fi collection from the shows we discussed.”
>
> “Hide the default Popular rows and move my MCU collection to the top.”
>
> “Which Breaking Bad episodes have I watched?”
>
> “Find the IMDb IDs for Suits and Spider-Man 3.” *(Optional title lookup)*
>
> “Open Breaking Bad season 3, episode 4 on my TV.” *(Optional TV remote)*

## Get started

Self-host the Go server on **Vercel**, with an **Upstash-compatible Redis REST** database. Follow the [Quickstart](https://stremio-catalog-mcp.ashray.xyz/docs/quickstart) to connect your Stremio account and MCP client. The guide includes a setup helper and a manual option.

The MCP has **five core tools**. Two more can be enabled independently:

| Feature | Enable with | Added tool |
| --- | --- | --- |
| TMDB title lookup | `TMDB_READ_ACCESS_TOKEN` (or `TMDB_API_KEY`) | `title_lookup` |
| Android TV remote | `TV_OPEN_ENABLED=true` and [TV setup](https://stremio-catalog-mcp.ashray.xyz/docs/tv-remote) | `tv_open_title` |

With both enabled, your agent sees **seven tools**. Neither integration is required for catalog and history management. See the [tools reference](https://stremio-catalog-mcp.ashray.xyz/docs/tools) for complete input schemas and copyable examples.

## Development

The MCP server and Stremio add-on are written in Go. The documentation site is Astro. Run `make check` for Go tests and vet, or see the [architecture guide](https://stremio-catalog-mcp.ashray.xyz/docs/architecture) for implementation and deployment details.

Licensed under [Apache 2.0](LICENSE).
