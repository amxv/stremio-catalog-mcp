# Contributing to Stremio Catalog MCP

This is a **catalog bridge**, not a recommendation engine or streaming provider. Keep the server small, self-hostable, and compatible with existing Stremio add-ons.

Start with the [documentation website](https://stremio-catalog-mcp.ashray.xyz/docs). For Go changes run `make check` and `go test -race ./...`. For documentation run `make docs-check` and `make docs-build` sequentially.

Tests must not require live credentials or modify a real Stremio account. The setup helper has mocked Upstash and Vercel tests; extend them when changing provisioning behavior. Do not commit `.env` files, `.local-setup` receipts, private manifest URLs, or any account session.

Follow [AGENTS.md](AGENTS.md) for repository conventions. Documentation improvements should stay approachable for humans while giving coding agents concrete setup and troubleshooting instructions.
