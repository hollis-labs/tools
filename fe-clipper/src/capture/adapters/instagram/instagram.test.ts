import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";

import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";

import { PageCaptureAdapterRegistry, toPageCaptureLocation } from "../../registry";
import type { PageCaptureResult } from "../../types";
import { buildPayload } from "@/lib/payload";
import { INSTAGRAM_ADAPTER_VERSION, instagramPageCaptureAdapter } from ".";

const fixtureDirectory = resolve(
  process.cwd(),
  "src/capture/adapters/instagram/fixtures",
);

function fixture(name: string): string {
  return readFileSync(join(fixtureDirectory, name), "utf8");
}

function capture(name: string, url: string, selectionText = ""): PageCaptureResult {
  const document = new JSDOM(fixture(name), { url }).window.document;
  return new PageCaptureAdapterRegistry([instagramPageCaptureAdapter]).capture({
    document,
    location: toPageCaptureLocation(url),
    selectionText,
  });
}

function instagramExtension(result: PageCaptureResult) {
  return result.extensions.find((extension) => extension.namespace === "provider.instagram");
}

describe("Instagram page capture adapter", () => {
  it("supports current username-prefixed permalinks and Relay media records", () => {
    const result = capture(
      "current-state-image.html",
      "https://www.instagram.com/nasa/p/CURRENT123/",
    );

    expect(result.adapter).toMatchObject({ id: "instagram", usedGenericFallback: false });
    expect(result.identity).toMatchObject({
      canonicalUrl: "https://www.instagram.com/p/CURRENT123/",
      providerItemId: "CURRENT123",
    });
    expect(result.media[0]).toMatchObject({
      providerMediaId: "3976681299041141597_528817151",
      kind: "image",
      altText: "Earth after an orbital sunset",
    });
    expect(result.media[0]?.variants).toEqual([
      expect.objectContaining({
        kind: "original",
        sourceUrl: "https://cdn.example.test/current-image-original.jpg",
        width: 2048,
        height: 1364,
      }),
      expect.objectContaining({
        kind: "thumbnail",
        sourceUrl: "https://cdn.example.test/current-image-small.jpg",
      }),
    ]);
    expect(instagramExtension(result)?.data).toMatchObject({
      shortcode: "CURRENT123",
      author: { username: "nasa", display_name: "NASA" },
      hashtags: ["NASA", "Clouds"],
      media_count: 1,
    });
  });

  it("extracts stable image-post identity, provider context, and observed image variants", () => {
    const result = capture(
      "image-post.html",
      "https://m.instagram.com/p/IMAGE123/?igsh=tracking#comments",
      "  Keep this highlighted detail.  ",
    );

    expect(result.adapter).toEqual({
      id: "instagram",
      version: INSTAGRAM_ADAPTER_VERSION,
      matchConfidence: 100,
      usedGenericFallback: false,
    });
    expect(result.identity).toEqual({
      submittedUrl: "https://m.instagram.com/p/IMAGE123/?igsh=tracking#comments",
      canonicalUrl: "https://www.instagram.com/p/IMAGE123/",
      provider: "instagram",
      providerItemId: "IMAGE123",
      segmentKey: "root",
    });
    expect(result.document).toMatchObject({
      description: "A bright studio still life. #StillLife #Color",
      selection: "Keep this highlighted detail.",
      usedFallback: false,
    });
    expect(result.document.contentMarkdown).toContain("synthetic post description");
    expect(result.media).toEqual([
      {
        candidateId: "instagram:IMAGE123:180000000000001",
        providerMediaId: "180000000000001",
        kind: "image",
        role: "primary",
        position: 0,
        altText: "Orange vase beside two blue books",
        variants: [
          {
            variantId: "instagram:IMAGE123:180000000000001:original",
            kind: "original",
            sourceUrl: "https://cdn.example.test/image-123-original.jpg",
            acquisitionPreference: "browser_preferred",
            mimeType: "image/jpeg",
            width: 1440,
            height: 1080,
          },
          {
            variantId: "instagram:IMAGE123:180000000000001:thumbnail",
            kind: "thumbnail",
            sourceUrl: "https://cdn.example.test/image-123-thumb.jpg",
            acquisitionPreference: "browser_preferred",
          },
        ],
      },
    ]);
    expect(instagramExtension(result)).toEqual({
      namespace: "provider.instagram",
      schemaVersion: "1.0.0",
      data: {
        shortcode: "IMAGE123",
        content_kind: "post",
        caption: "A bright studio still life. #StillLife #Color",
        author: { username: "studio_example", display_name: "Studio Example" },
        published_at: "2026-09-01T17:00:00.000Z",
        hashtags: ["StillLife", "Color"],
        media_count: 1,
        observation_sources: ["serialized_state", "dom"],
      },
    });
    expect(result.capabilities.observed).toEqual([
      "canonical_identity",
      "title",
      "description",
      "body",
      "selection",
      "gallery_manifest",
      "original_media",
      "thumbnail_or_poster",
      "provider_metadata",
    ]);
    expect(result.warnings).toEqual([]);
  });

  it("retains generic body/highlight and popup note/tags alongside Instagram context", () => {
    const result = capture(
      "image-post.html",
      "https://www.instagram.com/p/IMAGE123/",
      "A selected sentence.",
    );
    const payload = buildPayload(result, {
      title: "",
      tags: ["link", "ceramics"],
      note: "Compare the palette later.",
    });

    expect(payload.content).toContain("> **Note:** Compare the palette later.");
    expect(payload.content).toContain("synthetic post description");
    expect(payload.selection).toBe("A selected sentence.");
    expect(payload.tags).toEqual(["link", "ceramics"]);
    expect(instagramExtension(result)?.data.caption).toBe(
      "A bright studio still life. #StillLife #Color",
    );
  });

  it("emits reel video bytes as reference-only and its poster as browser-preferred", () => {
    const result = capture(
      "reel-video.html",
      "https://www.instagram.com/reels/REEL456/?utm_medium=copy_link",
    );

    expect(result.identity).toMatchObject({
      canonicalUrl: "https://www.instagram.com/reel/REEL456/",
      providerItemId: "REEL456",
    });
    expect(result.media).toHaveLength(1);
    expect(result.media[0]).toMatchObject({
      candidateId: "instagram:REEL456:180000000000002",
      providerMediaId: "180000000000002",
      kind: "video",
      role: "primary",
      position: 0,
      altText: "Hands shaping a clay cup on a wheel",
    });
    expect(result.media[0]?.variants).toEqual([
      {
        variantId: "instagram:REEL456:180000000000002:stream_reference",
        kind: "stream_reference",
        sourceUrl:
          "https://video.example.test/reel-456.mp4?expires=1893456000&signature=fixture",
        acquisitionPreference: "reference_only",
        mimeType: "video/mp4",
        width: 1080,
        height: 1920,
        durationSeconds: 17.25,
        expiresAt: "2030-01-01T00:00:00.000Z",
      },
      {
        variantId: "instagram:REEL456:180000000000002:poster",
        kind: "poster",
        sourceUrl: "https://cdn.example.test/reel-456-poster.jpg",
        acquisitionPreference: "browser_preferred",
        mimeType: "image/jpeg",
        width: 1080,
        height: 1920,
      },
    ]);
    expect(instagramExtension(result)?.data).toMatchObject({
      content_kind: "reel",
      author: { username: "maker_example", display_name: "Maker Example" },
      hashtags: ["Pottery", "Process"],
    });
  });

  it("preserves every mixed-carousel item in source order without recommendation contamination", () => {
    const result = capture(
      "mixed-carousel.html",
      "https://www.instagram.com/p/MIXED789/",
    );

    expect(result.media.map((item) => ({
      id: item.providerMediaId,
      kind: item.kind,
      role: item.role,
      position: item.position,
      variants: item.variants.map((variant) => variant.kind),
    }))).toEqual([
      {
        id: "carousel-image-a",
        kind: "image",
        role: "gallery_item",
        position: 0,
        variants: ["original", "thumbnail"],
      },
      {
        id: "carousel-video-b",
        kind: "video",
        role: "gallery_item",
        position: 1,
        variants: ["stream_reference", "poster"],
      },
      {
        id: "carousel-image-c",
        kind: "image",
        role: "gallery_item",
        position: 2,
        variants: ["original", "thumbnail"],
      },
    ]);
    expect(result.media.map((item) => item.candidateId)).toEqual([
      "instagram:MIXED789:carousel-image-a",
      "instagram:MIXED789:carousel-video-b",
      "instagram:MIXED789:carousel-image-c",
    ]);
    expect(JSON.stringify(result)).not.toContain("recommendation-must-not-appear");
    expect(result.warnings).toEqual([]);
  });

  it("keeps stable sparse positions and reports unsupported or partial media", () => {
    const result = capture(
      "sparse-carousel.html",
      "https://www.instagram.com/p/SPARSE01/",
    );

    expect(result.media.map((item) => ({
      id: item.providerMediaId,
      kind: item.kind,
      position: item.position,
      variants: item.variants.map((variant) => variant.kind),
    }))).toEqual([
      { id: "sparse-image-a", kind: "image", position: 0, variants: ["original"] },
      { id: "sparse-unsupported-b", kind: "other", position: 1, variants: [] },
      { id: "sparse-video-c", kind: "video", position: 2, variants: ["poster"] },
    ]);
    expect(result.warnings.map((warning) => warning.code)).toEqual([
      "instagram.image_thumbnail_missing",
      "instagram.media_item_unsupported",
      "instagram.video_stream_missing",
    ]);
    expect(result.document.contentMarkdown).toContain("synthetic sparse carousel");
  });

  it("falls back from malformed serialized state to normalized JSON-LD with a warning", () => {
    const result = capture(
      "malformed-state.html",
      "https://www.instagram.com/p/FALLBACK2/",
    );

    expect(result.identity.providerItemId).toBe("FALLBACK2");
    expect(result.media).toHaveLength(1);
    expect(result.media[0]).toMatchObject({
      providerMediaId: "fallback-image",
      kind: "image",
      position: 0,
    });
    expect(result.media[0]?.variants.map((variant) => variant.kind)).toEqual([
      "original",
      "thumbnail",
    ]);
    expect(instagramExtension(result)?.data).toMatchObject({
      caption: "Fallback description #Resilient",
      author: { username: "fallback_author", display_name: "Fallback Author" },
      hashtags: ["Resilient"],
    });
    expect(result.warnings).toContainEqual({
      code: "instagram.serialized_state_malformed",
      severity: "warning",
      stage: "provider_metadata",
      message:
        "Instagram serialized page state could not be parsed; other page observations were retained.",
    });
  });

  it("uses the rendered post DOM when serialized state and JSON-LD are absent", () => {
    const result = capture(
      "dom-only.html",
      "https://www.instagram.com/p/DOMONLY1/",
    );

    expect(result.media).toEqual([
      {
        candidateId: "instagram:DOMONLY1:dom-media-1",
        providerMediaId: "dom-media-1",
        kind: "image",
        role: "primary",
        position: 0,
        altText: "Rendered landscape photograph",
        variants: [
          {
            variantId: "instagram:DOMONLY1:dom-media-1:original",
            kind: "original",
            sourceUrl: "https://cdn.example.test/dom-original.jpg",
            acquisitionPreference: "browser_preferred",
            mimeType: "image/jpeg",
            width: 1280,
            height: 960,
          },
          {
            variantId: "instagram:DOMONLY1:dom-media-1:thumbnail",
            kind: "thumbnail",
            sourceUrl: "https://cdn.example.test/dom-thumb.jpg",
            acquisitionPreference: "browser_preferred",
          },
        ],
      },
    ]);
    expect(instagramExtension(result)?.data).toMatchObject({
      caption: "Rendered-only caption. #Rendered",
      author: { username: "rendered_example" },
      hashtags: ["Rendered"],
      observation_sources: ["dom"],
    });
  });

  it("rejects unrelated serialized state and selects JSON-LD for the URL shortcode", () => {
    const result = capture(
      "target-isolation.html",
      "https://www.instagram.com/p/TARGET42/",
    );
    const serialized = JSON.stringify(result);

    expect(result.identity).toMatchObject({
      canonicalUrl: "https://www.instagram.com/p/TARGET42/",
      providerItemId: "TARGET42",
    });
    expect(result.media).toHaveLength(1);
    expect(result.media[0]).toMatchObject({
      candidateId: "instagram:TARGET42:target-jsonld-media",
      providerMediaId: "target-jsonld-media",
      kind: "image",
      position: 0,
    });
    expect(instagramExtension(result)?.data).toMatchObject({
      caption: "Target-scoped caption. #TargetOnly",
      author: { username: "target_author", display_name: "Target Author" },
      hashtags: ["TargetOnly"],
      observation_sources: ["json_ld", "dom"],
    });
    expect(result.warnings.map((warning) => warning.code)).toContain(
      "instagram.serialized_state_target_mismatch",
    );
    for (const unrelated of [
      "UNRELATED_STATE_MEDIA_MUST_NOT_LEAK",
      "UNRELATED_STATE_AUTHOR_MUST_NOT_LEAK",
      "UNRELATED_STATE_CAPTION_MUST_NOT_LEAK",
      "UNRELATED_JSONLD_MEDIA_MUST_NOT_LEAK",
      "UNRELATED_JSONLD_AUTHOR_MUST_NOT_LEAK",
      "UNRELATED_JSONLD_CAPTION_MUST_NOT_LEAK",
      "RECOMMEND1",
      "RECOMMEND2",
    ]) {
      expect(serialized, unrelated).not.toContain(unrelated);
    }
  });

  it("excludes credentials, storage, auth state, unrelated headers, and raw state blobs", () => {
    const result = capture(
      "credential-exclusion.html",
      "https://TEST_ONLY_PAGE_USER_MUST_NOT_LEAK:TEST_ONLY_PAGE_PASSWORD_MUST_NOT_LEAK@www.instagram.com/p/SAFE999/?igsh=SHARE_CONTEXT_RETAINED&access_token=TEST_ONLY_PAGE_QUERY_TOKEN_MUST_NOT_LEAK#comments",
    );
    const serialized = JSON.stringify(result);

    expect(result.identity).toMatchObject({
      submittedUrl:
        "https://www.instagram.com/p/SAFE999/?igsh=SHARE_CONTEXT_RETAINED#comments",
      canonicalUrl: "https://www.instagram.com/p/SAFE999/",
      providerItemId: "SAFE999",
    });

    for (const forbidden of [
      "TEST_ONLY_ACCESS_SECRET_MUST_NOT_LEAK",
      "TEST_ONLY_CSRF_SECRET_MUST_NOT_LEAK",
      "TEST_ONLY_COOKIE_SECRET_MUST_NOT_LEAK",
      "TEST_ONLY_COOKIE_JAR_MUST_NOT_LEAK",
      "TEST_ONLY_BEARER_SECRET_MUST_NOT_LEAK",
      "TEST_ONLY_LOCAL_STORAGE_MUST_NOT_LEAK",
      "TEST_ONLY_SESSION_STORAGE_MUST_NOT_LEAK",
      "TEST_ONLY_AUTH_STATE_MUST_NOT_LEAK",
      "TEST_ONLY_URL_USER_MUST_NOT_LEAK",
      "TEST_ONLY_URL_PASSWORD_MUST_NOT_LEAK",
      "TEST_ONLY_URL_TOKEN_MUST_NOT_LEAK",
      "TEST_ONLY_URL_FRAGMENT_TOKEN_MUST_NOT_LEAK",
      "TEST_ONLY_PAGE_USER_MUST_NOT_LEAK",
      "TEST_ONLY_PAGE_PASSWORD_MUST_NOT_LEAK",
      "TEST_ONLY_PAGE_QUERY_TOKEN_MUST_NOT_LEAK",
      "TEST_ONLY_HEADER_MUST_NOT_LEAK",
      "TEST_ONLY_RAW_STATE_MUST_NOT_LEAK",
      "TEST_ONLY_NESTED_AUTH_STATE_MUST_NOT_LEAK",
      "access_token",
      "csrf_token",
      "authorization",
      "cookie",
      "cookies",
      "localStorage",
      "sessionStorage",
      "auth_state",
      "raw_page_state",
      "x-private",
    ]) {
      expect(serialized, forbidden).not.toContain(forbidden);
    }
    expect(serialized).toContain("SIGNED_SOURCE_OBSERVATION");
    expect(result.media[0]?.variants[0]).toMatchObject({
      kind: "original",
      acquisitionPreference: "browser_preferred",
      expiresAt: "2030-01-01T00:00:00.000Z",
    });
    expect(result.media[0]?.variants[1]).toMatchObject({
      kind: "thumbnail",
      sourceUrl:
        "https://cdn.example.test/safe-thumb.jpg?signature=SIGNED_THUMBNAIL_OBSERVATION",
    });
  });

  it("does not claim Instagram profile pages and keeps the generic fallback", () => {
    const document = new JSDOM(
      "<html><head><title>Profile</title></head><body><p>Profile page.</p></body></html>",
      { url: "https://www.instagram.com/example/" },
    ).window.document;
    const result = new PageCaptureAdapterRegistry([instagramPageCaptureAdapter]).capture({
      document,
      location: toPageCaptureLocation("https://www.instagram.com/example/"),
      selectionText: "",
    });

    expect(result.adapter.id).toBe("generic");
    expect(result.adapter.usedGenericFallback).toBe(true);
  });

  it("keeps the committed fixture corpus explicit, synthetic, and network-independent", () => {
    const htmlFixtures = readdirSync(fixtureDirectory)
      .filter((name) => name.endsWith(".html"))
      .sort();
    expect(htmlFixtures).toEqual([
      "credential-exclusion.html",
      "current-state-image.html",
      "dom-only.html",
      "image-post.html",
      "malformed-state.html",
      "mixed-carousel.html",
      "reel-video.html",
      "sparse-carousel.html",
      "target-isolation.html",
    ]);

    for (const name of htmlFixtures) {
      const html = fixture(name);
      expect(html, name).not.toContain("cdninstagram.com");
      expect(html, name).not.toContain("fbcdn.net");
      expect(html, name).toContain("synthetic");
    }
  });
});
