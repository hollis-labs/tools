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
  /** Freeform context the user typed in the popup before sending. Prepended
   * to the wire payload's content as a labeled blockquote (see
   * lib/payload.ts's prependNote) rather than sent as its own field, so it
   * reaches FE's ingest system without requiring a new /v1/intake field. */
  note: string;
}

export const DEFAULT_TAGS = ["link"] as const;

/** Messages exchanged between popup and the background service worker. */
export type ExtensionMessage =
  | { type: "SEND_CLIP"; payload: ClipPayload; endpoint: string }
  | { type: "SEND_CLIP_RESULT"; ok: true; status: number; body: unknown }
  | { type: "SEND_CLIP_RESULT"; ok: false; error: string };

export interface FEIntakeResult {
  fragment_id: string;
  outcome: string;
  status: string;
  link_url?: string;
}
