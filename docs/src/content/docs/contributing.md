---
title: Development & docs hosting
description: Run the tests, edit the site, and optionally deploy your own copy of the docs.
order: 6
category: Reference
summary: Source layout, quality checks, and optional docs CI for maintainers.
---

You only need to self-host the **Go API** to use this MCP. The [public documentation website](https://stremio-catalog-mcp.ashray.xyz/) is shared; a separate docs deployment is optional.

## Develop locally

From the repository root:

```bash
make check            # Go tests and vet
make docs-install     # Bun dependencies for Astro/ZueDocs
make docs-check       # Type-check the site
make docs-build       # Build the static documentation
```

The unit tests use in-memory/mock storage and don't require private credentials. For changes to catalog or account synchronization, test both normal operations and preservation of other installed Stremio add-ons.

## Edit the documentation

Guides are in `docs/src/content/docs/`, site navigation in `docs/src/data/docs.ts`, and the homepage in `docs/src/pages/index.astro`. Please keep the main README short and link to the website rather than duplicating long instructions. Give priority to clear, agent-executable setup steps and human-friendly everyday examples.

## Host a separate docs site (optional)

The repository includes a **docs-only, path-filtered GitHub Actions workflow** at `.github/workflows/docs-deploy.yml`. It builds the static Astro site in GitHub Actions and deploys prebuilt output to a **second Vercel project**. Relevant docs pull requests also receive a Preview URL. The Go API is deployed independently through its own path-filtered workflow.

To use it in your own fork:

1. Create a separate Vercel project with **Root Directory** set to `docs` and **Framework Preset** set to Astro. Link it to your CI credentials, but leave automatic Vercel Git deployments disabled (already configured in `docs/vercel.json`). You don't need Vercel's Git integration because the path-filtered GitHub Action creates both production and Preview deployments.
2. Create a **project-scoped** Vercel token limited to the docs project. In the fork, store it as a GitHub Actions secret named `VERCEL_TOKEN`.
3. Add GitHub Actions variables `VERCEL_ORG_ID` and `VERCEL_PROJECT_ID`, corresponding to that docs project. Never commit the token.
4. Push a docs source or build-input change to `main`, or run the **Docs** workflow manually. A same-repository docs pull request gets a Preview URL; Go-only, Android TV, and test-only changes create no docs deployments. The action filters paths before asking Vercel to deploy, preserving quota.

Your docs site does **not** need `MCP_API_KEY`, the Stremio session key, or Redis credentials. Keep those only in the Go API Vercel project. If you don't configure docs CI in your fork, simply skip deploying a custom docs site and point people to the shared [website](https://stremio-catalog-mcp.ashray.xyz/).

## Public release checklist

Keep tokens out of source control and examples, review the CLI setup output for private data, run `go test -race ./...`, and verify the documentation build. Review the current Stremio account API behavior before shipping account-sync changes. Contributions should preserve existing streaming add-ons and must never require paid services without explicit opt-in.
