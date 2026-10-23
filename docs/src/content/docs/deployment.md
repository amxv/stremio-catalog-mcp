---
title: Deployment
description: Set up the Go MCP Function and optional ZueDocs site as separate Vercel projects.
order: 3
category: Operations
summary: Vercel, custom domains, API keys, and Git-triggered deployments.
---

## API Vercel project

Create a new Vercel project from your Git repository. Its **Root Directory** should be the repository root (`/`), Framework Preset **Other**. The root `vercel.json` maps `/mcp` to the Go Function under `api/mcp.go` and limits Git auto-deployments to `main`.

Set **`MCP_API_KEY`** as a Production environment variable; use a long random value and keep it secret.

```text
https://api.your-domain.example/mcp?key=<your-secret>
```

Test the MCP initialization handshake and verify that unauthenticated requests get 401 before connecting a client.

## Documentation project

If desired, create a separate Vercel project from the same Git repository and set Root Directory to **`docs`** and Framework Preset to **Astro**. `docs/vercel.json` configures the isolated Bun installation and Astro build. The docs project doesn't need access to MCP credentials.

## Custom domains

Add each host to the corresponding Vercel project, inspect its required DNS records, and configure those records in your DNS provider. Do not assume that the API host and docs host should point to the same project.

## Local checks

```bash
make check
make docs-install
make docs-check
make docs-build
```

Run the last two commands serially.
