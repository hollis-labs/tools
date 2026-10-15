import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";

import { PageCaptureAdapterRegistry, toPageCaptureLocation } from "../../registry";
import type { PageCaptureContext } from "../../types";
import { feContractValidators, type FECaptureMediaItem } from "@/contracts/fragmentsEngine";
import { buildPayload } from "@/lib/payload";
import alternatePageStateHtml from "./fixtures/alternate-page-state.html?raw";
import changedPageStateHtml from "./fixtures/changed-page-state.html?raw";
import completeHtml from "./fixtures/complete.html?raw";
import jsonLdHtml from "./fixtures/json-ld.html?raw";
import partialHtml from "./fixtures/partial.html?raw";
import pinResourceHtml from "./fixtures/pin-resource.html?raw";
import recommendationHeavyHtml from "./fixtures/recommendation-heavy.html?raw";
import sparseHtml from "./fixtures/sparse.html?raw";
import {
  PINTEREST_ADAPTER_VERSION,
  pinterestPageCaptureAdapter,
  pinIdFromUrl,
} from ".";

const registry = new PageCaptureAdapterRegistry([pinterestPageCaptureAdapter]);

function context(html: string, url: string, selectionText = ""): PageCaptureContext {
  return {
    document: new JSDOM(html, { url }).window.document,
    location: toPageCaptureLocation(url),
    selectionText,
  };
}

function capture(html: string, url: string, selectionText = "") {
  return registry.capture(context(html, url, selectionText));
}

function mediaContractItem(result: ReturnType<typeof capture>): FECaptureMediaItem {
  const media = result.media[0];
  if (!media) throw new Error("Expected a Pinterest media observation.");
  return {
    client_media_id: media.candidateId,
    ...(media.providerMediaId ? { provider_media_id: media.providerMediaId } : {}),
    kind: media.kind,
    role: media.role,
    position: media.position,
    source_locator: result.identity.canonicalUrl,
    ...(media.altText ? { alt_text: media.altText } : {}),
    default_custody: "mirror",
    variants: media.variants.map((variant) => ({
      client_variant_id: variant.variantId,
      kind: variant.kind === "stream_reference" || variant.kind === "other" ? "original" : variant.kind,
      source_url: variant.sourceUrl,
      transfer_preference: variant.acquisitionPreference,
      requested_custody: "mirror",
      ...(variant.mimeType ? { mime_type: variant.mimeType } : {}),
      ...(variant.width ? { width: variant.width } : {}),
      ...(variant.height ? { height: variant.height } : {}),
    })),
  };
}

describe("Pinterest pin identity", () => {
  it.each([
    ["https://www.pinterest.com/pin/104427285104852001/", "104427285104852001"],
    ["https://m.pinterest.com/pin/220113869289381002/?invite_code=sanitized", "220113869289381002"],
    [
      "https://www.pinterest.co.uk/pin/quiet-reading-nook--774056873529403003/",
      "774056873529403003",
    ],
    ["https://pinterest.com/pin/330224222818734004/sent/", "330224222818734004"],
  ])("extracts a stable ID from %s", (url, expectedId) => {
    expect(pinIdFromUrl(url)).toBe(expectedId);
  });

  it("normalizes supported host and slug forms to the stable canonical URL", () => {
    const result = capture(
      alternatePageStateHtml,
      "https://www.pinterest.co.uk/pin/quiet-reading-nook--774056873529403003/?utm_source=share",
    );

    expect(result.adapter).toEqual({
      id: "pinterest",
      version: PINTEREST_ADAPTER_VERSION,
      matchConfidence: 100,
      usedGenericFallback: false,
    });
    expect(result.identity).toEqual({
      submittedUrl:
        "https://www.pinterest.co.uk/pin/quiet-reading-nook--774056873529403003/?utm_source=share",
      canonicalUrl: "https://www.pinterest.com/pin/774056873529403003/",
      provider: "pinterest",
      providerItemId: "774056873529403003",
      segmentKey: "root",
    });
  });

  it("does not claim non-pin Pinterest pages or lookalike hosts", () => {
    expect(
      registry.select(
        context("<html><title>Search</title></html>", "https://www.pinterest.com/search/pins/?q=herbs"),
      ).adapter.id,
    ).toBe("generic");
    expect(pinIdFromUrl("https://pinterest.example.invalid/pin/104427285104852001/")).toBeUndefined();
  });
});

describe("Pinterest complete capture", () => {
  it("preserves generic readable content and annotations while adding attributed pin metadata", () => {
    const result = capture(
      completeHtml,
      "https://page-user:page-password@m.pinterest.com/pin/104427285104852001/?utm_source=share&access_token=page-query-secret#section=details&session_token=page-fragment-secret",
      "  Keep the drainage advice.  ",
    );

    expect(result.identity.submittedUrl).toBe(
      "https://m.pinterest.com/pin/104427285104852001/?utm_source=share#section=details",
    );
    expect(JSON.stringify(result)).not.toContain("page-user:page-password");
    expect(JSON.stringify(result)).not.toContain("page-query-secret");
    expect(JSON.stringify(result)).not.toContain("page-fragment-secret");
    expect(result.document.title).toBe("Window herb garden");
    expect(result.document.contentMarkdown).toContain("containers with drainage");
    expect(result.document.selection).toBe("Keep the drainage advice.");
    expect(result.document.description).toBe("A compact herb garden for a sunny kitchen window.");

    expect(result.extensions).toEqual([
      {
        namespace: "provider.pinterest",
        schemaVersion: "1",
        data: {
          pinId: "104427285104852001",
          parserVersion: "pws-data.v1",
          title: "Window herb garden",
          description: "A compact herb garden for a sunny kitchen window.",
          creator: {
            displayName: "Avery Example",
            username: "avery_example",
            profileUrl: "https://m.pinterest.com/avery_example/?tab=pins#view=grid",
          },
          board: {
            name: "Small-space gardens",
            url: "https://m.pinterest.com/avery_example/small-space-gardens/?sort=recent#layout=compact",
          },
          providerTags: ["Container Gardening", "Fresh Herbs"],
          fieldSources: {
            title: "serialized_page_state",
            description: "serialized_page_state",
            creator: "serialized_page_state",
            board: "serialized_page_state",
            providerTags: ["serialized_page_state"],
          },
          primaryMediaObservation: {
            original: {
              observedFrom: "serialized_page_state",
              sourceLocator: "pin.images.orig",
            },
            thumbnail: {
              observedFrom: "serialized_page_state",
              sourceLocator: "pin.images.236x",
            },
          },
        },
      },
    ]);

    const payload = buildPayload(result, {
      title: result.document.title,
      tags: ["link", "garden"],
      note: "Try this layout in spring.",
    });
    expect(payload.tags).toEqual(["link", "garden"]);
    expect(payload.selection).toBe("Keep the drainage advice.");
    expect(payload.content).toContain("> **Note:** Try this layout in spring.");
    expect(payload.content).toContain("containers with drainage");
  });

  it("selects the largest observed target source and a genuine smaller thumbnail", () => {
    const result = capture(
      completeHtml,
      "https://www.pinterest.com/pin/104427285104852001/",
    );
    const media = result.media[0];

    expect(media).toMatchObject({
      candidateId: "pinterest:104427285104852001:primary",
      providerMediaId: "104427285104852001",
      kind: "image",
      role: "primary",
      position: 0,
      altText: "Herbs growing in small pots by a window",
    });
    expect(media?.variants).toEqual([
      {
        variantId: "pinterest:104427285104852001:original",
        kind: "original",
        sourceUrl:
          "https://i.pinimg.com/originals/aa/bb/cc/window-herbs.jpg?width=1200#quality=full",
        acquisitionPreference: "browser_preferred",
        mimeType: "image/jpeg",
        width: 1200,
        height: 1800,
      },
      {
        variantId: "pinterest:104427285104852001:thumbnail",
        kind: "thumbnail",
        sourceUrl:
          "https://i.pinimg.com/236x/aa/bb/cc/window-herbs.jpg?width=236#crop=center",
        acquisitionPreference: "browser_preferred",
        mimeType: "image/jpeg",
        width: 236,
        height: 354,
      },
    ]);
    expect(result.capabilities.observed).toEqual(
      expect.arrayContaining([
        "body",
        "gallery_manifest",
        "original_media",
        "thumbnail_or_poster",
        "provider_metadata",
      ]),
    );
    expect(feContractValidators.captureMediaItem(mediaContractItem(result))).toBe(true);
  });

  it("does not serialize credentials, raw page state, or unrelated recommendation records", () => {
    const result = capture(
      completeHtml,
      "https://www.pinterest.com/pin/104427285104852001/",
    );
    const serialized = JSON.stringify(result);

    expect(serialized).not.toContain("fixture-access-token-must-not-escape");
    expect(serialized).not.toContain("fixture-csrf-must-not-escape");
    expect(serialized).not.toContain("fixture-cookie-must-not-escape");
    expect(serialized).not.toContain("media-user");
    expect(serialized).not.toContain("media-password");
    expect(serialized).not.toContain("thumb-user");
    expect(serialized).not.toContain("thumb-password");
    expect(serialized).not.toContain("profile-query-secret");
    expect(serialized).not.toContain("profile-fragment-secret");
    expect(serialized).not.toContain("board-query-secret");
    expect(serialized).not.toContain("board-fragment-secret");
    expect(serialized).not.toContain("media-query-secret");
    expect(serialized).not.toContain("media-fragment-secret");
    expect(serialized).not.toContain("thumb-query-secret");
    expect(serialized).not.toContain("thumb-fragment-secret");
    expect(serialized).not.toContain("recommendation-state.jpg");
    expect(serialized).not.toContain("999999999999999999");
  });
});

describe("Pinterest fixture resilience", () => {
  it("captures the current anonymous PinResource and close-up image shape", () => {
    const result = capture(
      pinResourceHtml,
      "https://www.pinterest.com/pin/104427285104852001/",
    );

    expect(result.media[0]?.variants).toEqual([
      expect.objectContaining({
        kind: "original",
        sourceUrl: "https://i.pinimg.com/originals/85/ad/aa/85adaaac27716eb19fa698081874b513.jpg",
        width: 1511,
        height: 1511,
      }),
      expect.objectContaining({
        kind: "thumbnail",
        sourceUrl: "https://i.pinimg.com/236x/85/ad/aa/85adaaac27716eb19fa698081874b513.jpg",
        width: 236,
        height: 236,
      }),
    ]);
    expect(result.extensions[0]?.data).toMatchObject({
      description: "Christmas Decor | Antique Farmhouse",
      creator: { displayName: "Julie Kline", username: "jkline78" },
      board: { name: "Christmas" },
    });
    expect(JSON.stringify(result)).not.toContain("recommendation.jpg");
    expect(result.warnings.map((item) => item.code)).not.toContain(
      "pinterest.page_state_unrecognized",
    );
  });

  it("normalizes target-scoped JSON-LD without requiring serialized page state", () => {
    const result = capture(
      jsonLdHtml,
      "https://www.pinterest.com/pin/660558998124037007/",
    );

    expect(result.media[0]?.variants).toEqual([
      expect.objectContaining({
        kind: "original",
        sourceUrl: "https://i.pinimg.com/originals/aa/00/77/woven.jpg",
        width: 1024,
        height: 1536,
        mimeType: "image/jpeg",
      }),
      expect.objectContaining({
        kind: "thumbnail",
        sourceUrl: "https://i.pinimg.com/236x/aa/00/77/woven.jpg",
        width: 236,
        height: 354,
      }),
    ]);
    expect(result.extensions[0]?.data).toMatchObject({
      title: "Woven wall hanging",
      description: "A neutral woven wall hanging.",
      creator: { displayName: "Taylor Example", username: "taylor_makes" },
      board: { name: "Textile ideas" },
      providerTags: ["Weaving", "Wall Decor"],
      fieldSources: {
        title: "json_ld",
        description: "json_ld",
        creator: "json_ld",
        board: "json_ld",
        providerTags: ["json_ld"],
      },
    });
  });

  it("captures an alternate serialized page-state shape without losing stable media", () => {
    const result = capture(
      alternatePageStateHtml,
      "https://www.pinterest.co.uk/pin/quiet-reading-nook--774056873529403003/",
    );

    expect(result.media[0]?.variants).toEqual([
      expect.objectContaining({
        kind: "original",
        sourceUrl: "https://i.pinimg.com/originals/dd/ee/ff/reading-nook.png",
        width: 1600,
        height: 1200,
        mimeType: "image/png",
      }),
      expect.objectContaining({
        kind: "thumbnail",
        sourceUrl: "https://i.pinimg.com/236x/dd/ee/ff/reading-nook.png",
        width: 236,
        height: 177,
      }),
    ]);
    expect(result.extensions[0]?.data).toMatchObject({
      title: "Quiet reading nook",
      creator: { displayName: "Morgan Example", username: "morgan_reads" },
      board: { name: "Home library" },
      providerTags: ["Reading Nook", "Bookshelves"],
    });
    expect(result.warnings.map((item) => item.code)).not.toContain(
      "pinterest.page_state_unrecognized",
    );
  });

  it("ignores larger recommendation, related-pin, card, and avatar images", () => {
    const result = capture(
      recommendationHeavyHtml,
      "https://www.pinterest.com/pin/330224222818734004/",
    );
    const serializedMedia = JSON.stringify(result.media);

    expect(result.media[0]?.variants[0]).toMatchObject({
      kind: "original",
      sourceUrl: "https://i.pinimg.com/originals/11/22/33/target-glaze.jpg",
      width: 900,
      height: 1200,
    });
    expect(serializedMedia).not.toContain("huge-rec");
    expect(serializedMedia).not.toContain("huge-related");
    expect(serializedMedia).not.toContain("avatars");
  });

  it("keeps sparse pins useful and reports missing optional observations", () => {
    const result = capture(
      sparseHtml,
      "https://www.pinterest.com/pin/220113869289381002/",
      "Sparse highlight",
    );

    expect(result.adapter.id).toBe("pinterest");
    expect(result.document.contentMarkdown).toContain("sparse pin page");
    expect(result.document.selection).toBe("Sparse highlight");
    expect(result.media).toEqual([]);
    expect(result.warnings).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ code: "pinterest.primary_media_missing", stage: "media" }),
        expect.objectContaining({ code: "pinterest.metadata_sparse", stage: "provider_metadata" }),
      ]),
    );
  });

  it("returns partial Open Graph observations when serialized sources are malformed", () => {
    const result = capture(
      partialHtml,
      "https://www.pinterest.com/pin/440336776902815005/",
    );

    expect(result.media[0]?.variants).toEqual([
      {
        variantId: "pinterest:440336776902815005:original",
        kind: "original",
        sourceUrl: "https://i.pinimg.com/originals/44/55/66/partial.webp",
        acquisitionPreference: "browser_preferred",
        mimeType: "image/webp",
      },
    ]);
    expect(result.warnings).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ code: "pinterest.page_state_invalid" }),
        expect.objectContaining({ code: "pinterest.json_ld_invalid" }),
        expect.objectContaining({ code: "pinterest.thumbnail_missing" }),
        expect.objectContaining({ code: "pinterest.original_dimensions_missing" }),
      ]),
    );
    expect(JSON.stringify(result.warnings)).not.toContain("unfinished");
  });

  it("tolerates a changed page-state shape and falls back to explicit close-up DOM media", () => {
    const result = capture(
      changedPageStateHtml,
      "https://www.pinterest.com/pin/550447887013926006/",
    );

    expect(result.media[0]?.variants).toEqual([
      expect.objectContaining({
        kind: "original",
        sourceUrl: "https://i.pinimg.com/736x/77/88/99/changed.jpg",
        width: 736,
        height: 1104,
      }),
    ]);
    expect(result.warnings).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ code: "pinterest.page_state_unrecognized" }),
      ]),
    );
  });

  it("never manufactures an upscaled original or thumbnail URL", () => {
    const result = capture(
      changedPageStateHtml,
      "https://www.pinterest.com/pin/550447887013926006/",
    );
    const variants = result.media[0]?.variants ?? [];

    expect(variants).toHaveLength(1);
    expect(variants[0]?.sourceUrl).toBe("https://i.pinimg.com/736x/77/88/99/changed.jpg");
    expect(JSON.stringify(variants)).not.toContain("/originals/");
  });
});
