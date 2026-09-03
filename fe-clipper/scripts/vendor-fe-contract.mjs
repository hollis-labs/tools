import { execFileSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(scriptDirectory, "..");
const destination = join(repositoryRoot, "vendor/fragments-engine-browser-contract-v1");
const pinPath = join(destination, "FE_COMMIT");
const contractPath = "contracts/browser-capture-reader/v1";
const feRepository = process.env.FE_REPO;

if (!feRepository) {
  throw new Error("Set FE_REPO to the local Fragments Engine repository before vendoring.");
}

const commit = readFileSync(pinPath, "utf8").trim();
if (!/^[0-9a-f]{40}$/.test(commit)) {
  throw new Error(`${pinPath} must contain one full 40-character FE commit SHA.`);
}

execFileSync("git", ["-C", feRepository, "cat-file", "-e", `${commit}^{commit}`], {
  stdio: "inherit",
});

const temporaryRoot = mkdtempSync(join(tmpdir(), "fe-clipper-contract-"));
const archivePath = join(temporaryRoot, "contract.tar");
const extractedPath = join(temporaryRoot, "extracted");

try {
  mkdirSync(extractedPath);
  execFileSync(
    "git",
    ["-C", feRepository, "archive", "--format=tar", `--output=${archivePath}`, commit, contractPath],
    { stdio: "inherit" },
  );
  execFileSync(
    "tar",
    ["-xf", archivePath, "--strip-components=3", "-C", extractedPath],
    { stdio: "inherit" },
  );

  rmSync(destination, { recursive: true, force: true });
  mkdirSync(dirname(destination), { recursive: true });
  renameSync(extractedPath, destination);
  writeFileSync(pinPath, `${commit}\n`, "utf8");
} finally {
  rmSync(temporaryRoot, { recursive: true, force: true });
}

console.log(`Vendored ${contractPath} from FE ${commit}.`);
