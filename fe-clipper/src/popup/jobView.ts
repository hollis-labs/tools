import type { CaptureJobSummary } from "@/lib/types";

export interface JobPresentation {
  stateLabel: string;
  detail: string;
  tone: "working" | "success" | "warning" | "error";
  canRetry: boolean;
  canDismiss: boolean;
  ariaLabel: string;
}

const ACTIVE_PHASES: Record<string, string> = {
  pending_manifest: "Saved locally",
  preflighting: "Checking compatibility",
  reconciling: "Checking Fragments Engine status",
  submitting_manifest: "Sending manifest",
  accepted: "Manifest accepted",
  pending_transfer: "Media pending",
  completing: "Finalizing capture",
};

export function variantStateLabel(variant: CaptureJobSummary["variants"][number]): string {
  const state = variant.state === "reference_only" ? "reference only" : variant.state;
  return `${variant.mediaKind} ${variant.kind}: ${state}`;
}

export function presentCaptureJob(job: CaptureJobSummary): JobPresentation {
  let stateLabel = ACTIVE_PHASES[job.phase] ?? "Capture failed";
  let detail = "The durable job can be reopened safely.";
  let tone: JobPresentation["tone"] = "working";

  if (job.phase === "complete") {
    stateLabel = "Complete";
    detail = "Fragments Engine confirmed the capture and all final outcomes.";
    tone = "success";
  } else if (job.phase === "partial") {
    stateLabel = "Partially complete";
    detail = "Fragments Engine confirmed the capture with one or more unavailable assets.";
    tone = "warning";
  } else if (job.terminal) {
    stateLabel = "Failed";
    detail = job.error?.message ?? (job.accepted
      ? "Fragments Engine confirmed that the accepted capture could not be completed."
      : "The capture ended before it could be accepted.");
    tone = "error";
  } else if (job.error) {
    if (job.error.code === "media_permission" || job.error.code === "fe_permission") {
      stateLabel = job.accepted
        ? job.error.code === "fe_permission"
          ? "Accepted · FE access needed"
          : "Accepted · media access needed"
        : "Permission needed";
      detail = job.error.code === "fe_permission"
        ? "Retry to grant this capture's Fragments Engine origin and reconcile the same capture ID."
        : "Retry to grant only the listed media origins and continue this capture.";
    } else if (job.error.stage === "preflight" && !job.error.retryable) {
      stateLabel = "Compatibility issue";
      detail = "Update Fragments Engine or the extension, then retry this same capture.";
    } else if (job.error.retryable) {
      stateLabel = job.accepted ? "Accepted · retry needed" : "Retry needed";
      detail = "The request outcome is unknown or temporary. Retry will reconcile this capture ID first.";
    } else {
      stateLabel = job.accepted ? "Accepted · action needed" : "Capture needs attention";
      detail = job.error.message;
    }
    tone = "error";
  } else if (job.accepted) {
    detail = job.phase === "pending_transfer"
      ? "The manifest is safe in Fragments Engine; media continues independently."
      : "The manifest is safe in Fragments Engine while final work continues.";
  }

  const ariaLabel = `${job.title || "Untitled capture"}, ${job.provider}, ${stateLabel}. ${detail}`;
  return {
    stateLabel,
    detail,
    tone,
    canRetry: !job.terminal,
    canDismiss: job.terminal,
    ariaLabel,
  };
}
