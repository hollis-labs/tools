import { afterEach, describe, expect, it, vi } from "vitest";

import {
  ACCEPTED_AUTO_CLOSE_MS,
  acceptedCaptureNotice,
  scheduleAcceptedPopupClose,
  shouldAutoCloseFreshCapture,
} from "./lifecycle";

afterEach(() => vi.useRealTimers());

describe("accepted popup lifecycle", () => {
  it("leaves accepted success visible briefly, then closes without waiting for media", () => {
    vi.useFakeTimers();
    const close = vi.fn();
    scheduleAcceptedPopupClose(close);

    vi.advanceTimersByTime(ACCEPTED_AUTO_CLOSE_MS - 1);
    expect(close).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(close).toHaveBeenCalledOnce();
  });

  it("auto-closes clean fresh acceptance while keeping partial and warning states open", () => {
    expect(shouldAutoCloseFreshCapture({ phase: "accepted" })).toBe(true);
    expect(shouldAutoCloseFreshCapture({ phase: "pending_transfer" })).toBe(true);
    expect(shouldAutoCloseFreshCapture({ phase: "complete" })).toBe(true);
    expect(shouldAutoCloseFreshCapture({ phase: "accepted", warning: "needs attention" })).toBe(false);
    expect(shouldAutoCloseFreshCapture({ phase: "pending_transfer", warning: "needs attention" })).toBe(false);
    expect(shouldAutoCloseFreshCapture({ phase: "partial" })).toBe(false);
  });

  it("makes recovered acceptance warnings actionable and copyable for fresh and retry paths", () => {
    const fresh = acceptedCaptureNotice(
      { phase: "accepted", warning: "local status write failed" },
      { fresh: true },
    );
    const retry = acceptedCaptureNotice(
      { phase: "pending_transfer", warning: "reconciliation is unknown" },
      { fresh: false, mediaPermissionDenied: true },
    );

    expect(fresh).toMatchObject({ kind: "error", autoClose: false });
    expect(fresh.message).toContain("Manifest accepted");
    expect(fresh.message).toContain("Attention needed");
    expect(retry).toMatchObject({ kind: "error", autoClose: false });
    expect(retry.message).toContain("accepted manifest and IDs remain safe");
    expect(retry.message).toContain("reconciliation is unknown");
  });
});
