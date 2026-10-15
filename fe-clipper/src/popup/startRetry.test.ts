import { describe, expect, it, vi } from "vitest";

import type { CapturedPage, ExtensionMessage } from "@/lib/types";
import type { PermissionsApiLike } from "@/lib/permissions";
import { dispatchExistingCaptureRetry } from "./startRetry";

function request(): Extract<ExtensionMessage, { type: "START_CAPTURE_JOB" }> {
  const captured = {
    adapter: { id: "generic", version: "1", matchConfidence: 100, usedGenericFallback: false },
    identity: {
      submittedUrl: "https://page.test/item",
      canonicalUrl: "https://page.test/item",
      provider: "web",
      segmentKey: "root",
    },
    document: {
      title: "Capture",
      description: "",
      contentMarkdown: "Body",
      selection: "",
      usedFallback: false,
    },
    media: [{
      candidateId: "image-1",
      kind: "image",
      role: "hero",
      position: 0,
      variants: [{
        variantId: "original",
        kind: "original",
        sourceUrl: "https://media.test/signed?token=private",
        acquisitionPreference: "browser_preferred",
      }],
    }],
    extensions: [],
    capabilities: { declared: [], observed: [] },
    warnings: [],
  } satisfies CapturedPage;
  return {
    type: "START_CAPTURE_JOB",
    captureId: "same-capture-id",
    captured,
    edits: { title: "Edited", tags: ["link"], note: "note" },
    configuration: { endpoint: "https://fe.test/v1/captures", principalId: "local-user" },
    permittedSourceOrigins: ["https://media.test"],
  };
}

function permissionApi(grant: readonly string[]): PermissionsApiLike & {
  contains: ReturnType<typeof vi.fn>;
  request: ReturnType<typeof vi.fn>;
} {
  const held = new Set<string>();
  return {
    contains: vi.fn(async ({ origins }) =>
      (origins as string[] | undefined)?.every((origin: string) => held.has(origin)) ?? true),
    request: vi.fn(async ({ origins }) => {
      for (const origin of (origins ?? []) as string[]) if (grant.includes(origin)) held.add(origin);
      return false;
    }),
  };
}

describe("lost-response form retry", () => {
  it("rechecks revoked FE/media grants once and resends the unchanged capture ID and content", async () => {
    const original = request();
    const api = permissionApi(["https://fe.test/*", "https://media.test/*"]);
    const send = vi.fn(async () => ({ ok: true }));
    const prepared = vi.fn();

    await dispatchExistingCaptureRetry(original, send, prepared, api);

    expect(api.request).toHaveBeenCalledOnce();
    expect(api.request).toHaveBeenCalledWith({
      origins: ["https://fe.test/*", "https://media.test/*"],
    });
    expect(prepared).toHaveBeenCalledOnce();
    const updated = prepared.mock.calls[0]![0];
    expect(updated).toEqual({ ...original, permittedSourceOrigins: ["https://media.test"] });
    expect(updated.captureId).toBe("same-capture-id");
    expect(updated.captured).toEqual(original.captured);
    expect(updated.edits).toEqual(original.edits);
    expect(send).toHaveBeenCalledWith(updated);
  });

  it("does not send any background message when FE permission remains denied", async () => {
    const api = permissionApi(["https://media.test/*"]);
    const send = vi.fn();
    const prepared = vi.fn();

    await expect(dispatchExistingCaptureRetry(request(), send, prepared, api)).rejects.toThrow(
      /Permission to reach https:\/\/fe.test was denied/,
    );

    expect(api.request).toHaveBeenCalledOnce();
    expect(send).not.toHaveBeenCalled();
    expect(prepared).not.toHaveBeenCalled();
  });
});
