// Content script: injected on demand (via chrome.scripting.executeScript
// with `files`, from the popup) into the active tab. Runs the
// Readability -> Turndown extraction pipeline against the live page and
// stashes the result on `window` for a follow-up executeScript call (using
// `func`) to read back -- `func`-based injections can only return
// self-contained closures, so heavier logic that depends on bundled
// third-party libraries (Readability, Turndown) has to run via a `files`
// injection like this one instead.
//
// Capture completes asynchronously because an adapter may need a pre-pass
// before the page holds what it claims to show; X mounts conversation replies
// only near the viewport, so its adapter walks the thread first. The readback
// therefore polls, and the synchronous prelude below clears any previous
// result so that poll can never observe a stale capture from an earlier
// injection into this same tab.
//
// This file must stay a fully self-contained bundle (no runtime imports of
// its own) since it's injected as a classic content-script file, not loaded
// as an ES module.

import {
  createFailedPageCapture,
  pageCaptureAdapterRegistry,
  toPageCaptureLocation,
} from "@/capture/registry";
import type { PageCaptureContext } from "@/capture/types";
import type { CapturedPage } from "@/lib/types";

declare global {
  interface Window {
    __FE_CLIPPER_CAPTURE__?: CapturedPage;
  }
}

// Synchronous prelude. `executeScript({files})` resolves once this runs, so
// the popup can rely on the stale result being gone before it starts polling.
delete window.__FE_CLIPPER_CAPTURE__;

function captureContext(): PageCaptureContext {
  return {
    document,
    location: toPageCaptureLocation(window.location.href),
    // Read before any pre-pass: scrolling the page can collapse the selection.
    selectionText: window.getSelection()?.toString() ?? "",
  };
}

const context = captureContext();

void (async () => {
  try {
    const prepared = await pageCaptureAdapterRegistry.prepare(context);
    window.__FE_CLIPPER_CAPTURE__ = pageCaptureAdapterRegistry.capture(context, prepared);
  } catch (err) {
    window.__FE_CLIPPER_CAPTURE__ = createFailedPageCapture(
      context,
      err instanceof Error ? err.message : String(err),
    );
  }
})();
