import { genericPageCaptureAdapter } from "../generic";
import {
  ADAPTER_MATCH_CONFIDENCE,
  type AdapterCapability,
  type AdapterCanonicalIdentityObservation,
  type AdapterDocumentObservation,
  type AdapterExtractionWarning,
  type AdapterMediaCandidate,
  type AdapterMediaVariantCandidate,
  type AdapterMetadataValue,
  type AdapterProviderMetadataExtension,
  type AdapterStageResult,
  type PageCaptureAdapter,
  type PageCaptureContext,
} from "../../types";
import {
  canonicalYouTubeWatchUrl,
  extractYouTubeVideoId,
  parseYouTubePage,
  sanitizeYouTubePageUrl,
  YOUTUBE_PARSER_VERSION,
  type YouTubeCaptionTrackObservation,
  type YouTubePageObservation,
  type YouTubePosterObservation,
} from "./parser";

export const YOUTUBE_ADAPTER_VERSION = "1.0.0";

const YOUTUBE_CAPABILITIES = [
  "canonical_identity",
  "title",
  "description",
  "body",
  "selection",
  "gallery_manifest",
  "thumbnail_or_poster",
  "transcript",
  "provider_metadata",
] as const satisfies readonly AdapterCapability[];

const parsedPages = new WeakMap<Document, Map<string, YouTubePageObservation>>();

function videoId(context: PageCaptureContext): string {
  const id = extractYouTubeVideoId(context.location.href);
  if (!id) throw new Error("No stable YouTube video ID was present in the page URL.");
  return id;
}

function parse(context: PageCaptureContext): YouTubePageObservation {
  const id = videoId(context);
  let documentObservations = parsedPages.get(context.document);
  if (!documentObservations) {
    documentObservations = new Map();
    parsedPages.set(context.document, documentObservations);
  }
  const cached = documentObservations.get(id);
  if (cached) return cached;
  const observation = parseYouTubePage(context.document, id);
  documentObservations.set(id, observation);
  return observation;
}

function observedDocumentCapabilities(
  document: AdapterDocumentObservation,
): readonly AdapterCapability[] {
  const observed: AdapterCapability[] = [];
  if (document.title) observed.push("title");
  if (document.description) observed.push("description");
  if (document.contentMarkdown) observed.push("body");
  if (document.selection) observed.push("selection");
  return observed;
}

function parserWarnings(observation: YouTubePageObservation): AdapterExtractionWarning[] {
  return observation.warnings.map((warning) => ({
    code: warning.code,
    severity: "warning",
    stage: "provider_metadata",
    message: warning.message,
  }));
}

function slug(value: string): string {
  return (
    value
      .toLowerCase()
      .replace(/[^a-z0-9_-]+/g, "-")
      .replace(/^-+|-+$/g, "") || "unknown"
  );
}

function posterVariantId(poster: YouTubePosterObservation, used: Set<string>): string {
  const filename = new URL(poster.url).pathname.split("/").pop()?.replace(/\.[^.]+$/, "") ?? "poster";
  const base = `poster:${slug(filename)}`;
  if (!used.has(base)) {
    used.add(base);
    return base;
  }
  const dimensions = poster.width && poster.height ? `${poster.width}x${poster.height}` : "observed";
  let candidate = `${base}-${dimensions}`;
  let suffix = 2;
  while (used.has(candidate)) {
    candidate = `${base}-${dimensions}-${suffix}`;
    suffix += 1;
  }
  used.add(candidate);
  return candidate;
}

function posterVariants(
  posters: readonly YouTubePosterObservation[],
  usedIds: Set<string>,
): AdapterMediaVariantCandidate[] {
  return posters.map((poster) => ({
    variantId: posterVariantId(poster, usedIds),
    kind: "poster",
    sourceUrl: poster.url,
    acquisitionPreference: "browser_preferred",
    ...(poster.mimeType ? { mimeType: poster.mimeType } : {}),
    ...(poster.width ? { width: poster.width } : {}),
    ...(poster.height ? { height: poster.height } : {}),
  }));
}

function captionVariantId(track: YouTubeCaptionTrackObservation, used: Set<string>): string {
  const label = `${slug(track.languageCode)}-${slug(track.kind ?? "manual")}`;
  const base = `subtitles:${label}`;
  if (!used.has(base)) {
    used.add(base);
    return base;
  }
  let suffix = 2;
  while (used.has(`${base}-${suffix}`)) suffix += 1;
  const candidate = `${base}-${suffix}`;
  used.add(candidate);
  return candidate;
}

function sourceExpiry(url: string): string | undefined {
  const epochSeconds = Number.parseInt(new URL(url).searchParams.get("expire") ?? "", 10);
  if (!Number.isFinite(epochSeconds) || epochSeconds <= 0) return undefined;
  const expiresAt = new Date(epochSeconds * 1_000);
  return Number.isFinite(expiresAt.getTime()) ? expiresAt.toISOString() : undefined;
}

function captionVariants(
  tracks: readonly YouTubeCaptionTrackObservation[],
  usedIds: Set<string>,
): AdapterMediaVariantCandidate[] {
  return tracks.map((track) => {
    const expiresAt = sourceExpiry(track.url);
    return {
      variantId: captionVariantId(track, usedIds),
      kind: "subtitles",
      sourceUrl: track.url,
      acquisitionPreference: "browser_preferred",
      ...(track.mimeType ? { mimeType: track.mimeType } : {}),
      ...(expiresAt ? { expiresAt } : {}),
    };
  });
}

function mediaWarnings(observation: YouTubePageObservation): AdapterExtractionWarning[] {
  const warnings: AdapterExtractionWarning[] = [];
  if (!observation.posters.length) {
    warnings.push({
      code: "youtube.posters_unavailable",
      severity: "info",
      stage: "media",
      message: "No safe poster URL was available in the rendered YouTube page.",
    });
  }
  if (!observation.captionTracks.length && !observation.transcript) {
    warnings.push({
      code: "youtube.captions_unavailable",
      severity: "info",
      stage: "media",
      message: "No caption track or transcript was available in the rendered YouTube page.",
    });
  }
  return warnings;
}

function metadataData(videoId: string, observation: YouTubePageObservation) {
  const data: Record<string, AdapterMetadataValue> = {
    parser_version: YOUTUBE_PARSER_VERSION,
    video_id: videoId,
    observation_sources: observation.sources,
    ...(observation.title ? { title: observation.title } : {}),
    ...(observation.description ? { description: observation.description } : {}),
    ...(observation.channelId || observation.channelName
      ? {
          channel: {
            ...(observation.channelId ? { id: observation.channelId } : {}),
            ...(observation.channelName ? { name: observation.channelName } : {}),
          },
        }
      : {}),
    ...(observation.tags.length ? { tags: observation.tags } : {}),
    ...(observation.chapters.length
      ? {
          chapters: observation.chapters.map((chapter) => ({
            title: chapter.title,
            start_seconds: chapter.startSeconds,
          })),
        }
      : {}),
    ...(observation.durationSeconds ? { duration_seconds: observation.durationSeconds } : {}),
    ...(observation.publishedAt ? { published_at: observation.publishedAt } : {}),
    poster_count: observation.posters.length,
    ...(observation.captionTracks.length
      ? {
          caption_tracks: observation.captionTracks.map((track) => ({
            language_code: track.languageCode,
            name: track.name,
            kind: track.kind ?? "manual",
            is_translatable: track.isTranslatable,
          })),
        }
      : {}),
    ...(observation.transcript
      ? {
          transcript: {
            source: observation.transcript.source,
            ...(observation.transcript.languageCode
              ? { language_code: observation.transcript.languageCode }
              : {}),
            segments: observation.transcript.segments.map((segment) => ({
              start_seconds: segment.startSeconds,
              ...(segment.durationSeconds
                ? { duration_seconds: segment.durationSeconds }
                : {}),
              text: segment.text,
            })),
          },
        }
      : {}),
  };
  return data;
}

export const youtubePageCaptureAdapter: PageCaptureAdapter = {
  id: "youtube",
  version: YOUTUBE_ADAPTER_VERSION,

  matches(context) {
    return extractYouTubeVideoId(context.location.href)
      ? ADAPTER_MATCH_CONFIDENCE.exact
      : ADAPTER_MATCH_CONFIDENCE.none;
  },

  identify(context): AdapterStageResult<AdapterCanonicalIdentityObservation> {
    const id = videoId(context);
    return {
      observation: {
        submittedUrl: sanitizeYouTubePageUrl(context.location.href),
        canonicalUrl: canonicalYouTubeWatchUrl(id),
        provider: "youtube",
        providerItemId: id,
        segmentKey: "root",
      },
      observedCapabilities: ["canonical_identity"],
    };
  },

  extractDocument(context): AdapterStageResult<AdapterDocumentObservation> {
    const sanitizedHref = sanitizeYouTubePageUrl(context.location.href);
    const generic = genericPageCaptureAdapter.extractDocument({
      ...context,
      location: { ...context.location, href: sanitizedHref },
    });
    const observation = parse(context);
    const document = {
      ...generic.observation,
      title: observation.title ?? generic.observation.title,
      description: observation.description ?? generic.observation.description,
    };
    return {
      observation: document,
      observedCapabilities: observedDocumentCapabilities(document),
      warnings: generic.warnings,
    };
  },

  extractMedia(context): AdapterStageResult<readonly AdapterMediaCandidate[]> {
    const id = videoId(context);
    const observation = parse(context);
    const usedIds = new Set<string>();
    const variants: AdapterMediaVariantCandidate[] = [
      {
        variantId: "playback:youtube",
        kind: "stream_reference",
        sourceUrl: canonicalYouTubeWatchUrl(id),
        acquisitionPreference: "reference_only",
      },
      ...posterVariants(observation.posters, usedIds),
      ...captionVariants(observation.captionTracks, usedIds),
    ];

    return {
      observation: [
        {
          candidateId: `youtube:${id}`,
          providerMediaId: id,
          kind: "video",
          role: "primary",
          position: 0,
          ...(observation.title ? { altText: observation.title } : {}),
          variants,
        },
      ],
      observedCapabilities: [
        "gallery_manifest",
        ...(observation.posters.length ? (["thumbnail_or_poster"] as const) : []),
        ...(observation.captionTracks.length || observation.transcript
          ? (["transcript"] as const)
          : []),
      ],
      warnings: mediaWarnings(observation),
    };
  },

  extractProviderMetadata(
    context,
  ): AdapterStageResult<readonly AdapterProviderMetadataExtension[]> {
    const id = videoId(context);
    const observation = parse(context);
    const missingMetadata =
      !observation.title &&
      !observation.description &&
      !observation.channelId &&
      !observation.channelName &&
      !observation.tags.length &&
      !observation.chapters.length;
    return {
      observation: [
        {
          namespace: "provider.youtube",
          schemaVersion: "1",
          data: metadataData(id, observation),
        },
      ],
      observedCapabilities: ["provider_metadata"],
      warnings: [
        ...parserWarnings(observation),
        ...(missingMetadata
          ? [
              {
                code: "youtube.metadata_sparse",
                severity: "info" as const,
                stage: "provider_metadata" as const,
                message: "Only stable video identity was available from this YouTube page.",
              },
            ]
          : []),
      ],
    };
  },

  reportCapabilities() {
    return YOUTUBE_CAPABILITIES;
  },
};
