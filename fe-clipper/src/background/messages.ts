import type { CaptureJobCoordinator } from "./captureJobs";
import { captureJobSummary, publicCaptureMessage } from "./captureJobSummary";
import type { CapturedPage, ExtensionMessage } from "@/lib/types";

type CaptureRequest = Extract<
  ExtensionMessage,
  { type: "START_CAPTURE_JOB" | "RETRY_CAPTURE_JOB" | "QUERY_CAPTURE_JOBS" | "DISMISS_CAPTURE_JOB" }
>;
type CaptureRequestType = CaptureRequest["type"];

export interface CaptureJobController {
  start: CaptureJobCoordinator["start"];
  retry: CaptureJobCoordinator["retry"];
  list: CaptureJobCoordinator["list"];
  dismiss: CaptureJobCoordinator["dismiss"];
}

interface RuntimeBoundary {
  id: string;
  getURL(path: string): string;
  onMessage: {
    addListener(
      callback: (
        message: unknown,
        sender: chrome.runtime.MessageSender,
        sendResponse: (response: ExtensionMessage) => void,
      ) => boolean | undefined,
    ): void;
  };
}

function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function actionIdentifier(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0 && value.length <= 255;
}

function nonemptyString(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function stringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === "string");
}

function canonicalOrigin(value: string): boolean {
  try {
    const url = new URL(value);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      !url.username &&
      !url.password &&
      url.origin === value
    );
  } catch {
    return false;
  }
}

function originArray(value: unknown): value is string[] {
  return (
    stringArray(value) &&
    value.every(canonicalOrigin) &&
    new Set(value).size === value.length
  );
}

function optionalString(value: unknown): boolean {
  return value === undefined || typeof value === "string";
}

function optionalPositiveInteger(value: unknown): boolean {
  return value === undefined || (Number.isSafeInteger(value) && (value as number) > 0);
}

function optionalNonnegativeNumber(value: unknown): boolean {
  return value === undefined || (typeof value === "number" && Number.isFinite(value) && value >= 0);
}

function parseableUrl(value: unknown): value is string {
  if (!nonemptyString(value)) return false;
  try {
    new URL(value);
    return true;
  } catch {
    return false;
  }
}

function capturedPage(value: unknown): value is CapturedPage {
  if (!record(value)) return false;
  const adapter = value.adapter;
  const identity = value.identity;
  const document = value.document;
  const capabilities = value.capabilities;
  if (!record(adapter) || !record(identity) || !record(document) || !record(capabilities)) return false;
  if (
    !nonemptyString(adapter.id) ||
    !nonemptyString(adapter.version) ||
    ![0, 25, 75, 100].includes(adapter.matchConfidence as number) ||
    typeof adapter.usedGenericFallback !== "boolean" ||
    !parseableUrl(identity.submittedUrl) ||
    !parseableUrl(identity.canonicalUrl) ||
    !nonemptyString(identity.provider) ||
    !nonemptyString(identity.segmentKey) ||
    !optionalString(identity.providerItemId) ||
    typeof document.title !== "string" ||
    typeof document.description !== "string" ||
    typeof document.contentMarkdown !== "string" ||
    typeof document.selection !== "string" ||
    !optionalString(document.language) ||
    typeof document.usedFallback !== "boolean" ||
    !stringArray(capabilities.declared) ||
    !stringArray(capabilities.observed) ||
    !Array.isArray(value.media) ||
    !Array.isArray(value.extensions) ||
    !Array.isArray(value.warnings)
  ) {
    return false;
  }
  for (const media of value.media) {
    if (
      !record(media) ||
      !nonemptyString(media.candidateId) ||
      !["image", "video", "audio", "document", "timed_text", "other"].includes(media.kind as string) ||
      !["primary", "gallery_item", "hero", "inline", "poster", "transcript", "other"].includes(media.role as string) ||
      !Number.isSafeInteger(media.position) ||
      (media.position as number) < 0 ||
      !optionalString(media.providerMediaId) ||
      !optionalString(media.altText) ||
      !optionalString(media.caption) ||
      !optionalString(media.sourceContext) ||
      !Array.isArray(media.variants)
    ) {
      return false;
    }
    for (const variant of media.variants) {
      if (
        !record(variant) ||
        !nonemptyString(variant.variantId) ||
        !["original", "preview", "thumbnail", "poster", "stream_reference", "audio", "subtitles", "transcript", "other"].includes(variant.kind as string) ||
        !parseableUrl(variant.sourceUrl) ||
        !["browser_preferred", "reference_only"].includes(variant.acquisitionPreference as string) ||
        !optionalString(variant.mimeType) ||
        !optionalPositiveInteger(variant.width) ||
        !optionalPositiveInteger(variant.height) ||
        !optionalNonnegativeNumber(variant.durationSeconds) ||
        !optionalPositiveInteger(variant.byteSize) ||
        (variant.expiresAt !== undefined &&
          (typeof variant.expiresAt !== "string" || Number.isNaN(Date.parse(variant.expiresAt))))
      ) {
        return false;
      }
    }
  }
  return value.extensions.every(
    (extension) =>
      record(extension) &&
      typeof extension.namespace === "string" &&
      extension.namespace.startsWith("provider.") &&
      nonemptyString(extension.schemaVersion) &&
      record(extension.data),
  ) && value.warnings.every(
    (warning) =>
      record(warning) &&
      nonemptyString(warning.code) &&
      ["info", "warning", "error"].includes(warning.severity as string) &&
      ["matching", "identity", "document", "media", "provider_metadata", "capture"].includes(warning.stage as string) &&
      typeof warning.message === "string",
  );
}

function requestType(message: unknown): CaptureRequestType | undefined {
  if (!record(message) || typeof message.type !== "string") return undefined;
  return ["START_CAPTURE_JOB", "RETRY_CAPTURE_JOB", "QUERY_CAPTURE_JOBS", "DISMISS_CAPTURE_JOB"]
    .includes(message.type)
    ? message.type as CaptureRequestType
    : undefined;
}

function validateRequest(message: unknown): CaptureRequest {
  const type = requestType(message);
  if (!type || !record(message)) throw new Error("Invalid capture-control message.");
  if (type === "QUERY_CAPTURE_JOBS") return { type };
  if (!actionIdentifier(message.captureId)) throw new Error("Capture action ID must contain 1–255 characters.");
  if (type === "DISMISS_CAPTURE_JOB") return { type, captureId: message.captureId };
  if (type === "RETRY_CAPTURE_JOB") {
    if (message.permittedSourceOrigins !== undefined && !originArray(message.permittedSourceOrigins)) {
      throw new Error("Retry permission origins must be unique credential-free HTTP(S) origins.");
    }
    return {
      type,
      captureId: message.captureId,
      ...(message.permittedSourceOrigins
        ? { permittedSourceOrigins: message.permittedSourceOrigins }
        : {}),
    };
  }
  if (
    !capturedPage(message.captured) ||
    !record(message.edits) ||
    typeof message.edits.title !== "string" ||
    !stringArray(message.edits.tags) ||
    typeof message.edits.note !== "string" ||
    !record(message.configuration) ||
    !nonemptyString(message.configuration.endpoint) ||
    !actionIdentifier(message.configuration.principalId) ||
    !originArray(message.permittedSourceOrigins)
  ) {
    throw new Error("The start-capture message is malformed.");
  }
  const endpoint = new URL(message.configuration.endpoint);
  if (
    (endpoint.protocol !== "http:" && endpoint.protocol !== "https:") ||
    endpoint.username ||
    endpoint.password
  ) {
    throw new Error("The start-capture endpoint must be a credential-free HTTP(S) URL.");
  }
  return message as unknown as CaptureRequest;
}

export function trustedPopupSender(
  sender: chrome.runtime.MessageSender,
  runtime: Pick<RuntimeBoundary, "id" | "getURL">,
): boolean {
  return (
    sender.id === runtime.id &&
    sender.tab === undefined &&
    sender.url === runtime.getURL("popup.html")
  );
}

function rejected(type: CaptureRequestType, captureId: string, error: string): ExtensionMessage {
  const safeError = publicCaptureMessage(error);
  if (type === "QUERY_CAPTURE_JOBS") {
    return { type: "CAPTURE_JOBS_RESULT", ok: false, error: safeError };
  }
  if (type === "DISMISS_CAPTURE_JOB") {
    return { type: "CAPTURE_JOB_DISMISS_RESULT", ok: false, captureId, error: safeError };
  }
  return {
    type: "CAPTURE_JOB_RESULT",
    ok: false,
    accepted: false,
    ...(captureId ? { captureId } : {}),
    error: safeError,
  };
}

export function jobResult(
  job: Awaited<ReturnType<CaptureJobController["start"]>>,
): ExtensionMessage {
  const acceptedState = job.acceptance ?? job.status;
  if (acceptedState) {
    return {
      type: "CAPTURE_JOB_RESULT",
      ok: true,
      accepted: true,
      captureId: job.captureId,
      phase: job.phase,
      terminal: job.terminal,
      fragmentId: acceptedState.fragment_id,
      ...(job.error ? { warning: publicCaptureMessage(job.error.message) } : {}),
    };
  }
  return {
    type: "CAPTURE_JOB_RESULT",
    ok: false,
    accepted: false,
    captureId: job.captureId,
    phase: job.phase,
    terminal: job.terminal,
    error: publicCaptureMessage(
      job.error?.message ?? "The capture job stopped before Fragments Engine accepted it.",
    ),
  };
}

export async function handleCaptureMessage(
  message: CaptureRequest,
  controller: CaptureJobController,
): Promise<ExtensionMessage> {
  try {
    switch (message.type) {
      case "START_CAPTURE_JOB":
        return jobResult(await controller.start(message));
      case "RETRY_CAPTURE_JOB":
        return jobResult(
          await controller.retry(message.captureId, message.permittedSourceOrigins ?? []),
        );
      case "QUERY_CAPTURE_JOBS": {
        const jobs = await controller.list();
        return {
          type: "CAPTURE_JOBS_RESULT",
          ok: true,
          jobs: jobs
            .map(captureJobSummary)
            .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt)),
        };
      }
      case "DISMISS_CAPTURE_JOB": {
        const removed = await controller.dismiss(message.captureId);
        if (!removed) return rejected(message.type, message.captureId, `Capture ${message.captureId} was not found.`);
        return { type: "CAPTURE_JOB_DISMISS_RESULT", ok: true, captureId: message.captureId };
      }
    }
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    if (message.type === "START_CAPTURE_JOB" || message.type === "RETRY_CAPTURE_JOB") {
      try {
        const recovered = (await controller.list()).find((job) => job.captureId === message.captureId);
        if (recovered?.acceptance || recovered?.status) {
          const result = jobResult(recovered);
          if (result.type === "CAPTURE_JOB_RESULT" && result.ok) {
            return {
              ...result,
              warning: publicCaptureMessage(
                `The accepted capture is preserved, but a local status update failed (${detail}). Retry this same capture ID to reconcile.`,
              ),
            };
          }
        }
      } catch {
        // The recovery read is best effort; the caller still receives the
        // original capture ID and explicit unknown-outcome guidance below.
      }
      return rejected(
        message.type,
        message.captureId,
        `The capture outcome is unknown after a local update failed (${detail}). The durable job was preserved when possible. Retry this same capture ID to reconcile before creating another capture.`,
      );
    }
    return rejected(
      message.type,
      "captureId" in message ? message.captureId : "",
      detail,
    );
  }
}

export function registerCaptureMessages(
  runtime: RuntimeBoundary,
  controller: CaptureJobController,
): void {
  runtime.onMessage.addListener((rawMessage, sender, sendResponse) => {
    const type = requestType(rawMessage);
    if (!type) return undefined;
    const rawCaptureId = record(rawMessage) && actionIdentifier(rawMessage.captureId)
      ? rawMessage.captureId
      : "";
    if (!trustedPopupSender(sender, runtime)) {
      sendResponse(rejected(type, rawCaptureId, "Capture controls are available only to the extension popup."));
      return false;
    }
    let message: CaptureRequest;
    try {
      message = validateRequest(rawMessage);
    } catch (error) {
      sendResponse(rejected(type, rawCaptureId, error instanceof Error ? error.message : String(error)));
      return false;
    }
    void handleCaptureMessage(message, controller).then(sendResponse);
    return true;
  });
}
