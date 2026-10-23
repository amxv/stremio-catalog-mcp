export const siteConfig = {
  name: "Stremio Catalog MCP",
  strapline: "Recommendations, right on your TV",
  description:
    "Publish curated movie and TV collections from an MCP agent straight to native Stremio catalogs without changing your streaming addons.",
  repoUrl: "https://github.com/amxv/stremio-catalog-mcp",
  accentColor: "#446c36",
  accentColorDark: "#d5f77c",
  footerSections: [
    {
      title: "From chat to couch",
      text: "An agent-led setup, your own hosted MCP server, and catalog rows you can change by chatting."
    },
    {
      title: "Self-hosted by default",
      text: "Go, Vercel, Upstash-compatible Redis REST, and the Stremio addons you already use."
    },
    {
      title: "Repository",
      linkPrefix: "Source: ",
      linkHref: "https://github.com/amxv/stremio-catalog-mcp",
      linkLabel: "github.com/amxv/stremio-catalog-mcp"
    }
  ]
} as const;

export const docCategories = ["Start", "Guides", "Reference"] as const;

export const primaryNav = [
  { href: "/docs", label: "Docs" },
  { href: siteConfig.repoUrl, label: "GitHub", external: true }
];
