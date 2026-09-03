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
import { extractPage } from "@/lib/extractPage";

const GENERIC_CAPABILITIES = [
  "canonical_identity",
  "title",
  "description",
  "body",
  "selection",
] as const satisfies readonly AdapterCapability[];

function canonicalUrl(context: PageCaptureContext): string {
  const submittedUrl = context.location.href;
  const observedCanonical = context.document
    .querySelector('link[rel~="canonical"]')
    ?.getAttribute("href")
    ?.trim();

  if (!observedCanonical) return submittedUrl;

  try {
    const resolved = new URL(observedCanonical, submittedUrl);
    return resolved.protocol === "http:" || resolved.protocol === "https:"
      ? resolved.href
      : submittedUrl;
  } catch {
    return submittedUrl;
  }
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
  version: "1.0.0",

  matches() {
    // The registry invokes this adapter as a fallback, not as a competitor.
    return ADAPTER_MATCH_CONFIDENCE.none;
  },

  identify(context): AdapterStageResult<AdapterCanonicalIdentityObservation> {
    return {
      observation: {
        submittedUrl: context.location.href,
        canonicalUrl: canonicalUrl(context),
        provider: "web",
        segmentKey: "root",
      },
      observedCapabilities: ["canonical_identity"],
    };
  },

  extractDocument(context): AdapterStageResult<AdapterDocumentObservation> {
    const document = extractPage(context.document, {
      url: context.location.href,
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

  extractMedia(): AdapterStageResult<readonly AdapterMediaCandidate[]> {
    return { observation: [] };
  },

  extractProviderMetadata(): AdapterStageResult<readonly AdapterProviderMetadataExtension[]> {
    return { observation: [] };
  },

  reportCapabilities() {
    return GENERIC_CAPABILITIES;
  },
};
