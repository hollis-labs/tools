# fe-clipper

A Chrome Manifest V3 extension that captures the active tab's already-rendered
content and provider media into Fragments Engine's manifest-first
`/v1/captures` contract. It reads the page the browser drew; it does not fetch
or crawl pages itself, does not own the capture contract, and does not download
YouTube video bytes.

## Start Here

- `README.md` explains the capture flow, the supported providers, the build and
  the manual Chrome load step.
- `src/capture/registry.ts` owns deterministic adapter selection and the generic
  fallback; each provider's selectors live under `src/capture/adapters/`.
- `src/background/captureJobs.ts` is the durable manifest-first job coordinator.
- `src/background/captureApi.ts` is the FE client: route derivation, upload and
  completion.
- `vendor/fragments-engine-browser-contract-v1/` is the pinned FE contract;
  `FE_COMMIT` records the reviewed FE commit it came from.

## Commands

```bash
npm run test:run
npm run lint
npm run typecheck
npm run contracts:check
npm run build
```

`npm run build` is the real gate: it re-checks the generated validators,
typechecks, bundles, and fails on eval-like code in the packaged output.
`test:conformance:live` needs a running FE instance and is opt-in; everything
else is deterministic. `npm run contracts:generate` rewrites tracked files under
`src/generated/` — use `contracts:check` unless you are deliberately re-pinning.

## Boundaries

FE owns the capture contract. `vendor/fragments-engine-browser-contract-v1/` is
vendored and pinned and `src/generated/` is produced from it, so schema changes
start at the FE source, then re-vendor and regenerate. Never hand-edit generated
output or treat it as design authority; `contracts:check` compares it
byte-for-byte.

Browser credentials never leave the browser. Cookies, tokens, local storage and
unrelated headers must not enter an envelope or an upload.
`sanitizeCapturePageUrl` strips userinfo and credential-shaped query and fragment
keys; the adapters' `credential-exclusion.html` fixtures and `captureApi.test.ts`'s
"uploads exact bytes with RFC 9530 digest and no credentials or unrelated
headers" guard it by name. Host permission is scoped to the configured origin and
requested rather than assumed, and transfer fails closed on a revoked or stale
permission snapshot rather than fetching.

An FE-accepted manifest is success. Optional media failure, popup close, or an
MV3 worker restart must never cancel or erase an accepted capture. Retrying a
durable job reuses its capture ID; a later deliberate clip is a new attempt.

MV3's CSP forbids `eval` and `new Function`, which is why the FE JSON Schemas are
precompiled into standalone validators instead of being compiled at runtime.
`scripts/check-extension-csp.mjs` fails the build on eval-like code reaching
`dist/`, so a dependency needing runtime compilation cannot be added here.

Provider markup changes without warning. Keep selectors and parsers inside their
adapter, version them, and test against sanitized fixtures — a live site is never
a required dependency of a test run. Every provider path also preserves the
generic readable body, selection, note and tags; a successful media extraction
never replaces them.
