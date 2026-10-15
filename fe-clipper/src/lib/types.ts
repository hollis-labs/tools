// Shared types for the fe-clipper extension. Kept dependency-free so they
// can be imported from content-script, background, popup, and unit tests
// without pulling in any runtime code.

import type { PageCaptureResult } from "@/capture/types";

/** Serializable adapter result captured by the content script. */
export type CapturedPage = PageCaptureResult;

/** Exact wire shape POSTed to FE's /v1/intake endpoint. */
export interface ClipPayload {
  content: string;
  title: string;
  source_url: string;
  description: string;
  selection: string;
  tags: string[];
}

/** User-editable fields surfaced in the popup before sending. */
export interface EditableFields {
  title: string;
  tags: string[];
  /** Freeform context the user typed in the popup before sending. The v1
   * capture flow sends this as an append-only capture_note annotation. */
  note: string;
}

export const DEFAULT_TAGS = ["link"] as const;

export interface CaptureConfigurationSnapshot {
  endpoint: string;
  principalId: string;
}

/** HTTP(S) source origins for which the popup verified host permission during
 * the explicit Send gesture. The background treats these as an allowlist, not
 * as evidence that it may request more permissions later. */
export type PermittedSourceOrigins = readonly string[];

export type CaptureJobPhase =
  | "pending_manifest"
  | "preflighting"
  | "reconciling"
  | "submitting_manifest"
  | "accepted"
  | "pending_transfer"
  | "completing"
  | "complete"
  | "partial"
  | "failed";

export type CaptureVariantStatus =
  | "pending"
  | "transferring"
  | "available"
  | "reference_only"
  | "failed";

/** Explicitly allowlisted, redacted capture status exposed to extension pages. */
export interface CaptureJobSummary {
  captureId: string;
  provider: string;
  title: string;
  phase: CaptureJobPhase;
  terminal: boolean;
  accepted: boolean;
  feOrigin: string;
  createdAt: string;
  updatedAt: string;
  fragmentId?: string;
  fragmentRevisionId?: string;
  captureAttemptId?: string;
  error?: {
    stage: "preflight" | "reconcile" | "manifest" | "transfer" | "completion";
    message: string;
    retryable: boolean;
    httpStatus?: number;
    code?: string;
  };
  variants: Array<{
    clientVariantId: string;
    mediaKind: string;
    kind: string;
    state: CaptureVariantStatus;
    attempts: number;
    updatedAt: string;
    retryable?: boolean;
    reason?: string;
    sourceOrigin?: string;
  }>;
  /** Exact envelope-bound media origins needed by incomplete retry work. */
  retryPermissionOrigins: string[];
}

/** Messages exchanged between popup and the background service worker. */
export type ExtensionMessage =
  | {
      type: "START_CAPTURE_JOB";
      captureId: string;
      captured: CapturedPage;
      edits: EditableFields;
      configuration: CaptureConfigurationSnapshot;
      permittedSourceOrigins: PermittedSourceOrigins;
    }
  | {
      type: "RETRY_CAPTURE_JOB";
      captureId: string;
      /** Grants obtained from this retry's explicit popup gesture. */
      permittedSourceOrigins?: PermittedSourceOrigins;
    }
  | { type: "QUERY_CAPTURE_JOBS" }
  | { type: "DISMISS_CAPTURE_JOB"; captureId: string }
  | {
      type: "CAPTURE_JOB_RESULT";
      ok: true;
      accepted: true;
      captureId: string;
      phase: CaptureJobPhase;
      terminal: boolean;
      fragmentId: string;
      warning?: string;
    }
  | {
      type: "CAPTURE_JOB_RESULT";
      ok: false;
      accepted: false;
      captureId?: string;
      phase?: CaptureJobPhase;
      terminal?: boolean;
      error: string;
    }
  | { type: "CAPTURE_JOBS_RESULT"; ok: true; jobs: CaptureJobSummary[] }
  | { type: "CAPTURE_JOBS_RESULT"; ok: false; error: string }
  | { type: "CAPTURE_JOB_DISMISS_RESULT"; ok: true; captureId: string }
  | { type: "CAPTURE_JOB_DISMISS_RESULT"; ok: false; captureId: string; error: string };

export interface FEIntakeResult {
  fragment_id: string;
  outcome: string;
  status: string;
  link_url?: string;
}
