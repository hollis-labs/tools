import { readFileSync } from "node:fs";
import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";
import { PageCaptureAdapterRegistry, toPageCaptureLocation } from "../registry";
import { ADAPTER_MATCH_CONFIDENCE } from "../types";

const ARTICLE_TEXT =
  "This is substantial readable article prose with enough natural-language content for the extraction engine to recognize it as the central page content. ".repeat(
    12,
  );

function capture(html: string, url: string, selectionText = "") {
  const document = new JSDOM(html, { url }).window.document;
  return new PageCaptureAdapterRegistry().capture({
    document,
    location: toPageCaptureLocation(url),
    selectionText,
  });
}

function fixture(name: string): string {
  return readFileSync(`src/capture/adapters/fixtures/generic/${name}.html`, "utf8").replace(
    "{{ARTICLE_TEXT}}",
    ARTICLE_TEXT,
  );
}

describe("generic page capture adapter", () => {
  it("preserves the current Readability, description, and selection behavior", () => {
    const result = capture(
      `<html lang="en">
        <head>
          <title>Browser title</title>
          <meta property="og:description" content="Open Graph description">
          <meta name="description" content="Preferred description">
          <link rel="canonical" href="/canonical-article">
        </head>
        <body>
          <nav>Navigation chrome</nav>
          <article><h1>Readable heading</h1><p>${ARTICLE_TEXT}</p></article>
        </body>
      </html>`,
      "https://example.com/article?tracking=1#section",
      "  Highlighted sentence.  ",
    );

    expect(result.adapter).toEqual({
      id: "generic",
      version: "1.1.0",
      matchConfidence: ADAPTER_MATCH_CONFIDENCE.none,
      usedGenericFallback: true,
    });
    expect(result.identity).toEqual({
      submittedUrl: "https://example.com/article?tracking=1#section",
      canonicalUrl: "https://example.com/canonical-article",
      provider: "web",
      segmentKey: "root",
    });
    expect(result.document.title).toBe("Browser title");
    expect(result.document.description).toBe("Preferred description");
    expect(result.document.selection).toBe("Highlighted sentence.");
    expect(result.document.contentMarkdown).toContain("# Readable heading");
    expect(result.document.contentMarkdown).not.toContain("Navigation chrome");
    expect(result.document.language).toBe("en");
    expect(result.document.usedFallback).toBe(false);
    expect(result.media).toEqual([]);
    expect(result.extensions).toEqual([]);
    expect(result.capabilities.observed).toEqual([
      "canonical_identity",
      "title",
      "description",
      "body",
      "selection",
    ]);
    expect(result.warnings).toEqual([]);
  });

  it("emits a metadata hero and meaningful inline images in deterministic source order", () => {
    const result = capture(
      fixture("article-media"),
      "https://news.example.test/read/story/index.html?campaign=kept#selection",
      "  Keep this selected observation.  ",
    );

    expect(result.identity).toEqual({
      submittedUrl: "https://news.example.test/read/story/index.html?campaign=kept#selection",
      canonicalUrl: "https://news.example.test/stories/media-rich",
      provider: "web",
      segmentKey: "root",
    });
    expect(result.document).toMatchObject({
      title: "Media Rich Article | Example News",
      description: "A deterministic article media fixture.",
      language: "en-US",
      selection: "Keep this selected observation.",
      usedFallback: false,
    });
    expect(result.media).toEqual([
      {
        candidateId: "generic:image:0",
        kind: "image",
        role: "hero",
        position: 0,
        altText: "A river winding through a broad valley",
        sourceContext: "Document metadata: og:image",
        variants: [
          {
            variantId: "original",
            kind: "original",
            sourceUrl: "https://news.example.test/media/hero.jpg",
            acquisitionPreference: "browser_preferred",
            mimeType: "image/jpeg",
            width: 1800,
            height: 1000,
          },
        ],
      },
      {
        candidateId: "generic:image:1",
        kind: "image",
        role: "inline",
        position: 1,
        altText: "A chart comparing the measurements",
        caption: "Measurements increased throughout the study.",
        sourceContext: "Under heading: Measurements",
        variants: [
          {
            variantId: "original",
            kind: "original",
            sourceUrl:
              "https://news.example.test/media/chart-1600.webp?utm_source=fixture",
            acquisitionPreference: "browser_preferred",
            mimeType: "image/webp",
            width: 1600,
          },
        ],
      },
      {
        candidateId: "generic:image:2",
        kind: "image",
        role: "inline",
        position: 2,
        altText: "Researchers recording observations",
        caption: "Researchers documented each observation.",
        sourceContext: "Under heading: Field notes",
        variants: [
          {
            variantId: "original",
            kind: "original",
            sourceUrl: "https://news.example.test/read/photos/detail.png",
            acquisitionPreference: "browser_preferred",
            mimeType: "image/png",
            width: 1200,
            height: 800,
          },
        ],
      },
    ]);
    expect(result.capabilities.declared).toContain("gallery_manifest");
    expect(result.capabilities.declared).toContain("original_media");
    expect(result.capabilities.observed).toEqual([
      "canonical_identity",
      "title",
      "description",
      "body",
      "selection",
      "gallery_manifest",
      "original_media",
    ]);
    expect(result.warnings).toEqual([
      {
        code: "generic.media_sources_unusable",
        severity: "info",
        stage: "media",
        message: "2 observed image sources were omitted because they were not safe HTTP(S) URLs.",
      },
    ]);
  });

  it("returns an empty media manifest without treating a text-only article as an error", () => {
    const result = capture(fixture("no-image"), "https://example.com/text-only");

    expect(result.media).toEqual([]);
    expect(result.capabilities.observed).not.toContain("gallery_manifest");
    expect(result.capabilities.observed).not.toContain("original_media");
    expect(result.warnings).toEqual([]);
  });

  it("keeps useful observed media when readable-document extraction falls back", () => {
    const result = capture(
      fixture("extraction-fallback"),
      "https://example.com/notes/visual",
    );

    expect(result.document).toMatchObject({
      title: "Visual field note",
      contentMarkdown: "![A field observation](/field/observation.jpeg)",
      language: "es",
      usedFallback: true,
    });
    expect(result.media).toEqual([
      {
        candidateId: "generic:image:0",
        kind: "image",
        role: "hero",
        position: 0,
        altText: "A field observation",
        variants: [
          {
            variantId: "original",
            kind: "original",
            sourceUrl: "https://example.com/field/observation.jpeg",
            acquisitionPreference: "browser_preferred",
            mimeType: "image/jpeg",
            width: 1280,
            height: 720,
          },
        ],
      },
    ]);
    expect(result.warnings).toContainEqual({
      code: "generic.readability_fallback",
      severity: "info",
      stage: "document",
      message: "No readable article region was found; captured the page body instead.",
    });
  });

  it("normalizes a generic URL when the page has no canonical link", () => {
    const result = capture(
      `<html><head><title>Canonical page</title></head><body></body></html>`,
      "https://example.com/story?chapter=2&utm_medium=email&fbclid=tracking#paragraph",
    );

    expect(result.identity.submittedUrl).toBe(
      "https://example.com/story?chapter=2&utm_medium=email&fbclid=tracking#paragraph",
    );
    expect(result.identity.canonicalUrl).toBe("https://example.com/story?chapter=2");
  });

  it("ignores a canonical link that could serialize URL credentials", () => {
    const result = capture(
      `<html><head><title>Safe identity</title><link rel="canonical" href="https://user:secret@example.com/private"></head><body></body></html>`,
      "https://example.com/safe#section",
    );

    expect(result.identity.canonicalUrl).toBe("https://example.com/safe");
    expect(result.warnings).toContainEqual({
      code: "generic.canonical_url_ignored",
      severity: "info",
      stage: "identity",
      message:
        "The page's canonical link was not a safe HTTP(S) URL; the submitted URL was used instead.",
    });
    expect(JSON.stringify(result)).not.toContain("secret");
  });

  it("keeps the raw-body fallback visible as a structured warning", () => {
    const result = capture(
      "<html><head><title>Empty Page</title></head><body></body></html>",
      "https://example.com/empty",
    );

    expect(result.document.usedFallback).toBe(true);
    expect(result.document.contentMarkdown).toBe("");
    expect(result.warnings).toEqual([
      {
        code: "generic.readability_fallback",
        severity: "info",
        stage: "document",
        message: "No readable article region was found; captured the page body instead.",
      },
    ]);
  });
});
