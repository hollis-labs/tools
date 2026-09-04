# fe-clipper

Chrome (Manifest V3) web clipper for Fragments Engine. Captures the
active tab's readable content client-side and submits FE's manifest-first `/v1/captures` contract, so
content that would otherwise trip server-side bot-blocking (paywalled/gated articles, sites
that reject headless fetchers, etc.) can still land in FE's inbox — the browser already
rendered the page, so the extension just reads it.

## How it works

1. Click the toolbar icon. The popup opens and immediately injects a bundled content script
   into the active tab.
2. The content script runs [`@mozilla/readability`](https://github.com/mozilla/readability)
   (the engine behind Firefox Reader View) against the page, converts the extracted HTML to
   Markdown with [`turndown`](https://github.com/mixmark-io/turndown), and separately captures:
   - the current text selection (`window.getSelection().toString()`), kept distinct from the
     main content, never merged into it;
   - the page's own meta description (`meta[name="description"]`, falling back to
     `meta[property="og:description"]`);
   - the page title, language, submitted URL, and normalized canonical URL;
   - a metadata- or article-derived hero plus meaningful inline images in source order. The
     generic adapter selects only observed HTTP(S) sources (including the largest declared
     `srcset` candidate) and deterministically filters page chrome, avatars/icons/logos,
     hidden/decorative images, tracking pixels, and duplicate URLs.
3. The popup shows the captured title (editable), a tags field pre-filled with `link`
   (editable), and an optional **Note** field, so you can course-correct — and add context —
   before anything is sent, consistent with how manual intake works elsewhere in FE.
4. On **Send**, the popup snapshots the configured endpoint and principal and preallocates one
   capture ID before messaging the background service worker. The worker builds and validates the
   pinned FE envelope and persists the complete job to `chrome.storage.local` before making a
   network request. Highlighted text and the capture note are separate append-only annotations;
   neither changes the extracted source body.
5. The worker capability-checks FE, submits the manifest, and persists FE's stable fragment,
   revision, attempt, and asset-instruction IDs. The accepted manifest is immediate popup success;
   requested media uploads remain a durable `pending_transfer` job for the background transfer
   phase. Retries and worker/browser restart reconciliation reuse the original capture ID and query
   FE before any uncertain resend. Completed jobs have bounded local retention; quota recovery can
   discard only corrupt or oldest terminal records, never an active transfer or retry job.
6. The popup distinguishes manifest acceptance from optional media work. It shows pending,
   transferring, partial, failed, retrying, and complete states from the durable job. Retry first
   rechecks only the currently required FE/media origins and resumes the same capture ID. A later
   deliberate clip creates a new attempt under FE's existing stable fragment.

## Supported capture sources

- **Pinterest:** target Pin identity, readable context, and the primary original/thumbnail image;
  recommendation images are excluded.
- **Instagram:** image posts, reels/video, and ordered mixed carousels. Video streams remain
  reference-only while posters and supported images may be transferred.
- **YouTube:** video identity, readable context, posters, and available subtitle/transcript
  candidates. The extension never automatically downloads YouTube video bytes.
- **Other pages:** the generic Readability/Turndown path plus meaningful hero/inline images,
  selection, note, and tags.

Optional media failure never erases an FE-accepted text capture. Missing or withheld source-host
permission produces an explicit retryable/reference outcome; granting that exact origin and
clicking Retry resumes the durable job rather than creating a second capture.

## Project layout

```
manifest.json              MV3 manifest (source; copied into dist/ at build time)
popup.html / options.html  Extension pages (built by Vite)
src/
  background/              Service worker, strict FE client, durable capture coordinator
  content/index.ts          Injected content script: runs the extraction pipeline in-page
  capture/                  PageCaptureAdapter types, deterministic registry, generic fallback
  contracts/                Pinned FE runtime validators and capture capability preflight
  generated/                Generated FE OpenAPI types (never edit by hand)
  popup/                    Popup UI (capture preview + edit + send)
  options/                  Options page (configurable FE endpoint)
  lib/
    extractPage.ts          Readability -> Turndown pipeline, meta-description precedence
    payload.ts               Retained legacy `/v1/intake` compatibility mapper/tests
    jobStorage.ts            Durable per-capture MV3 job records
    storage.ts               Reads/writes endpoint and local-principal settings
    permissions.ts            Requests/checks host permission for the configured origin
    types.ts                  Shared types
    *.test.ts                 Unit tests (vitest + jsdom)
scripts/
  build-extension.mjs        esbuild bundling for background.js / content-script.js + copies
                              manifest.json and icons/ into dist/
  vendor-fe-contract.mjs     Re-vendors the pinned FE contract from a local FE checkout
  check-generated-contract.mjs
                              Checks generated types byte-for-byte against vendored OpenAPI
  generate-icons.mjs         One-off placeholder icon generator (already run; dist icons exist
                              under src/icons/)
vendor/
  fragments-engine-browser-contract-v1/
                              Exact FE-owned v1 artifacts plus the reviewed FE commit pin
```

## Build

```bash
npm install
npm run build
```

`npm run build` runs `tsc --noEmit` (typecheck), then `vite build` (popup.html/options.html),
then `scripts/build-extension.mjs` (esbuild-bundles `background.js` and `content-script.js` as
self-contained IIFEs, and copies `manifest.json` + `src/icons/` into `dist/`). The result is a
complete, loadable unpacked-extension directory at `dist/`.

Other scripts:

```bash
npm run typecheck   # tsc --noEmit only
npm run lint        # eslint .
npm test            # vitest (watch mode)
npm run test:run    # vitest run (single pass)
npm run contracts:generate # regenerate TypeScript API types from the pinned OpenAPI file
npm run contracts:check    # fail when generated output is stale
npm run test:conformance   # deterministic provider/FE scenario matrix
```

## Pinned Fragments Engine capture contract

The canonical browser-capture contract is vendored from Fragments Engine under
`vendor/fragments-engine-browser-contract-v1`. `FE_COMMIT` records the exact reviewed FE commit.
TypeScript API types are generated by the lockfile-pinned `openapi-typescript@7.10.1`. Strict
`ajv@8.17.1` and `ajv-formats@3.0.1` compile the vendored JSON Schema 2020-12 files into checked-in
standalone validators during contract generation; the extension bundles those static functions so
its MV3 worker never needs CSP-forbidden `eval`/`new Function` code. Extension-owned adapter
observations remain separate from these generated transport types.

To deliberately refresh the vendored files from their currently recorded pin, point `FE_REPO` at
a local checkout containing that commit, then regenerate and verify:

```bash
FE_REPO=/path/to/fragments-engine npm run contracts:vendor
npm run contracts:generate
npm run contracts:check
```

`preflightCaptureManifest` derives `/v1/capabilities` from the configured FE origin, omits browser
credentials, validates the response, and requires both accepted `fe.capture.v1` support and runtime
`capture_manifest` readiness. The durable coordinator additionally checks advertised media,
variant, custody, transfer, manifest-size, and completion capabilities before using them. It returns
UI-ready incompatibility, network, server, and invalid-body messages.

## Loading the unpacked extension in Chrome (manual step — not done by this build)

1. `npm run build`
2. Open `chrome://extensions`
3. Enable **Developer mode** (top-right toggle)
4. Click **Load unpacked**, and select this project's `dist/` directory
5. The FE Clipper icon should appear in the toolbar (pin it if it's hidden behind the
   extensions overflow menu)

## Configuring the FE endpoint

Right-click the toolbar icon → **Options** (or `chrome://extensions` → FE Clipper → Details →
Extension options). The endpoint defaults to `http://127.0.0.1:8091/v1/captures`, and the local
principal defaults to `local-user`. Changing the endpoint to a different origin triggers a Chrome
permission prompt for that origin — the extension only ever
holds host permission for wherever it's actually configured to POST, not a standing wildcard
grant. Saving is rejected if that permission prompt is denied.

## Verification status

**Verified as part of this build:**

- `npm run build` produces a clean, loadable `dist/` unpacked-extension directory (manifest,
  background worker, content script, popup, options page, icons — see full command output in
  the task record).
- `npm run lint` and `npm run typecheck` pass with zero errors/warnings.
- `npm run test:run` — 231 tests across 25 files (plus one opt-in live test skipped) covering
  deterministic adapter selection and
  generic fallback, the shared adapter result, the Readability → Turndown → payload pipeline,
  meta-description precedence, distinct text-selection capture, generic hero/inline media,
  `srcset` selection, media filtering/deduplication, extraction fallback/error results, the exact
  six-field legacy `/v1/intake` compatibility wire shape, every portable FE contract fixture,
  strict request/response and media/outcome validation, capability preflight, envelope mapping,
  durable storage, retry/reconciliation, completion, corruption isolation, provider conformance,
  current live provider page shapes, and credential exclusion.
- The retained legacy `/v1/intake` wire contract was previously verified with a **scripted `curl`
  POST** against a real, locally running `fragments-engine` dev server (`serve-api`). The response
  was `HTTP 201` with `outcome: "inserted"`, and
  fetching the resulting fragment back (`GET /v1/fragments/get`) confirmed: `source_type` =
  `"article"`, `Content` byte-identical to the posted Markdown (headings/lists/links/fenced code
  intact), `Title` correct, a `tag` entity `link` present, and `MetadataJSON` containing
  `selection` (distinct from `Content`, never merged into it), `url`/`domain` derived from
  `source_url`, and `capture_method: "web_clipper"`.

The complete installed-extension matrix passed on 2026-09-04 against disposable FE commit
`4e87a8d`: Pinterest image; Instagram image, reel, and mixed carousel; YouTube with and without
captions; and generic pages with and without media. The run also proved deliberate recapture,
same-ID permission recovery across extension-worker restart, Reader projection/order, and
credential-free manifest/status/upload/completion requests. Safe IDs and reproducible procedure
are recorded in [the provider capture smoke matrix](docs/testing/provider-capture-smoke.md).
