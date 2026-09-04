import {
  type FEAssetInstruction,
  type FEAssetOutcome,
  type FECaptureAssetVariant,
  type FECaptureEnvelope,
  type FECaptureManifestResponse,
  type FECaptureStatus,
} from "@/contracts/fragmentsEngine";
import type { CaptureVariantTransfer } from "@/lib/jobStorage";
import { CaptureApiError, deriveAssetUploadUrl } from "./captureApi";

export interface EnvelopeVariant {
  mediaKind: string;
  variant: FECaptureAssetVariant;
}

export function envelopeVariants(envelope: FECaptureEnvelope): Map<string, EnvelopeVariant> {
  const variants = new Map<string, EnvelopeVariant>();
  for (const media of envelope.media) {
    for (const variant of media.variants) {
      if (variants.has(variant.client_variant_id)) {
        throw new CaptureApiError(
          `Capture envelope contains duplicate client variant ID ${variant.client_variant_id}.`,
          "invalid_response",
          false,
        );
      }
      variants.set(variant.client_variant_id, { mediaKind: media.kind, variant });
    }
  }
  return variants;
}

function httpOrigin(rawUrl: string): string {
  const url = new URL(rawUrl);
  if (
    (url.protocol !== "http:" && url.protocol !== "https:") ||
    url.username ||
    url.password
  ) {
    throw new CaptureApiError(
      "A browser transfer source is not a credential-free HTTP(S) URL.",
      "invalid_response",
      false,
    );
  }
  return url.origin;
}

export function bindPermittedSourceOrigins(
  envelope: FECaptureEnvelope,
  candidates: readonly string[],
): string[] {
  const permittedByEnvelope = new Set(
    [...envelopeVariants(envelope).values()]
      .filter(({ variant }) => variant.transfer_preference === "browser_preferred" && variant.source_url)
      .map(({ variant }) => httpOrigin(variant.source_url!)),
  );
  const bound = new Set<string>();
  for (const candidate of candidates) {
    let origin: string;
    try {
      const url = new URL(candidate);
      if (url.origin !== candidate) throw new Error("not an origin");
      origin = httpOrigin(candidate);
    } catch {
      throw new CaptureApiError(
        "The popup supplied an invalid media permission origin.",
        "invalid_response",
        false,
      );
    }
    if (!permittedByEnvelope.has(origin)) {
      throw new CaptureApiError(
        `The popup supplied a media permission outside this capture envelope: ${origin}.`,
        "invalid_response",
        false,
      );
    }
    bound.add(origin);
  }
  return [...bound].sort();
}

function noAutomaticYouTubeVideo(
  envelope: FECaptureEnvelope,
  mediaKind: string,
  variant: FECaptureAssetVariant,
): boolean {
  return (
    envelope.source.provider === "youtube" &&
    mediaKind === "video" &&
    !["poster", "subtitles", "transcript"].includes(variant.kind)
  );
}

function instructionTransfer(
  envelope: FECaptureEnvelope,
  endpoint: string,
  instruction: FEAssetInstruction,
  observed: EnvelopeVariant,
  now: string,
): CaptureVariantTransfer {
  const common = {
    clientVariantId: instruction.client_variant_id,
    action: instruction.action,
    attempts: 0,
    updatedAt: now,
    ...(observed.variant.source_url ? { sourceUrl: observed.variant.source_url } : {}),
    ...(observed.variant.source_url ? { sourceOrigin: httpOrigin(observed.variant.source_url) } : {}),
  };
  switch (instruction.action) {
    case "request_upload": {
      if (
        observed.variant.transfer_preference !== "browser_preferred" ||
        !observed.variant.source_url ||
        noAutomaticYouTubeVideo(envelope, observed.mediaKind, observed.variant)
      ) {
        throw new CaptureApiError(
          `Fragments Engine requested browser bytes for variant ${instruction.client_variant_id}, but its persisted transfer policy forbids that upload.`,
          "invalid_response",
          false,
        );
      }
      const uploadUrl = deriveAssetUploadUrl(
        endpoint,
        envelope.capture_id,
        instruction.client_variant_id,
        instruction.upload_href,
      );
      if (
        instruction.expected_digest &&
        observed.variant.digest &&
        instruction.expected_digest.value !== observed.variant.digest.value
      ) {
        throw new CaptureApiError(
          `Fragments Engine expected digest contradicts the captured digest for variant ${instruction.client_variant_id}.`,
          "invalid_response",
          false,
        );
      }
      const expectedDigest = instruction.expected_digest ?? observed.variant.digest;
      return {
        ...common,
        assetVariantId: instruction.asset_variant_id,
        state: "pending",
        uploadUrl,
        ...(expectedDigest ? { expectedDigest } : {}),
      };
    }
    case "reuse_blob":
      if (observed.variant.digest && observed.variant.digest.value !== instruction.digest.value) {
        throw new CaptureApiError(
          `Fragments Engine reuse digest contradicts the captured digest for variant ${instruction.client_variant_id}.`,
          "invalid_response",
          false,
        );
      }
      return {
        ...common,
        assetVariantId: instruction.asset_variant_id,
        state: "available",
        digest: instruction.digest,
        outcome: {
          client_variant_id: instruction.client_variant_id,
          outcome: "already_available",
          digest: instruction.digest,
        },
      };
    case "server_acquire":
    case "reference_only":
      return {
        ...common,
        assetVariantId: instruction.asset_variant_id,
        state: "reference_only",
        outcome: { client_variant_id: instruction.client_variant_id, outcome: "deferred" },
      };
    case "rejected": {
      const outcome: FEAssetOutcome = {
        client_variant_id: instruction.client_variant_id,
        outcome: "not_available",
        reason: instruction.reason,
        retryable: false,
      };
      return {
        ...common,
        state: "failed",
        retryable: false,
        reason: instruction.reason,
        outcome,
      };
    }
  }
}

/** Validates the complete instruction set before any source fetch or upload. */
export function planCaptureTransfers(
  envelope: FECaptureEnvelope,
  endpoint: string,
  acceptance: FECaptureManifestResponse,
  now: string,
): CaptureVariantTransfer[] {
  const variants = envelopeVariants(envelope);
  const instructions = new Set<string>();
  for (const instruction of acceptance.asset_instructions) {
    if (instructions.has(instruction.client_variant_id)) {
      throw new CaptureApiError(
        `Fragments Engine returned duplicate instructions for variant ${instruction.client_variant_id}.`,
        "invalid_response",
        false,
      );
    }
    if (!variants.has(instruction.client_variant_id)) {
      throw new CaptureApiError(
        `Fragments Engine returned an instruction for unknown variant ${instruction.client_variant_id}.`,
        "invalid_response",
        false,
      );
    }
    instructions.add(instruction.client_variant_id);
  }
  if (instructions.size !== variants.size) {
    const missing = [...variants.keys()].find((variantId) => !instructions.has(variantId));
    throw new CaptureApiError(
      `Fragments Engine omitted an instruction for variant ${missing ?? "unknown"}.`,
      "invalid_response",
      false,
    );
  }
  return acceptance.asset_instructions.map((instruction) =>
    instructionTransfer(envelope, endpoint, instruction, variants.get(instruction.client_variant_id)!, now),
  );
}

function immutableTransferFields(transfer: CaptureVariantTransfer): string {
  return JSON.stringify({
    clientVariantId: transfer.clientVariantId,
    action: transfer.action,
    assetVariantId: transfer.assetVariantId ?? null,
    sourceUrl: transfer.sourceUrl ?? null,
    sourceOrigin: transfer.sourceOrigin ?? null,
    uploadUrl: transfer.uploadUrl ?? null,
    expectedDigest: transfer.expectedDigest ?? null,
    instructionDigest: transfer.action === "reuse_blob" ? transfer.digest ?? null : null,
    instructionReason: transfer.action === "rejected" ? transfer.reason ?? null : null,
  });
}

/** Re-derives server/envelope-bound fields before trusting resumed state. */
export function assertPersistedTransferPlan(
  envelope: FECaptureEnvelope,
  endpoint: string,
  acceptance: FECaptureManifestResponse,
  transfers: readonly CaptureVariantTransfer[],
): void {
  const expected = planCaptureTransfers(envelope, endpoint, acceptance, new Date(0).toISOString());
  if (
    expected.length !== transfers.length ||
    expected.some(
      (transfer, index) => immutableTransferFields(transfer) !== immutableTransferFields(transfers[index]!),
    )
  ) {
    throw new CaptureApiError(
      "Persisted transfer state no longer matches the accepted capture instructions; no media request was made.",
      "invalid_response",
      false,
    );
  }
}

function sameDigest(
  left: { algorithm: "sha256"; value: string } | undefined,
  right: { algorithm: "sha256"; value: string } | undefined,
): boolean {
  return !left || !right || (left.algorithm === right.algorithm && left.value === right.value);
}

function exactDigest(
  left: { algorithm: "sha256"; value: string } | undefined,
  right: { algorithm: "sha256"; value: string } | undefined,
): boolean {
  return Boolean(
    left && right && left.algorithm === right.algorithm && left.value === right.value,
  );
}

/** Validates action-specific mutable state before resuming or completing. */
export function assertTransferRuntimeState(transfers: readonly CaptureVariantTransfer[]): void {
  for (const transfer of transfers) {
    const outcome = transfer.outcome;
    let valid = false;
    switch (transfer.action) {
      case "reuse_blob":
        valid = transfer.state === "available" &&
          outcome?.outcome === "already_available" &&
          Boolean(transfer.digest) &&
          exactDigest(transfer.digest, outcome.digest);
        break;
      case "server_acquire":
      case "reference_only":
        valid = transfer.state === "reference_only" && outcome?.outcome === "deferred";
        break;
      case "rejected":
        valid = transfer.state === "failed" &&
          transfer.retryable === false &&
          outcome?.outcome === "not_available" &&
          outcome.retryable === false &&
          transfer.reason === outcome.reason;
        break;
      case "request_upload":
        if (transfer.state === "pending" || transfer.state === "transferring") {
          valid = outcome === undefined && transfer.retryable === undefined && transfer.reason === undefined;
        } else if (transfer.state === "available") {
          valid = (outcome?.outcome === "uploaded" || outcome?.outcome === "already_available") &&
            Boolean(transfer.digest) &&
            exactDigest(transfer.digest, outcome.digest) &&
            sameDigest(transfer.expectedDigest, transfer.digest) &&
            (outcome.outcome !== "uploaded" || transfer.byteSize === outcome.byte_size);
        } else if (transfer.state === "failed") {
          valid = outcome?.outcome === "failed" &&
            typeof transfer.retryable === "boolean" &&
            transfer.reason === outcome.reason &&
            transfer.retryable === (outcome.retryable ?? false) &&
            sameDigest(transfer.expectedDigest, transfer.digest);
        }
        break;
    }
    if (!valid) {
      throw new CaptureApiError(
        `Persisted transfer outcome is inconsistent for variant ${transfer.clientVariantId}; no media request was made.`,
        "invalid_response",
        false,
      );
    }
  }
}

/** Semantically binds status outcomes before using them to suppress a retry. */
export function reconcileTransferStatus(
  transfers: readonly CaptureVariantTransfer[],
  status: FECaptureStatus,
  now: string,
): CaptureVariantTransfer[] {
  const byId = new Map(transfers.map((transfer) => [transfer.clientVariantId, transfer]));
  const seen = new Set<string>();
  for (const outcome of status.assets) {
    const transfer = byId.get(outcome.client_variant_id);
    if (!transfer || seen.has(outcome.client_variant_id)) {
      throw new CaptureApiError(
        `Fragments Engine status returned an unknown or duplicate variant outcome for ${outcome.client_variant_id}.`,
        "invalid_response",
        false,
      );
    }
    seen.add(outcome.client_variant_id);
    if (
      transfer.action === "request_upload" &&
      (outcome.outcome === "already_available" || outcome.outcome === "uploaded")
    ) {
      const digest = outcome.digest;
      if (!digest) {
        throw new CaptureApiError(
          `Fragments Engine reported variant ${outcome.client_variant_id} available without a digest.`,
          "invalid_response",
          false,
        );
      }
      if (
        (transfer.expectedDigest && transfer.expectedDigest.value !== digest.value) ||
        (transfer.digest && transfer.digest.value !== digest.value)
      ) {
        throw new CaptureApiError(
          `Fragments Engine status digest does not match the persisted digest for variant ${outcome.client_variant_id}.`,
          "invalid_response",
          false,
        );
      }
      byId.set(outcome.client_variant_id, {
        ...transfer,
        state: "available",
        digest,
        ...(outcome.outcome === "uploaded" ? { byteSize: outcome.byte_size } : {}),
        outcome,
        retryable: undefined,
        reason: undefined,
        updatedAt: now,
      });
    }
  }
  if (seen.size !== transfers.length) {
    const missing = transfers.find((transfer) => !seen.has(transfer.clientVariantId));
    throw new CaptureApiError(
      `Fragments Engine status omitted variant outcome ${missing?.clientVariantId ?? "unknown"}.`,
      "invalid_response",
      false,
    );
  }
  return transfers.map((transfer) => byId.get(transfer.clientVariantId)!);
}

export function transfersReadyForCompletion(transfers: readonly CaptureVariantTransfer[]): boolean {
  assertTransferRuntimeState(transfers);
  return transfers.every(
    (transfer) =>
      ["available", "reference_only", "failed"].includes(transfer.state) &&
      transfer.outcome !== undefined &&
      !(transfer.state === "failed" && transfer.retryable === true),
  );
}

export function transferCompletionOutcomes(
  transfers: readonly CaptureVariantTransfer[],
): FEAssetOutcome[] {
  if (!transfersReadyForCompletion(transfers)) {
    throw new Error("Capture completion cannot run while browser transfers remain retryable or pending.");
  }
  return transfers.map((transfer) => transfer.outcome!);
}
