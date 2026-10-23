---
title: Self-hosting reference
description: Agent-oriented setup steps for a private Go MCP service on your own Vercel and Upstash accounts.
order: 4
category: Reference
summary: Provision resources, handle keys safely, and deploy the Go server.
---

This reference is written primarily for a **coding agent operating on the human's behalf**. Start with the [Quickstart](/docs/quickstart); use this page for exact setup commands, requirements, and recovery.

## What you need

- A fork or clone of [this repository](https://github.com/amxv/stremio-catalog-mcp).
- **Go 1.26+**, the **Vercel CLI**, and authenticated access to the human's Vercel account (`vercel login`). GitHub CLI is useful for managing the fork.
- An **Upstash account** and a **Developer API email/key** with permission to create one Free Redis database. Get these from the Upstash console's Developer API section. Do not request an Upstash password.
- The human's **Stremio session key**, for automatic appearance of newly created, renamed, or removed rows. The human signs in to Stremio Web first.

One deployment is tied to **one Stremio account**. Give each human their own project, database, and secrets. This is not yet a multi-tenant hosted service.

## Automatic setup (recommended)

From the repository root, **preview first**:

```bash
go run ./cmd/setup --project my-stremio-catalog
```

With human approval, complete setup:

```bash
go run ./cmd/setup --apply --project my-stremio-catalog
```

The agent should handle the process. The helper:

1. Creates or links the Vercel project, checking existing production configuration.
2. Creates or reuses a **dedicated Free Upstash Redis** database (TLS enabled, eviction disabled, no paid fallback).
3. Generates separate strong MCP-write and Stremio-feed tokens; stores a local owner-only recovery receipt and configures the Vercel production environment.
4. Validates an optional Stremio session key; connects GitHub for future deploys when possible; deploys the Go API to Vercel.
5. Copies `https://YOUR-API-ORIGIN/mcp?key=YOUR_PRIVATE_KEY` to the **local clipboard without displaying the key**.

It is safe to rerun with the same project and recovery receipt. It avoids overwriting existing MCP/feed keys or accidentally creating duplicate Redis databases. It refuses paid plans. It does **not** set up another hosted docs site, since the official [website](https://stremio-catalog-mcp.ashray.xyz/) already provides the documentation.

### Noninteractive, agent-driven setup

For agents with a secret broker such as Fidelius, prefer private temporary files. Never pass secrets in CLI arguments or print them to a chat, terminal transcript, commit, or log.

```bash
go run ./cmd/setup \
  --apply --yes \
  --project my-stremio-catalog \
  --upstash-email-file /private/secrets/UPSTASH_EMAIL \
  --upstash-key-file /private/secrets/UPSTASH_API_KEY \
  --stremio-key-file /private/secrets/STREMIO_AUTH_KEY
```

Secret files must be owner-readable only (`chmod 600`). Run `--yes` only after the human expressly approves provisioning. The helper also accepts `UPSTASH_EMAIL`, `UPSTASH_API_KEY`, and `STREMIO_AUTH_KEY` in the current process environment if protected files aren't available. With no file/environment value, it uses a hidden terminal prompt when running interactively.

The program saves only the generated MCP/feed connection keys in **`.local-setup/credentials.json`**, with owner-only permissions and Git ignore rules. It does **not** save the Upstash management key, Redis REST token, or Stremio session key there. Protect the receipt; Vercel production secrets may not be readable back later.

### Finding a Stremio session key

Ask the human to sign in to [web.stremio.com](https://web.stremio.com/). The current web app may expose the active session in its browser storage under `profile.auth.key`:

```js
JSON.parse(localStorage.getItem("profile") || "{}").auth?.key
```

This is a **sensitive account session**, not a public identifier. Don't paste it into chat or publish it. The agent should pass it directly to a private file or secure prompt for setup. Storage formats can change; if this no longer works, use Stremio's current account/session mechanism. Never ask for the human's Stremio password.

Without a session key, you can still publish content and manually install the add-on URL. After connecting the MCP, ask your agent to call `catalog_install_url`; add that manifest URL through **Stremio → Add-ons**. Reinstall the updated manifest when row names or declarations change. **Automatic discovery of newly named rows won't work** until account sync is configured. This is why the session key is recommended for the full experience.

### Clipboard handoff and ChatGPT

The helper attempts to use `pbcopy` on macOS, Windows clipboard APIs, or `wl-copy` / `xclip` / `xsel` on Linux. It must run where the **human's desktop clipboard** is available. A cloud agent or remote container cannot automatically copy to the human's Mac without a trusted local bridge.

If needed, rerun this from the same repository on the human's computer:

```bash
go run ./cmd/setup --copy-url
```

Then the human opens **ChatGPT → Plugins → Create custom MCP server**, pastes the complete URL including `?key=`, selects **No authentication**, enters a name, and clicks **Add as plugin**. No authentication here means *no separate OAuth exchange*; the URL's API key still protects the server.

## Variables and manual deployment

The helper configures these Vercel **Production** variables in the API project:

| Name | Required | Description |
| --- | --- | --- |
| `MCP_API_KEY` | Yes | Private key for ChatGPT and other MCP clients. |
| `STREMIO_FEED_TOKEN` | Yes | Separate private Stremio catalog URL token. |
| `PUBLIC_BASE_URL` | Yes | HTTPS origin of the deployed Go API, with no trailing route. |
| `UPSTASH_REDIS_REST_URL` | Yes | HTTPS endpoint of this project's Redis database. |
| `UPSTASH_REDIS_REST_TOKEN` | Yes | Private Redis REST token. |
| `STREMIO_AUTH_KEY` | Recommended | Account session enabling automatic manifest synchronization. |

To deploy **manually** instead, create a dedicated Free Redis database in the [Upstash Console](https://console.upstash.com/), import the repository to Vercel with **Root Directory `/`**, Framework **Other**, configure these variables as server-side secrets, then deploy the `main` branch. Use the root `vercel.json` with the `/mcp` and `/stremio/*` rewrites; do not host the API under `docs/`.

For a custom domain, attach it to the **Go API project**, configure your DNS provider using Vercel's exact target, and set `PUBLIC_BASE_URL` to the verified `https://` origin. Redeploy so the updated variable is active. The docs domain is independent.

## Validate and troubleshoot

```bash
make check
```

Ask the agent to verify that a request to `/mcp` without `?key=` returns HTTP 401; the authenticated `tools/list` exposes seven tools; the private Stremio `manifest.json` has `resources: ["catalog"]`; and `catalog_list` reads the initial empty row store or saved rows.

Test one temporary row with `catalog_put`, then delete it with `catalog_delete`. Confirm the original Stremio add-ons stay installed. New rows may require refreshing or restarting the TV app because Stremio caches its manifest.

If the Free Upstash quota is exhausted, **stop** and ask the human how to proceed. Do not silently reuse another project's Redis or upgrade to a paid tier. If a rerun reports existing production keys but no `.local-setup` receipt, do not overwrite them: recover the receipt or deliberately migrate the instance with backup and explicit approval.

See [Architecture and security](/docs/architecture) for the underlying Stremio account API risks, backups, and data model.
