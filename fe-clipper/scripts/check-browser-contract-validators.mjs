import { readFileSync } from "node:fs";
import path from "node:path";
import { runInNewContext } from "node:vm";
import { fileURLToPath } from "node:url";

import { build } from "esbuild";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const captureSchemaReference =
  "https://schemas.hollis-labs.dev/fragments-engine/browser-capture-reader/v1/capture-envelope.schema.json";

const buildResult = await build({
  entryPoints: [path.join(root, "src/generated/fe-contract-validators.js")],
  bundle: true,
  format: "iife",
  globalName: "feContractBundle",
  platform: "browser",
  target: "chrome110",
  write: false,
  logLevel: "silent",
});
const bundleSource = buildResult.outputFiles?.[0]?.text;

if (!bundleSource) {
  throw new Error("Browser contract validator check did not produce a bundle.");
}

const context = {};
runInNewContext(bundleSource, context);
const validateCapture = context.feContractBundle?.validatorsBySchemaReference?.[captureSchemaReference];
if (typeof validateCapture !== "function") {
  throw new Error("Browser bundle is missing the capture-envelope validator.");
}

const fixturePath = path.join(
  root,
  "vendor/fragments-engine-browser-contract-v1/fixtures/valid-capture-youtube.json",
);
const validCapture = JSON.parse(readFileSync(fixturePath, "utf8"));
if (!validateCapture(validCapture)) {
  throw new Error("Browser-bundled validator rejected the valid capture fixture.");
}

const duplicateTagsCapture = {
  ...validCapture,
  tags: ["browser-bundle-regression", "browser-bundle-regression"],
};
if (validateCapture(duplicateTagsCapture)) {
  throw new Error("Browser-bundled validator accepted duplicate tags.");
}

console.log("Browser-bundled FE validators execute correctly with Ajv runtime helpers.");
