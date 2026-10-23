---
title: Documentation site
description: Run or customize the documentation website.
order: 4
category: Operations
summary: A standalone website with isolated docs CI and Vercel deployment.
---

This documentation site uses Astro and ZueDocs in `docs/`. It can be built and deployed without access to the Go service or any credentials.

```bash
make docs-install
make docs-dev
```

Edit guides in `docs/src/content/docs/`, navigation in `docs/src/data/docs.ts`, and the homepage in `docs/src/pages/index.astro`. Run `make docs-check` then `make docs-build` **sequentially** before pushing.

Deploy to a separate Vercel project with Root Directory `docs`. The docs GitHub Action builds only on relevant changes and pushes the prebuilt site. Keep all real connection URLs, account keys, Redis tokens, and MCP secrets in the API project's private settings, never in docs build variables.
