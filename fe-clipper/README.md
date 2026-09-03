# fe-clipper

Chrome (Manifest V3) web clipper for [Fragments Engine](../fragments-engine). Captures the
active tab's readable content client-side and POSTs it to FE's `/v1/intake` endpoint, so
content that would otherwise trip server-side bot-blocking (paywalled/gated articles, sites
that reject headless fetchers, etc.) can still land in FE's inbox — the browser already
rendered the page, so the extension just reads it.

Part of epic `EP-20260816-0005`, task `CW-20260816-0073`. Depends on FE's sibling task
`CW-20260816-0071`, which added `source_url` / `description` / `selection` to `/v1/intake` and
already landed in the `fragments-engine` repo (commit `d03fd5d`).

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
   - the page title and URL.
3. The popup shows the captured title (editable), a tags field pre-filled with `link`
   (editable), and an optional **Note** field, so you can course-correct — and add context —
   before anything is sent, consistent with how manual intake works elsewhere in FE.
4. On **Send**, the popup asks the background service worker to `POST` the payload to the
   configured FE endpoint (default `http://127.0.0.1:8091/v1/intake`):

   ```json
   {
     "content": "<markdown>",
     "title": "<page title>",
     "source_url": "<page url>",
     "description": "<meta description, if present>",
     "selection": "<highlighted text, if any>",
     "tags": ["link"]
   }
   ```

   There's no dedicated `note` field on `/v1/intake` (CW-20260816-0071's contract is
   content/title/source_url/description/selection/tags only), so a non-empty note is prepended
   to `content` as a labeled Markdown blockquote (`> **Note:** ...`, see `lib/payload.ts`'s
   `prependNote`) rather than sent as its own field — it lands in the fragment's actual
   searchable body, and shows up in FE's auto-derived summary too when no meta description is
   present, so it's visible at a glance in the inbox.
5. The popup shows a clear success or failure confirmation. On success, the popup auto-closes
   about 1.2s after showing the confirmation. On failure — FE unreachable (dev server not
   running, wrong port, etc.), or FE rejecting the request — the popup stays open, shows the
   error, and a **Copy error** button appears next to it so you can paste the exact message
   somewhere (e.g. when asking for help). It never fails silently.

## Project layout

```
manifest.json              MV3 manifest (source; copied into dist/ at build time)
popup.html / options.html  Extension pages (built by Vite)
src/
  background/index.ts      Service worker: owns the one outbound fetch() to FE
  content/index.ts          Injected content script: runs the extraction pipeline in-page
  capture/                  PageCaptureAdapter types, deterministic registry, generic fallback
  contracts/                Pinned FE runtime validators and capture capability preflight
  generated/                Generated FE OpenAPI types (never edit by hand)
  popup/                    Popup UI (capture preview + edit + send)
  options/                  Options page (configurable FE endpoint)
  lib/
    extractPage.ts          Readability -> Turndown pipeline, meta-description precedence
    payload.ts               Maps captured data + edits -> exact /v1/intake wire shape
    storage.ts                Reads/writes the configured endpoint (chrome.storage.local)
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
```

## Pinned Fragments Engine capture contract

The canonical browser-capture contract is vendored from Fragments Engine under
`vendor/fragments-engine-browser-contract-v1`. `FE_COMMIT` records the exact reviewed FE commit.
TypeScript API types are generated by the lockfile-pinned `openapi-typescript@7.10.1`; runtime
validation uses the vendored JSON Schema 2020-12 files through strict `ajv@8.17.1` and
`ajv-formats@3.0.1`. Extension-owned adapter observations remain separate from these generated
transport types.

To deliberately refresh the vendored files from their currently recorded pin, point `FE_REPO` at
a local checkout containing that commit, then regenerate and verify:

```bash
FE_REPO=/path/to/fragments-engine npm run contracts:vendor
npm run contracts:generate
npm run contracts:check
```

`preflightCaptureManifest` derives `/v1/capabilities` from the configured FE origin, omits browser
credentials, validates the response, and requires both accepted `fe.capture.v1` support and runtime
`capture_manifest` readiness. It returns UI-ready incompatibility, network, server, and invalid-body
messages. The existing `/v1/intake` submission deliberately remains unchanged until the durable
manifest job is introduced: `/v1/intake` is not treated as the v1 capture API, and a contract-only FE
server correctly reports manifest capture as unavailable.

## Loading the unpacked extension in Chrome (manual step — not done by this build)

1. `npm run build`
2. Open `chrome://extensions`
3. Enable **Developer mode** (top-right toggle)
4. Click **Load unpacked**, and select this project's `dist/` directory
5. The FE Clipper icon should appear in the toolbar (pin it if it's hidden behind the
   extensions overflow menu)

## Configuring the FE endpoint

Right-click the toolbar icon → **Options** (or `chrome://extensions` → FE Clipper → Details →
Extension options). The endpoint defaults to `http://127.0.0.1:8091/v1/intake`. Changing it to a
different origin triggers a Chrome permission prompt for that origin — the extension only ever
holds host permission for wherever it's actually configured to POST, not a standing wildcard
grant. Saving is rejected if that permission prompt is denied.

## What has and hasn't been verified

**Verified as part of this build:**

- `npm run build` produces a clean, loadable `dist/` unpacked-extension directory (manifest,
  background worker, content script, popup, options page, icons — see full command output in
  the task record).
- `npm run lint` and `npm run typecheck` pass with zero errors/warnings.
- `npm test` — 39 unit tests (vitest + jsdom) covering deterministic adapter selection and
  generic fallback, the shared adapter result, the Readability → Turndown → payload pipeline,
  meta-description precedence, distinct text-selection capture, extraction fallback/error
  results, the exact six-field `/v1/intake` compatibility wire shape, every portable FE contract
  fixture, strict request/response and media/outcome validation, and capability-preflight failure
  states.
- The wire contract itself was verified with a **scripted `curl` POST** against a real, locally
  running `fragments-engine` dev server (`serve-api`), using a payload shaped exactly like what
  `src/background/index.ts` sends. The response was `HTTP 201` with `outcome: "inserted"`, and
  fetching the resulting fragment back (`GET /v1/fragments/get`) confirmed: `source_type` =
  `"article"`, `Content` byte-identical to the posted Markdown (headings/lists/links/fenced code
  intact), `Title` correct, a `tag` entity `link` present, and `MetadataJSON` containing
  `selection` (distinct from `Content`, never merged into it), `url`/`domain` derived from
  `source_url`, and `capture_method: "web_clipper"`.

**NOT verified — still needs a human or a browser-automation-capable session:**

- Interactive browser verification has **not** been done. Nobody has loaded this unpacked
  extension into an actual Chrome window, clicked the toolbar icon on a real page, confirmed the
  popup renders and shows the captured title/tags/selection correctly, clicked Send and watched
  a real end-to-end round trip land in FE's inbox through the actual UI, or confirmed the
  failure-state UX when FE is unreachable. Everything above was validated via unit tests
  (mocked DOM/fetch) and a scripted HTTP call that bypasses the extension UI entirely — not
  through loading and clicking through the extension itself. That interactive pass is a
  separate, required step before this is considered fully done end-to-end.
