import { describe, expect, it } from "vitest";

import type { CapturedPage, ExtensionMessage } from "@/lib/types";
import { CaptureActionState, CaptureMutationGuard } from "./captureActionState";

function request(captureId: string): Extract<ExtensionMessage, { type: "START_CAPTURE_JOB" }> {
  return {
    type: "START_CAPTURE_JOB",
    captureId,
    captured: {} as CapturedPage,
    edits: { title: "", tags: [], note: "" },
    configuration: { endpoint: "https://fe.test", principalId: "local-user" },
    permittedSourceOrigins: [],
  };
}

describe("fresh capture action identity", () => {
  it("retains nonterminal unknown outcomes but clears terminal conflicts for deliberate recapture", () => {
    const state = new CaptureActionState();
    state.begin(request("first-id"));
    state.applyResult({
      type: "CAPTURE_JOB_RESULT",
      ok: false,
      accepted: false,
      captureId: "first-id",
      terminal: false,
      error: "outcome unknown",
    });
    expect(state.activeJobId).toBe("first-id");

    state.applyResult({
      type: "CAPTURE_JOB_RESULT",
      ok: true,
      accepted: true,
      captureId: "another-job",
      phase: "accepted",
      terminal: false,
      fragmentId: "fragment-2",
    });
    expect(state.activeJobId).toBe("first-id");

    state.applyResult({
      type: "CAPTURE_JOB_RESULT",
      ok: false,
      accepted: false,
      captureId: "first-id",
      phase: "failed",
      terminal: true,
      error: "conflict",
    });
    expect(state.activeStartRequest).toBeUndefined();
    state.begin(request("second-id"));
    expect(state.activeJobId).toBe("second-id");
  });

  it("clears a lost-response request when polling or dismissal reveals terminal state", () => {
    const state = new CaptureActionState();
    state.begin(request("lost-response"));
    expect(state.sync([{
      captureId: "lost-response",
      provider: "web",
      title: "Capture",
      phase: "complete",
      terminal: true,
      accepted: true,
      feOrigin: "https://fe.test",
      createdAt: "2026-09-03T20:00:00.000Z",
      updatedAt: "2026-09-03T20:01:00.000Z",
      variants: [],
      retryPermissionOrigins: [],
    }])).toBe(true);
    expect(state.activeJobId).toBeUndefined();

    state.begin(request("dismissed"));
    expect(state.clearMatching("dismissed")).toBe(true);
    expect(state.activeJobId).toBeUndefined();
  });

  it("serializes form and card mutations while leaving copy available", () => {
    const guard = new CaptureMutationGuard();
    expect(guard.begin()).toBe(true);
    expect(guard.begin()).toBe(false);
    expect(guard.allows("send")).toBe(false);
    expect(guard.allows("retry")).toBe(false);
    expect(guard.allows("dismiss")).toBe(false);
    expect(guard.allows("copy")).toBe(true);
    guard.end();
    expect(guard.allows("send")).toBe(true);
  });
});
