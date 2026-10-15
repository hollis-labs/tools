import { describe, expect, it } from "vitest";

import type { CaptureJobSummary } from "@/lib/types";
import { presentCaptureJob, variantStateLabel } from "./jobView";

export function summary(overrides: Partial<CaptureJobSummary> = {}): CaptureJobSummary {
  return {
    captureId: "capture-1",
    provider: "instagram",
    title: "A capture",
    phase: "pending_transfer",
    terminal: false,
    accepted: true,
    feOrigin: "https://fe.test",
    createdAt: "2026-09-03T20:00:00.000Z",
    updatedAt: "2026-09-03T20:01:00.000Z",
    variants: [],
    retryPermissionOrigins: ["https://media.test"],
    ...overrides,
  };
}

describe("capture job presentation", () => {
  it("reports manifest acceptance independently from media work with an accessible label", () => {
    const presented = presentCaptureJob(summary());
    expect(presented).toMatchObject({
      stateLabel: "Media pending",
      tone: "working",
      canRetry: true,
      canDismiss: false,
    });
    expect(presented.detail).toContain("manifest is safe");
    expect(presented.ariaLabel).toContain("A capture, instagram, Media pending");
  });

  it("distinguishes permission, media network, and pre-acceptance FE network failures", () => {
    const permission = presentCaptureJob(summary({ error: {
      stage: "transfer", message: "permission denied", retryable: true, code: "media_permission",
    } }));
    const mediaNetwork = presentCaptureJob(summary({ error: {
      stage: "transfer", message: "source timeout", retryable: true, code: "media_network",
    } }));
    const feNetwork = presentCaptureJob(summary({
      accepted: false,
      phase: "submitting_manifest",
      error: { stage: "manifest", message: "FE timeout", retryable: true },
    }));
    const fePermission = presentCaptureJob(summary({ error: {
      stage: "preflight", message: "FE permission revoked", retryable: true, code: "fe_permission",
    } }));

    expect(permission.stateLabel).toBe("Accepted · media access needed");
    expect(mediaNetwork.stateLabel).toBe("Accepted · retry needed");
    expect(feNetwork.stateLabel).toBe("Retry needed");
    expect(fePermission.stateLabel).toBe("Accepted · FE access needed");
  });

  it("describes accepted terminal failure separately from pre-acceptance conflict", () => {
    const acceptedFailure = presentCaptureJob(summary({
      phase: "failed", terminal: true, accepted: true, retryPermissionOrigins: [],
    }));
    const conflict = presentCaptureJob(summary({
      phase: "failed", terminal: true, accepted: false, retryPermissionOrigins: [],
    }));

    expect(acceptedFailure.detail).toContain("accepted capture could not be completed");
    expect(conflict.detail).toContain("before it could be accepted");
    expect(acceptedFailure.canDismiss).toBe(true);
    expect(acceptedFailure.canRetry).toBe(false);
  });

  it("makes FE-confirmed partial state and per-variant state explicit", () => {
    const presented = presentCaptureJob(summary({
      phase: "partial", terminal: true, retryPermissionOrigins: [],
    }));
    expect(presented).toMatchObject({ stateLabel: "Partially complete", tone: "warning" });
    expect(variantStateLabel({
      clientVariantId: "poster",
      mediaKind: "video",
      kind: "poster",
      state: "reference_only",
      attempts: 0,
      updatedAt: "2026-09-03T20:00:00.000Z",
    })).toBe("video poster: reference only");
  });
});
