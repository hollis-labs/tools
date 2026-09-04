// The MV3 worker owns durable capture execution. The popup contributes one
// deliberate action and may disappear as soon as FE accepts its manifest.

import { CaptureApi } from "./captureApi";
import { CaptureJobCoordinator } from "./captureJobs";
import { CaptureJobStore, type CaptureJobRecord } from "@/lib/jobStorage";
import type { ExtensionMessage } from "@/lib/types";

const coordinator = new CaptureJobCoordinator(new CaptureJobStore(), new CaptureApi(), {
  clientVersion: chrome.runtime.getManifest().version,
});

function jobResult(job: CaptureJobRecord): ExtensionMessage {
  const acceptedState = job.acceptance ?? job.status;
  if (acceptedState) {
    return {
      type: "CAPTURE_JOB_RESULT",
      ok: true,
      accepted: true,
      captureId: job.captureId,
      phase: job.phase,
      fragmentId: acceptedState.fragment_id,
      ...(job.error ? { warning: job.error.message } : {}),
    };
  }
  return {
    type: "CAPTURE_JOB_RESULT",
    ok: false,
    accepted: false,
    captureId: job.captureId,
    phase: job.phase,
    error: job.error?.message ?? "The capture job stopped before Fragments Engine accepted it.",
  };
}

async function handleMessage(message: ExtensionMessage): Promise<ExtensionMessage> {
  try {
    if (message.type === "START_CAPTURE_JOB") {
      return jobResult(await coordinator.start(message));
    }
    if (message.type === "RETRY_CAPTURE_JOB") {
      return jobResult(await coordinator.retry(message.captureId));
    }
    return {
      type: "CAPTURE_JOB_RESULT",
      ok: false,
      accepted: false,
      error: "Unsupported background capture message.",
    };
  } catch (error) {
    return {
      type: "CAPTURE_JOB_RESULT",
      ok: false,
      accepted: false,
      error: error instanceof Error ? error.message : String(error),
    };
  }
}

chrome.runtime.onMessage.addListener((message: ExtensionMessage, _sender, sendResponse) => {
  if (message.type !== "START_CAPTURE_JOB" && message.type !== "RETRY_CAPTURE_JOB") {
    return undefined;
  }
  void handleMessage(message).then(sendResponse);
  return true;
});

function resumeDurableJobs(): void {
  void coordinator.resumeAll().catch((error) => {
    console.error("Could not resume durable FE capture jobs:", error);
  });
}

// Module evaluation is worker startup; browser startup is registered
// explicitly as well because Chrome may keep an already-running worker alive.
resumeDurableJobs();
chrome.runtime.onStartup.addListener(resumeDurableJobs);
