import type {
  AdapterCapability,
  AdapterCanonicalIdentityObservation,
  AdapterDocumentObservation,
  AdapterMediaCandidate,
  AdapterProviderMetadataExtension,
  AdapterStageResult,
  PageCaptureAdapter,
  PageCaptureContext,
} from "../types";
import { ADAPTER_MATCH_CONFIDENCE } from "../types";
import { sanitizeCapturePageUrl } from "../urlSafety";
import { extractPage } from "@/lib/extractPage";
import { extractGenericMedia } from "./genericMedia";

const GENERIC_CAPABILITIES = [
  "canonical_identity",
  "title",
  "description",
  "body",
  "selection",
  "gallery_manifest",
  "original_media",
] as const satisfies readonly AdapterCapability[];

const TRACKING_QUERY_PARAMETER =
  /^(?:utm_.+|fbclid|gclid|dclid|msclkid|mc_cid|mc_eid|_ga)$/i;

function normalizedDocumentUrl(rawUrl: string, baseUrl: string): string | undefined {
  try {
    const resolved = new URL(rawUrl, baseUrl);
    if (resolved.protocol !== "http:" && resolved.protocol !== "https:") return undefined;
    if (resolved.username || resolved.password) return undefined;
    const sanitized = new URL(sanitizeCapturePageUrl(resolved.href));
    sanitized.hash = "";
    for (const key of [...sanitized.searchParams.keys()]) {
      if (TRACKING_QUERY_PARAMETER.test(key)) sanitized.searchParams.delete(key);
    }
    return sanitized.href;
  } catch {
    return undefined;
  }
}

function submittedUrl(context: PageCaptureContext): string {
  return sanitizeCapturePageUrl(context.location.href);
}

function canonicalUrl(context: PageCaptureContext): {
  url: string;
  ignoredObservedCanonical: boolean;
} {
  const submitted = submittedUrl(context);
  const observedCanonical = context.document
    .querySelector('link[rel~="canonical"]')
    ?.getAttribute("href")
    ?.trim();

  if (!observedCanonical) {
    return {
      url: normalizedDocumentUrl(submitted, submitted) ?? submitted,
      ignoredObservedCanonical: false,
    };
  }

  const normalizedCanonical = normalizedDocumentUrl(observedCanonical, submitted);
  return {
    url: normalizedCanonical ?? normalizedDocumentUrl(submitted, submitted) ?? submitted,
    ignoredObservedCanonical: normalizedCanonical === undefined,
  };
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

/** The always-available ordinary web-page extraction path. */
export const genericPageCaptureAdapter: PageCaptureAdapter = {
  id: "generic",
  version: "1.1.0",

  matches() {
    // The registry invokes this adapter as a fallback, not as a competitor.
    return ADAPTER_MATCH_CONFIDENCE.none;
  },

  identify(context): AdapterStageResult<AdapterCanonicalIdentityObservation> {
    const submitted = submittedUrl(context);
    const canonical = canonicalUrl(context);
    return {
      observation: {
        submittedUrl: submitted,
        canonicalUrl: canonical.url,
        provider: "web",
        segmentKey: "root",
      },
      observedCapabilities: ["canonical_identity"],
      warnings: canonical.ignoredObservedCanonical
        ? [
            {
              code: "generic.canonical_url_ignored",
              severity: "info",
              stage: "identity",
              message:
                "The page's canonical link was not a safe HTTP(S) URL; the submitted URL was used instead.",
            },
          ]
        : [],
    };
  },

  extractDocument(context): AdapterStageResult<AdapterDocumentObservation> {
    const document = extractPage(context.document, {
      url: submittedUrl(context),
      selectionText: context.selectionText,
    });

    return {
      observation: document,
      observedCapabilities: observedDocumentCapabilities(document),
      warnings: document.usedFallback
        ? [
            {
              code: "generic.readability_fallback",
              severity: "info",
              stage: "document",
              message: "No readable article region was found; captured the page body instead.",
            },
          ]
        : [],
    };
  },

  extractMedia(context): AdapterStageResult<readonly AdapterMediaCandidate[]> {
    return extractGenericMedia(context);
  },

  extractProviderMetadata(): AdapterStageResult<readonly AdapterProviderMetadataExtension[]> {
    return { observation: [] };
  },

  reportCapabilities() {
    return GENERIC_CAPABILITIES;
  },
};
