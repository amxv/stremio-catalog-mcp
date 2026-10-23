import { defineConfig } from "astro/config";
import zuedocs from "zuedocs/astro";

export default defineConfig({
  output: "static",
  integrations: [zuedocs()],
  vite: {
    build: {
      // ZueDocs lazy-loads Mermaid only for diagram pages. Mermaid's generated
      // parser is a single ~650 kB module, so it cannot be split further.
      chunkSizeWarningLimit: 700
    }
  }
});
