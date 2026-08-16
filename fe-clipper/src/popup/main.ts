import { buildPayload, parseTagsInput } from "@/lib/payload";
import { requestHostPermission } from "@/lib/permissions";
import { getEndpoint } from "@/lib/storage";
import { DEFAULT_TAGS, type CapturedPage, type ExtensionMessage } from "@/lib/types";

const statusEl = document.getElementById("status") as HTMLDivElement;
const formEl = document.getElementById("clip-form") as HTMLFormElement;
const titleInput = document.getElementById("title-input") as HTMLInputElement;
const tagsInput = document.getElementById("tags-input") as HTMLInputElement;
const selectionPreview = document.getElementById("selection-preview") as HTMLParagraphElement;
const sendButton = document.getElementById("send-button") as HTMLButtonElement;

let captured: CapturedPage | null = null;

function setStatus(message: string, kind: "loading" | "error" | "success" | "idle" = "idle") {
  statusEl.textContent = message;
  statusEl.dataset.kind = kind;
}

async function captureActiveTab(): Promise<CapturedPage> {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id) {
    throw new Error("No active tab to capture.");
  }

  // Step 1: inject the bundled content script (Readability + Turndown +
  // extraction logic). It stashes its result on `window` rather than
  // returning it directly -- `files`-based injections don't reliably
  // surface a return value the way `func` injections do.
  await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    files: ["content-script.js"],
  });

  // Step 2: read the stashed result back with a small, fully self-contained
  // `func` injection (allowed to return a value directly).
  const [{ result } = { result: undefined }] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: () => window.__FE_CLIPPER_CAPTURE__,
  });

  if (!result) {
    throw new Error("Capture failed: the content script returned nothing.");
  }
  return result;
}

async function runCapture() {
  setStatus("Capturing page content...", "loading");
  try {
    captured = await captureActiveTab();
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    setStatus(`Couldn't capture this page: ${message}`, "error");
    return;
  }

  titleInput.value = captured.title;
  tagsInput.value = DEFAULT_TAGS.join(", ");
  selectionPreview.textContent = captured.selection ? captured.selection : "None";

  if (captured.error) {
    setStatus(`Extraction had a problem (${captured.error}); review before sending.`, "error");
  } else if (captured.usedFallback) {
    setStatus("No article region found; used the raw page as a fallback.", "loading");
  } else {
    setStatus(`Captured from ${captured.url}`, "idle");
  }

  formEl.hidden = false;
}

async function handleSubmit(event: SubmitEvent) {
  event.preventDefault();
  if (!captured) return;

  sendButton.disabled = true;
  setStatus("Sending to Fragments Engine...", "loading");

  try {
    const endpoint = await getEndpoint();

    const granted = await requestHostPermission(endpoint);
    if (!granted) {
      setStatus(
        `Permission to reach ${endpoint} was denied. Grant it from the options page.`,
        "error",
      );
      return;
    }

    const payload = buildPayload(captured, {
      title: titleInput.value,
      tags: parseTagsInput(tagsInput.value),
    });

    const response = (await chrome.runtime.sendMessage({
      type: "SEND_CLIP",
      payload,
      endpoint,
    } satisfies ExtensionMessage)) as ExtensionMessage;

    if (response.type !== "SEND_CLIP_RESULT") {
      setStatus("Unexpected response from the extension background worker.", "error");
      return;
    }

    if (response.ok) {
      setStatus("Sent. Check the Fragments Engine inbox.", "success");
    } else {
      setStatus(response.error, "error");
    }
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    setStatus(`Failed to send: ${message}`, "error");
  } finally {
    sendButton.disabled = false;
  }
}

formEl.addEventListener("submit", (event) => {
  void handleSubmit(event);
});

void runCapture();
