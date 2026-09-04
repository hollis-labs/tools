import { describe, expect, it, vi } from "vitest";

import type { PageCaptureResult } from "@/capture/types";
import {
  browserPreferredSourceOrigins,
  requestCapturePermissions,
  requestRetryPermissions,
  type PermissionsApiLike,
} from "./permissions";

function captured(urls: Array<{ url: string; preference?: "browser_preferred" | "reference_only" }>): PageCaptureResult {
  return {
    adapter: { id: "test", version: "1", matchConfidence: 100, usedGenericFallback: false },
    identity: {
      submittedUrl: "https://page.test/item",
      canonicalUrl: "https://page.test/item",
      provider: "web",
      segmentKey: "root",
    },
    document: {
      title: "Item",
      description: "",
      contentMarkdown: "",
      selection: "",
      usedFallback: false,
    },
    media: urls.map(({ url, preference }, index) => ({
      candidateId: `media-${index}`,
      kind: "image",
      role: "inline",
      position: index,
      variants: [{
        variantId: `variant-${index}`,
        kind: "original",
        sourceUrl: url,
        acquisitionPreference: preference ?? "browser_preferred",
      }],
    })),
    extensions: [],
    capabilities: { declared: [], observed: [] },
    warnings: [],
  };
}

function permissionApi(
  initiallyGranted: readonly string[],
  grantOnRequest: readonly string[],
): PermissionsApiLike & { request: ReturnType<typeof vi.fn>; contains: ReturnType<typeof vi.fn> } {
  const granted = new Set(initiallyGranted);
  return {
    contains: vi.fn(async ({ origins }) => origins?.every((origin: string) => granted.has(origin)) ?? true),
    request: vi.fn(async ({ origins }) => {
      for (const origin of (origins ?? []) as string[]) {
        if (grantOnRequest.includes(origin)) granted.add(origin);
      }
      return ((origins ?? []) as string[]).every((origin: string) => granted.has(origin));
    }),
  };
}

describe("capture host permission flow", () => {
  it("derives only exact credential-free browser-preferred source origins", () => {
    expect(browserPreferredSourceOrigins(captured([
      { url: "https://cdn.example/a.jpg" },
      { url: "https://cdn.example/b.jpg" },
      { url: "https://reference.example/video", preference: "reference_only" },
      { url: "https://user:secret@private.example/image.jpg" },
      { url: "data:image/png;base64,AA==" },
    ]))).toEqual(["https://cdn.example"]);
  });

  it("checks grants and makes at most one request for the complete missing set", async () => {
    const api = permissionApi(
      ["https://fe.test/*"],
      ["https://a.test/*", "https://b.test/*"],
    );
    const result = await requestCapturePermissions(
      "https://fe.test/v1/captures",
      captured([{ url: "https://b.test/b" }, { url: "https://a.test/a" }]),
      api,
    );

    expect(api.request).toHaveBeenCalledOnce();
    expect(api.request).toHaveBeenCalledWith({ origins: ["https://a.test/*", "https://b.test/*"] });
    expect(result).toEqual({
      feGranted: true,
      feOrigin: "https://fe.test",
      permittedSourceOrigins: ["https://a.test", "https://b.test"],
      deniedSourceOrigins: [],
    });
  });

  it("distinguishes required FE denial from optional media denial", async () => {
    const api = permissionApi(["https://fe.test/*"], []);
    const result = await requestCapturePermissions(
      "https://fe.test/v1/captures",
      captured([{ url: "https://media.test/item" }]),
      api,
    );

    expect(result).toMatchObject({
      feGranted: true,
      permittedSourceOrigins: [],
      deniedSourceOrigins: ["https://media.test"],
    });
    expect(api.request).toHaveBeenCalledOnce();
  });

  it("retains same-FE-origin media in the source allowlist without duplicate prompts", async () => {
    const api = permissionApi(["https://fe.test/*"], []);
    const result = await requestCapturePermissions(
      "https://fe.test/v1/captures",
      captured([{ url: "https://fe.test/media/item.png" }]),
      api,
    );

    expect(api.request).not.toHaveBeenCalled();
    expect(result.permittedSourceOrigins).toEqual(["https://fe.test"]);
  });

  it("never requests an automatic YouTube video-byte origin", () => {
    const page = captured([{ url: "https://googlevideo.test/playback" }]);
    page.identity.provider = "youtube";
    page.media[0]!.kind = "video";
    expect(browserPreferredSourceOrigins(page)).toEqual([]);
  });

  it("rechecks revoked retry grants and requests the exact missing set once", async () => {
    const api = permissionApi(
      ["https://fe.test/*", "https://still-granted.test/*"],
      ["https://revoked.test/*"],
    );

    const result = await requestRetryPermissions(
      "https://fe.test",
      ["https://still-granted.test", "https://revoked.test"],
      api,
    );

    expect(api.request).toHaveBeenCalledOnce();
    expect(api.request).toHaveBeenCalledWith({ origins: ["https://revoked.test/*"] });
    expect(result).toEqual({
      feGranted: true,
      feOrigin: "https://fe.test",
      permittedSourceOrigins: ["https://revoked.test", "https://still-granted.test"],
      deniedSourceOrigins: [],
    });
  });

  it("keeps optional media denial separate from required FE denial on retry", async () => {
    const api = permissionApi([], ["https://media.test/*"]);

    const result = await requestRetryPermissions(
      "https://fe.test",
      ["https://media.test"],
      api,
    );

    expect(api.request).toHaveBeenCalledOnce();
    expect(api.request).toHaveBeenCalledWith({
      origins: ["https://fe.test/*", "https://media.test/*"],
    });
    expect(result).toMatchObject({
      feGranted: false,
      permittedSourceOrigins: ["https://media.test"],
      deniedSourceOrigins: [],
    });
  });

  it("rejects unsafe retry origins before any permission prompt", async () => {
    const api = permissionApi([], []);

    await expect(requestRetryPermissions(
      "https://fe.test",
      ["https://media.test/private"],
      api,
    )).rejects.toThrow(/invalid/);
    expect(api.contains).not.toHaveBeenCalled();
    expect(api.request).not.toHaveBeenCalled();
  });
});
