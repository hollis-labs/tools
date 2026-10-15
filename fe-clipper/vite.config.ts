import { resolve } from "node:path";
import { defineConfig } from "vite";

// Builds only the two "real page" surfaces (popup.html, options.html) as a
// normal multi-page Vite app -- these load as regular chrome-extension://
// pages, so standard ES module code-splitting is fine. background.js and
// content-script.js are built separately (see scripts/build-extension.mjs)
// as self-contained bundles, since they run as classic scripts.
export default defineConfig({
  base: "./",
  root: __dirname,
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: {
      input: {
        popup: resolve(__dirname, "popup.html"),
        options: resolve(__dirname, "options.html"),
      },
    },
  },
  resolve: {
    alias: {
      "@": resolve(__dirname, "./src"),
    },
  },
});
