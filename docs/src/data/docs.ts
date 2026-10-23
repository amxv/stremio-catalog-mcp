export const siteConfig = {
  name: "Go MCP Template",
  strapline: "A small Go MCP server for Vercel",
  description:
    "A reusable Go MCP server template with stateless Streamable HTTP, API-key authentication, sample tools, Vercel deployment and ZueDocs.",
  repoUrl: "https://github.com/amxv/go-mcp-template",
  accentColor: "#0369a1",
  accentColorDark: "#38bdf8",
  footerSections: [
    {
      title: "Go MCP Template",
      text: "A provider-agnostic Go MCP server with a tiny tool surface and dependable Vercel hosting."
    },
    {
      title: "What is included",
      text: "Two runnable sample tools, stateless MCP HTTP, simple authentication, automated Go checks, and bundled ZueDocs."
    },
    {
      title: "Repository",
      linkPrefix: "Source: ",
      linkHref: "https://github.com/amxv/go-mcp-template",
      linkLabel: "github.com/amxv/go-mcp-template"
    }
  ]
} as const;

export const docCategories = ["Start", "Design", "Operations"] as const;

export const primaryNav = [
  { href: "/docs", label: "Docs" },
  { href: siteConfig.repoUrl, label: "GitHub", external: true }
];
