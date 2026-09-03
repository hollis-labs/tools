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
      version: "1.0.0",
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
