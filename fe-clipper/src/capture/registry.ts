import { genericPageCaptureAdapter } from "./adapters/generic";
import { instagramPageCaptureAdapter } from "./adapters/instagram";
import { pinterestPageCaptureAdapter } from "./adapters/pinterest";
import { redditPageCaptureAdapter } from "./adapters/reddit";
import { youtubePageCaptureAdapter } from "./adapters/youtube";
import {
  ADAPTER_MATCH_CONFIDENCE,
  type AdapterCapability,
  type AdapterExtractionWarning,
  type AdapterMatchConfidence,
  type AdapterStageResult,
  type PageCaptureAdapter,
  type PageCaptureContext,
  type PageCaptureLocation,
  type PageCaptureResult,
} from "./types";
import { sanitizeCapturePageUrl } from "./urlSafety";

const MATCH_CONFIDENCES = new Set<number>(Object.values(ADAPTER_MATCH_CONFIDENCE));

export interface AdapterSelection {
  adapter: PageCaptureAdapter;
  confidence: AdapterMatchConfidence;
  usedGenericFallback: boolean;
  warnings: readonly AdapterExtractionWarning[];
}

function uniqueCapabilities(results: readonly AdapterStageResult<unknown>[]): AdapterCapability[] {
  const capabilities = new Set<AdapterCapability>();
  for (const result of results) {
    for (const capability of result.observedCapabilities ?? []) {
      capabilities.add(capability);
    }
  }
  return [...capabilities];
}

function collectWarnings(results: readonly AdapterStageResult<unknown>[]): AdapterExtractionWarning[] {
  return results.flatMap((result) => result.warnings ?? []);
}

function isMatchConfidence(value: unknown): value is AdapterMatchConfidence {
  return typeof value === "number" && MATCH_CONFIDENCES.has(value);
}

function executeAdapter(
  adapter: PageCaptureAdapter,
  context: PageCaptureContext,
  confidence: AdapterMatchConfidence,
  usedGenericFallback: boolean,
  registryWarnings: readonly AdapterExtractionWarning[],
): PageCaptureResult {
  const identity = adapter.identify(context);
  const document = adapter.extractDocument(context);
  const media = adapter.extractMedia(context);
  const metadata = adapter.extractProviderMetadata(context);
  const stageResults: readonly AdapterStageResult<unknown>[] = [
    identity,
    document,
    media,
    metadata,
  ];

  return {
    adapter: {
      id: adapter.id,
      version: adapter.version,
      matchConfidence: confidence,
      usedGenericFallback,
    },
    identity: identity.observation,
    document: document.observation,
    media: media.observation,
    extensions: metadata.observation,
    capabilities: {
      declared: [...new Set(adapter.reportCapabilities())],
      observed: uniqueCapabilities(stageResults),
    },
    warnings: [...registryWarnings, ...collectWarnings(stageResults)],
  };
}

/**
 * Selects the strongest provider match and always retains a generic fallback.
 * Ties are resolved by adapter ID, so registration order cannot change which
 * adapter handles a page.
 */
export class PageCaptureAdapterRegistry {
  readonly #adapters: readonly PageCaptureAdapter[];
  readonly #genericAdapter: PageCaptureAdapter;

  constructor(
    adapters: readonly PageCaptureAdapter[] = [],
    genericAdapter: PageCaptureAdapter = genericPageCaptureAdapter,
  ) {
    const sortedAdapters = [...adapters].sort((left, right) => {
      if (left.id < right.id) return -1;
      if (left.id > right.id) return 1;
      return 0;
    });
    const ids = new Set<string>();

    for (const adapter of [genericAdapter, ...sortedAdapters]) {
      if (!adapter.id.trim()) throw new Error("Page capture adapters must have a non-empty ID.");
      if (!adapter.version.trim()) {
        throw new Error(`Page capture adapter "${adapter.id}" must have a version.`);
      }
      if (ids.has(adapter.id)) {
        throw new Error(`Duplicate page capture adapter ID: ${adapter.id}`);
      }
      ids.add(adapter.id);
    }

    this.#adapters = sortedAdapters;
    this.#genericAdapter = genericAdapter;
  }

  select(context: PageCaptureContext): AdapterSelection {
    let selected: PageCaptureAdapter | undefined;
    let selectedConfidence: AdapterMatchConfidence = ADAPTER_MATCH_CONFIDENCE.none;
    const warnings: AdapterExtractionWarning[] = [];

    for (const adapter of this.#adapters) {
      try {
        const confidence = adapter.matches(context);
        if (!isMatchConfidence(confidence)) {
          warnings.push({
            code: "registry.invalid_match_confidence",
            severity: "warning",
            stage: "matching",
            message: `Adapter "${adapter.id}" returned an unsupported match confidence.`,
          });
          continue;
        }

        if (confidence > selectedConfidence) {
          selected = adapter;
          selectedConfidence = confidence;
        }
      } catch {
        warnings.push({
          code: "registry.match_failed",
          severity: "warning",
          stage: "matching",
          message: `Adapter "${adapter.id}" could not inspect this page.`,
        });
      }
    }

    if (!selected) {
      return {
        adapter: this.#genericAdapter,
        confidence: ADAPTER_MATCH_CONFIDENCE.none,
        usedGenericFallback: true,
        warnings,
      };
    }

    return {
      adapter: selected,
      confidence: selectedConfidence,
      usedGenericFallback: false,
      warnings,
    };
  }

  capture(context: PageCaptureContext): PageCaptureResult {
    const selection = this.select(context);

    try {
      return executeAdapter(
        selection.adapter,
        context,
        selection.confidence,
        selection.usedGenericFallback,
        selection.warnings,
      );
    } catch (error) {
      if (selection.usedGenericFallback) throw error;

      return executeAdapter(
        this.#genericAdapter,
        context,
        ADAPTER_MATCH_CONFIDENCE.none,
        true,
        [
          ...selection.warnings,
          {
            code: "registry.adapter_extraction_failed",
            severity: "warning",
            stage: "capture",
            message: `Adapter "${selection.adapter.id}" could not extract this page; used the generic adapter.`,
          },
        ],
      );
    }
  }
}

export function toPageCaptureLocation(url: string): PageCaptureLocation {
  const parsed = new URL(url);
  return {
    href: parsed.href,
    protocol: parsed.protocol,
    hostname: parsed.hostname,
    pathname: parsed.pathname,
    search: parsed.search,
    hash: parsed.hash,
  };
}

export function createFailedPageCapture(
  context: PageCaptureContext,
  message: string,
): PageCaptureResult {
  const pageUrl = sanitizeCapturePageUrl(context.location.href);
  const title = context.document.title.trim() || pageUrl;
  const selection = context.selectionText.trim();

  return {
    adapter: {
      id: genericPageCaptureAdapter.id,
      version: genericPageCaptureAdapter.version,
      matchConfidence: ADAPTER_MATCH_CONFIDENCE.none,
      usedGenericFallback: true,
    },
    identity: {
      submittedUrl: pageUrl,
      canonicalUrl: pageUrl,
      provider: "web",
      segmentKey: "root",
    },
    document: {
      title,
      description: "",
      contentMarkdown: "",
      selection,
      usedFallback: true,
    },
    media: [],
    extensions: [],
    capabilities: {
      declared: genericPageCaptureAdapter.reportCapabilities(),
      observed: [
        "canonical_identity",
        ...(title ? (["title"] as const) : []),
        ...(selection ? (["selection"] as const) : []),
      ],
    },
    warnings: [
      {
        code: "capture.extraction_failed",
        severity: "error",
        stage: "capture",
        message,
      },
    ],
  };
}

export const pageCaptureAdapterRegistry = new PageCaptureAdapterRegistry([
  instagramPageCaptureAdapter,
  pinterestPageCaptureAdapter,
  redditPageCaptureAdapter,
  youtubePageCaptureAdapter,
]);
