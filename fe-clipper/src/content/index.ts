// Content script: injected on demand (via chrome.scripting.executeScript
// with `files`, from the popup) into the active tab. Runs the
// Readability -> Turndown extraction pipeline against the live page and
// stashes the result on `window` for a follow-up executeScript call (using
// `func`) to read back -- `func`-based injections can only return
// self-contained closures, so heavier logic that depends on bundled
// third-party libraries (Readability, Turndown) has to run via a `files`
// injection like this one instead.
//
// This file must stay a fully self-contained bundle (no runtime imports of
// its own) since it's injected as a classic content-script file, not loaded
// as an ES module.

import { extractPage } from "@/lib/extractPage";
import type { CapturedPage } from "@/lib/types";

declare global {
  interface Window {
    __FE_CLIPPER_CAPTURE__?: CapturedPage;
  }
}

function capture(): CapturedPage {
  const selectionText = window.getSelection()?.toString() ?? "";
  return extractPage(document, { url: window.location.href, selectionText });
}

try {
  window.__FE_CLIPPER_CAPTURE__ = capture();
} catch (err) {
  window.__FE_CLIPPER_CAPTURE__ = {
    title: document.title || window.location.href,
    url: window.location.href,
    contentMarkdown: "",
    description: "",
    selection: window.getSelection()?.toString().trim() ?? "",
    usedFallback: true,
    error: err instanceof Error ? err.message : String(err),
  };
}
