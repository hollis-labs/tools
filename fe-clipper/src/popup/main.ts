import { parseTagsInput } from "@/lib/payload";
import { requestHostPermission } from "@/lib/permissions";
import { getCaptureConfiguration } from "@/lib/storage";
import { DEFAULT_TAGS, type CapturedPage, type ExtensionMessage } from "@/lib/types";

const statusEl = document.getElementById("status") as HTMLDivElement;
const copyErrorButton = document.getElementById("copy-error-button") as HTMLButtonElement;
const formEl = document.getElementById("clip-form") as HTMLFormElement;
const titleInput = document.getElementById("title-input") as HTMLInputElement;
const tagsInput = document.getElementById("tags-input") as HTMLInputElement;
const noteInput = document.getElementById("note-input") as HTMLTextAreaElement;
const selectionPreview = document.getElementById("selection-preview") as HTMLParagraphElement;
const sendButton = document.getElementById("send-button") as HTMLButtonElement;

let captured: CapturedPage | null = null;
let activeJobId: string | undefined;
type StartCaptureMessage = Extract<ExtensionMessage, { type: "START_CAPTURE_JOB" }>;
let activeStartRequest: StartCaptureMessage | undefined;

// Give the user a moment to read the success message before the popup
// closes itself; errors never auto-close (setStatus below only shows the
// copy-error button on "error", and nothing schedules a close for it).
const SUCCESS_AUTO_CLOSE_MS = 1200;
const COPY_BUTTON_RESET_MS = 1500;

function setStatus(message: string, kind: "loading" | "error" | "success" | "idle" = "idle") {
  statusEl.textContent = message;
  statusEl.dataset.kind = kind;
  copyErrorButton.hidden = kind !== "error";
}

async function copyErrorToClipboard() {
  const text = statusEl.textContent ?? "";
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
    copyErrorButton.textContent = "Copied!";
  } catch {
    copyErrorButton.textContent = "Copy failed";
  } finally {
    setTimeout(() => {
      copyErrorButton.textContent = "Copy error";
    }, COPY_BUTTON_RESET_MS);
  }
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

  titleInput.value = captured.document.title;
  tagsInput.value = DEFAULT_TAGS.join(", ");
  selectionPreview.textContent = captured.document.selection
    ? captured.document.selection
    : "None";

  const extractionError = captured.warnings.find((warning) => warning.severity === "error");
  if (extractionError) {
    setStatus(
      `Extraction had a problem (${extractionError.message}); review before sending.`,
      "error",
    );
  } else if (captured.document.usedFallback) {
    setStatus("No article region found; used the raw page as a fallback.", "loading");
  } else {
    setStatus(`Captured from ${captured.identity.submittedUrl}`, "idle");
  }

  formEl.hidden = false;
}

async function handleSubmit(event: SubmitEvent) {
  event.preventDefault();
  if (!captured) return;

  sendButton.disabled = true;
  setStatus("Sending to Fragments Engine...", "loading");

  try {
    let request: ExtensionMessage;
    if (activeStartRequest) {
      request = activeStartRequest;
    } else {
      const configuration = await getCaptureConfiguration();
      const granted = await requestHostPermission(configuration.endpoint);
      if (!granted) {
        setStatus(
          `Permission to reach ${configuration.endpoint} was denied. Grant it from the options page.`,
          "error",
        );
        return;
      }
      activeJobId = crypto.randomUUID();
      activeStartRequest = {
        type: "START_CAPTURE_JOB",
        captureId: activeJobId,
        captured,
        edits: {
          title: titleInput.value,
          tags: parseTagsInput(tagsInput.value),
          note: noteInput.value,
        },
        configuration,
      };
      request = activeStartRequest;
    }

    const response = (await chrome.runtime.sendMessage(request)) as ExtensionMessage;

    if (response.type !== "CAPTURE_JOB_RESULT") {
      setStatus("Unexpected response from the extension background worker.", "error");
      return;
    }

    if (response.ok) {
      activeJobId = undefined;
      activeStartRequest = undefined;
      const transfer = response.phase === "pending_transfer"
        ? " Media transfer will continue in the background."
        : "";
      const warning = response.warning ? ` ${response.warning}` : "";
      setStatus(`Captured in Fragments Engine.${transfer}${warning}`, "success");
      setTimeout(() => window.close(), SUCCESS_AUTO_CLOSE_MS);
    } else {
      activeJobId = response.captureId ?? activeJobId;
      if (activeJobId) sendButton.textContent = "Retry capture";
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

copyErrorButton.addEventListener("click", () => {
  void copyErrorToClipboard();
});

void runCapture();
