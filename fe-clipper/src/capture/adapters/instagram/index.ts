import { genericPageCaptureAdapter } from "../generic";
import type {
  AdapterCapability,
  AdapterDocumentObservation,
  AdapterExtractionWarning,
  AdapterMediaCandidate,
  AdapterMediaVariantCandidate,
  AdapterMetadataValue,
  AdapterProviderMetadataExtension,
  AdapterStageResult,
  PageCaptureAdapter,
  PageCaptureContext,
} from "../../types";
import { ADAPTER_MATCH_CONFIDENCE } from "../../types";
import {
  matchInstagramPostUrl,
  parseInstagramPage,
  sanitizeInstagramPageUrl,
  type InstagramMediaObservation,
  type InstagramParserResult,
} from "./parsers";

export const INSTAGRAM_ADAPTER_VERSION = "1.0.0";

const INSTAGRAM_CAPABILITIES = [
  "canonical_identity",
  "title",
  "description",
  "body",
  "selection",
  "gallery_manifest",
  "original_media",
  "thumbnail_or_poster",
  "provider_metadata",
] as const satisfies readonly AdapterCapability[];

interface CachedInstagramObservation {
  pageUrl: string;
  result: InstagramParserResult;
}

const observationCache = new WeakMap<Document, CachedInstagramObservation>();

function observation(context: PageCaptureContext): InstagramParserResult {
  const cached = observationCache.get(context.document);
  if (cached?.pageUrl === context.location.href) return cached.result;
  const result = parseInstagramPage(context.document, context.location.href);
  observationCache.set(context.document, { pageUrl: context.location.href, result });
  return result;
}

function canonicalUrl(shortcode: string, routeKind: "post" | "reel" | undefined): string {
  const route = routeKind === "reel" ? "reel" : "p";
  return `https://www.instagram.com/${route}/${shortcode}/`;
}

function identifierPart(value: string): string {
  return value.replace(/[^A-Za-z0-9._-]/g, "_").slice(0, 96) || "unknown";
}

function clientMediaId(
  shortcode: string,
  media: InstagramMediaObservation,
): string {
  const providerPart = media.providerMediaId
    ? identifierPart(media.providerMediaId)
    : `position-${media.position}`;
  return `instagram:${identifierPart(shortcode)}:${providerPart}`;
}

function observedExpiry(sourceUrl: string): string | undefined {
  try {
    const url = new URL(sourceUrl);
    const decimal = url.searchParams.get("expires") ?? url.searchParams.get("expires_at");
    const oe = url.searchParams.get("oe");
    const epochSeconds = decimal && /^\d{10}$/.test(decimal)
      ? Number(decimal)
      : oe && /^[a-f0-9]{8}$/i.test(oe)
        ? Number.parseInt(oe, 16)
        : undefined;
    return epochSeconds && Number.isFinite(epochSeconds)
      ? new Date(epochSeconds * 1000).toISOString()
      : undefined;
  } catch {
    return undefined;
  }
}

function observedImageMime(sourceUrl: string): string | undefined {
  try {
    const pathname = new URL(sourceUrl).pathname.toLowerCase();
    if (pathname.endsWith(".jpg") || pathname.endsWith(".jpeg")) return "image/jpeg";
    if (pathname.endsWith(".png")) return "image/png";
    if (pathname.endsWith(".webp")) return "image/webp";
    if (pathname.endsWith(".avif")) return "image/avif";
  } catch {
    // Source URLs have already been normalized by the parser. Omit rather
    // than inventing a MIME observation if an unexpected value reaches here.
  }
  return undefined;
}

function variant(
  mediaId: string,
  kind: AdapterMediaVariantCandidate["kind"],
  sourceUrl: string,
  acquisitionPreference: AdapterMediaVariantCandidate["acquisitionPreference"],
  details: Pick<
    InstagramMediaObservation,
    "mimeType" | "width" | "height" | "durationSeconds"
  > = {},
): AdapterMediaVariantCandidate {
  const expiresAt = observedExpiry(sourceUrl);
  return {
    variantId: `${mediaId}:${kind}`,
    kind,
    sourceUrl,
    acquisitionPreference,
    ...(details.mimeType ? { mimeType: details.mimeType } : {}),
    ...(details.width ? { width: details.width } : {}),
    ...(details.height ? { height: details.height } : {}),
    ...(details.durationSeconds ? { durationSeconds: details.durationSeconds } : {}),
    ...(expiresAt ? { expiresAt } : {}),
  };
}

function variantsForMedia(
  mediaId: string,
  item: InstagramMediaObservation,
): AdapterMediaVariantCandidate[] {
  const variants: AdapterMediaVariantCandidate[] = [];
  if (item.kind === "image") {
    if (item.originalUrl) {
      variants.push(
        variant(mediaId, "original", item.originalUrl, "browser_preferred", {
          ...(item.mimeType ? { mimeType: item.mimeType } : {}),
          ...(item.width ? { width: item.width } : {}),
          ...(item.height ? { height: item.height } : {}),
        }),
      );
    }
    if (item.thumbnailUrl) {
      variants.push(variant(mediaId, "thumbnail", item.thumbnailUrl, "browser_preferred"));
    }
  }
  if (item.kind === "video") {
    if (item.streamUrl) {
      variants.push(
        variant(mediaId, "stream_reference", item.streamUrl, "reference_only", {
          ...(item.mimeType ? { mimeType: item.mimeType } : {}),
          ...(item.width ? { width: item.width } : {}),
          ...(item.height ? { height: item.height } : {}),
          ...(item.durationSeconds ? { durationSeconds: item.durationSeconds } : {}),
        }),
      );
    }
    if (item.posterUrl) {
      const posterMime = observedImageMime(item.posterUrl);
      variants.push(
        variant(mediaId, "poster", item.posterUrl, "browser_preferred", {
          ...(posterMime ? { mimeType: posterMime } : {}),
          ...(item.width ? { width: item.width } : {}),
          ...(item.height ? { height: item.height } : {}),
        }),
      );
    }
  }
  return variants;
}

function mediaWarnings(items: readonly InstagramMediaObservation[]): AdapterExtractionWarning[] {
  if (items.length === 0) {
    return [
      {
        code: "instagram.media_missing",
        severity: "warning",
        stage: "media",
        message: "No Instagram post media was observed; the readable page capture was retained.",
      },
    ];
  }

  const warnings: AdapterExtractionWarning[] = [];
  for (const item of items) {
    if (item.kind === "unsupported") {
      warnings.push({
        code: "instagram.media_item_unsupported",
        severity: "warning",
        stage: "media",
        message: `Instagram carousel item ${item.position} had an unsupported or incomplete media shape; its ordered placeholder was retained.`,
      });
      continue;
    }
    if (item.kind === "image" && !item.originalUrl) {
      warnings.push({
        code: "instagram.image_original_missing",
        severity: "warning",
        stage: "media",
        message: `Instagram image item ${item.position} had no observed original source URL.`,
      });
    }
    if (item.kind === "image" && !item.thumbnailUrl) {
      warnings.push({
        code: "instagram.image_thumbnail_missing",
        severity: "info",
        stage: "media",
        message: `Instagram image item ${item.position} had no distinct observed thumbnail.`,
      });
    }
    if (item.kind === "video" && !item.streamUrl) {
      warnings.push({
        code: "instagram.video_stream_missing",
        severity: "warning",
        stage: "media",
        message: `Instagram video item ${item.position} had no observed stream reference.`,
      });
    }
    if (item.kind === "video" && !item.posterUrl) {
      warnings.push({
        code: "instagram.video_poster_missing",
        severity: "info",
        stage: "media",
        message: `Instagram video item ${item.position} had no observed poster.`,
      });
    }
  }
  return warnings;
}

function observedDocumentCapabilities(
  document: AdapterDocumentObservation,
): readonly AdapterCapability[] {
  const capabilities: AdapterCapability[] = [];
  if (document.title) capabilities.push("title");
  if (document.description) capabilities.push("description");
  if (document.contentMarkdown) capabilities.push("body");
  if (document.selection) capabilities.push("selection");
  return capabilities;
}

function providerMetadata(result: InstagramParserResult): AdapterProviderMetadataExtension {
  const page = result.observation;
  const data: Record<string, AdapterMetadataValue> = {
    ...(page.shortcode ? { shortcode: page.shortcode } : {}),
    ...(page.routeKind ? { content_kind: page.routeKind } : {}),
    ...(page.caption ? { caption: page.caption } : {}),
    ...(page.authorUsername || page.authorName
      ? {
          author: {
            ...(page.authorUsername ? { username: page.authorUsername } : {}),
            ...(page.authorName ? { display_name: page.authorName } : {}),
          },
        }
      : {}),
    ...(page.publishedAt ? { published_at: page.publishedAt } : {}),
    ...(page.hashtags.length > 0 ? { hashtags: page.hashtags } : {}),
    media_count: page.media.length,
    observation_sources: page.sources,
  };
  return {
    namespace: "provider.instagram",
    schemaVersion: "1.0.0",
    data,
  };
}

/** Versioned Instagram post/reel extraction; registration remains integration-owned. */
export const instagramPageCaptureAdapter: PageCaptureAdapter = {
  id: "instagram",
  version: INSTAGRAM_ADAPTER_VERSION,

  matches(context) {
    return matchInstagramPostUrl(context.location.href)
      ? ADAPTER_MATCH_CONFIDENCE.exact
      : ADAPTER_MATCH_CONFIDENCE.none;
  },

  identify(context) {
    const parsed = observation(context);
    const shortcode = parsed.observation.shortcode;
    const submittedUrl = sanitizeInstagramPageUrl(context.location.href);
    if (!shortcode) {
      return {
        observation: {
          submittedUrl,
          canonicalUrl: submittedUrl,
          provider: "instagram",
          segmentKey: "root",
        },
        warnings: [
          ...parsed.warnings,
          {
            code: "instagram.identity_missing",
            severity: "error",
            stage: "identity",
            message: "The Instagram post or reel shortcode could not be resolved.",
          },
        ],
      };
    }
    return {
      observation: {
        submittedUrl,
        canonicalUrl: canonicalUrl(shortcode, parsed.observation.routeKind),
        provider: "instagram",
        providerItemId: shortcode,
        segmentKey: "root",
      },
      observedCapabilities: ["canonical_identity"],
      warnings: parsed.warnings,
    };
  },

  extractDocument(context): AdapterStageResult<AdapterDocumentObservation> {
    const sanitizedContext = {
      ...context,
      location: {
        ...context.location,
        href: sanitizeInstagramPageUrl(context.location.href),
      },
    };
    const generic = genericPageCaptureAdapter.extractDocument(sanitizedContext);
    const caption = observation(context).observation.caption;
    const document = {
      ...generic.observation,
      description: caption ?? generic.observation.description,
    };
    return {
      observation: document,
      observedCapabilities: observedDocumentCapabilities(document),
      warnings: generic.warnings,
    };
  },

  extractMedia(context): AdapterStageResult<readonly AdapterMediaCandidate[]> {
    const items = observation(context).observation.media;
    const shortcode = observation(context).observation.shortcode ?? "unknown";
    const role = items.length > 1 ? "gallery_item" : "primary";
    const media = items.map((item) => {
      const candidateId = clientMediaId(shortcode, item);
      return {
        candidateId,
        ...(item.providerMediaId ? { providerMediaId: item.providerMediaId } : {}),
        kind: item.kind === "unsupported" ? "other" : item.kind,
        role,
        position: item.position,
        ...(item.altText ? { altText: item.altText } : {}),
        variants: variantsForMedia(candidateId, item),
      } satisfies AdapterMediaCandidate;
    });
    const observedCapabilities: AdapterCapability[] = [];
    if (items.length > 0) observedCapabilities.push("gallery_manifest");
    if (media.some((item) => item.variants.some((candidate) =>
      candidate.kind === "original" || candidate.kind === "stream_reference"))) {
      observedCapabilities.push("original_media");
    }
    if (media.some((item) => item.variants.some((candidate) =>
      candidate.kind === "thumbnail" || candidate.kind === "poster"))) {
      observedCapabilities.push("thumbnail_or_poster");
    }
    return {
      observation: media,
      observedCapabilities,
      warnings: mediaWarnings(items),
    };
  },

  extractProviderMetadata(context) {
    const parsed = observation(context);
    return {
      observation: [providerMetadata(parsed)],
      observedCapabilities: ["provider_metadata"],
    };
  },

  reportCapabilities() {
    return INSTAGRAM_CAPABILITIES;
  },
};
