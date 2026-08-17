import { describe, expect, it } from "vitest";
import { buildPayload, parseTagsInput, prependNote } from "./payload";
import type { CapturedPage } from "./types";

const captured: CapturedPage = {
  title: "Captured Title",
  url: "https://example.com/post",
  contentMarkdown: "# Heading\n\nBody text.",
  description: "A meta description.",
  selection: "Highlighted bit.",
  usedFallback: false,
};

describe("buildPayload", () => {
  it("maps to the exact FE /v1/intake wire shape (CW-20260816-0071)", () => {
    const payload = buildPayload(captured, {
      title: "Edited Title",
      tags: ["link", "reading"],
      note: "",
    });

    expect(payload).toEqual({
      content: "# Heading\n\nBody text.",
      title: "Edited Title",
      source_url: "https://example.com/post",
      description: "A meta description.",
      selection: "Highlighted bit.",
      tags: ["link", "reading"],
    });
    // Exactly these six keys -- no extras, nothing missing. `note` never
    // appears as its own wire field; see the "prepends a non-empty note"
    // test below for how it reaches FE instead.
    expect(Object.keys(payload).sort()).toEqual(
      ["content", "description", "selection", "source_url", "tags", "title"].sort(),
    );
  });

  it("falls back to the captured title when the edited title is blank", () => {
    const payload = buildPayload(captured, { title: "   ", tags: ["link"], note: "" });
    expect(payload.title).toBe("Captured Title");
  });

  it("uses the trimmed edited title when provided", () => {
    const payload = buildPayload(captured, { title: "  Custom Title  ", tags: ["link"], note: "" });
    expect(payload.title).toBe("Custom Title");
  });

  it("keeps content and selection as separate fields, never merged", () => {
    const payload = buildPayload(captured, { title: "T", tags: ["link"], note: "" });
    expect(payload.content).not.toContain(captured.selection);
    expect(payload.selection).toBe(captured.selection);
  });

  it("defaults tags to whatever the caller passes through untouched", () => {
    const payload = buildPayload(captured, { title: "T", tags: ["link"], note: "" });
    expect(payload.tags).toEqual(["link"]);
  });

  it("prepends a non-empty note as a labeled blockquote ahead of the extracted content", () => {
    const payload = buildPayload(captured, {
      title: "T",
      tags: ["link"],
      note: "Remember to follow up on this.",
    });
    expect(payload.content).toBe(
      "> **Note:** Remember to follow up on this.\n\n# Heading\n\nBody text.",
    );
  });

  it("leaves content untouched when note is blank/whitespace-only", () => {
    const payload = buildPayload(captured, { title: "T", tags: ["link"], note: "   " });
    expect(payload.content).toBe(captured.contentMarkdown);
  });
});

describe("prependNote", () => {
  it("returns content unchanged for an empty note", () => {
    expect(prependNote("Body.", "")).toBe("Body.");
  });

  it("returns content unchanged for a whitespace-only note", () => {
    expect(prependNote("Body.", "   \n  ")).toBe("Body.");
  });

  it("quotes every line of a multi-line note, labeling only the first", () => {
    expect(prependNote("Body.", "Line one\nLine two")).toBe(
      "> **Note:** Line one\n> Line two\n\nBody.",
    );
  });

  it("trims leading/trailing whitespace from the note before quoting", () => {
    expect(prependNote("Body.", "  spaced out  ")).toBe("> **Note:** spaced out\n\nBody.");
  });
});

describe("parseTagsInput", () => {
  it("splits comma-separated tags and trims whitespace", () => {
    expect(parseTagsInput(" link, reading ,  ")).toEqual(["link", "reading"]);
  });

  it("drops empty entries from repeated separators", () => {
    expect(parseTagsInput("link,,reading")).toEqual(["link", "reading"]);
  });

  it("defaults to an empty array for blank input", () => {
    expect(parseTagsInput("   ")).toEqual([]);
  });
});
