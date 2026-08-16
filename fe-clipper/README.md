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
3. The popup shows the captured title (editable) and a tags field pre-filled with `link`
   (editable), so you can course-correct before anything is sent — consistent with how manual
   intake works elsewhere in FE.
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

5. The popup shows a clear success or failure confirmation. If FE is unreachable (dev server
   not running, wrong port, etc.), the fetch failure is surfaced in the popup — it never fails
   silently.

## Project layout

```
manifest.json              MV3 manifest (source; copied into dist/ at build time)
popup.html / options.html  Extension pages (built by Vite)
src/
  background/index.ts      Service worker: owns the one outbound fetch() to FE
  content/index.ts          Injected content script: runs the extraction pipeline in-page
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
  generate-icons.mjs         One-off placeholder icon generator (already run; dist icons exist
                              under src/icons/)
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
```

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
- `npm test` — 16 unit tests (vitest + jsdom) covering the Readability → Turndown → payload
  pipeline: normal article extraction (headings/lists/links/code blocks preserved as Markdown),
  meta-description detection and precedence (`meta[name="description"]` over
  `meta[property="og:description"]`, and the no-description case), text-selection capture kept
  distinct from the main content, a Readability-finds-nothing fallback path, and the payload
  builder producing the exact six-field `/v1/intake` wire shape.
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
