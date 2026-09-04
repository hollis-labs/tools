import { parseTagsInput } from "@/lib/payload";
import { requestCapturePermissions, requestRetryPermissions } from "@/lib/permissions";
import { getCaptureConfiguration } from "@/lib/storage";
import {
  DEFAULT_TAGS,
  type CapturedPage,
  type CaptureJobSummary,
  type ExtensionMessage,
} from "@/lib/types";
import { CaptureJobListRenderer } from "./jobListView";
import { acceptedCaptureNotice, scheduleAcceptedPopupClose } from "./lifecycle";
import { CaptureActionState, CaptureMutationGuard } from "./captureActionState";
import { dispatchExistingCaptureRetry } from "./startRetry";

/**
 * Ceiling for in-page extraction. It must clear the X adapter's own scroll
 * budget with room to spare, or a long conversation would time out here after
 * the walk had already succeeded.
 */
const CAPTURE_TIMEOUT_MS = 45_000;
const CAPTURE_POLL_MS = 150;

const statusEl = document.getElementById("status") as HTMLDivElement;
const copyErrorButton = document.getElementById("copy-error-button") as HTMLButtonElement;
const formEl = document.getElementById("clip-form") as HTMLFormElement;
const titleInput = document.getElementById("title-input") as HTMLInputElement;
const tagsInput = document.getElementById("tags-input") as HTMLInputElement;
const noteInput = document.getElementById("note-input") as HTMLTextAreaElement;
const selectionPreview = document.getElementById("selection-preview") as HTMLParagraphElement;
const sendButton = document.getElementById("send-button") as HTMLButtonElement;
const captureJobsEl = document.getElementById("capture-jobs") as HTMLElement;
const captureJobListEl = document.getElementById("capture-job-list") as HTMLDivElement;
const jobListRenderer = new CaptureJobListRenderer(captureJobsEl, captureJobListEl);

let captured: CapturedPage | null = null;
let jobs: CaptureJobSummary[] = [];
let refreshInFlight: Promise<void> | undefined;
type StartCaptureMessage = Extract<ExtensionMessage, { type: "START_CAPTURE_JOB" }>;
const captureAction = new CaptureActionState();
const mutationGuard = new CaptureMutationGuard();

const COPY_BUTTON_RESET_MS = 1500;
const JOB_REFRESH_MS = 1500;

function setStatus(message: string, kind: "loading" | "error" | "success" | "idle" = "idle") {
  statusEl.textContent = message;
  statusEl.dataset.kind = kind;
  copyErrorButton.hidden = kind !== "error";
}

async function copyText(text: string, button: HTMLButtonElement) {
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
    button.textContent = "Copied!";
  } catch {
    button.textContent = "Copy failed";
  } finally {
    setTimeout(() => {
      button.textContent = "Copy error";
    }, COPY_BUTTON_RESET_MS);
  }
}

function renderJobs() {
  jobListRenderer.render(jobs, mutationGuard.busy);
}

function beginMutation(): boolean {
  if (!mutationGuard.begin()) return false;
  sendButton.disabled = true;
  renderJobs();
  return true;
}

function endMutation(): void {
  mutationGuard.end();
  sendButton.disabled = false;
  renderJobs();
}

async function refreshJobs(reportFailure = false, afterCurrent = false): Promise<void> {
  if (refreshInFlight) {
    await refreshInFlight;
    // A mutation needs a query begun after any older polling query completed,
    // otherwise that stale response could outlive the popup-wide busy guard.
    if (afterCurrent) await refreshJobs(reportFailure);
    return;
  }
  const operation = (async () => {
    try {
      const response = (await chrome.runtime.sendMessage({ type: "QUERY_CAPTURE_JOBS" })) as ExtensionMessage;
      if (response.type !== "CAPTURE_JOBS_RESULT" || !response.ok) {
        if (reportFailure) {
          setStatus(
            response.type === "CAPTURE_JOBS_RESULT" ? response.error : "Could not read capture jobs.",
            "error",
          );
        }
        return;
      }
      jobs = response.jobs;
      if (captureAction.sync(jobs)) sendButton.textContent = "Capture this page again";
      renderJobs();
    } catch (error) {
      if (reportFailure) {
        setStatus(`Could not read capture jobs: ${error instanceof Error ? error.message : String(error)}`, "error");
      }
    }
  })();
  refreshInFlight = operation;
  try {
    await operation;
  } finally {
    if (refreshInFlight === operation) refreshInFlight = undefined;
  }
}

/**
 * Capture finishes asynchronously in the page, because an adapter may walk a
 * virtualized list before reading it. The content script clears the previous
 * result in its synchronous prelude, so a defined value here is always this
 * capture's own.
 */
async function captureActiveTab(): Promise<CapturedPage> {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id) throw new Error("No active tab to capture.");
  const tabId = tab.id;
  await chrome.scripting.executeScript({ target: { tabId }, files: ["content-script.js"] });

  const deadline = Date.now() + CAPTURE_TIMEOUT_MS;
  for (;;) {
    const [{ result } = { result: undefined }] = await chrome.scripting.executeScript({
      target: { tabId },
      func: () => window.__FE_CLIPPER_CAPTURE__,
    });
    if (result) return result;
    if (Date.now() >= deadline) {
      throw new Error("the page did not finish extracting in time.");
    }
    await new Promise((resolve) => setTimeout(resolve, CAPTURE_POLL_MS));
  }
}

async function runCapture() {
  setStatus("Capturing page content...", "loading");
  try {
    captured = await captureActiveTab();
  } catch (error) {
    setStatus(
      `Couldn't capture this page: ${error instanceof Error ? error.message : String(error)}`,
      "error",
    );
    return;
  }

  titleInput.value = captured.document.title;
  tagsInput.value = DEFAULT_TAGS.join(", ");
  selectionPreview.textContent = captured.document.selection || "None";
  const extractionError = captured.warnings.find((warning) => warning.severity === "error");
  if (extractionError) {
    setStatus(`Extraction had a problem (${extractionError.message}); review before sending.`, "error");
  } else if (captured.document.usedFallback) {
    setStatus("No article region found; used the raw page as a fallback.", "loading");
  } else {
    setStatus(`Captured from ${captured.identity.submittedUrl}`, "idle");
  }
  formEl.hidden = false;
}

async function handleSubmit(event: SubmitEvent) {
  event.preventDefault();
  if (!captured || !beginMutation()) return;
  setStatus("Sending manifest to Fragments Engine...", "loading");

  try {
    let request: StartCaptureMessage;
    let rawResponse: unknown;
    if (captureAction.activeStartRequest) {
      request = captureAction.activeStartRequest;
      rawResponse = await dispatchExistingCaptureRetry(
        request,
        (prepared) => chrome.runtime.sendMessage(prepared),
        (prepared) => captureAction.begin(prepared),
      );
    } else {
      const configuration = await getCaptureConfiguration();
      const permissions = await requestCapturePermissions(configuration.endpoint, captured);
      if (!permissions.feGranted) {
        setStatus(
          `Permission to reach ${configuration.endpoint} was denied. Grant it from the options page.`,
          "error",
        );
        return;
      }
      const captureId = crypto.randomUUID();
      request = {
        type: "START_CAPTURE_JOB",
        captureId,
        captured,
        edits: {
          title: titleInput.value,
          tags: parseTagsInput(tagsInput.value),
          note: noteInput.value,
        },
        configuration,
        permittedSourceOrigins: permissions.permittedSourceOrigins,
      };
      captureAction.begin(request);
      rawResponse = await chrome.runtime.sendMessage(request);
    }

    const response = rawResponse as ExtensionMessage;
    if (response.type !== "CAPTURE_JOB_RESULT") {
      setStatus("Unexpected response from the extension background worker.", "error");
      return;
    }
    captureAction.applyResult(response);
    if (response.ok) {
      sendButton.textContent = "Capture this page again";
      const notice = acceptedCaptureNotice(response, { fresh: true });
      setStatus(notice.message, notice.kind);
      if (notice.autoClose) {
        scheduleAcceptedPopupClose(() => window.close());
      }
      await refreshJobs(false, true);
    } else {
      if (response.terminal) {
        sendButton.textContent = "Capture this page again";
      } else {
        if (captureAction.activeJobId) sendButton.textContent = "Retry same capture";
      }
      setStatus(response.error, "error");
      await refreshJobs(false, true);
    }
  } catch (error) {
    if (captureAction.activeJobId) sendButton.textContent = "Retry same capture";
    setStatus(`Failed to send: ${error instanceof Error ? error.message : String(error)}`, "error");
    await refreshJobs(false, true);
  } finally {
    endMutation();
  }
}

async function retryJob(job: CaptureJobSummary): Promise<void> {
  if (!beginMutation()) return;
  setStatus(`Retrying ${job.title || "capture"}...`, "loading");
  try {
    const permissions = await requestRetryPermissions(job.feOrigin, job.retryPermissionOrigins);
    if (!permissions.feGranted) {
      setStatus(`Permission to reach ${job.feOrigin} was denied; the durable job was preserved.`, "error");
      return;
    }
    const response = (await chrome.runtime.sendMessage({
      type: "RETRY_CAPTURE_JOB",
      captureId: job.captureId,
      permittedSourceOrigins: permissions.permittedSourceOrigins,
    })) as ExtensionMessage;
    if (response.type !== "CAPTURE_JOB_RESULT") {
      throw new Error("Unexpected response from the extension background worker.");
    }
    captureAction.applyResult(response);
    if (response.ok) {
      const notice = acceptedCaptureNotice(response, {
        fresh: false,
        mediaPermissionDenied: permissions.deniedSourceOrigins.length > 0,
      });
      setStatus(notice.message, notice.kind);
    } else {
      setStatus(response.error, "error");
    }
  } catch (error) {
    setStatus(`Retry failed: ${error instanceof Error ? error.message : String(error)}`, "error");
  } finally {
    await refreshJobs(false, true);
    endMutation();
  }
}

async function dismissJob(job: CaptureJobSummary): Promise<void> {
  if (!beginMutation()) return;
  try {
    const response = (await chrome.runtime.sendMessage({
      type: "DISMISS_CAPTURE_JOB",
      captureId: job.captureId,
    })) as ExtensionMessage;
    if (response.type !== "CAPTURE_JOB_DISMISS_RESULT" || !response.ok) {
      throw new Error(
        response.type === "CAPTURE_JOB_DISMISS_RESULT" ? response.error : "Unexpected dismiss response.",
      );
    }
    if (captureAction.clearMatching(job.captureId)) {
      sendButton.textContent = "Capture this page again";
    }
    setStatus("Removed the confirmed terminal capture from this device.", "idle");
  } catch (error) {
    setStatus(`Could not dismiss capture: ${error instanceof Error ? error.message : String(error)}`, "error");
  } finally {
    await refreshJobs(false, true);
    endMutation();
  }
}

formEl.addEventListener("submit", (event) => void handleSubmit(event));
copyErrorButton.addEventListener("click", () => void copyText(statusEl.textContent ?? "", copyErrorButton));
captureJobListEl.addEventListener("click", (event) => {
  const button = (event.target as HTMLElement).closest<HTMLButtonElement>("button[data-action]");
  const job = jobs.find((candidate) => candidate.captureId === button?.dataset.captureId);
  if (!button || !job) return;
  const action = button.dataset.action as "retry" | "dismiss" | "copy";
  if (!mutationGuard.allows(action)) return;
  if (button.dataset.action === "retry") void retryJob(job);
  if (button.dataset.action === "dismiss") void dismissJob(job);
  if (button.dataset.action === "copy" && job.error) void copyText(job.error.message, button);
});

void refreshJobs(true);
void runCapture();
const refreshTimer = window.setInterval(() => void refreshJobs(), JOB_REFRESH_MS);
window.addEventListener("pagehide", () => window.clearInterval(refreshTimer), { once: true });
