import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";
import { extractPage, pickDescription } from "./extractPage";

function makeDoc(html: string, url = "https://example.com/"): Document {
  return new JSDOM(html, { url }).window.document;
}

// Readability requires a candidate region to clear a minimum character
// count (default threshold 500) before it's treated as the article -- pad
// well past that so extraction is reliable regardless of exact heuristics.
const LONG_PARAGRAPH_A =
  "Readability needs enough real prose in a candidate region before it will treat it as the main article, so this paragraph repeats plausible sentence-shaped text several times over to clear that length threshold reliably. ".repeat(
    4,
  );
const LONG_PARAGRAPH_B =
  "A second substantial paragraph keeps the article region scoring above short blurbs elsewhere on the page, again padded well past the default character threshold for safety. ".repeat(
    4,
  );

describe("extractPage: normal article extraction", () => {
  it("converts a Readability-extracted article to markdown, preserving structure", () => {
    const doc = makeDoc(
      `
      <html>
        <head><title>Example Article Page</title></head>
        <body>
          <nav><a href="/">Home</a></nav>
          <article>
            <h1>Article Heading</h1>
            <p>${LONG_PARAGRAPH_A}</p>
            <p>${LONG_PARAGRAPH_B} See <a href="https://example.com/ref">the reference</a> for more.</p>
            <ul><li>First point</li><li>Second point</li></ul>
            <pre><code>const answer = 42;</code></pre>
          </article>
        </body>
      </html>
    `,
      "https://example.com/article",
    );

    const result = extractPage(doc, { url: "https://example.com/article", selectionText: "" });

    expect(result.usedFallback).toBe(false);
    expect(result.url).toBe("https://example.com/article");
    // Headings survive as Markdown ATX headings.
    expect(result.contentMarkdown).toMatch(/^#+\s+Article Heading/m);
    // Lists survive as Markdown bullets.
    expect(result.contentMarkdown).toMatch(/^-\s+First point/m);
    expect(result.contentMarkdown).toMatch(/^-\s+Second point/m);
    // Links survive as Markdown links.
    expect(result.contentMarkdown).toContain("[the reference](https://example.com/ref)");
    // Code blocks survive as fenced code.
    expect(result.contentMarkdown).toContain("```");
    expect(result.contentMarkdown).toContain("const answer = 42;");
    // Nav chrome outside the <article> region is not part of the extraction.
    expect(result.contentMarkdown).not.toContain("Home");
    expect(result.selection).toBe("");
  });

  it("falls back to the raw body when Readability finds no article-shaped region", () => {
    const doc = makeDoc(`<html><head><title>Empty Page</title></head><body></body></html>`);

    const result = extractPage(doc, { url: "https://example.com/empty", selectionText: "" });

    expect(result.usedFallback).toBe(true);
    expect(result.contentMarkdown).toBe("");
  });
});

describe("extractPage: selection capture", () => {
  it("keeps the current text selection distinct from the main content", () => {
    const doc = makeDoc(`
      <html>
        <head><title>Selection Page</title></head>
        <body>
          <article>
            <h1>Selectable Article</h1>
            <p>${LONG_PARAGRAPH_A}</p>
            <p>${LONG_PARAGRAPH_B}</p>
          </article>
        </body>
      </html>
    `);

    const result = extractPage(doc, {
      url: "https://example.com/selectable",
      selectionText: "  Just the highlighted sentence.  ",
    });

    expect(result.selection).toBe("Just the highlighted sentence.");
    expect(result.contentMarkdown).not.toContain("Just the highlighted sentence.");
  });

  it("captures an empty selection as an empty string, not omitted", () => {
    const doc = makeDoc(`<html><head></head><body><article><h1>H</h1><p>${LONG_PARAGRAPH_A}</p></article></body></html>`);
    const result = extractPage(doc, { url: "https://example.com/x", selectionText: "" });
    expect(result.selection).toBe("");
  });
});

describe("pickDescription: meta description precedence", () => {
  it("prefers meta[name=description] over og:description when both are present", () => {
    const doc = makeDoc(`
      <html><head>
        <meta property="og:description" content="OG description text" />
        <meta name="description" content="Standard description text" />
      </head><body></body></html>
    `);
    expect(pickDescription(doc)).toBe("Standard description text");
  });

  it("falls back to og:description when meta[name=description] is absent", () => {
    const doc = makeDoc(`
      <html><head>
        <meta property="og:description" content="OG-only description" />
      </head><body></body></html>
    `);
    expect(pickDescription(doc)).toBe("OG-only description");
  });

  it("returns an empty string when neither meta tag is present", () => {
    const doc = makeDoc(`<html><head></head><body></body></html>`);
    expect(pickDescription(doc)).toBe("");
  });

  it("is threaded through extractPage onto CapturedPage.description", () => {
    const doc = makeDoc(`
      <html><head>
        <title>Has description</title>
        <meta name="description" content="Page-level description" />
      </head><body><article><h1>H</h1><p>${LONG_PARAGRAPH_A}</p></article></body></html>
    `);
    const result = extractPage(doc, { url: "https://example.com/x", selectionText: "" });
    expect(result.description).toBe("Page-level description");
  });
});
