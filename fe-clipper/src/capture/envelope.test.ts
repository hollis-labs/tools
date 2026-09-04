import { describe, expect, it } from "vitest";

import { assertFECaptureEnvelope } from "@/contracts/fragmentsEngine";
import type { PageCaptureResult } from "./types";
import { buildCaptureEnvelope } from "./envelope";

function capture(overrides: Partial<PageCaptureResult> = {}): PageCaptureResult {
  return {
    adapter: {
      id: "generic",
      version: "1.0.0",
      matchConfidence: 100,
      usedGenericFallback: false,
    },
    identity: {
      submittedUrl: "https://example.test/article?token=secret&view=reader#session=secret&section=2",
      canonicalUrl: "https://example.test/article?view=reader",
      provider: "web",
      segmentKey: "root",
    },
    document: {
      title: "Original title",
      description: "Description",
      contentMarkdown: "# Source body\n\nBody only.",
      selection: "selected source text",
      language: "en",
      usedFallback: false,
    },
    media: [],
    extensions: [],
    capabilities: {
      declared: ["canonical_identity", "title", "body", "selection"],
      observed: ["canonical_identity", "title", "body", "selection"],
    },
    warnings: [],
    ...overrides,
  };
}

function options() {
  let id = 0;
  return {
    captureId: "capture-1",
    capturedAt: "2026-09-03T20:00:00.000Z",
    principalId: "local-user",
    clientVersion: "0.1.0",
    createId: () => `annotation-${++id}`,
    digestIdentifier: async (value: string) =>
      (value.includes(":position:1:") ? "e" : "d").repeat(64),
  };
}

describe("FE capture envelope mapping", () => {
  it("keeps source body immutable while sending note and highlight as annotations", async () => {
    const envelope = await buildCaptureEnvelope(
      capture(),
      { title: "Edited title", tags: ["link", "research", "link"], note: "Personal note" },
      options(),
    );

    assertFECaptureEnvelope(envelope);
    expect(envelope.document.content?.body).toBe("# Source body\n\nBody only.");
    expect(envelope.document.content?.body).not.toContain("Personal note");
    expect(envelope.annotations).toEqual([
      {
        annotation_id: "annotation-1",
        kind: "highlight",
        text: "selected source text",
        selector: { exact: "selected source text" },
      },
      { annotation_id: "annotation-2", kind: "capture_note", text: "Personal note" },
    ]);
    expect(envelope.tags).toEqual(["link", "research"]);
    expect(envelope.extraction.observed_capabilities).toEqual(["title", "body"]);
    expect(envelope.source.submitted_url).toBe("https://example.test/article?view=reader#section=2");
  });

  it("maps media variants, positions, source context, and sparse placeholders safely", async () => {
    const envelope = await buildCaptureEnvelope(
      capture({
        media: [
          {
            candidateId: "generic:image:0",
            kind: "image",
            role: "hero",
            position: 0,
            sourceContext: "Document metadata: og:image",
            variants: [
              {
                variantId: "original",
                kind: "original",
                sourceUrl: "https://cdn.example.test/hero.jpg",
                acquisitionPreference: "browser_preferred",
                mimeType: "image/jpeg",
              },
            ],
          },
          {
            candidateId: "instagram:post:missing",
            kind: "other",
            role: "gallery_item",
            position: 1,
            variants: [
              {
                variantId: "unsupported",
                kind: "other",
                sourceUrl: "",
                acquisitionPreference: "reference_only",
              },
            ],
          },
          {
            candidateId: "generic:image:1",
            kind: "video",
            role: "inline",
            position: 2,
            variants: [
              {
                variantId: "original",
                kind: "stream_reference",
                sourceUrl: "https://video.example.test/watch/1",
                acquisitionPreference: "reference_only",
                mimeType: "video/mp4",
              },
            ],
          },
        ],
      }),
      { title: "", tags: [], note: "" },
      options(),
    );

    expect(envelope.media).toHaveLength(2);
    expect(envelope.media.map((item) => item.position)).toEqual([0, 1]);
    expect(envelope.media[0]?.source_context).toBe("Document metadata: og:image");
    expect(envelope.media[0]?.variants[0]).toMatchObject({
      kind: "original",
      transfer_preference: "browser_preferred",
      requested_custody: "mirror",
    });
    expect(envelope.media[1]?.variants[0]).toMatchObject({
      kind: "original",
      transfer_preference: "reference_only",
      requested_custody: "reference",
    });
    expect(envelope.media[0]?.variants[0]?.client_variant_id).not.toBe(
      envelope.media[1]?.variants[0]?.client_variant_id,
    );
    expect(envelope.extraction.warnings).toContainEqual({
      code: "capture.media_omitted",
      message:
        "Media instagram:post:missing at source position 1 had no FE-compatible variants; its position remains recorded by this warning.",
    });
  });

  it("sanitizes provider extensions and creates collision-safe long and duplicate IDs", async () => {
    const longUrl = `https://example.test/${"a".repeat(300)}`;
    const longCandidate = `generic:${"m".repeat(300)}`;
    const envelope = await buildCaptureEnvelope(
      capture({
        identity: {
          submittedUrl: longUrl,
          canonicalUrl: longUrl,
          provider: "web",
          segmentKey: "root",
        },
        extensions: [
          {
            namespace: "provider.web",
            schemaVersion: "1.0.0",
            data: {
              author: "Ada",
              access_token: "do-not-send",
              locator: "https://example.test/path?token=do-not-send&safe=yes",
            },
          },
        ],
        media: [0, 1].map((position) => ({
          candidateId: longCandidate,
          kind: "image" as const,
          role: "inline" as const,
          position,
          variants: [
            {
              variantId: "original",
              kind: "original" as const,
              sourceUrl: `https://cdn.example.test/${position}.jpg`,
              acquisitionPreference: "browser_preferred" as const,
            },
          ],
        })),
      }),
      { title: "", tags: [], note: "" },
      options(),
    );

    expect(envelope.source.source_item_key).toBe(`web-url:${"d".repeat(64)}`);
    expect(new Set(envelope.media.map((item) => item.client_media_id)).size).toBe(2);
    expect(
      new Set(envelope.media.flatMap((item) => item.variants.map((variant) => variant.client_variant_id))).size,
    ).toBe(2);
    const serialized = JSON.stringify(envelope);
    expect(serialized).not.toContain("do-not-send");
    expect(serialized).toContain("safe=yes");
    expect(envelope.extraction.warnings).toContainEqual({
      code: "capture.credentials_omitted",
      message: "Credential-shaped data was removed from provider.web.",
    });
  });

  it("rejects overlong tags instead of dropping user input", async () => {
    await expect(
      buildCaptureEnvelope(capture(), { title: "", tags: ["x".repeat(129)], note: "" }, options()),
    ).rejects.toThrow(/exceeds 128/);
  });

  it("qualifies provider item identities before applying length-safe hashing", async () => {
    const providerItemId = "3RmtNXqnreI";
    const buildForProvider = (provider: string) =>
      buildCaptureEnvelope(
        capture({
          identity: {
            submittedUrl: `https://${provider}.example.test/watch/${providerItemId}`,
            canonicalUrl: `https://${provider}.example.test/watch/${providerItemId}`,
            provider,
            providerItemId,
            segmentKey: "root",
          },
        }),
        { title: "", tags: [], note: "" },
        options(),
      );

    const youtube = await buildForProvider("youtube");
    const vimeo = await buildForProvider("vimeo");

    expect(youtube.source.source_item_key).toBe("youtube:3RmtNXqnreI");
    expect(vimeo.source.source_item_key).toBe("vimeo:3RmtNXqnreI");
    expect(youtube.source.source_item_key).not.toBe(vimeo.source.source_item_key);
  });
});
