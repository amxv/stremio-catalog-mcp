import { getCollection } from "astro:content";
import { docCategories, siteConfig } from "../data/docs";

export async function GET() {
  const entries = (await getCollection("docs")).sort((a, b) => a.data.order - b.data.order);

  const lines = [
    `# ${siteConfig.name} Docs`,
    "",
    "This is the raw markdown index for the documentation routes.",
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
