/**
 * Internal capture concepts shared by page adapters.
 *
 * These are extension-owned observations, not Fragments Engine transport
 * DTOs. The generated-contract layer maps and validates them at the network
 * boundary; adapters should never depend on an FE request shape directly.
 */

export const ADAPTER_MATCH_CONFIDENCE = {
  none: 0,
  possible: 25,
  likely: 75,
  exact: 100,
} as const;

export type AdapterMatchConfidence =
  (typeof ADAPTER_MATCH_CONFIDENCE)[keyof typeof ADAPTER_MATCH_CONFIDENCE];

export interface PageCaptureLocation {
  href: string;
  protocol: string;
  hostname: string;
  pathname: string;
  search: string;
  hash: string;
}

/** Live page inputs. DOM and provider page-state inspection stop here. */
export interface PageCaptureContext {
  document: Document;
  location: PageCaptureLocation;
  selectionText: string;
}

export interface AdapterCanonicalIdentityObservation {
  submittedUrl: string;
  canonicalUrl: string;
  provider: string;
  providerItemId?: string;
  segmentKey: string;
}

export interface AdapterDocumentObservation {
  title: string;
  description: string;
  contentMarkdown: string;
  selection: string;
  language?: string;
  /** True when Readability could not identify an article-shaped region. */
  usedFallback: boolean;
}

export type AdapterMediaKind = "image" | "video" | "audio" | "document" | "timed_text" | "other";

export type AdapterMediaRole =
  | "primary"
  | "gallery_item"
  | "hero"
  | "inline"
  | "poster"
  | "transcript"
  | "other";

export type AdapterMediaVariantKind =
  | "original"
  | "preview"
  | "thumbnail"
  | "poster"
  | "stream_reference"
  | "audio"
  | "subtitles"
  | "transcript"
  | "other";

export type AdapterMediaAcquisitionPreference = "browser_preferred" | "reference_only";

export interface AdapterMediaVariantCandidate {
  variantId: string;
  kind: AdapterMediaVariantKind;
  sourceUrl: string;
  acquisitionPreference: AdapterMediaAcquisitionPreference;
  mimeType?: string;
  width?: number;
  height?: number;
  durationSeconds?: number;
  byteSize?: number;
  expiresAt?: string;
}

export interface AdapterMediaCandidate {
  candidateId: string;
  providerMediaId?: string;
  kind: AdapterMediaKind;
  role: AdapterMediaRole;
  /** Zero-based source order. Adapters, not transport code, determine it. */
  position: number;
  altText?: string;
  caption?: string;
  /** Short semantic context for the attachment, such as its nearest heading. */
  sourceContext?: string;
  variants: readonly AdapterMediaVariantCandidate[];
}

export type AdapterMetadataValue =
  | null
  | boolean
  | number
  | string
  | readonly AdapterMetadataValue[]
  | { readonly [key: string]: AdapterMetadataValue };

export type AdapterProviderNamespace = `provider.${string}`;

/**
 * Where a provider extension belongs in the capture envelope. The two
 * placements are durably different in Fragments Engine:
 *
 * - `document` lands in fragment and revision metadata, which is an
 *   observation about the captured item itself and survives with it.
 * - `extraction` lands on the capture attempt, which is provenance about how
 *   this capture ran and is not carried on the fragment.
 *
 * `document` is the default because a provider observation describes the
 * captured item, so it should outlive the attempt that happened to observe it.
 * An adapter opts into `extraction` only for data that genuinely describes the
 * run rather than the item.
 */
export type AdapterProviderMetadataPlacement = "document" | "extraction";

/** Normalized provider observations only; never raw DOM or serialized state. */
export interface AdapterProviderMetadataExtension {
  namespace: AdapterProviderNamespace;
  schemaVersion: string;
  placement?: AdapterProviderMetadataPlacement;
  data: { readonly [key: string]: AdapterMetadataValue };
}

export type AdapterCapability =
  | "canonical_identity"
  | "title"
  | "description"
  | "body"
  | "selection"
  | "gallery_manifest"
  | "original_media"
  | "thumbnail_or_poster"
  | "transcript"
  | "provider_metadata";

export interface AdapterCapabilityReport {
  /** Capabilities the selected adapter knows how to inspect. */
  declared: readonly AdapterCapability[];
  /** Capabilities actually observed on this page. */
  observed: readonly AdapterCapability[];
}

export type AdapterWarningSeverity = "info" | "warning" | "error";

export type AdapterExtractionStage =
  | "matching"
  | "identity"
  | "document"
  | "media"
  | "provider_metadata"
  | "capture";

export interface AdapterExtractionWarning {
  code: string;
  severity: AdapterWarningSeverity;
  stage: AdapterExtractionStage;
  message: string;
}

export interface AdapterStageResult<T> {
  observation: T;
  observedCapabilities?: readonly AdapterCapability[];
  warnings?: readonly AdapterExtractionWarning[];
}

export interface PageCaptureAdapter {
  readonly id: string;
  readonly version: string;
  matches(context: PageCaptureContext): AdapterMatchConfidence;
  identify(
    context: PageCaptureContext,
  ): AdapterStageResult<AdapterCanonicalIdentityObservation>;
  extractDocument(context: PageCaptureContext): AdapterStageResult<AdapterDocumentObservation>;
  extractMedia(context: PageCaptureContext): AdapterStageResult<readonly AdapterMediaCandidate[]>;
  extractProviderMetadata(
    context: PageCaptureContext,
  ): AdapterStageResult<readonly AdapterProviderMetadataExtension[]>;
  reportCapabilities(): readonly AdapterCapability[];
}

export interface SelectedPageCaptureAdapter {
  id: string;
  version: string;
  matchConfidence: AdapterMatchConfidence;
  usedGenericFallback: boolean;
}

/** Serializable adapter output stashed by the injected content script. */
export interface PageCaptureResult {
  adapter: SelectedPageCaptureAdapter;
  identity: AdapterCanonicalIdentityObservation;
  document: AdapterDocumentObservation;
  media: readonly AdapterMediaCandidate[];
  extensions: readonly AdapterProviderMetadataExtension[];
  capabilities: AdapterCapabilityReport;
  warnings: readonly AdapterExtractionWarning[];
}
