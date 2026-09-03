import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { JSDOM } from "jsdom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { buildPayload } from "@/lib/payload";
import { PageCaptureAdapterRegistry, toPageCaptureLocation } from "../../registry";
import type { PageCaptureContext } from "../../types";
import {
  extractYouTubeVideoId,
  parseYouTubePage,
  youtubePageCaptureAdapter,
  YOUTUBE_ADAPTER_VERSION,
  YOUTUBE_PARSER_VERSION,
} from ".";

function fixture(name: string): string {
  return readFileSync(resolve("src/capture/adapters/youtube/fixtures", name), "utf8");
}

function fixtureContext(name: string, url: string, selectionText = ""): PageCaptureContext {
  const dom = new JSDOM(fixture(name), { url });
  return {
    document: dom.window.document,
    location: toPageCaptureLocation(url),
    selectionText,
  };
}

function capture(name: string, url: string, selectionText = "") {
  return new PageCaptureAdapterRegistry([youtubePageCaptureAdapter]).capture(
    fixtureContext(name, url, selectionText),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("YouTube URL identity", () => {
  const cases = [
    ["standard watch", "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42s", "dQw4w9WgXcQ"],
    ["share", "https://youtu.be/aqz-KE-bpKQ?si=fixture", "aqz-KE-bpKQ"],
    ["shorts", "https://www.youtube.com/shorts/M7lc1UVf-VE?feature=share", "M7lc1UVf-VE"],
    ["embed", "https://www.youtube-nocookie.com/embed/3JZ_D3ELwOQ?start=10", "3JZ_D3ELwOQ"],
    ["live", "https://m.youtube.com/live/jNQXAC9IVRw?feature=share", "jNQXAC9IVRw"],
  ] as const;

  it.each(cases)("normalizes a %s URL to the canonical watch identity", (_name, url, id) => {
    expect(extractYouTubeVideoId(url)).toBe(id);
    const context = fixtureContext("shorts-sparse-malformed.html", url);
    expect(youtubePageCaptureAdapter.identify(context).observation).toEqual({
      submittedUrl: url,
      canonicalUrl: `https://www.youtube.com/watch?v=${id}`,
      provider: "youtube",
      providerItemId: id,
      segmentKey: "root",
    });
  });

  it("rejects unsupported pages and lookalike hosts", () => {
    expect(extractYouTubeVideoId("https://www.youtube.com/playlist?list=PLfixture")).toBeUndefined();
    expect(
      extractYouTubeVideoId("https://www.youtube.com.example.test/watch?v=dQw4w9WgXcQ"),
    ).toBeUndefined();
    expect(extractYouTubeVideoId("https://youtu.be/not-valid")).toBeUndefined();
    expect(extractYouTubeVideoId("ftp://www.youtube.com/watch?v=dQw4w9WgXcQ")).toBeUndefined();
  });

  it("removes userinfo and credential-shaped URL keys from submitted provenance", () => {
    const url =
      "https://alice:password@www.youtube.com/watch?v=dQw4w9WgXcQ&utm_source=fixture&access_token=QUERY_SECRET&api_key=API_KEY_SECRET#t=42&client_secret=FRAGMENT_SECRET";
    const identity = youtubePageCaptureAdapter.identify(
      fixtureContext("watch-captions.html", url),
    ).observation;

    expect(identity.submittedUrl).toBe(
      "https://www.youtube.com/watch?v=dQw4w9WgXcQ&utm_source=fixture#t=42",
    );
    expect(JSON.stringify(identity)).not.toContain("password");
    expect(JSON.stringify(identity)).not.toContain("QUERY_SECRET");
    expect(JSON.stringify(identity)).not.toContain("API_KEY_SECRET");
    expect(JSON.stringify(identity)).not.toContain("FRAGMENT_SECRET");

    const emptyDom = new JSDOM("<html><body></body></html>", { url });
    const documentObservation = youtubePageCaptureAdapter.extractDocument({
      document: emptyDom.window.document,
      location: toPageCaptureLocation(url),
      selectionText: "",
    }).observation;
    expect(documentObservation.title).toBe(
      "https://www.youtube.com/watch?v=dQw4w9WgXcQ&utm_source=fixture#t=42",
    );
    expect(JSON.stringify(documentObservation)).not.toContain("QUERY_SECRET");
    expect(JSON.stringify(documentObservation)).not.toContain("FRAGMENT_SECRET");
  });
});

describe("YouTube adapter capture", () => {
  it("captures watch metadata, ordered posters, captions, transcript, and generic content", () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    const context = fixtureContext(
      "watch-captions.html",
      "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42s",
      "  highlighted rendered words  ",
    );
    context.document.defaultView?.localStorage.setItem("authToken", "SECRET_LOCAL_STORAGE");
    context.document.cookie = "SID=SECRET_DOCUMENT_COOKIE";

    const result = new PageCaptureAdapterRegistry([youtubePageCaptureAdapter]).capture(context);

    expect(result.adapter).toEqual({
      id: "youtube",
      version: YOUTUBE_ADAPTER_VERSION,
      matchConfidence: 100,
      usedGenericFallback: false,
    });
    expect(result.identity).toEqual({
      submittedUrl: "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42s",
      canonicalUrl: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
      provider: "youtube",
      providerItemId: "dQw4w9WgXcQ",
      segmentKey: "root",
    });
    expect(result.document).toMatchObject({
      title: "Player video title",
      description: "Official description.\n0:00 Opening\n0:42 Second chapter",
      selection: "highlighted rendered words",
      language: "en",
    });
    expect(result.document.contentMarkdown).toContain("Readable video page");
    expect(result.document.contentMarkdown).toContain("generic Readability and Turndown path");

    const media = result.media[0];
    expect(media).toMatchObject({
      candidateId: "youtube:dQw4w9WgXcQ",
      providerMediaId: "dQw4w9WgXcQ",
      kind: "video",
      role: "primary",
      position: 0,
      altText: "Player video title",
    });
    expect(media?.variants[0]).toEqual({
      variantId: "playback:youtube",
      kind: "stream_reference",
      sourceUrl: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
      acquisitionPreference: "reference_only",
    });
    expect(
      media?.variants
        .filter((variant) => variant.kind === "poster")
        .map((variant) => [variant.variantId, variant.width, variant.height]),
    ).toEqual([
      ["poster:maxresdefault", 1280, 720],
      ["poster:hqdefault", 480, 360],
      ["poster:mqdefault", 320, 180],
    ]);
    expect(
      media?.variants
        .filter((variant) => variant.kind === "subtitles")
        .map((variant) => ({
          id: variant.variantId,
          preference: variant.acquisitionPreference,
          mime: variant.mimeType,
        })),
    ).toEqual([
      { id: "subtitles:en-manual", preference: "browser_preferred", mime: "text/vtt" },
      { id: "subtitles:es-manual", preference: "browser_preferred", mime: "text/vtt" },
    ]);
    expect(media?.variants.some((variant) => variant.kind === "original")).toBe(false);
    expect(fetchSpy).not.toHaveBeenCalled();

    expect(result.extensions).toEqual([
      {
        namespace: "provider.youtube",
        schemaVersion: "1",
        data: expect.objectContaining({
          parser_version: YOUTUBE_PARSER_VERSION,
          video_id: "dQw4w9WgXcQ",
          title: "Player video title",
          channel: { id: "UC38IQsAvIsxxjztdMZQtwHA", name: "Example Channel" },
          tags: ["official", "music video"],
          duration_seconds: 213,
          published_at: "2009-10-25",
          poster_count: 3,
          chapters: [
            { title: "Opening", start_seconds: 0 },
            { title: "Second chapter", start_seconds: 42 },
          ],
          caption_tracks: [
            {
              language_code: "en",
              name: "English",
              kind: "manual",
              is_translatable: true,
            },
            {
              language_code: "es",
              name: "Spanish",
              kind: "manual",
              is_translatable: false,
            },
          ],
          transcript: {
            source: "rendered_dom",
            language_code: "en",
            segments: [
              { start_seconds: 0, duration_seconds: 4, text: "Rendered first line" },
              { start_seconds: 42, duration_seconds: 3, text: "Rendered second line" },
            ],
          },
        }),
      },
    ]);
    expect(result.capabilities.observed).toEqual([
      "canonical_identity",
      "title",
      "description",
      "body",
      "selection",
      "gallery_manifest",
      "thumbnail_or_poster",
      "transcript",
      "provider_metadata",
    ]);
    expect(result.warnings).toContainEqual(
      expect.objectContaining({ code: "youtube.caption_url_rejected" }),
    );

    const serialized = JSON.stringify(result);
    for (const secret of [
      "SUPER_SECRET_ACCESS_TOKEN",
      "SECRET_COOKIE",
      "SECRET_VISITOR_DATA",
      "SECRET_BEARER",
      "DO_NOT_SERIALIZE",
      "FRAGMENT_TRACK_SECRET",
      "SECRET_PAGE_STATE_TOKEN",
      "SECRET_LOCAL_STORAGE",
      "SECRET_DOCUMENT_COOKIE",
    ]) {
      expect(serialized).not.toContain(secret);
    }

    const payload = buildPayload(result, {
      title: "",
      tags: ["watch-later", "video"],
      note: "Revisit the highlighted section.",
    });
    expect(payload.content).toContain("> **Note:** Revisit the highlighted section.");
    expect(payload.content).toContain("Readable video page");
    expect(payload.selection).toBe("highlighted rendered words");
    expect(payload.tags).toEqual(["watch-later", "video"]);
  });

  it("uses sanitized JSON-LD and DOM observations for a share URL without captions", () => {
    const result = capture(
      "share-no-captions.html",
      "https://youtu.be/aqz-KE-bpKQ?si=fixture",
    );

    expect(result.identity).toMatchObject({
      canonicalUrl: "https://www.youtube.com/watch?v=aqz-KE-bpKQ",
      providerItemId: "aqz-KE-bpKQ",
    });
    expect(result.document).toMatchObject({
      title: "JSON-LD share title",
      description: "A public share-page description.",
    });
    expect(result.media).toHaveLength(1);
    expect(result.media[0]?.variants).toEqual([
      {
        variantId: "playback:youtube",
        kind: "stream_reference",
        sourceUrl: "https://www.youtube.com/watch?v=aqz-KE-bpKQ",
        acquisitionPreference: "reference_only",
      },
      {
        variantId: "poster:hqdefault",
        kind: "poster",
        sourceUrl: "https://i.ytimg.com/vi/aqz-KE-bpKQ/hqdefault.jpg",
        acquisitionPreference: "browser_preferred",
        mimeType: "image/jpeg",
        width: 480,
        height: 360,
      },
    ]);
    expect(result.warnings).toContainEqual({
      code: "youtube.captions_unavailable",
      severity: "info",
      stage: "media",
      message: "No caption track or transcript was available in the rendered YouTube page.",
    });
    expect(result.capabilities.observed).not.toContain("transcript");
  });

  it("keeps sparse and malformed Shorts state non-fatal with structured warnings", () => {
    const result = capture(
      "shorts-sparse-malformed.html",
      "https://www.youtube.com/shorts/M7lc1UVf-VE?feature=share",
    );

    expect(result.adapter.id).toBe("youtube");
    expect(result.identity).toMatchObject({
      canonicalUrl: "https://www.youtube.com/watch?v=M7lc1UVf-VE",
      providerItemId: "M7lc1UVf-VE",
    });
    expect(result.document.contentMarkdown).toContain("sparse Shorts page");
    expect(result.media[0]?.variants).toEqual([
      {
        variantId: "playback:youtube",
        kind: "stream_reference",
        sourceUrl: "https://www.youtube.com/watch?v=M7lc1UVf-VE",
        acquisitionPreference: "reference_only",
      },
    ]);
    expect(result.warnings).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ code: "youtube.posters_unavailable", stage: "media" }),
        expect.objectContaining({ code: "youtube.captions_unavailable", stage: "media" }),
        expect.objectContaining({
          code: "youtube.serialized_state_malformed",
          stage: "provider_metadata",
        }),
        expect.objectContaining({ code: "youtube.metadata_sparse", stage: "provider_metadata" }),
      ]),
    );
  });

  it("falls back to normalized serialized transcript segments when none are rendered", () => {
    const context = fixtureContext(
      "watch-captions.html",
      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
    );
    context.document.querySelector("[data-youtube-transcript-language]")?.remove();

    expect(parseYouTubePage(context.document, "dQw4w9WgXcQ").transcript).toEqual({
      source: "serialized_page_state",
      segments: [
        { startSeconds: 0, durationSeconds: 4, text: "Serialized first line" },
        { startSeconds: 42, durationSeconds: 3, text: "Serialized second line" },
      ],
    });
  });

  it("scopes JSON-LD metadata to the current video instead of a leading unrelated object", () => {
    const url = "https://www.youtube.com/watch?v=dQw4w9WgXcQ";
    const document = new JSDOM(
      `<html><head><script type="application/ld+json">{
        "@context":"https://schema.org",
        "@graph":[
          {"@type":"VideoObject","url":"https://www.youtube.com/watch?v=aqz-KE-bpKQ","name":"Unrelated recommendation"},
          {"@type":"VideoObject","url":"${url}","name":"Current video"}
        ]
      }</script></head><body></body></html>`,
      { url },
    ).window.document;

    expect(parseYouTubePage(document, "dQw4w9WgXcQ").title).toBe("Current video");
  });
});
