---
title: Documentation site
description: Customize the bundled Astro/ZueDocs app for your new MCP project.
order: 4
category: Operations
summary: Docs navigation, Markdown content, and site deployment.
---

The ZueDocs application is self-contained in `docs/`. It can be built or deployed without installing or publishing a CLI.

```bash
make docs-install
make docs-dev
```

Write articles in `docs/src/content/docs/`, update branding/navigation in `docs/src/data/docs.ts`, and customize the landing page in `docs/src/pages/index.astro`. Run `make docs-check` then `make docs-build` **sequentially** before merging changes.

Create a separate Vercel project with Root Directory `docs` to serve the static site. Keep the MCP API key only in the API project, never in documentation build variables.
