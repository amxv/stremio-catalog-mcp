---
title: Quickstart
description: Have your AI agent self-host the service, then add it to ChatGPT in a few clicks.
order: 1
category: Start
summary: Set up with an agent and start sending recommendations to Stremio.
---

This is a **self-hosted MCP server**, not a shared public recommendation service. Your agent can handle most of the setup; you handle account sign-ins and the final ChatGPT connection. It uses your own Vercel, Upstash, and Stremio accounts.

## 1. Ask your agent to set it up

Fork or clone [Stremio Catalog MCP](https://github.com/amxv/stremio-catalog-mcp) and give your coding agent this instruction:

> Set up this repository for my Stremio account. Follow the [self-hosting instructions](/docs/deployment). Deploy to my Vercel account using either a Free Upstash Redis database or my existing compatible Redis REST URL/token. You can use the Go helper or do the steps manually. Ask me to log into Stremio in headed Agent Browser, then securely obtain the session key. Verify the deployment and copy the **full MCP URL** to my clipboard. Never post tokens to chat, commit secrets, or enable paid services without approval.

The agent can preview the plan and then run the bootstrap once you approve:

```bash
go run ./cmd/setup --project my-stremio-catalog
go run ./cmd/setup --apply --project my-stremio-catalog --stremio-browser
```

This default provisions Free Redis. If you **already have a Redis REST URL and token**, use the alternate setup path:

```bash
go run ./cmd/setup --apply --project my-stremio-catalog \
  --redis-mode existing \
  --redis-rest-url-file /private/secrets/REDIS_REST_URL \
  --redis-rest-token-file /private/secrets/REDIS_REST_TOKEN \
  --stremio-browser
```

The helper configures Vercel, tests the Redis connection, and copies the authenticated MCP URL. **The helper is optional**—the [self-hosting reference](/docs/deployment#manual-deployment-no-setup-helper) explains the complete manual deployment. You can inspect the [bootstrap source on GitHub](https://github.com/amxv/stremio-catalog-mcp/blob/main/cmd/setup/main.go) before running it.

## 2. Do the few human-only steps

Your agent may need you to:

- Sign in to **Vercel** and, if provisioning a new Redis database, **Upstash**. Supply a Developer API key privately, or simply provide your existing REST URL and token instead.
- Sign in to **[Stremio Web](https://web.stremio.com/)** when your agent opens it with Agent Browser. The helper reads your session key from the signed-in browser without displaying it. You can also provide the session key through a private file if a headed browser isn't available.
- Approve any cloud resources before your agent creates them. The setup helper requests a Free Redis plan and never automatically upgrades to paid.

For sharing keys, tokens, and other sensitive setup values with your agent, we recommend **[Fidelius](https://fidelius.ashray.xyz/)**. It is optional; use another trusted secret-sharing method if you prefer. **Never paste a secret into a public issue or repo.**

The full MCP URL looks like this:

```text
https://YOUR-VERCEL-PROJECT.vercel.app/mcp?key=YOUR_PRIVATE_KEY
```

The `key` value is generated for your deployment. **Do not use the public documentation website URL.** If you need to copy your private URL again, have your agent run:

```bash
go run ./cmd/setup --copy-url
```

The helper puts the URL directly on your clipboard without printing the secret.

## 3. Add the MCP in ChatGPT

This is the part you do yourself:

1. Open ChatGPT **Plugins**.
2. Click **Create custom MCP server**.
3. Paste the full MCP URL from your clipboard, including `?key=...`.
4. Select **No authentication**, choose a name (for example, *Stremio Catalog*), and click **Add as plugin**.

Select *No authentication* because the URL already carries an API key; a separate OAuth sign-in is not needed. **The URL is still a secret.** UI labels may vary slightly by ChatGPT version.

## 4. Send your first recommendations

Try asking ChatGPT:

> Find six excellent mind-bending sci-fi TV shows and send them to Stremio as a new catalog called *Sci-Fi Worth Watching*.

Open Stremio on your TV using the same account. Your new row should appear when Stremio refreshes its add-ons; restarting the app sometimes helps. Existing streaming add-ons handle playback.

Next: [things to try](/docs/using-catalogs) · [MCP tools](/docs/tools)

## Optional: have your agent connect an Android TV

The base Stremio Catalog MCP needs **no TV companion or Firebase account** and exposes **five tools**. If you explicitly opt in, it can also open movie and series **details** (never autoplay) on **one paired Android TV** with the single extra tool `tv_open_title`.

Once your normal MCP deployment is working, give your coding agent this instruction:

> Follow https://stremio-catalog-mcp.ashray.xyz/docs/tv-remote for **my** Vercel account and **my** Android TV. Provision a separate no-cost Firebase Spark project, opt in only on my existing Go API Vercel project, build and sideload the receiver over ADB, pair my TV through the private setup page, verify a real Firebase push opens a Stremio details page, then disconnect ADB. Never enable billing, expose secrets, start playback, or leave an always-on Mac or TV process. Ask me to authorize Google sign-in, TV debugging and the deployment as needed.

The [TV remote guide](/docs/tv-remote) provides a full **agent execution checklist**, Firebase/GCloud and Vercel commands, Android build requirements, ADB connection options, pairing instructions, and end-to-end verification steps. A **second Google account is not required**; a separate Firebase project under the same Google account is sufficient. If you don't want TV control, **skip this entire step**—the default MCP remains unchanged.
