import {
  assertFECaptureEnvelope,
  type FECaptureEnvelope,
} from "@/contracts/fragmentsEngine";
import { isSensitiveCaptureFieldName, sanitizeCapturePageUrl } from "./urlSafety";
import type {
  AdapterMediaCandidate,
  AdapterMediaVariantCandidate,
  AdapterMetadataValue,
  AdapterProviderMetadataExtension,
  PageCaptureResult,
} from "./types";
import type { EditableFields } from "@/lib/types";

const FE_CAPABILITIES = new Set([
  "title",
  "description",
  "body",
  "gallery_manifest",
  "original_media",
  "thumbnail_or_poster",
  "transcript",
]);

const FE_WARNING_CODE = /^[a-z][a-z0-9_.-]*$/;
const FE_PROVIDER = /^[a-z][a-z0-9_-]*$/;
const LANGUAGE = /^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$/;
const MAX_IDENTIFIER_LENGTH = 255;
const MAX_TAG_LENGTH = 128;

export interface CaptureEnvelopeOptions {
  captureId: string;
  capturedAt: string;
  principalId: string;
  clientVersion: string;
  createId: () => string;
  digestIdentifier?: (value: string) => Promise<string>;
}

interface MappingWarning {
  code: string;
  message: string;
  subject_id?: string;
}

interface SanitizedExtensionValue {
  value: AdapterMetadataValue;
  removedCredential: boolean;
}

function identifier(value: string, fallback: string): string {
  const normalized = value.trim();
  if (normalized.length > MAX_IDENTIFIER_LENGTH) {
    throw new Error(`Identifier beginning with ${normalized.slice(0, 32)}… exceeds 255 characters.`);
  }
  return normalized || fallback;
}

async function sha256Identifier(value: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function semanticIdentifier(
  prefix: string,
  value: string,
  digest: (value: string) => Promise<string>,
): Promise<string> {
  const normalized = value.trim();
  if (normalized.length <= MAX_IDENTIFIER_LENGTH) return identifier(normalized, prefix);
  return `${prefix}:${await digest(normalized)}`;
}

function validHttpUrl(value: string): string | undefined {
  try {
    const sanitized = new URL(sanitizeCapturePageUrl(value));
    if (sanitized.protocol !== "http:" && sanitized.protocol !== "https:") return undefined;
    if (sanitized.username || sanitized.password) return undefined;
    return sanitized.href;
  } catch {
    return undefined;
  }
}

function requiredHttpUrl(value: string, boundary: string): string {
  const sanitized = validHttpUrl(value);
  if (!sanitized) throw new Error(`${boundary} must be an HTTP(S) URL without credentials.`);
  return sanitized;
}

function sanitizeExtensionValue(value: AdapterMetadataValue): SanitizedExtensionValue {
  if (Array.isArray(value)) {
    const sanitized = value.map(sanitizeExtensionValue);
    return {
      value: sanitized.map((item) => item.value),
      removedCredential: sanitized.some((item) => item.removedCredential),
    };
  }
  if (typeof value === "object" && value !== null) {
    const result: Record<string, AdapterMetadataValue> = {};
    let removedCredential = false;
    for (const [key, child] of Object.entries(value)) {
      if (isSensitiveCaptureFieldName(key)) {
        removedCredential = true;
        continue;
      }
      const sanitized = sanitizeExtensionValue(child);
      result[key] = sanitized.value;
      removedCredential ||= sanitized.removedCredential;
    }
    return { value: result, removedCredential };
  }
  if (typeof value === "string" && /^https?:\/\//i.test(value)) {
    const sanitized = validHttpUrl(value);
    return sanitized
      ? { value: sanitized, removedCredential: sanitized !== value }
      : { value: "", removedCredential: true };
  }
  return { value, removedCredential: false };
}

function providerExtensions(
  extensions: readonly AdapterProviderMetadataExtension[],
  warnings: MappingWarning[],
): Record<string, object> | undefined {
  const result: Record<string, object> = {};
  for (const extension of extensions) {
    if (!/^provider\.[a-z0-9][a-z0-9_-]*$/.test(extension.namespace)) {
      warnings.push({
        code: "capture.extension_omitted",
        message: `Provider extension ${extension.namespace} was omitted because its namespace is invalid.`,
      });
      continue;
    }
    const sanitized = sanitizeExtensionValue(extension.data);
    result[extension.namespace] = {
      schema_version: extension.schemaVersion,
      data: sanitized.value,
    };
    if (sanitized.removedCredential) {
      warnings.push({
        code: "capture.credentials_omitted",
        message: `Credential-shaped data was removed from ${extension.namespace}.`,
      });
    }
  }
  return Object.keys(result).length > 0 ? result : undefined;
}

function optionalPositive(value: number | undefined): number | undefined {
  return value !== undefined && Number.isFinite(value) && value > 0 ? value : undefined;
}

function optionalNonNegativeInteger(value: number | undefined): number | undefined {
  return value !== undefined && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}

async function mapVariant(
  media: AdapterMediaCandidate,
  mediaId: string,
  variant: AdapterMediaVariantCandidate,
  variantIndex: number,
  digest: (value: string) => Promise<string>,
  warnings: MappingWarning[],
): Promise<Record<string, unknown> | undefined> {
  if (variant.kind === "other") {
    warnings.push({
      code: "capture.variant_omitted",
      message: `Internal variant ${variant.variantId} on ${media.candidateId} was omitted because FE has no matching variant kind.`,
    });
    return undefined;
  }

  const sourceUrl = variant.sourceUrl ? validHttpUrl(variant.sourceUrl) : undefined;
  if (variant.sourceUrl && !sourceUrl) {
    warnings.push({
      code: "capture.variant_url_omitted",
      message: `Variant ${variant.variantId} on ${media.candidateId} had an unsafe source URL and was omitted.`,
    });
    return undefined;
  }

  const referenceOnly = variant.kind === "stream_reference" || variant.acquisitionPreference === "reference_only";
  const kind = variant.kind === "stream_reference" ? "original" : variant.kind;
  const expiresAt = variant.expiresAt && !Number.isNaN(Date.parse(variant.expiresAt))
    ? new Date(variant.expiresAt).toISOString()
    : undefined;

  return {
    client_variant_id: await semanticIdentifier(
      "variant",
      `${mediaId}:${variant.variantId}:${variantIndex}`,
      digest,
    ),
    kind,
    ...(sourceUrl ? { source_url: sourceUrl } : {}),
    ...(expiresAt ? { source_expires_at: expiresAt } : {}),
    ...(variant.mimeType ? { mime_type: variant.mimeType.toLowerCase() } : {}),
    ...(optionalPositive(variant.width) ? { width: optionalPositive(variant.width) } : {}),
    ...(optionalPositive(variant.height) ? { height: optionalPositive(variant.height) } : {}),
    ...(optionalPositive(variant.durationSeconds)
      ? { duration_seconds: optionalPositive(variant.durationSeconds) }
      : {}),
    ...(optionalNonNegativeInteger(variant.byteSize) !== undefined
      ? { byte_size: optionalNonNegativeInteger(variant.byteSize) }
      : {}),
    transfer_preference: referenceOnly ? "reference_only" : "browser_preferred",
    requested_custody: referenceOnly ? "reference" : "mirror",
  };
}

async function mapMedia(
  captured: PageCaptureResult,
  digest: (value: string) => Promise<string>,
  warnings: MappingWarning[],
): Promise<Record<string, unknown>[]> {
  const sorted = [...captured.media].sort((left, right) => left.position - right.position);
  const result: Record<string, unknown>[] = [];
  const usedMediaIds = new Set<string>();

  for (const [sourceIndex, media] of sorted.entries()) {
    let mediaId = await semanticIdentifier("media", media.candidateId, digest);
    if (usedMediaIds.has(mediaId)) {
      mediaId = await semanticIdentifier(
        "media",
        `${media.candidateId}:position:${media.position}:index:${sourceIndex}`,
        digest,
      );
    }
    usedMediaIds.add(mediaId);

    const mappedVariants = await Promise.all(
      media.variants.map((variant, variantIndex) =>
        mapVariant(media, mediaId, variant, variantIndex, digest, warnings),
      ),
    );
    const variants = mappedVariants.filter(
      (variant): variant is Record<string, unknown> => variant !== undefined,
    );

    if (variants.length === 0) {
      warnings.push({
        code: "capture.media_omitted",
        message: `Media ${media.candidateId} at source position ${media.position} had no FE-compatible variants; its position remains recorded by this warning.`,
      });
      continue;
    }

    const sourceLocator = variants
      .map((variant) => variant.source_url)
      .find((value): value is string => typeof value === "string");
    result.push({
      client_media_id: mediaId,
      ...(media.providerMediaId
        ? {
            provider_media_id: await semanticIdentifier(
              "provider-media",
              media.providerMediaId,
              digest,
            ),
          }
        : {}),
      kind: media.kind,
      role: media.role,
      position: result.length,
      ...(sourceLocator ? { source_locator: sourceLocator } : {}),
      ...(media.altText ? { alt_text: media.altText } : {}),
      ...(media.caption ? { caption: media.caption } : {}),
      ...(media.sourceContext ? { source_context: media.sourceContext } : {}),
      default_custody: variants.some((variant) => variant.requested_custody === "mirror")
        ? "mirror"
        : "reference",
      variants,
    });
  }
  return result;
}

function contractWarnings(captured: PageCaptureResult, extra: readonly MappingWarning[]) {
  const warnings = captured.warnings
    .filter((warning) => warning.message.trim())
    .map((warning) => ({
      code: FE_WARNING_CODE.test(warning.code) ? warning.code : "capture.adapter_warning",
      message: warning.message.trim(),
    }));
  for (const warning of extra) warnings.push({ code: warning.code, message: warning.message });
  return warnings;
}

function sourceProvider(captured: PageCaptureResult): string {
  const candidate = captured.identity.provider.trim().toLowerCase();
  return FE_PROVIDER.test(candidate) ? candidate : "web";
}

/** Maps extension-owned observations to FE's generated, strictly validated envelope. */
export async function buildCaptureEnvelope(
  captured: PageCaptureResult,
  edits: EditableFields,
  options: CaptureEnvelopeOptions,
): Promise<FECaptureEnvelope> {
  const mappingWarnings: MappingWarning[] = [];
  const digest = options.digestIdentifier ?? sha256Identifier;
  const submittedUrl = requiredHttpUrl(captured.identity.submittedUrl, "Submitted URL");
  const canonicalUrl = requiredHttpUrl(captured.identity.canonicalUrl, "Canonical URL");
  const media = await mapMedia(captured, digest, mappingWarnings);
  const extensions = providerExtensions(captured.extensions, mappingWarnings);
  const annotations = [];
  const selection = captured.document.selection.trim();
  const note = edits.note.trim();
  if (selection) {
    annotations.push({
      annotation_id: options.createId(),
      kind: "highlight",
      text: selection,
      selector: { exact: selection },
    });
  }
  if (note) {
    annotations.push({ annotation_id: options.createId(), kind: "capture_note", text: note });
  }

  const title = edits.title.trim() || captured.document.title.trim();
  const description = captured.document.description.trim();
  const body = captured.document.contentMarkdown;
  const provider = sourceProvider(captured);
  const providerItemId = captured.identity.providerItemId?.trim();
  const sourceItemKey = await semanticIdentifier(
    providerItemId ? "provider-item" : "web-url",
    providerItemId ? `${provider}:${providerItemId}` : canonicalUrl,
    digest,
  );
  const observedCapabilities = [...new Set(captured.capabilities.observed)].filter((capability) =>
    FE_CAPABILITIES.has(capability),
  );
  const tags = [...new Set(edits.tags.map((tag) => tag.trim()).filter(Boolean))];
  const longTag = tags.find((tag) => tag.length > MAX_TAG_LENGTH);
  if (longTag) throw new Error(`Tag ${longTag.slice(0, 32)}… exceeds 128 characters.`);

  const envelope: unknown = {
    schema_version: "fe.capture.v1",
    capture_id: identifier(options.captureId, "capture"),
    captured_at: options.capturedAt,
    principal_id: identifier(options.principalId, "local-user"),
    client: { kind: "fe-clipper", version: identifier(options.clientVersion, "unknown") },
    source: {
      source_registration_id: "browser-capture",
      submitted_url: submittedUrl,
      canonical_url: canonicalUrl,
      provider,
      ...(providerItemId
        ? { provider_item_id: await semanticIdentifier("provider-item", providerItemId, digest) }
        : {}),
      source_item_key: sourceItemKey,
      segment_key: identifier(captured.identity.segmentKey, "root"),
      canonicalizer: {
        adapter: identifier(captured.adapter.id, "generic"),
        version: identifier(captured.adapter.version, "unknown"),
      },
    },
    document: {
      ...(title ? { title } : {}),
      ...(description ? { description } : {}),
      ...(captured.document.language && LANGUAGE.test(captured.document.language)
        ? { language: captured.document.language }
        : {}),
      ...(body ? { content: { format: "markdown", body } } : {}),
    },
    tags,
    annotations,
    media,
    extraction: {
      adapter: identifier(captured.adapter.id, "generic"),
      adapter_version: identifier(captured.adapter.version, "unknown"),
      observed_capabilities: observedCapabilities,
      warnings: contractWarnings(captured, mappingWarnings),
      ...(extensions ? { extensions } : {}),
    },
    page_context: {
      document_url: submittedUrl,
      ...(captured.document.title ? { document_title: captured.document.title } : {}),
      selection_present: Boolean(selection),
    },
    ...(extensions ? { extensions } : {}),
  };

  assertFECaptureEnvelope(envelope);
  return envelope;
}
