export const siteConfig = {
  name: "Stremio Catalog MCP",
  strapline: "Recommendations, right on your TV",
  description:
    "Publish curated movie and TV collections from an MCP agent straight to native Stremio catalogs without changing your streaming addons.",
  repoUrl: "https://github.com/amxv/stremio-catalog-mcp",
  accentColor: "#0369a1",
  accentColorDark: "#38bdf8",
  footerSections: [
    {
      title: "Made for your Stremio screen",
      text: "Dynamic named rows, Cinemeta-compatible IDs, and existing streaming addons left untouched."
    },
    {
      title: "Minimal by design",
      text: "Go MCP, native catalog protocol, Vercel deployment, and a small durable row store. No recommendation engine."
    },
    {
      title: "Repository",
      linkPrefix: "Source: ",
      linkHref: "https://github.com/amxv/stremio-catalog-mcp",
      linkLabel: "github.com/amxv/stremio-catalog-mcp"
    }
  ]
} as const;

export const docCategories = ["Start", "Tools", "Design", "Operations"] as const;

export const primaryNav = [
  { href: "/docs", label: "Docs" },
  { href: siteConfig.repoUrl, label: "GitHub", external: true }
];
