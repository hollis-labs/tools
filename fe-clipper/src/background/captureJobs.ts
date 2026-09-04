import { buildCaptureEnvelope } from "@/capture/envelope";
import {
  assertFECaptureCompletion,
  type FECapabilityDiscovery,
  type FECaptureCompletionRequest,
  type FECaptureManifestResponse,
  type FECaptureStatus,
} from "@/contracts/fragmentsEngine";
import type {
  CaptureJobError,
  CaptureJobErrorStage,
  CaptureJobPersistence,
  CaptureJobRecord,
  CaptureVariantTransfer,
} from "@/lib/jobStorage";
import type { CaptureConfigurationSnapshot, CapturedPage, EditableFields } from "@/lib/types";
import {
  CaptureApiError,
  deriveCaptureManifestUrl,
  type CaptureApiClient,
} from "./captureApi";
import {
  bindPermittedSourceOrigins,
  assertPersistedTransferPlan,
  assertTransferRuntimeState,
  envelopeVariants,
  planCaptureTransfers,
  reconcileTransferStatus,
  transferCompletionOutcomes,
  transfersReadyForCompletion,
} from "./captureTransfer";
import {
  BrowserMediaAcquirer,
  DEFAULT_MAX_ASSET_BYTES,
  MediaTransferError,
  type AcquiredAsset,
  type MediaAcquirer,
} from "./mediaTransfer";

export interface StartCaptureJobInput {
  captureId: string;
  captured: CapturedPage;
  edits: EditableFields;
  configuration: CaptureConfigurationSnapshot;
  permittedSourceOrigins: readonly string[];
}

export interface CaptureJobCoordinatorOptions {
  now?: () => Date;
  createId?: () => string;
  clientVersion?: string;
  mediaAcquirer?: MediaAcquirer;
}

function terminalCompletion(completion: FECaptureStatus["completion"]): boolean {
  return completion === "complete" || completion === "partial" || completion === "failed";
}

function phaseForStatus(status: FECaptureStatus): CaptureJobRecord["phase"] {
  if (status.completion === "complete") return "complete";
  if (status.completion === "partial") return "partial";
  if (status.completion === "failed") return "failed";
  return "accepted";
}

function hasRequestedUploads(acceptance: FECaptureManifestResponse): boolean {
  return acceptance.asset_instructions.some((instruction) => instruction.action === "request_upload");
}

function stableIdsMatch(acceptance: FECaptureManifestResponse, status: FECaptureStatus): boolean {
  return (
    acceptance.capture_id === status.capture_id &&
    acceptance.fragment_id === status.fragment_id &&
    acceptance.fragment_revision_id === status.fragment_revision_id &&
    acceptance.capture_attempt_id === status.capture_attempt_id
  );
}

function unsupportedEnvelopeCapability(
  job: CaptureJobRecord,
  capabilities: FECapabilityDiscovery,
): string | undefined {
  const encodedBytes = new TextEncoder().encode(JSON.stringify(job.envelope)).byteLength;
  if (capabilities.capture.max_manifest_bytes && encodedBytes > capabilities.capture.max_manifest_bytes) {
    return `Capture manifest is ${encodedBytes} bytes, exceeding this FE server's ${capabilities.capture.max_manifest_bytes}-byte limit.`;
  }
  const mediaKinds = new Set(capabilities.capture.media_kinds);
  const variantKinds = new Set(capabilities.capture.variant_kinds);
  const custodyModes = new Set(capabilities.capture.custody_modes);
  const transferPreferences = new Set(capabilities.capture.transfer_preferences);
  for (const media of job.envelope.media) {
    if (!mediaKinds.has(media.kind)) return `Fragments Engine does not advertise media kind ${media.kind}.`;
    if (media.default_custody && !custodyModes.has(media.default_custody)) {
      return `Fragments Engine does not advertise custody mode ${media.default_custody}.`;
    }
    for (const variant of media.variants) {
      if (!variantKinds.has(variant.kind)) {
        return `Fragments Engine does not advertise variant kind ${variant.kind}.`;
      }
      if (!custodyModes.has(variant.requested_custody)) {
        return `Fragments Engine does not advertise custody mode ${variant.requested_custody}.`;
      }
      if (!transferPreferences.has(variant.transfer_preference)) {
        return `Fragments Engine does not advertise transfer preference ${variant.transfer_preference}.`;
      }
    }
  }
  return undefined;
}

function captureError(error: unknown, stage: CaptureJobErrorStage, now: string): CaptureJobError {
  if (error instanceof CaptureApiError) {
    return {
      stage,
      message: error.message,
      retryable: error.retryable,
      at: now,
      ...(error.status !== undefined ? { httpStatus: error.status } : {}),
      ...(error.code ? { code: error.code } : {}),
    };
  }
  if (error instanceof MediaTransferError) {
    return {
      stage,
      message: error.message,
      retryable: error.retryable,
      at: now,
      code: `media_${error.kind}`,
    };
  }
  return {
    stage,
    message: error instanceof Error ? error.message : String(error),
    retryable: false,
    at: now,
  };
}

function identifierForAction(value: string): string {
  const captureId = value.trim();
  if (!captureId || captureId.length > 255) {
    throw new Error("Capture action ID must contain 1–255 characters.");
  }
  return captureId;
}

function effectiveAssetLimit(capabilities: FECapabilityDiscovery): number {
  return Math.min(capabilities.capture.max_asset_bytes ?? DEFAULT_MAX_ASSET_BYTES, DEFAULT_MAX_ASSET_BYTES);
}

function transferWarnings(job: CaptureJobRecord) {
  const warnings = [...(job.acceptance?.warnings ?? [])];
  for (const transfer of job.transfers ?? []) {
    if (transfer.state !== "failed" || !transfer.reason) continue;
    warnings.push({
      code: "capture.asset_transfer_failed",
      message: transfer.reason,
      subject_id: transfer.clientVariantId,
    });
  }
  return warnings;
}

function shouldContinueAcceptedJob(job: CaptureJobRecord): boolean {
  if (!job.acceptance || job.terminal) return false;
  if (!job.error || job.error.retryable) return true;
  return (job.transfers ?? []).some(
    (transfer) =>
      transfer.state === "pending" ||
      transfer.state === "transferring" ||
      (transfer.state === "failed" && transfer.retryable === true),
  );
}

export class CaptureJobCoordinator {
  private readonly now: () => Date;
  private readonly createId: () => string;
  private readonly clientVersion: string;
  private readonly mediaAcquirer: MediaAcquirer;
  private readonly running = new Map<string, Promise<CaptureJobRecord>>();

  constructor(
    private readonly store: CaptureJobPersistence,
    private readonly api: CaptureApiClient,
    options: CaptureJobCoordinatorOptions = {},
  ) {
    this.now = options.now ?? (() => new Date());
    this.createId = options.createId ?? (() => crypto.randomUUID());
    this.clientVersion = options.clientVersion ?? "0.1.0";
    this.mediaAcquirer = options.mediaAcquirer ?? new BrowserMediaAcquirer();
  }

  async start(input: StartCaptureJobInput): Promise<CaptureJobRecord> {
    const active = this.running.get(input.captureId);
    if (active) {
      const persisted = await this.store.getJob(input.captureId);
      if (persisted?.acceptance) return persisted;
      return active;
    }
    const operation = this.startOrResume(input).finally(() => {
      if (this.running.get(input.captureId) === operation) {
        this.running.delete(input.captureId);
      }
    });
    this.running.set(input.captureId, operation);
    void operation
      .then((accepted) => {
        if (shouldContinueAcceptedJob(accepted)) {
          queueMicrotask(() => {
            void this.run(accepted.captureId, true).catch((error) => {
              console.error(`Could not continue accepted capture ${accepted.captureId}:`, error);
            });
          });
        }
      })
      .catch(() => {
        // The caller observes the rejection; this prevents an unhandled
        // rejection on the detached continuation observer.
      });
    return operation;
  }

  private async startOrResume(input: StartCaptureJobInput): Promise<CaptureJobRecord> {
    const captureId = identifierForAction(input.captureId);
    const existing = await this.store.getJob(captureId);
    if (existing) {
      const permittedSourceOrigins = bindPermittedSourceOrigins(existing.envelope, [
        ...existing.permittedSourceOrigins,
        ...input.permittedSourceOrigins,
      ]);
      if (permittedSourceOrigins.join("\n") !== existing.permittedSourceOrigins.join("\n")) {
        await this.store.saveJob({
          ...existing,
          permittedSourceOrigins,
          updatedAt: this.now().toISOString(),
        });
      }
      if (existing.acceptance) {
        return (await this.store.getJob(captureId))!;
      }
      return this.process(captureId, true, true);
    }
    const completionIdempotencyKey = this.createId();
    const capturedAt = this.now().toISOString();
    const principalId = input.configuration.principalId.trim();
    if (!principalId || principalId.length > 255) {
      throw new Error("The configured FE principal ID must contain 1–255 characters.");
    }
    const envelope = await buildCaptureEnvelope(input.captured, input.edits, {
      captureId,
      capturedAt,
      principalId,
      clientVersion: this.clientVersion,
      createId: this.createId,
    });
    const job: CaptureJobRecord = {
      schemaVersion: 2,
      captureId,
      endpoint: deriveCaptureManifestUrl(input.configuration.endpoint),
      principalId,
      envelope,
      completionIdempotencyKey,
      phase: "pending_manifest",
      terminal: false,
      manifestAttempts: 0,
      permittedSourceOrigins: bindPermittedSourceOrigins(envelope, input.permittedSourceOrigins),
      createdAt: capturedAt,
      updatedAt: capturedAt,
    };

    await this.store.saveJob(job);
    return this.process(job.captureId, false, true);
  }

  async retry(
    captureId: string,
    newlyPermittedSourceOrigins: readonly string[] = [],
  ): Promise<CaptureJobRecord> {
    const prior = this.running.get(captureId);
    const operation = (async () => {
      if (prior) await prior;
      const existing = await this.store.getJob(captureId);
      if (!existing) throw new Error(`Durable capture job ${captureId} was not found.`);
      if (newlyPermittedSourceOrigins.length > 0) {
        const permittedSourceOrigins = bindPermittedSourceOrigins(existing.envelope, [
          ...existing.permittedSourceOrigins,
          ...newlyPermittedSourceOrigins,
        ]);
        await this.store.saveJob({
          ...existing,
          permittedSourceOrigins,
          updatedAt: this.now().toISOString(),
        });
      }
      return this.process(captureId, true, false);
    })().finally(() => {
      if (this.running.get(captureId) === operation) this.running.delete(captureId);
    });
    this.running.set(captureId, operation);
    return operation;
  }

  async resumeAll(): Promise<CaptureJobRecord[]> {
    const jobs = await this.store.listJobs();
    const resumed: CaptureJobRecord[] = [];
    // One job at a time bounds MV3 memory even when many 64 MiB variants were
    // pending at browser startup. Per-variant requests within each job are also
    // sequential and individually timeout.
    for (const job of jobs) {
      if (job.terminal) continue;
      try {
        resumed.push(await this.run(job.captureId, true));
      } catch (error) {
        console.error(`Could not resume durable capture ${job.captureId}:`, error);
      }
    }
    return resumed;
  }

  private run(captureId: string, reconcile: boolean): Promise<CaptureJobRecord> {
    const active = this.running.get(captureId);
    if (active) return active;
    const operation = this.process(captureId, reconcile, false).finally(() => {
      if (this.running.get(captureId) === operation) this.running.delete(captureId);
    });
    this.running.set(captureId, operation);
    return operation;
  }

  private async process(
    captureId: string,
    reconcile: boolean,
    returnAfterAcceptance: boolean,
  ): Promise<CaptureJobRecord> {
    let job = await this.store.getJob(captureId);
    if (!job) throw new Error(`Durable capture job ${captureId} was not found.`);
    if (job.terminal) return job;

    try {
      const reboundOrigins = bindPermittedSourceOrigins(job.envelope, job.permittedSourceOrigins);
      if (reboundOrigins.join("\n") !== job.permittedSourceOrigins.join("\n")) {
        throw new CaptureApiError(
          "Persisted media permission origins no longer match this capture envelope.",
          "invalid_response",
          false,
        );
      }
    } catch (error) {
      return this.failFromError(job, "transfer", error);
    }

    job = await this.update(job, { phase: "preflighting", error: undefined });
    const preflight = await this.api.preflight(job.endpoint);
    if (!preflight.ok) {
      return this.fail(job, "preflight", preflight.message, preflight.kind === "network" || preflight.kind === "server-error");
    }
    const unsupported = unsupportedEnvelopeCapability(job, preflight.capabilities);
    if (unsupported) return this.fail(job, "preflight", unsupported, false);
    job = await this.update(job, { maxAssetBytes: effectiveAssetLimit(preflight.capabilities) });
    const completionAvailable = preflight.capabilities.operations.capture_completion;
    const uploadAvailable = preflight.capabilities.operations.asset_upload;

    if (job.acceptance && !job.transfers) {
      try {
        job = await this.update(job, {
          transfers: planCaptureTransfers(job.envelope, job.endpoint, job.acceptance, this.now().toISOString()),
        });
      } catch (error) {
        return this.failFromError(job, "transfer", error);
      }
    }
    if (job.acceptance && job.transfers) {
      try {
        assertPersistedTransferPlan(job.envelope, job.endpoint, job.acceptance, job.transfers);
        assertTransferRuntimeState(job.transfers);
      } catch (error) {
        return this.failFromError(job, "transfer", error);
      }
    }

    if (reconcile && job.manifestAttempts > 0) {
      job = await this.update(job, { phase: "reconciling" });
      try {
        const status = await this.api.getStatus(job.endpoint, job.captureId);
        if (job.acceptance && !stableIdsMatch(job.acceptance, status)) {
          throw new CaptureApiError(
            "Fragments Engine status changed stable IDs for an accepted capture. The local acceptance was preserved.",
            "invalid_response",
            false,
          );
        }
        const transfers = job.transfers
          ? reconcileTransferStatus(job.transfers, status, this.now().toISOString())
          : undefined;
        job = await this.update(job, {
          status,
          ...(transfers ? { transfers } : {}),
          phase: phaseForStatus(status),
          terminal: terminalCompletion(status.completion),
          error: undefined,
        });
        if (job.terminal) return job;
        if (job.acceptance) return this.continueAccepted(job, uploadAvailable, completionAvailable);
      } catch (error) {
        if (!(error instanceof CaptureApiError) || error.kind !== "not_found") {
          return this.failFromError(job, "reconcile", error);
        }
      }
    } else if (job.acceptance) {
      return this.continueAccepted(job, uploadAvailable, completionAvailable);
    }

    job = await this.update(job, {
      phase: "submitting_manifest",
      manifestAttempts: job.manifestAttempts + 1,
      error: undefined,
    });
    let acceptance: FECaptureManifestResponse;
    try {
      acceptance = await this.api.submitManifest(job.endpoint, job.envelope);
    } catch (error) {
      return this.failFromError(job, "manifest", error);
    }

    if (job.status && !stableIdsMatch(acceptance, job.status)) {
      return this.failFromError(
        job,
        "manifest",
        new CaptureApiError(
          "Manifest replay changed stable IDs after status reconciliation. The reconciled status was preserved.",
          "invalid_response",
          false,
        ),
      );
    }
    try {
      const transfers = planCaptureTransfers(job.envelope, job.endpoint, acceptance, this.now().toISOString());
      job = await this.update(job, {
        acceptance,
        transfers,
        phase: hasRequestedUploads(acceptance) ? "pending_transfer" : "accepted",
        terminal: false,
        error: undefined,
      });
    } catch (error) {
      job = await this.update(job, { acceptance, phase: "accepted", terminal: false });
      return this.failFromError(job, "transfer", error);
    }
    if (returnAfterAcceptance) return job;
    return this.continueAccepted(job, uploadAvailable, completionAvailable);
  }

  private async continueAccepted(
    job: CaptureJobRecord,
    uploadAvailable: boolean,
    completionAvailable: boolean,
  ): Promise<CaptureJobRecord> {
    if (!job.acceptance || !job.transfers) return job;
    if (hasRequestedUploads(job.acceptance) && !uploadAvailable) {
      return this.fail(
        job,
        "transfer",
        "Fragments Engine accepted the manifest but does not advertise asset upload. The accepted IDs and pending variants were retained.",
        true,
      );
    }

    const variants = envelopeVariants(job.envelope);
    const transferOrder = job.transfers
      .map((_, index) => index)
      .sort((left, right) => {
        const leftVariant = variants.get(job.transfers![left]!.clientVariantId)!.variant;
        const rightVariant = variants.get(job.transfers![right]!.clientVariantId)!.variant;
        const expiry = (value: string | undefined) => {
          const parsed = value ? Date.parse(value) : Number.NaN;
          return Number.isNaN(parsed) ? Number.POSITIVE_INFINITY : parsed;
        };
        return expiry(leftVariant.source_expires_at) - expiry(rightVariant.source_expires_at) || left - right;
      });
    for (const index of transferOrder) {
      let transfer = job.transfers![index]!;
      if (transfer.action !== "request_upload" || transfer.state === "available") continue;
      if (transfer.state === "failed" && transfer.retryable === false) continue;
      const observed = variants.get(transfer.clientVariantId)!;
      if (!transfer.sourceOrigin || !job.permittedSourceOrigins.includes(transfer.sourceOrigin)) {
        const error = new MediaTransferError(
          `Media permission was denied for ${transfer.sourceOrigin ?? "the source origin"}; variant ${transfer.clientVariantId} remains retryable.`,
          "permission",
          true,
        );
        job = await this.recordTransferFailure(job, index, error);
        continue;
      }

      transfer = {
        ...transfer,
        state: "transferring",
        attempts: transfer.attempts + 1,
        updatedAt: this.now().toISOString(),
      };
      delete transfer.outcome;
      delete transfer.reason;
      delete transfer.retryable;
      job = await this.replaceTransfer(job, index, transfer, { phase: "pending_transfer", error: undefined });

      let acquired: AcquiredAsset;
      try {
        acquired = await this.mediaAcquirer.acquire(
          observed.variant,
          job.permittedSourceOrigins,
          job.maxAssetBytes ?? DEFAULT_MAX_ASSET_BYTES,
        );
        if (transfer.expectedDigest && acquired.digest.value !== transfer.expectedDigest.value) {
          throw new MediaTransferError(
            `Variant ${transfer.clientVariantId} did not match Fragments Engine's expected SHA-256 digest.`,
            "digest_mismatch",
            false,
          );
        }

      } catch (error) {
        if (!(error instanceof MediaTransferError)) throw error;
        job = await this.recordTransferFailure(job, index, error);
        continue;
      }

      transfer = {
        ...transfer,
        digest: acquired.digest,
        byteSize: acquired.byteSize,
        mimeType: acquired.mimeType,
        observedMimeType: acquired.observedMimeType,
        ...(acquired.width ? { width: acquired.width } : {}),
        ...(acquired.height ? { height: acquired.height } : {}),
        updatedAt: this.now().toISOString(),
      };
      // Persistence failure here must escape: without this durability boundary
      // no PUT may begin and no permanent per-asset failure may be invented.
      job = await this.replaceTransfer(job, index, transfer);
      try {
        await this.api.uploadAsset(
          job.endpoint,
          job.captureId,
          observed.variant,
          transfer.uploadUrl!,
          acquired.bytes,
          acquired.mimeType,
          acquired.digest,
        );
      } catch (error) {
        if (!(error instanceof CaptureApiError)) throw error;
        job = await this.recordTransferFailure(job, index, error);
        continue;
      }
      transfer = {
        ...transfer,
        state: "available",
        outcome: {
          client_variant_id: transfer.clientVariantId,
          outcome: "uploaded",
          digest: acquired.digest,
          byte_size: acquired.byteSize,
        },
        updatedAt: this.now().toISOString(),
      };
      // A failed post-204 write also escapes. The persisted transferring state
      // forces GET-first reconciliation on restart instead of false completion.
      job = await this.replaceTransfer(job, index, transfer, { error: undefined });
    }

    if (!transfersReadyForCompletion(job.transfers!)) {
      return this.update(job, { phase: "pending_transfer", terminal: false });
    }
    if (!completionAvailable) {
      return this.fail(
        job,
        "completion",
        "Fragments Engine accepted the manifest but does not advertise capture completion. Final asset outcomes were retained for retry after the server is upgraded.",
        true,
      );
    }

    const completion: FECaptureCompletionRequest = {
      schema_version: "fe.capture.completion.v1",
      idempotency_key: job.completionIdempotencyKey,
      assets: transferCompletionOutcomes(job.transfers!),
      warnings: transferWarnings(job),
    };
    const acceptance = job.acceptance!;
    assertFECaptureCompletion(completion);
    job = await this.update(job, { phase: "completing", error: undefined });
    try {
      const status = await this.api.completeCapture(job.endpoint, job.captureId, completion);
      if (!stableIdsMatch(acceptance, status)) {
        throw new CaptureApiError(
          "Capture completion response changed stable accepted IDs. The original acceptance was preserved.",
          "invalid_response",
          false,
        );
      }
      return this.update(job, {
        status,
        phase: phaseForStatus(status),
        terminal: terminalCompletion(status.completion),
        error: undefined,
      });
    } catch (error) {
      return this.failFromError(job, "completion", error);
    }
  }

  private async recordTransferFailure(
    job: CaptureJobRecord,
    index: number,
    error: unknown,
  ): Promise<CaptureJobRecord> {
    const detail = captureError(error, "transfer", this.now().toISOString());
    const previous = job.transfers![index]!;
    const transfer: CaptureVariantTransfer = {
      ...previous,
      state: "failed",
      reason: detail.message,
      retryable: detail.retryable,
      outcome: {
        client_variant_id: previous.clientVariantId,
        outcome: "failed",
        reason: detail.message,
        retryable: detail.retryable,
      },
      updatedAt: detail.at,
    };
    return this.replaceTransfer(job, index, transfer, {
      phase: "pending_transfer",
      terminal: false,
      error: detail,
    });
  }

  private replaceTransfer(
    job: CaptureJobRecord,
    index: number,
    transfer: CaptureVariantTransfer,
    changes: Partial<CaptureJobRecord> = {},
  ): Promise<CaptureJobRecord> {
    const transfers = [...(job.transfers ?? [])];
    transfers[index] = transfer;
    return this.update(job, { ...changes, transfers });
  }

  private async failFromError(
    job: CaptureJobRecord,
    stage: CaptureJobErrorStage,
    error: unknown,
  ): Promise<CaptureJobRecord> {
    const detail = captureError(error, stage, this.now().toISOString());
    const terminal = !job.acceptance && error instanceof CaptureApiError && error.kind === "conflict";
    return this.update(job, {
      phase: job.acceptance
        ? hasRequestedUploads(job.acceptance)
          ? "pending_transfer"
          : "accepted"
        : "failed",
      terminal,
      error: detail,
    });
  }

  private fail(
    job: CaptureJobRecord,
    stage: CaptureJobErrorStage,
    message: string,
    retryable: boolean,
  ): Promise<CaptureJobRecord> {
    return this.update(job, {
      phase: job.acceptance
        ? hasRequestedUploads(job.acceptance)
          ? "pending_transfer"
          : "accepted"
        : "failed",
      terminal: false,
      error: { stage, message, retryable, at: this.now().toISOString() },
    });
  }

  private async update(
    job: CaptureJobRecord,
    changes: Partial<CaptureJobRecord>,
  ): Promise<CaptureJobRecord> {
    const updated: CaptureJobRecord = {
      ...job,
      ...changes,
      updatedAt: this.now().toISOString(),
    };
    if (Object.hasOwn(changes, "error") && changes.error === undefined) delete updated.error;
    await this.store.saveJob(updated);
    return updated;
  }
}
