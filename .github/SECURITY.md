# Security policy

## Report a vulnerability privately

Please use [GitHub's private vulnerability reporting](https://github.com/amxv/stremio-catalog-mcp/security/advisories/new) rather than opening a public issue with exploit details. If that option is unavailable, open an issue requesting a private reporting channel **without including sensitive information**.

Never post MCP connection URLs (which include `?key=`), Stremio session keys, Redis REST tokens, or Vercel credentials in an issue, pull request, or shared log.

## Self-hosting security

Keep application credentials in the Vercel project's private environment variables, and keep the local `.local-setup/` receipt and `.env.local` files out of source control. Treat the Stremio account session as a sensitive credential: it allows account-level add-on changes. Rotate affected credentials promptly if they may have been disclosed.

This server manages catalogs and can modify a linked Stremio account's installed add-on collection. MCP `readOnlyHint` metadata is advisory and **does not** remove write capability; restrict access to the private MCP URL.
