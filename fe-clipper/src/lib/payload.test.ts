import { describe, expect, it } from "vitest";
import { buildPayload, parseTagsInput } from "./payload";
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
    const payload = buildPayload(captured, { title: "Edited Title", tags: ["link", "reading"] });

    expect(payload).toEqual({
      content: "# Heading\n\nBody text.",
      title: "Edited Title",
      source_url: "https://example.com/post",
      description: "A meta description.",
      selection: "Highlighted bit.",
      tags: ["link", "reading"],
    });
    // Exactly these six keys -- no extras, nothing missing.
    expect(Object.keys(payload).sort()).toEqual(
      ["content", "description", "selection", "source_url", "tags", "title"].sort(),
    );
  });

  it("falls back to the captured title when the edited title is blank", () => {
    const payload = buildPayload(captured, { title: "   ", tags: ["link"] });
    expect(payload.title).toBe("Captured Title");
  });

  it("uses the trimmed edited title when provided", () => {
    const payload = buildPayload(captured, { title: "  Custom Title  ", tags: ["link"] });
    expect(payload.title).toBe("Custom Title");
  });

  it("keeps content and selection as separate fields, never merged", () => {
    const payload = buildPayload(captured, { title: "T", tags: ["link"] });
    expect(payload.content).not.toContain(captured.selection);
    expect(payload.selection).toBe(captured.selection);
  });

  it("defaults tags to whatever the caller passes through untouched", () => {
    const payload = buildPayload(captured, { title: "T", tags: ["link"] });
    expect(payload.tags).toEqual(["link"]);
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
