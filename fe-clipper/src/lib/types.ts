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
      /** A later retry UI may add grants obtained from that retry's gesture. */
      permittedSourceOrigins?: PermittedSourceOrigins;
    }
  | {
      type: "CAPTURE_JOB_RESULT";
      ok: true;
      accepted: true;
      captureId: string;
      phase: CaptureJobPhase;
      fragmentId: string;
      warning?: string;
    }
  | {
      type: "CAPTURE_JOB_RESULT";
      ok: false;
      accepted: false;
      captureId?: string;
      phase?: CaptureJobPhase;
      error: string;
    };

export interface FEIntakeResult {
  fragment_id: string;
  outcome: string;
  status: string;
  link_url?: string;
}
