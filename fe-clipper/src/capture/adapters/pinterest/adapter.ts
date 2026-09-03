import { genericPageCaptureAdapter } from "../generic";
import type {
  AdapterCapability,
  AdapterExtractionWarning,
  AdapterMediaCandidate,
  AdapterMediaVariantCandidate,
  AdapterMetadataValue,
  AdapterProviderMetadataExtension,
  AdapterStageResult,
  PageCaptureAdapter,
} from "../../types";
import { ADAPTER_MATCH_CONFIDENCE } from "../../types";
import {
  PINTEREST_PAGE_STATE_PARSER_VERSION,
  canonicalPinterestPinUrl,
  inspectPinterestPage,
  resolvePinterestPinId,
  sanitizePinterestPageUrl,
  type PinterestImageObservation,
  type PinterestPageObservation,
} from "./parser";

export const PINTEREST_ADAPTER_VERSION = "1.0.0";
export const PINTEREST_EXTENSION_SCHEMA_VERSION = "1";

const PINTEREST_CAPABILITIES = [
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

function mediaWarning(
  code: string,
  severity: AdapterExtractionWarning["severity"],
  message: string,
): AdapterExtractionWarning {
  return { code, severity, stage: "media", message };
}

function compareKnownSize(
  left: PinterestImageObservation,
  right: PinterestImageObservation,
): number {
  if (left.width && left.height && right.width && right.height) {
    return left.width * left.height - right.width * right.height;
  }
  if (left.width && right.width && left.width !== right.width) return left.width - right.width;
  if (left.height && right.height && left.height !== right.height) return left.height - right.height;
  return 0;
}

function deterministicImageOrder(
  left: PinterestImageObservation,
  right: PinterestImageObservation,
): number {
  const knownSize = compareKnownSize(right, left);
  if (knownSize) return knownSize;
  const leftCompleteness = Number(Boolean(left.width)) + Number(Boolean(left.height));
  const rightCompleteness = Number(Boolean(right.width)) + Number(Boolean(right.height));
  if (leftCompleteness !== rightCompleteness) return rightCompleteness - leftCompleteness;
  return `${left.sourceLocator}\0${left.url}`.localeCompare(`${right.sourceLocator}\0${right.url}`);
}

function definitelySmaller(
  candidate: PinterestImageObservation,
  original: PinterestImageObservation,
): boolean {
  if (candidate.width && candidate.height && original.width && original.height) {
    return candidate.width * candidate.height < original.width * original.height;
  }
  if (candidate.width && original.width) return candidate.width < original.width;
  if (candidate.height && original.height) return candidate.height < original.height;
  return false;
}

export interface PinterestSelectedImages {
  original?: PinterestImageObservation;
  thumbnail?: PinterestImageObservation;
}

/** Selects only observed URLs. It never rewrites a CDN path or invents a size. */
export function selectPinterestImages(
  images: readonly PinterestImageObservation[],
): PinterestSelectedImages {
  if (!images.length) return {};

  const explicitOriginals = images
    .filter((image) => image.sizeHint === "original")
    .sort(deterministicImageOrder);
  let original = explicitOriginals[0] ?? [...images].sort(deterministicImageOrder)[0];
  if (!original) return {};

  // An explicitly observed original is preferred unless another source is
  // provably larger from comparable observed dimensions.
  for (const image of images) {
    if (compareKnownSize(image, original) > 0) original = image;
  }

  const smaller = images.filter(
    (image) => image.url !== original.url && definitelySmaller(image, original),
  );
  const explicitThumbnails = smaller
    .filter((image) => image.sizeHint === "thumbnail")
    .sort(deterministicImageOrder);
  const thumbnail = explicitThumbnails[0] ?? smaller.sort(deterministicImageOrder)[0];
  return { original, ...(thumbnail ? { thumbnail } : {}) };
}

function variantFromImage(
  pinId: string,
  kind: "original" | "thumbnail",
  image: PinterestImageObservation,
): AdapterMediaVariantCandidate {
  return {
    variantId: `pinterest:${pinId}:${kind}`,
    kind,
    sourceUrl: image.url,
    acquisitionPreference: "browser_preferred",
    ...(image.mimeType ? { mimeType: image.mimeType } : {}),
    ...(image.width ? { width: image.width } : {}),
    ...(image.height ? { height: image.height } : {}),
  };
}

function mediaStage(
  pinId: string,
  snapshot: PinterestPageObservation,
): AdapterStageResult<readonly AdapterMediaCandidate[]> {
  const selected = selectPinterestImages(snapshot.images);
  if (!selected.original) {
    return {
      observation: [],
      warnings: [
        mediaWarning(
          "pinterest.primary_media_missing",
          "warning",
          "No primary image source was observed for this Pinterest pin.",
        ),
      ],
    };
  }

  const warnings: AdapterExtractionWarning[] = [];
  if (!selected.thumbnail) {
    warnings.push(
      mediaWarning(
        "pinterest.thumbnail_missing",
        "info",
        "No genuinely smaller Pinterest image representation was observed.",
      ),
    );
  }
  if (!selected.original.width || !selected.original.height) {
    warnings.push(
      mediaWarning(
        "pinterest.original_dimensions_missing",
        "info",
        "The selected Pinterest original did not expose complete dimensions.",
      ),
    );
  }
  if (!selected.original.mimeType) {
    warnings.push(
      mediaWarning(
        "pinterest.original_mime_missing",
        "info",
        "The selected Pinterest original did not expose a MIME type.",
      ),
    );
  }

  const variants = [
    variantFromImage(pinId, "original", selected.original),
    ...(selected.thumbnail ? [variantFromImage(pinId, "thumbnail", selected.thumbnail)] : []),
  ];
  return {
    observation: [
      {
        candidateId: `pinterest:${pinId}:primary`,
        providerMediaId: pinId,
        kind: "image",
        role: "primary",
        position: 0,
        ...(selected.original.altText ? { altText: selected.original.altText } : {}),
        variants,
      },
    ],
    observedCapabilities: [
      "gallery_manifest",
      "original_media",
      ...(selected.thumbnail ? (["thumbnail_or_poster"] as const) : []),
    ],
    warnings,
  };
}

function providerMetadata(
  pinId: string,
  snapshot: PinterestPageObservation,
): AdapterStageResult<readonly AdapterProviderMetadataExtension[]> {
  const data: Record<string, AdapterMetadataValue> = {
    pinId,
    parserVersion: PINTEREST_PAGE_STATE_PARSER_VERSION,
  };
  const fieldSources: Record<string, AdapterMetadataValue> = {};

  if (snapshot.title) {
    data.title = snapshot.title.value;
    fieldSources.title = snapshot.title.source;
  }
  if (snapshot.description) {
    data.description = snapshot.description.value;
    fieldSources.description = snapshot.description.source;
  }
  if (snapshot.creator) {
    data.creator = {
      ...(snapshot.creator.displayName ? { displayName: snapshot.creator.displayName } : {}),
      ...(snapshot.creator.username ? { username: snapshot.creator.username } : {}),
      ...(snapshot.creator.profileUrl ? { profileUrl: snapshot.creator.profileUrl } : {}),
    };
    fieldSources.creator = snapshot.creator.source;
  }
  if (snapshot.board) {
    data.board = {
      ...(snapshot.board.name ? { name: snapshot.board.name } : {}),
      ...(snapshot.board.url ? { url: snapshot.board.url } : {}),
    };
    fieldSources.board = snapshot.board.source;
  }
  if (snapshot.providerTags.length) {
    data.providerTags = snapshot.providerTags;
    fieldSources.providerTags = snapshot.providerTagSources;
  }
  if (Object.keys(fieldSources).length) data.fieldSources = fieldSources;

  const selected = selectPinterestImages(snapshot.images);
  if (selected.original) {
    data.primaryMediaObservation = {
      original: {
        observedFrom: selected.original.source,
        sourceLocator: selected.original.sourceLocator,
      },
      ...(selected.thumbnail
        ? {
            thumbnail: {
              observedFrom: selected.thumbnail.source,
              sourceLocator: selected.thumbnail.sourceLocator,
            },
          }
        : {}),
    };
  }

  const missing = [
    !snapshot.title && "title",
    !snapshot.description && "description",
    !snapshot.creator && "creator",
    !snapshot.board && "board",
    !snapshot.providerTags.length && "provider tags",
  ].filter((field): field is string => Boolean(field));
  const warnings = [...snapshot.warnings];
  if (missing.length) {
    warnings.push({
      code: "pinterest.metadata_sparse",
      severity: "info",
      stage: "provider_metadata",
      message: `Optional Pinterest metadata was not observed: ${missing.join(", ")}.`,
    });
  }

  const hasProviderMetadata = missing.length < 5;
  return {
    observation: [
      {
        namespace: "provider.pinterest",
        schemaVersion: PINTEREST_EXTENSION_SCHEMA_VERSION,
        data,
      },
    ],
    observedCapabilities: hasProviderMetadata ? ["provider_metadata"] : [],
    warnings,
  };
}

export const pinterestPageCaptureAdapter: PageCaptureAdapter = {
  id: "pinterest",
  version: PINTEREST_ADAPTER_VERSION,

  matches(context) {
    return resolvePinterestPinId(context)
      ? ADAPTER_MATCH_CONFIDENCE.exact
      : ADAPTER_MATCH_CONFIDENCE.none;
  },

  identify(context) {
    const pinId = resolvePinterestPinId(context);
    if (!pinId) throw new Error("Pinterest pin identity was unavailable.");
    const submittedUrl = sanitizePinterestPageUrl(context.location.href);
    if (!submittedUrl) throw new Error("Pinterest page URL was unavailable.");
    return {
      observation: {
        submittedUrl,
        canonicalUrl: canonicalPinterestPinUrl(pinId),
        provider: "pinterest",
        providerItemId: pinId,
        segmentKey: "root",
      },
      observedCapabilities: ["canonical_identity"],
    };
  },

  extractDocument(context) {
    // Keep the established Readability/Turndown, description, and selection
    // behavior identical for provider pages.
    return genericPageCaptureAdapter.extractDocument(context);
  },

  extractMedia(context) {
    const pinId = resolvePinterestPinId(context);
    if (!pinId) return { observation: [] };
    return mediaStage(pinId, inspectPinterestPage(context, pinId));
  },

  extractProviderMetadata(context) {
    const pinId = resolvePinterestPinId(context);
    if (!pinId) return { observation: [] };
    return providerMetadata(pinId, inspectPinterestPage(context, pinId));
  },

  reportCapabilities() {
    return PINTEREST_CAPABILITIES;
  },
};
