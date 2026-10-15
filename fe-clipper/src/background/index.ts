// The MV3 worker owns durable capture execution. The popup contributes one
// deliberate action and may disappear as soon as FE accepts its manifest.

import { CaptureApi } from "./captureApi";
import { CaptureJobCoordinator } from "./captureJobs";
import { CaptureJobStore } from "@/lib/jobStorage";
import { registerCaptureMessages } from "./messages";

const coordinator = new CaptureJobCoordinator(new CaptureJobStore(), new CaptureApi(), {
  clientVersion: chrome.runtime.getManifest().version,
});

registerCaptureMessages(chrome.runtime, coordinator);

function resumeDurableJobs(): void {
  void coordinator.resumeAll().catch((error) => {
    console.error("Could not resume durable FE capture jobs:", error);
  });
}

// Module evaluation is worker startup; browser startup is registered
// explicitly as well because Chrome may keep an already-running worker alive.
resumeDurableJobs();
chrome.runtime.onStartup.addListener(resumeDurableJobs);
