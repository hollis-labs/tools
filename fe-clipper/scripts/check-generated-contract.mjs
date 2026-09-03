import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(scriptDirectory, "..");
const schemaPath = join(
  repositoryRoot,
  "vendor/fragments-engine-browser-contract-v1/openapi.json",
);
const generatedPath = join(repositoryRoot, "src/generated/fragments-engine-api.ts");
const generatorPath = join(repositoryRoot, "node_modules/.bin/openapi-typescript");
const temporaryRoot = mkdtempSync(join(tmpdir(), "fe-clipper-generated-"));
const candidatePath = join(temporaryRoot, "fragments-engine-api.ts");

try {
  execFileSync(generatorPath, [schemaPath, "--output", candidatePath], { stdio: "inherit" });
  const expected = readFileSync(generatedPath);
  const candidate = readFileSync(candidatePath);
  if (!expected.equals(candidate)) {
    throw new Error(
      "Generated FE API types are stale. Run `npm run contracts:generate` and commit the result.",
    );
  }
} finally {
  rmSync(temporaryRoot, { recursive: true, force: true });
}

console.log("Generated FE API types match the pinned OpenAPI contract byte-for-byte.");
