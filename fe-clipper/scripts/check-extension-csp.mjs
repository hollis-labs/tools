import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const distDir = path.join(root, "dist");

function packagedJavaScriptFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const entryPath = path.join(directory, entry.name);
    if (entry.isDirectory()) return packagedJavaScriptFiles(entryPath);
    return entry.isFile() && /\.(?:js|mjs|cjs)$/u.test(entry.name) ? [entryPath] : [];
  });
}

const forbiddenPatterns = [
  ["Function constructor", /\bFunction\s*\(/u],
  ["direct eval", /\beval\s*\(/u],
  ["indirect eval", /\beval\s*\)\s*\(/u],
  ["string setTimeout", /\bsetTimeout\s*\(\s*["'`]/u],
  ["string setInterval", /\bsetInterval\s*\(\s*["'`]/u],
];

for (const filePath of packagedJavaScriptFiles(distDir)) {
  const source = readFileSync(filePath, "utf8");
  for (const [label, pattern] of forbiddenPatterns) {
    if (pattern.test(source)) {
      throw new Error(
        `${path.relative(root, filePath)} contains ${label} code that Chrome MV3 CSP blocks.`,
      );
    }
  }
}

console.log("All packaged extension scripts are free of eval-like code blocked by MV3 CSP.");
