---
title: Self-hosting reference
description: Deploy manually with Vercel and Redis, or automate it with the optional Go setup helper.
order: 4
category: Reference
summary: Two Redis options, full manual deployment, safe secrets, and Stremio login.
---

This is written for **an agent setting up Stremio Catalog MCP for a human**. Hosting needs one Go deployment on Vercel and a Redis REST endpoint. The [project website](https://stremio-catalog-mcp.ashray.xyz/) already hosts the docs—you don't have to deploy those.

Choose either the **optional setup helper** or the **fully manual deployment** below. Both produce the same private ChatGPT MCP URL.

## Requirements

- Clone or fork the [GitHub repository](https://github.com/amxv/stremio-catalog-mcp). Use Go 1.26+ for the optional helper.
- Sign into [Vercel](https://vercel.com/) and install its CLI (`vercel login`).
- Choose a Redis service with an **Upstash-compatible HTTPS REST API**: JSON command arrays, bearer-token authentication, and Lua `EVAL`. A plain `redis://` or `rediss://` TCP URL is **not** compatible with this Go backend.
- A Stremio account for automatic new-row synchronization. Headed [Agent Browser](https://github.com/vercel-labs/agent-browser) can simplify the human login.

One deployment manages **one Stremio account**. Use a dedicated Redis database when practical. An existing database works if its other applications can safely share the keys that this service stores.

For transferring keys, session tokens, and protected files to agents, we recommend [Fidelius](https://fidelius.ashray.xyz/). It is **optional**; any trusted secret-sharing mechanism works. Never paste keys into a public issue, repository, chat transcript, or command log.

## Automated setup (optional)

**Inspect the [public bootstrap source](https://github.com/amxv/stremio-catalog-mcp/blob/main/cmd/setup/main.go) first.** Related files: [Redis REST checks](https://github.com/amxv/stremio-catalog-mcp/blob/main/cmd/setup/redis.go), [Upstash provisioning](https://github.com/amxv/stremio-catalog-mcp/blob/main/cmd/setup/upstash.go), and [Agent Browser login](https://github.com/amxv/stremio-catalog-mcp/blob/main/cmd/setup/browser.go).

### Option A · Automatically provision Free Upstash

Give the agent an Upstash Developer API email/key through a secure broker. It can preview the plan and provision a dedicated **Free** Redis instance without upgrading to paid:

```bash
go run ./cmd/setup --project my-stremio-catalog
go run ./cmd/setup --apply --project my-stremio-catalog --stremio-browser
```

`--stremio-browser` opens a headed Stremio Web window. The human logs in, then the helper reads the authenticated session **in memory**, validates it and sends it to Vercel without displaying the key. `--stremio-key-file` is the fallback.

### Option B · Reuse an existing Redis REST URL and token

**No Upstash Developer API key required.** The endpoint can be Upstash or another service implementing the same Redis HTTP protocol.

```bash
go run ./cmd/setup --project my-stremio-catalog --redis-mode existing

go run ./cmd/setup --apply --project my-stremio-catalog \
  --redis-mode existing \
  --redis-rest-url-file /private/secrets/REDIS_REST_URL \
  --redis-rest-token-file /private/secrets/REDIS_REST_TOKEN \
  --stremio-browser
```

The helper verifies `PING` and Lua `EVAL` using a temporary expiring key and does **not** touch existing catalog data. You may provide the endpoint using `--redis-rest-url https://redis.example` and the token through a protected file or `UPSTASH_REDIS_REST_TOKEN` environment variable.

Both helper modes set Vercel environment variables, deploy the Go API, and copy the authenticated MCP URL to the local clipboard. For agent automation use `--yes` only **after human approval** and pass secrets via private files (`chmod 600`). Helpful options: `--scope` for Vercel teams, `--base-url` for a verified custom domain, `--stremio-browser-session` for an existing headed browser session, and `--no-git-connect`/`--no-deploy` for custom workflows.

The recovery receipt `.local-setup/credentials.json` is owner-only and Git-ignored. Reruns reuse credentials instead of silently changing Redis instances or rotating keys. To copy the URL again: `go run ./cmd/setup --copy-url`. A remote agent needs an authorized local bridge to access the human's clipboard.

## Manual deployment (no setup helper)

Agents can do every step themselves with Vercel's CLI, ordinary Redis credentials, and a signed-in Stremio browser. There is no requirement to run the Go bootstrap.

### 1 · Clone and prepare Redis

```bash
git clone https://github.com/YOUR_ACCOUNT/stremio-catalog-mcp.git
cd stremio-catalog-mcp
```

Either create a Free database in [Upstash Console](https://console.upstash.com/) or reuse an existing Redis REST database. Collect its **HTTPS REST URL and bearer token** privately. The Developer API key is *only* needed for automatic provisioning, not here.

If using a different provider, verify compatibility with Upstash-style Redis REST (`POST` with JSON command arrays), `PING`, `GET`, `SET`, `DEL`, `EVAL`, `LPUSH`, and `LTRIM`. Generic Redis TCP connections and incompatible vendor REST APIs require a separate adapter.

### 2 · Link the Go project to Vercel

From the repository root (not `docs/`):

```bash
vercel login
vercel project add my-stremio-catalog
vercel link --yes --project my-stremio-catalog
```

Skip `project add` if you already have that project. Confirm **Root Directory `/`**, **Framework Preset Other**, and that the root `vercel.json` routes `/mcp` and `/stremio/*` to `api/mcp.go`. Connect your GitHub fork in Vercel (or use `vercel git connect` with its GitHub URL) if you want deployments on future pushes.

Identify the project's actual, **verified HTTPS** production domain, often `https://my-stremio-catalog.vercel.app`. Don't assume every project slug gets that exact domain.

### 3 · Add private Vercel production variables

| Variable | Purpose |
| --- | --- |
| `MCP_API_KEY` | New random secret for MCP write access. |
| `STREMIO_FEED_TOKEN` | Different random secret for Stremio catalog reads. |
| `UPSTASH_REDIS_REST_URL` | Redis HTTPS REST endpoint. |
| `UPSTASH_REDIS_REST_TOKEN` | Redis REST bearer token. |
| `PUBLIC_BASE_URL` | Verified Vercel API HTTPS origin, not the docs domain. |
| `STREMIO_AUTH_KEY` | Recommended Stremio session for adding/renaming/removing rows across devices. |

The agent can generate the **two independent 32-byte application keys** using `openssl rand -hex 32`, store them temporarily in owner-only files (or a password manager), and send private values to Vercel over **stdin**. For example:

```bash
vercel env add UPSTASH_REDIS_REST_URL --environment production --sensitive --yes < /private/secrets/REDIS_REST_URL
vercel env add UPSTASH_REDIS_REST_TOKEN --environment production --sensitive --yes < /private/secrets/REDIS_REST_TOKEN
vercel env add MCP_API_KEY --environment production --sensitive --yes < /private/secrets/MCP_API_KEY
vercel env add STREMIO_FEED_TOKEN --environment production --sensitive --yes < /private/secrets/STREMIO_FEED_TOKEN
printf '%s' 'https://YOUR_VERIFIED_API_HOST' | vercel env add PUBLIC_BASE_URL --environment production --no-sensitive --yes
```

Generate and secure the files before those commands. `vercel env ls production` lets an agent see which variables already exist; **do not overwrite production secrets automatically**. Never use the Upstash REST token as the MCP key. Protect a recovery copy of the generated MCP key because production secrets may not be readable back later.

### 4 · Connect Stremio via Agent Browser

This is human-assisted, not a password-scraping workflow:

1. Agent runs `agent-browser --session stremio-setup --headed open https://web.stremio.com/`.
2. Human signs in to Stremio Web normally.
3. Agent confirms that the browser is at `https://web.stremio.com`, then reads `JSON.parse(localStorage.getItem('profile') || '{}').auth?.key` with Agent Browser's `eval`, **capturing the output privately instead of printing it in a tool transcript**.
4. Agent passes that session key via stdin to `vercel env add STREMIO_AUTH_KEY --environment production --sensitive --yes`, then closes its temporary browser session.

The [browser helper source](https://github.com/amxv/stremio-catalog-mcp/blob/main/cmd/setup/browser.go) demonstrates private extraction and origin verification. The session format might change with Stremio updates; a secure session-key file is the fallback. Never request the human's Stremio password.

For a **fully manual, no-helper** flow, an agent can capture the Agent Browser result in its own local process and send it directly to Vercel without printing it. After the human has logged in to the named browser session:

```python
import json
import subprocess

script = """(() => {
  if (location.origin !== 'https://web.stremio.com') return null;
  return JSON.parse(localStorage.getItem('profile') || '{}').auth?.key || null;
})()"""
response = json.loads(subprocess.check_output([
    'agent-browser', '--session', 'stremio-setup', '--json', 'eval', script
]))
assert response['success'] and response['data']['origin'].rstrip('/') == 'https://web.stremio.com'
key = response['data']['result']
assert isinstance(key, str) and len(key) >= 20
subprocess.run([
    'vercel', 'env', 'add', 'STREMIO_AUTH_KEY', '--environment', 'production',
    '--sensitive', '--yes'
], input=key.encode(), check=True)
print('Stremio session securely configured in Vercel.')
```

Close the temporary Agent Browser session afterward. Do not output `response`, `key`, or full authenticated URLs to the agent's transcript.

Without `STREMIO_AUTH_KEY`, individual existing catalog rows can still be updated, but new/renamed rows require reinstalling the add-on manifest manually in **Stremio → Add-ons**. The MCP's `catalog_install_url` tool returns that private URL.

### 5 · Deploy and add the MCP to ChatGPT

```bash
vercel --prod --yes
```

Check the deployment is Ready and HTTPS is valid. Assemble the full private URL from the verified project domain and generated MCP key, then **copy it to the human's clipboard without printing it** (for example, `pbcopy`, `Set-Clipboard`, or `wl-copy`). Save it in the human's password manager if needed:

```text
https://YOUR_VERIFIED_API_HOST/mcp?key=YOUR_PRIVATE_MCP_KEY
```

The human opens **ChatGPT → Plugins → Create custom MCP server → No authentication → Name → Add as plugin** and pastes the entire URL. *No authentication* means no separate OAuth flow; the URL still contains a secret write key.

## Verify and troubleshoot

Run `make check`. The unauthenticated `/mcp` endpoint should return **HTTP 401**; authenticated `tools/list` should expose seven tools. Verify the private Stremio manifest advertises `catalog` without streams. Create a temporary catalog, then delete it after confirming the original streaming add-ons remain installed.

Stremio may cache both rows and manifests, so a refresh or restart can be necessary. The Stremio account API is not a stable public third-party interface; see [Architecture & security](/docs/architecture).

If the Free Upstash quota is exhausted, choose the **existing Redis REST** option rather than silently upgrading. If the helper finds existing Vercel production keys without its local receipt, it refuses to overwrite them; use the manual procedure or recover the original credentials instead.
