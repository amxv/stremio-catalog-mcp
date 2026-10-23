import { getCollection } from "astro:content";
import { docCategories, siteConfig } from "../data/docs";

export async function GET() {
  const entries = (await getCollection("docs")).sort((a, b) => a.data.order - b.data.order);

  const lines = [
    `# ${siteConfig.name} Docs`,
    "",
    "The source of truth for setting up and using Stremio Catalog MCP.",
    "",
    "If you are an AI coding agent setting up this service for a human, start with [Quickstart](/docs/quickstart.md), then follow [Self-hosting reference](/docs/deployment.md).",
    "",
    "The human must authorize account sign-ins and cloud resource creation, provide secrets privately, and finish ChatGPT's plugin-creation UI.",
    ""
  ];

  for (const category of docCategories) {
    const groupedEntries = entries.filter((entry) => entry.data.category === category);
    if (groupedEntries.length === 0) {
      continue;
    }

    lines.push(`## ${category}`, "");

    for (const entry of groupedEntries) {
      lines.push(`- [${entry.data.title}](/docs/${entry.id}.md): ${entry.data.description}`);
    }

    lines.push("");
  }

  return new Response(lines.join("\n"), {
    headers: {
      "Content-Type": "text/markdown; charset=utf-8"
    }
  });
}
