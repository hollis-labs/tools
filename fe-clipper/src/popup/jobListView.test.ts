import { afterEach, describe, expect, it, vi } from "vitest";

import type { CaptureJobSummary } from "@/lib/types";
import { CaptureJobListRenderer } from "./jobListView";

function summary(overrides: Partial<CaptureJobSummary> = {}): CaptureJobSummary {
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

afterEach(() => {
  vi.useRealTimers();
  document.body.replaceChildren();
});

function renderer() {
  const section = document.createElement("section");
  const list = document.createElement("div");
  section.append(list);
  document.body.append(section);
  return { section, list, renderer: new CaptureJobListRenderer(section, list) };
}

describe("durable capture job list", () => {
  it("does not replace or re-announce unchanged polling state and preserves focus", () => {
    vi.useFakeTimers();
    const view = renderer();
    const jobs = [summary()];
    expect(view.renderer.render(jobs)).toBe(true);
    const retry = view.list.querySelector<HTMLButtonElement>("button[data-action=retry]")!;
    retry.focus();
    const initialStatus = view.list.querySelector("[role=status]");
    const poll = window.setInterval(() => view.renderer.render(jobs), 1500);

    vi.advanceTimersByTime(4500);

    expect(document.activeElement).toBe(retry);
    expect(view.list.querySelector("[role=status]")).toBe(initialStatus);
    expect(view.list.hasAttribute("aria-live")).toBe(false);
    window.clearInterval(poll);
  });

  it("restores focus across meaningful state changes and globally guards mutating actions", () => {
    const view = renderer();
    const first = summary({ captureId: "first" });
    const second = summary({ captureId: "second" });
    view.renderer.render([first, second]);
    const firstRetry = view.list.querySelector<HTMLButtonElement>("button[data-capture-id=first]")!;
    firstRetry.focus();

    view.renderer.render([{ ...first, updatedAt: "2026-09-03T20:02:00.000Z" }, second], true);

    expect((document.activeElement as HTMLElement).dataset.captureId).toBe("first");
    const mutationButtons = [...view.list.querySelectorAll<HTMLButtonElement>(
      "button[data-action=retry], button[data-action=dismiss]",
    )];
    expect(mutationButtons.every((button) => button.getAttribute("aria-disabled") === "true")).toBe(true);
  });

  it("shows sanitized failed-variant reasons even after terminal partial clears job error", () => {
    const view = renderer();
    const partial = summary({
      phase: "partial",
      terminal: true,
      retryPermissionOrigins: [],
      variants: [
        {
          clientVariantId: "poster",
          mediaKind: "image",
          kind: "poster",
          state: "failed",
          attempts: 1,
          updatedAt: "2026-09-03T20:01:00.000Z",
          retryable: false,
          reason: "Source returned HTTP 404.",
          sourceOrigin: "https://media.test",
        },
        {
          clientVariantId: "video",
          mediaKind: "video",
          kind: "primary",
          state: "failed",
          attempts: 1,
          updatedAt: "2026-09-03T20:01:00.000Z",
          retryable: true,
          reason: "Source temporarily unavailable.",
          sourceOrigin: "https://media.test",
        },
      ],
    });

    view.renderer.render([partial]);

    expect(view.list.textContent).toContain("Partially complete");
    expect(view.list.textContent).toContain("Source returned HTTP 404. (not retryable)");
    expect(view.list.textContent).toContain("Source temporarily unavailable. (retryable)");
    expect(view.list.textContent).toContain("image poster: failed");
    expect(view.list.querySelector("button[data-action=dismiss]")).not.toBeNull();
    expect(view.list.querySelector("button[data-action=retry]")).toBeNull();
  });
});
