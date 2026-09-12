import { defineConfig } from "vite";

export default defineConfig({
  base: "/_cms/assets/editor-dist/",
  build: {
    outDir: "../editor-dist",
    emptyOutDir: true,
    sourcemap: false,
  },
});
