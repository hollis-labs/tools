import {
  browserPreferredSourceOrigins,
  requestRetryPermissions,
  type PermissionsApiLike,
} from "@/lib/permissions";
import type { ExtensionMessage } from "@/lib/types";

type StartMessage = Extract<ExtensionMessage, { type: "START_CAPTURE_JOB" }>;

export async function dispatchExistingCaptureRetry(
  request: StartMessage,
  sendMessage: (message: StartMessage) => Promise<unknown>,
  onPrepared: (message: StartMessage) => void,
  permissions: PermissionsApiLike = chrome.permissions,
): Promise<unknown> {
  const grants = await requestRetryPermissions(
    new URL(request.configuration.endpoint).origin,
    browserPreferredSourceOrigins(request.captured),
    permissions,
  );
  if (!grants.feGranted) {
    throw new Error(
      `Permission to reach ${grants.feOrigin} was denied; the same durable capture was preserved.`,
    );
  }
  const prepared: StartMessage = {
    ...request,
    permittedSourceOrigins: grants.permittedSourceOrigins,
  };
  // Update the retained request before the runtime boundary so a lost response
  // still retries the same ID with the latest verified permission snapshot.
  onPrepared(prepared);
  return sendMessage(prepared);
}
