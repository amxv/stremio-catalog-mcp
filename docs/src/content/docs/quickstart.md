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

> Set up this repository for my Stremio account. Follow the [self-hosting instructions](/docs/deployment), use the included Go setup helper, and deploy to my Vercel account with a dedicated Free Upstash Redis database. Ask me to sign in or provide secrets securely when needed. Verify the deployment, link my Stremio session for dynamic rows, and copy the **full MCP URL** to my clipboard. Don't post tokens to chat, commit secrets, or enable paid services.

The agent can preview the plan and then run the bootstrap once you approve:

```bash
go run ./cmd/setup --project my-stremio-catalog
go run ./cmd/setup --apply --project my-stremio-catalog
```

The helper creates or reuses the Vercel project, provisions Free Redis, sets production secrets, deploys the Go service, and copies the URL. It supports secure files and noninteractive agent operation; see [Self-hosting](/docs/deployment) for details.

## 2. Do the few human-only steps

Your agent may need you to:

- Sign in to **Vercel** and **Upstash**, then securely supply an Upstash Developer API key.
- Sign in to **[Stremio Web](https://web.stremio.com/)** and provide a Stremio **session key**, not your password, through a private prompt. This enables automatic creation and renaming of rows across your signed-in devices.
- Approve any cloud resources before your agent creates them. The setup helper requests a Free Redis plan and never automatically upgrades to paid.

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
