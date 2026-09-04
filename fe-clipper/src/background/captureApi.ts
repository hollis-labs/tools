import {
  assertFECaptureCompletion,
  assertFECaptureEnvelope,
  assertFECaptureManifestResponse,
  assertFECaptureStatus,
  feContractValidators,
  formatFEValidationIssues,
  type FEAPIProblem,
  type FECaptureAssetVariant,
  type FECaptureCompletionRequest,
  type FECaptureEnvelope,
  type FECaptureManifestResponse,
  type FECaptureStatus,
} from "@/contracts/fragmentsEngine";
import {
  deriveCapabilitiesUrl,
  preflightCaptureManifest,
  type CapturePreflightResult,
} from "@/contracts/capabilities";
import { originPattern } from "@/lib/permissions";

export type CaptureApiErrorKind =
  | "network"
  | "not_found"
  | "conflict"
  | "permission"
  | "server"
  | "invalid_response";

export class CaptureApiError extends Error {
  constructor(
    message: string,
    readonly kind: CaptureApiErrorKind,
    readonly retryable: boolean,
    readonly status?: number,
    readonly code?: string,
  ) {
    super(message);
    this.name = "CaptureApiError";
  }
}

export type FetchLike = (input: string | URL, init?: RequestInit) => Promise<Response>;
export type EndpointPermissionCheck = (originPattern: string) => Promise<boolean>;

export interface CaptureApiClient {
  preflight(endpoint: string): Promise<CapturePreflightResult>;
  submitManifest(endpoint: string, envelope: FECaptureEnvelope): Promise<FECaptureManifestResponse>;
  getStatus(endpoint: string, captureId: string): Promise<FECaptureStatus>;
  uploadAsset(
    endpoint: string,
    captureId: string,
    variant: FECaptureAssetVariant,
    uploadHref: string,
    bytes: Uint8Array,
    mimeType: string,
    digest: { algorithm: "sha256"; value: string },
  ): Promise<void>;
  completeCapture(
    endpoint: string,
    captureId: string,
    completion: FECaptureCompletionRequest,
  ): Promise<FECaptureStatus>;
}

export function deriveCaptureManifestUrl(configuredEndpoint: string): string {
  const endpoint = new URL(configuredEndpoint);
  if (endpoint.protocol !== "http:" && endpoint.protocol !== "https:") {
    throw new Error("the configured endpoint must use http:// or https://");
  }
  if (endpoint.username || endpoint.password) {
    throw new Error("the configured endpoint must not contain credentials");
  }
  return new URL("/v1/captures", endpoint.origin).toString();
}

function captureResourceUrl(endpoint: string, captureId: string, suffix = ""): string {
  const manifestUrl = new URL(deriveCaptureManifestUrl(endpoint));
  manifestUrl.pathname = `/v1/captures/${encodeURIComponent(captureId)}${suffix}`;
  return manifestUrl.toString();
}

export function deriveAssetUploadUrl(
  endpoint: string,
  captureId: string,
  clientVariantId: string,
  uploadHref: string,
): string {
  const manifestUrl = new URL(deriveCaptureManifestUrl(endpoint));
  const upload = new URL(uploadHref, manifestUrl.origin);
  const match = upload.pathname.match(/^\/v1\/captures\/([^/]+)\/assets\/([^/]+)\/content$/);
  let decodedCaptureId: string | undefined;
  let decodedVariantId: string | undefined;
  try {
    decodedCaptureId = match ? decodeURIComponent(match[1]!) : undefined;
    decodedVariantId = match ? decodeURIComponent(match[2]!) : undefined;
  } catch {
    // Invalid percent escapes are unsafe route data.
  }
  const unsafeSegment = (raw: string | undefined, decoded: string | undefined) =>
    !raw ||
    decoded === undefined ||
    /%2f|%5c/i.test(raw) ||
    decoded.includes("/") ||
    decoded.includes("\\") ||
    decoded === "." ||
    decoded === "..";
  if (
    upload.origin !== manifestUrl.origin ||
    upload.username ||
    upload.password ||
    upload.search ||
    upload.hash ||
    !match ||
    unsafeSegment(match?.[1], decodedCaptureId) ||
    unsafeSegment(match?.[2], decodedVariantId) ||
    decodedCaptureId !== captureId ||
    decodedVariantId !== clientVariantId
  ) {
    throw new CaptureApiError(
      `Fragments Engine returned an unsafe upload route for variant ${clientVariantId}.`,
      "invalid_response",
      false,
    );
  }
  return upload.toString();
}

function digestHeader(digest: { algorithm: "sha256"; value: string }): string {
  if (digest.algorithm !== "sha256" || !/^[a-f0-9]{64}$/.test(digest.value)) {
    throw new CaptureApiError("Cannot upload an invalid SHA-256 digest.", "invalid_response", false);
  }
  const digestBytes = new Uint8Array(32);
  for (let index = 0; index < digestBytes.length; index += 1) {
    digestBytes[index] = Number.parseInt(digest.value.slice(index * 2, index * 2 + 2), 16);
  }
  let binary = "";
  for (const byte of digestBytes) binary += String.fromCharCode(byte);
  return `sha-256=:${btoa(binary)}:`;
}

function describeProblem(problem: FEAPIProblem): string {
  const detail = problem.detail ? ` ${problem.detail}` : "";
  const violation = problem.errors?.[0]
    ? ` (${problem.errors[0].pointer || "/"}: ${problem.errors[0].message})`
    : "";
  return `${problem.title} [${problem.code}].${detail}${violation}`;
}

async function responseBody(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    throw new CaptureApiError(
      `Fragments Engine returned non-JSON data (HTTP ${response.status}). Check the configured endpoint and FE version.`,
      "invalid_response",
      false,
      response.status,
    );
  }
}

function responseError(response: Response, body: unknown): CaptureApiError {
  if (!feContractValidators.apiProblem(body)) {
    return new CaptureApiError(
      `Fragments Engine returned HTTP ${response.status} with an invalid problem body: ${formatFEValidationIssues(feContractValidators.apiProblem.errors)}.`,
      "invalid_response",
      false,
      response.status,
    );
  }
  const kind: CaptureApiErrorKind =
    response.status === 404 ? "not_found" : response.status === 409 ? "conflict" : "server";
  const retryable = body.retryable ?? response.status >= 500;
  return new CaptureApiError(
    `Fragments Engine rejected the capture request (HTTP ${response.status}): ${describeProblem(body)}`,
    kind,
    retryable,
    response.status,
    body.code,
  );
}

async function fetchResponse(
  fetchImplementation: FetchLike,
  url: string,
  init: RequestInit,
): Promise<Response> {
  try {
    return await fetchImplementation(url, { ...init, credentials: "omit" });
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    throw new CaptureApiError(`Could not reach Fragments Engine at ${url}: ${detail}`, "network", true);
  }
}

function requireMatchingCaptureId(captureId: string, actual: string, boundary: string): void {
  if (actual !== captureId) {
    throw new CaptureApiError(
      `${boundary} returned capture_id ${actual}, expected ${captureId}. The response was not persisted.`,
      "invalid_response",
      false,
    );
  }
}

function semanticResponseError(boundary: string, detail: string): CaptureApiError {
  return new CaptureApiError(
    `${boundary} failed semantic identity validation: ${detail}`,
    "invalid_response",
    false,
  );
}

function requireEqualSemanticValue(
  boundary: string,
  label: string,
  expected: unknown,
  actual: unknown,
): void {
  if (expected !== actual) {
    throw semanticResponseError(
      boundary,
      `${label} was ${String(actual)}, expected ${String(expected)}.`,
    );
  }
}

function requireExactUniqueIdentifiers(
  boundary: string,
  label: string,
  expected: readonly string[],
  actual: readonly string[],
): void {
  const expectedSet = new Set(expected);
  const actualSet = new Set(actual);
  if (
    expectedSet.size !== expected.length ||
    actualSet.size !== actual.length ||
    expectedSet.size !== actualSet.size ||
    [...expectedSet].some((identifier) => !actualSet.has(identifier))
  ) {
    throw semanticResponseError(
      boundary,
      `${label} did not exactly match the capture envelope.`,
    );
  }
}

/**
 * JSON Schema establishes shape. This establishes the cross-object identity
 * relationships that the capture protocol depends on for safe persistence and
 * transfer planning.
 */
export function assertCaptureManifestSemantics(
  envelope: FECaptureEnvelope,
  response: FECaptureManifestResponse,
): void {
  const boundary = "Capture manifest response";
  const reader = response.reader_item;
  requireEqualSemanticValue(boundary, "Reader fragment_id", response.fragment_id, reader.fragment_id);
  requireEqualSemanticValue(
    boundary,
    "Reader fragment_revision_id",
    response.fragment_revision_id,
    reader.fragment_revision_id,
  );
  requireEqualSemanticValue(
    boundary,
    "Reader reading-state fragment_id",
    response.fragment_id,
    reader.reading_state.fragment_id,
  );
  requireEqualSemanticValue(
    boundary,
    "Reader reading-state principal_id",
    envelope.principal_id,
    reader.reading_state.principal_id,
  );

  for (const field of [
    "source_registration_id",
    "provider",
    "provider_item_id",
    "source_item_key",
    "segment_key",
  ] as const) {
    const expected = envelope.source[field];
    if (expected !== undefined) {
      requireEqualSemanticValue(boundary, `Reader source.${field}`, expected, reader.source[field]);
    }
  }

  const envelopeVariants = envelope.media.flatMap((media) => media.variants);
  if (envelope.media.some((media, index) => media.position !== index)) {
    throw semanticResponseError(
      boundary,
      "capture-envelope media positions were not unique and contiguous.",
    );
  }
  requireExactUniqueIdentifiers(
    boundary,
    "asset-instruction client_variant_id values",
    envelopeVariants.map((variant) => variant.client_variant_id),
    response.asset_instructions.map((instruction) => instruction.client_variant_id),
  );

  const instructionByClientVariant = new Map(
    response.asset_instructions.map((instruction) => [instruction.client_variant_id, instruction]),
  );
  const readerMediaByPosition = new Map<number, (typeof reader.media)[number]>();
  const readerAttachmentIds = new Set<string>();
  for (const readerMedia of reader.media) {
    if (readerMediaByPosition.has(readerMedia.attachment.position)) {
      throw semanticResponseError(
        boundary,
        `Reader repeated attachment position ${readerMedia.attachment.position}.`,
      );
    }
    if (readerAttachmentIds.has(readerMedia.attachment.attachment_id)) {
      throw semanticResponseError(
        boundary,
        `Reader repeated attachment_id ${readerMedia.attachment.attachment_id}.`,
      );
    }
    readerMediaByPosition.set(readerMedia.attachment.position, readerMedia);
    readerAttachmentIds.add(readerMedia.attachment.attachment_id);
  }

  for (const [index, media] of envelope.media.entries()) {
    const readerMedia = readerMediaByPosition.get(media.position);
    if (!readerMedia) {
      throw semanticResponseError(boundary, `Reader omitted envelope media position ${media.position}.`);
    }
    requireEqualSemanticValue(
      boundary,
      `Reader media bound to envelope position ${index}.kind`,
      media.kind,
      readerMedia.kind,
    );
    requireEqualSemanticValue(
      boundary,
      `Reader media bound to envelope position ${index}.attachment.role`,
      media.role,
      readerMedia.attachment.role,
    );
    requireEqualSemanticValue(
      boundary,
      `Reader media bound to envelope position ${index}.attachment.fragment_revision_id`,
      response.fragment_revision_id,
      readerMedia.attachment.fragment_revision_id,
    );
    requireEqualSemanticValue(
      boundary,
      `Reader media bound to envelope position ${index}.attachment.media_asset_id`,
      readerMedia.media_asset_id,
      readerMedia.attachment.media_asset_id,
    );
    if (media.provider_media_id !== undefined) {
      requireEqualSemanticValue(
        boundary,
        `Reader media bound to envelope position ${index}.provider_media_id`,
        media.provider_media_id,
        readerMedia.provider_media_id,
      );
    }

    for (const variant of media.variants) {
      const instruction = instructionByClientVariant.get(variant.client_variant_id);
      if (!instruction) {
        throw semanticResponseError(
          boundary,
          `Reader media[${index}] had no instruction for ${variant.client_variant_id}.`,
        );
      }
      if (instruction.action === "rejected") continue;
      const readerVariant = readerMedia.variants.find(
        (candidate) => candidate.asset_variant_id === instruction.asset_variant_id,
      );
      if (!readerVariant) {
        throw semanticResponseError(
          boundary,
          `instruction ${variant.client_variant_id} referenced asset variant ${instruction.asset_variant_id}, which was absent from Reader media position ${media.position}.`,
        );
      }
      requireEqualSemanticValue(
        boundary,
        `Reader variant ${instruction.asset_variant_id}.kind`,
        variant.kind,
        readerVariant.kind,
      );
    }
  }
}

export function assertCaptureStatusSemantics(
  envelope: FECaptureEnvelope,
  status: FECaptureStatus,
): void {
  requireExactUniqueIdentifiers(
    "Capture status response",
    "asset outcome client_variant_id values",
    envelope.media.flatMap((media) =>
      media.variants.map((variant) => variant.client_variant_id),
    ),
    status.assets.map((asset) => asset.client_variant_id),
  );
}

export class CaptureApi implements CaptureApiClient {
  constructor(
    private readonly fetchImplementation: FetchLike = fetch,
    private readonly hasCurrentPermission: EndpointPermissionCheck = (pattern) =>
      chrome.permissions.contains({ origins: [pattern] }),
  ) {}

  async preflight(endpoint: string): Promise<CapturePreflightResult> {
    let capabilitiesUrl: string;
    try {
      capabilitiesUrl = deriveCapabilitiesUrl(endpoint);
    } catch {
      return preflightCaptureManifest(endpoint, this.fetchImplementation);
    }
    try {
      await this.assertCurrentPermission(capabilitiesUrl);
    } catch (error) {
      return {
        ok: false,
        kind: "permission",
        capabilitiesUrl,
        message: error instanceof Error ? error.message : String(error),
      };
    }
    return preflightCaptureManifest(endpoint, this.fetchImplementation);
  }

  private async assertCurrentPermission(url: string): Promise<void> {
    const pattern = originPattern(url);
    let allowed = false;
    try {
      allowed = await this.hasCurrentPermission(pattern);
    } catch {
      // Permission API uncertainty is never authority for FE network access.
    }
    if (!allowed) {
      throw new CaptureApiError(
        `Current extension permission to reach Fragments Engine at ${new URL(url).origin} is unavailable. Open the popup and retry the same capture to grant access.`,
        "permission",
        true,
        undefined,
        "fe_permission",
      );
    }
  }

  async submitManifest(
    endpoint: string,
    envelope: FECaptureEnvelope,
  ): Promise<FECaptureManifestResponse> {
    assertFECaptureEnvelope(envelope);
    const url = deriveCaptureManifestUrl(endpoint);
    await this.assertCurrentPermission(url);
    const response = await fetchResponse(this.fetchImplementation, url, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify(envelope),
      cache: "no-store",
      redirect: "error",
    });
    const body = await responseBody(response);
    if (!response.ok) throw responseError(response, body);
    if (response.status !== 200 && response.status !== 201) {
      throw new CaptureApiError(
        `Fragments Engine returned unexpected manifest status HTTP ${response.status}; expected 200 or 201.`,
        "invalid_response",
        false,
        response.status,
      );
    }
    try {
      assertFECaptureManifestResponse(body);
    } catch (error) {
      throw new CaptureApiError(
        error instanceof Error ? error.message : String(error),
        "invalid_response",
        false,
        response.status,
      );
    }
    requireMatchingCaptureId(envelope.capture_id, body.capture_id, "Capture manifest response");
    assertCaptureManifestSemantics(envelope, body);
    if ((response.status === 200) !== body.idempotent_replay) {
      throw new CaptureApiError(
        `Fragments Engine manifest HTTP status and idempotent_replay flag disagree for ${envelope.capture_id}.`,
        "invalid_response",
        false,
        response.status,
      );
    }
    return body;
  }

  async getStatus(endpoint: string, captureId: string): Promise<FECaptureStatus> {
    const url = captureResourceUrl(endpoint, captureId);
    await this.assertCurrentPermission(url);
    const response = await fetchResponse(this.fetchImplementation, url, {
      method: "GET",
      headers: { Accept: "application/json" },
      cache: "no-store",
      redirect: "error",
    });
    const body = await responseBody(response);
    if (!response.ok) throw responseError(response, body);
    if (response.status !== 200) {
      throw new CaptureApiError(
        `Fragments Engine returned unexpected status lookup HTTP ${response.status}; expected 200.`,
        "invalid_response",
        false,
        response.status,
      );
    }
    try {
      assertFECaptureStatus(body);
    } catch (error) {
      throw new CaptureApiError(
        error instanceof Error ? error.message : String(error),
        "invalid_response",
        false,
        response.status,
      );
    }
    requireMatchingCaptureId(captureId, body.capture_id, "Capture status response");
    return body;
  }

  async uploadAsset(
    endpoint: string,
    captureId: string,
    variant: FECaptureAssetVariant,
    uploadHref: string,
    bytes: Uint8Array,
    mimeType: string,
    digest: { algorithm: "sha256"; value: string },
  ): Promise<void> {
    const url = deriveAssetUploadUrl(endpoint, captureId, variant.client_variant_id, uploadHref);
    await this.assertCurrentPermission(url);
    const body = bytes.byteOffset === 0 && bytes.byteLength === bytes.buffer.byteLength
      ? bytes.buffer as ArrayBuffer
      : bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
    const response = await fetchResponse(this.fetchImplementation, url, {
      method: "PUT",
      headers: { "Content-Type": mimeType, Digest: digestHeader(digest) },
      body,
      cache: "no-store",
      redirect: "error",
      referrerPolicy: "no-referrer",
    });
    if (!response.ok) {
      const responseValue = await responseBody(response);
      throw responseError(response, responseValue);
    }
    if (response.status !== 204) {
      throw new CaptureApiError(
        `Fragments Engine returned unexpected asset upload HTTP ${response.status}; expected 204.`,
        "invalid_response",
        false,
        response.status,
      );
    }
  }

  async completeCapture(
    endpoint: string,
    captureId: string,
    completion: FECaptureCompletionRequest,
  ): Promise<FECaptureStatus> {
    assertFECaptureCompletion(completion);
    const url = captureResourceUrl(endpoint, captureId, "/complete");
    await this.assertCurrentPermission(url);
    const response = await fetchResponse(this.fetchImplementation, url, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify(completion),
      cache: "no-store",
      redirect: "error",
    });
    const body = await responseBody(response);
    if (!response.ok) throw responseError(response, body);
    if (response.status !== 200) {
      throw new CaptureApiError(
        `Fragments Engine returned unexpected capture completion HTTP ${response.status}; expected 200.`,
        "invalid_response",
        false,
        response.status,
      );
    }
    try {
      assertFECaptureStatus(body);
    } catch (error) {
      throw new CaptureApiError(
        error instanceof Error ? error.message : String(error),
        "invalid_response",
        false,
        response.status,
      );
    }
    requireMatchingCaptureId(captureId, body.capture_id, "Capture completion response");
    return body;
  }
}
