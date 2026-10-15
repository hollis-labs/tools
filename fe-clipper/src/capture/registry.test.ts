import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";
import {
  createFailedPageCapture,
  pageCaptureAdapterRegistry,
  PageCaptureAdapterRegistry,
  toPageCaptureLocation,
} from "./registry";
import {
  ADAPTER_MATCH_CONFIDENCE,
  type AdapterMatchConfidence,
  type PageCaptureAdapter,
  type PageCaptureContext,
} from "./types";

function makeContext(
  html = "<html><head><title>Page</title></head><body></body></html>",
  url = "https://example.com/post",
) {
  return {
    document: new JSDOM(html, { url }).window.document,
    location: toPageCaptureLocation(url),
    selectionText: "",
  } satisfies PageCaptureContext;
}

function makeAdapter(
  id: string,
  confidence: AdapterMatchConfidence,
  overrides: Partial<PageCaptureAdapter> = {},
): PageCaptureAdapter {
  return {
    id,
    version: "2.0.0",
    matches: () => confidence,
    identify: (context) => ({
      observation: {
        submittedUrl: context.location.href,
        canonicalUrl: `https://${id}.example/items/42`,
        provider: id,
        providerItemId: "42",
        segmentKey: "root",
      },
      observedCapabilities: ["canonical_identity"],
    }),
    extractDocument: () => ({
      observation: {
        title: "Provider title",
        description: "Provider description",
        contentMarkdown: "Readable provider page text.",
        selection: "Selected text.",
        usedFallback: false,
      },
      observedCapabilities: ["title", "description", "body", "selection"],
    }),
    extractMedia: () => ({
      observation: [
        {
          candidateId: `${id}:42:0`,
          providerMediaId: "42",
          kind: "image",
          role: "primary",
          position: 0,
          variants: [
            {
              variantId: "original",
              kind: "original",
              sourceUrl: `https://media.${id}.example/original.jpg`,
              acquisitionPreference: "browser_preferred",
              width: 1200,
              height: 800,
            },
          ],
        },
      ],
      observedCapabilities: ["gallery_manifest", "original_media"],
    }),
    extractProviderMetadata: () => ({
      observation: [
        {
          namespace: `provider.${id}`,
          schemaVersion: "1",
          data: { creator: "Example Creator" },
        },
      ],
      observedCapabilities: ["provider_metadata"],
      warnings: [
        {
          code: `${id}.optional_field_missing`,
          severity: "info",
          stage: "provider_metadata",
          message: "An optional normalized field was not present.",
        },
      ],
    }),
    reportCapabilities: () => [
      "canonical_identity",
      "title",
      "description",
      "body",
      "selection",
      "gallery_manifest",
      "original_media",
      "provider_metadata",
    ],
    ...overrides,
  };
}

describe("PageCaptureAdapterRegistry selection", () => {
  it.each([
    ["instagram", "https://www.instagram.com/p/POST123/"],
    ["pinterest", "https://www.pinterest.com/pin/104427285104852001/"],
    ["youtube", "https://www.youtube.com/watch?v=dQw4w9WgXcQ"],
  ])("registers the built-in %s adapter", (expectedAdapter, url) => {
    expect(pageCaptureAdapterRegistry.select(makeContext(undefined, url)).adapter.id).toBe(
      expectedAdapter,
    );
  });

  it("uses the generic adapter when no provider adapter matches", () => {
    const registry = new PageCaptureAdapterRegistry([
      makeAdapter("provider", ADAPTER_MATCH_CONFIDENCE.none),
    ]);

    const selection = registry.select(makeContext());

    expect(selection.adapter.id).toBe("generic");
    expect(selection.confidence).toBe(ADAPTER_MATCH_CONFIDENCE.none);
    expect(selection.usedGenericFallback).toBe(true);
  });

  it("selects the strongest match, resolving equal confidence by adapter ID", () => {
    const likely = makeAdapter("likely", ADAPTER_MATCH_CONFIDENCE.likely);
    const tieZ = makeAdapter("zeta", ADAPTER_MATCH_CONFIDENCE.exact);
    const tieA = makeAdapter("alpha", ADAPTER_MATCH_CONFIDENCE.exact);

    const forward = new PageCaptureAdapterRegistry([likely, tieZ, tieA]);
    const reverse = new PageCaptureAdapterRegistry([tieA, tieZ, likely]);

    expect(forward.select(makeContext()).adapter.id).toBe("alpha");
    expect(reverse.select(makeContext()).adapter.id).toBe("alpha");
  });

  it("rejects duplicate IDs so selection cannot be registration-order dependent", () => {
    expect(
      () =>
        new PageCaptureAdapterRegistry([
          makeAdapter("same", ADAPTER_MATCH_CONFIDENCE.possible),
          makeAdapter("same", ADAPTER_MATCH_CONFIDENCE.exact),
        ]),
    ).toThrow("Duplicate page capture adapter ID: same");
  });
});

describe("PageCaptureAdapterRegistry result", () => {
  it("assembles identity, document, ordered media, extensions, capabilities, and warnings", () => {
    const registry = new PageCaptureAdapterRegistry([
      makeAdapter("provider", ADAPTER_MATCH_CONFIDENCE.exact),
    ]);
    const context = makeContext();

    const result = registry.capture(context);

    expect(result.adapter).toEqual({
      id: "provider",
      version: "2.0.0",
      matchConfidence: ADAPTER_MATCH_CONFIDENCE.exact,
      usedGenericFallback: false,
    });
    expect(result.identity).toEqual({
      submittedUrl: "https://example.com/post",
      canonicalUrl: "https://provider.example/items/42",
      provider: "provider",
      providerItemId: "42",
      segmentKey: "root",
    });
    expect(result.document.selection).toBe("Selected text.");
    expect(result.media.map(({ candidateId, position }) => ({ candidateId, position }))).toEqual([
      { candidateId: "provider:42:0", position: 0 },
    ]);
    expect(result.media[0]?.variants[0]).toMatchObject({
      variantId: "original",
      kind: "original",
      acquisitionPreference: "browser_preferred",
    });
    expect(result.extensions).toEqual([
      {
        namespace: "provider.provider",
        schemaVersion: "1",
        data: { creator: "Example Creator" },
      },
    ]);
    expect(result.capabilities.observed).toEqual([
      "canonical_identity",
      "title",
      "description",
      "body",
      "selection",
      "gallery_manifest",
      "original_media",
      "provider_metadata",
    ]);
    expect(result.warnings).toEqual([
      expect.objectContaining({
        code: "provider.optional_field_missing",
        stage: "provider_metadata",
      }),
    ]);

    // The serializable result contains normalized observations, never the live
    // Document object supplied to the adapter.
    expect(Object.values(result)).not.toContain(context.document);
    expect(JSON.stringify(result)).not.toContain("HTMLDocument");
  });

  it("falls back to generic extraction when a selected provider throws", () => {
    const failing = makeAdapter("broken", ADAPTER_MATCH_CONFIDENCE.exact, {
      identify: () => {
        throw new Error("provider page-state details");
      },
    });

    const result = new PageCaptureAdapterRegistry([failing]).capture(
      makeContext("<html><head><title>Still useful</title></head><body><p>Fallback body</p></body></html>"),
    );

    expect(result.adapter.id).toBe("generic");
    expect(result.adapter.usedGenericFallback).toBe(true);
    expect(result.document.title).toBe("Still useful");
    expect(result.warnings).toContainEqual({
      code: "registry.adapter_extraction_failed",
      severity: "warning",
      stage: "capture",
      message: 'Adapter "broken" could not extract this page; used the generic adapter.',
    });
    expect(JSON.stringify(result)).not.toContain("provider page-state details");
  });

  it("represents an unrecoverable generic error without losing page context", () => {
    const context = makeContext("<html><head><title>Problem page</title></head><body></body></html>");
    context.selectionText = "  Keep this highlight.  ";

    const result = createFailedPageCapture(context, "Readability stopped unexpectedly.");

    expect(result.identity.submittedUrl).toBe("https://example.com/post");
    expect(result.document).toMatchObject({
      title: "Problem page",
      contentMarkdown: "",
      selection: "Keep this highlight.",
      usedFallback: true,
    });
    expect(result.warnings).toEqual([
      {
        code: "capture.extraction_failed",
        severity: "error",
        stage: "capture",
        message: "Readability stopped unexpectedly.",
      },
    ]);
  });

  it("does not leak URL credentials from an unrecoverable capture", () => {
    const context = makeContext(
      "<html><head><title>Problem page</title></head><body></body></html>",
      "https://user:password@example.com/post?chapter=2&access_token=query-secret#section=one&session_token=fragment-secret",
    );

    const result = createFailedPageCapture(context, "Extraction failed.");

    expect(result.identity.submittedUrl).toBe(
      "https://example.com/post?chapter=2#section=one",
    );
    expect(JSON.stringify(result)).not.toContain("password");
    expect(JSON.stringify(result)).not.toContain("query-secret");
    expect(JSON.stringify(result)).not.toContain("fragment-secret");
  });
});

describe("adapter pre-pass", () => {
  it("runs only the selected adapter's pre-pass", async () => {
    const ran: string[] = [];
    const registry = new PageCaptureAdapterRegistry([
      makeAdapter("weak", ADAPTER_MATCH_CONFIDENCE.possible, {
        prepare: async () => {
          ran.push("weak");
        },
      }),
      makeAdapter("strong", ADAPTER_MATCH_CONFIDENCE.exact, {
        prepare: async () => {
          ran.push("strong");
        },
      }),
    ]);

    await registry.prepare(makeContext());
    expect(ran).toEqual(["strong"]);
  });

  it("is a no-op for adapters that declare no pre-pass", async () => {
    const registry = new PageCaptureAdapterRegistry([
      makeAdapter("plain", ADAPTER_MATCH_CONFIDENCE.exact),
    ]);
    await expect(registry.prepare(makeContext())).resolves.toEqual([]);
  });

  it("contains a failing pre-pass rather than losing the capture", async () => {
    const registry = new PageCaptureAdapterRegistry([
      makeAdapter("scroller", ADAPTER_MATCH_CONFIDENCE.exact, {
        prepare: async () => {
          throw new Error("the page navigated away mid-walk");
        },
      }),
    ]);
    const context = makeContext();

    const warnings = await registry.prepare(context);
    expect(warnings.map((warning) => warning.code)).toEqual([
      "registry.adapter_prepare_failed",
    ]);
    expect(warnings[0]?.message).toContain("navigated away mid-walk");

    // The capture still happens, carrying the pre-pass warning with it.
    const result = registry.capture(context, warnings);
    expect(result.adapter.id).toBe("scroller");
    expect(result.warnings.map((warning) => warning.code)).toContain(
      "registry.adapter_prepare_failed",
    );
  });

  it("threads pre-pass warnings through a generic fallback", async () => {
    const registry = new PageCaptureAdapterRegistry([
      makeAdapter("broken", ADAPTER_MATCH_CONFIDENCE.exact, {
        prepare: async () => [
          {
            code: "provider.prepare_partial",
            severity: "info" as const,
            stage: "matching" as const,
            message: "Only part of the page was revealed.",
          },
        ],
        extractDocument: () => {
          throw new Error("extraction failed");
        },
      }),
    ]);
    const context = makeContext();

    const result = registry.capture(context, await registry.prepare(context));
    const codes = result.warnings.map((warning) => warning.code);
    expect(result.adapter.usedGenericFallback).toBe(true);
    expect(codes).toContain("provider.prepare_partial");
    expect(codes).toContain("registry.adapter_extraction_failed");
  });
});
