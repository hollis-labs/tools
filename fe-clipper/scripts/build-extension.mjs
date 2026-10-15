// Bundles the two "classic script" targets (background service worker,
// content script) with esbuild, then copies manifest.json and the icons
// into dist/ alongside the popup/options pages Vite already built.
//
// background.js and content-script.js are deliberately built as single,
// fully self-contained IIFE bundles (no runtime `import`s, no shared
// chunks) -- content scripts are injected as classic scripts and can't
// resolve ES module imports, and keeping background.js equally
// self-contained avoids needing "type": "module" plus cross-file
// resolution just to save a few duplicated KB of shared lib code.
import { build } from "esbuild";
import { cpSync, mkdirSync, copyFileSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, "..");
const distDir = path.join(root, "dist");

mkdirSync(distDir, { recursive: true });

await build({
  entryPoints: {
    background: path.join(root, "src/background/index.ts"),
    "content-script": path.join(root, "src/content/index.ts"),
  },
  outdir: distDir,
  bundle: true,
  format: "iife",
  target: "chrome110",
  platform: "browser",
  sourcemap: false,
  logLevel: "info",
  tsconfig: path.join(root, "tsconfig.json"),
});

copyFileSync(path.join(root, "manifest.json"), path.join(distDir, "manifest.json"));

const iconsSrc = path.join(root, "src/icons");
if (existsSync(iconsSrc)) {
  cpSync(iconsSrc, path.join(distDir, "icons"), { recursive: true });
} else {
  console.warn(
    `warning: ${iconsSrc} not found -- run "npm run icons" first, or Chrome will fall back to a generic icon.`,
  );
}

console.log("fe-clipper: background.js, content-script.js, manifest.json, and icons/ written to dist/");
