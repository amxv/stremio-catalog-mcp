import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { getCollection } from "astro:content";

export async function getStaticPaths() {
  const entries = await getCollection("docs");

  return entries.map((entry) => ({
    params: { slug: entry.id },
    props: { filePath: entry.filePath }
  }));
}

export async function GET({ props }: { props: { filePath?: string } }) {
  if (!props.filePath) {
    return new Response("Markdown source not found.\n", {
      status: 404,
      headers: {
        "Content-Type": "text/plain; charset=utf-8"
      }
    });
  }

  const source = await readFile(join(process.cwd(), props.filePath), "utf8");

  return new Response(source, {
    headers: {
      "Content-Type": "text/markdown; charset=utf-8"
    }
  });
}
