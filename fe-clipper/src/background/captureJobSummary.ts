import type { FECaptureAssetVariant } from "@/contracts/fragmentsEngine";
import type { CaptureJobRecord } from "@/lib/jobStorage";
import type { CaptureJobSummary } from "@/lib/types";

export function publicCaptureMessage(message: string): string {
  return message
    .replace(/https?:\/\/[^\s"'<>]+/giu, (value) => {
      try {
        return new URL(value.replace(/[),.;:!?]+$/u, "")).origin;
      } catch {
        return "[redacted URL]";
      }
    })
    .replace(/\b(authorization)\s*(?::|=)\s*(?:bearer|basic)\s+[^\s,;]+/giu, "$1: [redacted]")
    .replace(/\b(bearer|basic)\s+[^\s,;]+/giu, "$1 [redacted]")
    .replace(/\b(cookie|set-cookie)\s*:\s*[^\r\n]+/giu, "$1: [redacted]")
    .replace(
      /\b(access_token|refresh_token|api[_-]?key|token|secret|password|passwd|session(?:_?id)?|sid|cookie)\s*(?:=|:|%3d)\s*[^\s,;&]+/giu,
      "$1=[redacted]",
    );
}

function excludesAutomaticYouTubeVideo(
  provider: string,
  mediaKind: string,
  variant: FECaptureAssetVariant,
): boolean {
  return provider === "youtube" &&
    mediaKind === "video" &&
    !["poster", "subtitles", "transcript"].includes(variant.kind);
}

function sourceOrigin(
  provider: string,
  mediaKind: string,
  variant: FECaptureAssetVariant,
): string | undefined {
  if (excludesAutomaticYouTubeVideo(provider, mediaKind, variant)) return undefined;
  if (variant.transfer_preference !== "browser_preferred" || !variant.source_url) return undefined;
  try {
    const url = new URL(variant.source_url);
    if (
      (url.protocol !== "http:" && url.protocol !== "https:") ||
      url.username ||
      url.password
    ) {
      return undefined;
    }
    return url.origin;
  } catch {
    return undefined;
  }
}

/** Projects durable state through a strict allowlist; never spread a job here. */
export function captureJobSummary(job: CaptureJobRecord): CaptureJobSummary {
  const acceptedState = job.acceptance ?? job.status;
  const transferById = new Map(
    (job.transfers ?? []).map((transfer) => [transfer.clientVariantId, transfer] as const),
  );
  const statusById = new Map(
    (job.status?.assets ?? []).map((outcome) => [outcome.client_variant_id, outcome] as const),
  );
  const variants: CaptureJobSummary["variants"] = [];
  for (const media of job.envelope.media) {
    for (const variant of media.variants) {
      const transfer = transferById.get(variant.client_variant_id);
      const statusOutcome = statusById.get(variant.client_variant_id);
      const statusFailure = statusOutcome?.outcome === "failed" || statusOutcome?.outcome === "not_available"
        ? statusOutcome
        : undefined;
      const origin = sourceOrigin(job.envelope.source.provider, media.kind, variant);
      const statusState = statusOutcome
        ? statusOutcome.outcome === "uploaded" || statusOutcome.outcome === "already_available"
          ? "available" as const
          : statusOutcome.outcome === "failed" || statusOutcome.outcome === "not_available"
            ? "failed" as const
            : variant.transfer_preference === "reference_only"
              ? "reference_only" as const
              : "pending" as const
        : undefined;
      variants.push({
        clientVariantId: variant.client_variant_id,
        mediaKind: media.kind,
        kind: variant.kind,
        state: transfer?.state ?? statusState ?? "pending",
        attempts: transfer?.attempts ?? 0,
        updatedAt: transfer?.updatedAt ?? job.updatedAt,
        ...(transfer?.retryable !== undefined
          ? { retryable: transfer.retryable }
          : statusFailure?.retryable !== undefined
            ? { retryable: statusFailure.retryable }
            : {}),
        ...(transfer?.reason
          ? { reason: publicCaptureMessage(transfer.reason) }
          : statusFailure?.reason
            ? { reason: publicCaptureMessage(statusFailure.reason) }
            : {}),
        ...(origin ? { sourceOrigin: origin } : {}),
      });
    }
  }
  const retryPermissionOrigins = job.terminal ? [] : [
    ...new Set(
      job.envelope.media.flatMap((media) =>
        media.variants.flatMap((variant) => {
          const transfer = transferById.get(variant.client_variant_id);
          const incompleteUpload = transfer
            ? transfer.action === "request_upload" &&
              transfer.state !== "available" &&
              !(transfer.state === "failed" && transfer.retryable === false)
            : true;
          const origin = sourceOrigin(job.envelope.source.provider, media.kind, variant);
          return incompleteUpload && origin ? [origin] : [];
        }),
      ),
    ),
  ].sort();
  return {
    captureId: job.captureId,
    provider: job.envelope.source.provider,
    title: job.envelope.document.title ?? "",
    phase: job.phase,
    terminal: job.terminal,
    accepted: acceptedState !== undefined,
    feOrigin: new URL(job.endpoint).origin,
    createdAt: job.createdAt,
    updatedAt: job.updatedAt,
    ...(acceptedState
      ? {
          fragmentId: acceptedState.fragment_id,
          fragmentRevisionId: acceptedState.fragment_revision_id,
          captureAttemptId: acceptedState.capture_attempt_id,
        }
      : {}),
    ...(job.error
      ? {
          error: {
            stage: job.error.stage,
            message: publicCaptureMessage(job.error.message),
            retryable: job.error.retryable,
            ...(job.error.httpStatus !== undefined ? { httpStatus: job.error.httpStatus } : {}),
            ...(["media_permission", "fe_permission"].includes(job.error.code ?? "")
              ? { code: job.error.code }
              : {}),
          },
        }
      : {}),
    variants,
    retryPermissionOrigins,
  };
}
