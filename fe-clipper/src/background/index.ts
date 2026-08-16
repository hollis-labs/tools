// Background service worker: the only piece of the extension that ever
// makes the outbound network request. Popup sends a SEND_CLIP message with
// the fully-built payload and the configured endpoint; this worker POSTs it
// and reports success/failure back so the popup can show a clear
// confirmation (FE unreachable must fail visibly, never silently).

import type { ClipPayload, ExtensionMessage } from "@/lib/types";

chrome.runtime.onMessage.addListener((message: ExtensionMessage, _sender, sendResponse) => {
  if (message.type !== "SEND_CLIP") {
    return undefined;
  }

  void sendClip(message.endpoint, message.payload).then(sendResponse);
  // Returning true keeps the message channel open for the async
  // sendResponse call above (required by the extension messaging API).
  return true;
});

async function sendClip(endpoint: string, payload: ClipPayload): Promise<ExtensionMessage> {
  let response: Response;
  try {
    response = await fetch(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
  } catch (err) {
    // Network-level failure: FE unreachable (service not running, wrong
    // port, CORS/permission issue, etc). This is exactly the "fail
    // visibly" case the popup needs to surface.
    const message = err instanceof Error ? err.message : String(err);
    return { type: "SEND_CLIP_RESULT", ok: false, error: `Could not reach ${endpoint}: ${message}` };
  }

  const text = await response.text();
  let body: unknown = text;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    // FE always replies JSON on success; a non-JSON body just gets passed
    // through as text (e.g. a plain-text error page).
  }

  if (!response.ok) {
    const detail = typeof body === "string" ? body : JSON.stringify(body);
    return {
      type: "SEND_CLIP_RESULT",
      ok: false,
      error: `FE rejected the clip (HTTP ${response.status}): ${detail}`,
    };
  }

  return { type: "SEND_CLIP_RESULT", ok: true, status: response.status, body };
}
