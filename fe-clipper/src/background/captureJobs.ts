import { buildCaptureEnvelope } from "@/capture/envelope";
import {
  assertFECaptureCompletion,
  type FEAssetInstruction,
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
} from "@/lib/jobStorage";
import type { CaptureConfigurationSnapshot, CapturedPage, EditableFields } from "@/lib/types";
import {
  CaptureApiError,
  deriveCaptureManifestUrl,
  type CaptureApiClient,
} from "./captureApi";

export interface StartCaptureJobInput {
  captureId: string;
  captured: CapturedPage;
  edits: EditableFields;
  configuration: CaptureConfigurationSnapshot;
}

export interface CaptureJobCoordinatorOptions {
  now?: () => Date;
  createId?: () => string;
  clientVersion?: string;
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

function stableIdsMatch(
  acceptance: FECaptureManifestResponse,
  status: FECaptureStatus,
): boolean {
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

function completionOutcome(instruction: FEAssetInstruction) {
  switch (instruction.action) {
    case "reuse_blob": {
      if (!instruction.digest) throw new Error("FE reuse_blob instruction is missing its digest.");
      return {
        client_variant_id: instruction.client_variant_id,
        outcome: "already_available" as const,
        digest: instruction.digest,
      };
    }
    case "rejected":
      return {
        client_variant_id: instruction.client_variant_id,
        outcome: "not_available" as const,
        reason: instruction.reason || "Fragments Engine rejected this capture variant.",
        retryable: false,
      };
    case "server_acquire":
    case "reference_only":
      return { client_variant_id: instruction.client_variant_id, outcome: "deferred" as const };
    case "request_upload":
      throw new Error("Capture completion cannot run while browser uploads remain requested.");
  }
}

function captureError(
  error: unknown,
  stage: CaptureJobErrorStage,
  now: string,
): CaptureJobError {
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

export class CaptureJobCoordinator {
  private readonly now: () => Date;
  private readonly createId: () => string;
  private readonly clientVersion: string;
  private readonly running = new Map<string, Promise<CaptureJobRecord>>();

  constructor(
    private readonly store: CaptureJobPersistence,
    private readonly api: CaptureApiClient,
    options: CaptureJobCoordinatorOptions = {},
  ) {
    this.now = options.now ?? (() => new Date());
    this.createId = options.createId ?? (() => crypto.randomUUID());
    this.clientVersion = options.clientVersion ?? "0.1.0";
  }

  async start(input: StartCaptureJobInput): Promise<CaptureJobRecord> {
    const active = this.running.get(input.captureId);
    if (active) return active;
    const operation = this.startOrResume(input).finally(() => {
      this.running.delete(input.captureId);
    });
    this.running.set(input.captureId, operation);
    void operation
      .then((accepted) => {
        if (accepted.acceptance && accepted.phase === "accepted" && !accepted.terminal) {
          // Run only after the starting-operation lock is released.
          queueMicrotask(() => {
            void this.retry(accepted.captureId).catch((error) => {
              console.error(`Could not continue accepted capture ${accepted.captureId}:`, error);
            });
          });
        }
      })
      .catch(() => {
        // The caller receives the original rejection; this branch only keeps
        // the detached continuation observer from becoming unhandled.
      });
    return operation;
  }

  private async startOrResume(input: StartCaptureJobInput): Promise<CaptureJobRecord> {
    const captureId = identifierForAction(input.captureId);
    const existing = await this.store.getJob(captureId);
    if (existing) {
      // Repeated START is intentional: the popup may have lost the response
      // after the worker already persisted or submitted this exact action.
      return this.process(captureId, true, false);
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
      schemaVersion: 1,
      captureId,
      endpoint: deriveCaptureManifestUrl(input.configuration.endpoint),
      principalId,
      envelope,
      completionIdempotencyKey,
      phase: "pending_manifest",
      terminal: false,
      manifestAttempts: 0,
      createdAt: capturedAt,
      updatedAt: capturedAt,
    };

    // This awaited write is the durability boundary: no capability or capture
    // network request starts until the exact envelope and mutable settings
    // snapshot are safely outside popup/service-worker memory.
    await this.store.saveJob(job);
    return this.process(job.captureId, false, true);
  }

  retry(captureId: string): Promise<CaptureJobRecord> {
    return this.run(captureId, true);
  }

  async resumeAll(): Promise<CaptureJobRecord[]> {
    const jobs = await this.store.listJobs();
    const results = await Promise.allSettled(
      jobs.filter((job) => !job.terminal).map((job) => this.run(job.captureId, true)),
    );
    return results
      .filter((result): result is PromiseFulfilledResult<CaptureJobRecord> => result.status === "fulfilled")
      .map((result) => result.value);
  }

  private run(
    captureId: string,
    reconcile: boolean,
    returnAfterAcceptance = false,
  ): Promise<CaptureJobRecord> {
    const active = this.running.get(captureId);
    if (active) return active;
    const operation = this.process(captureId, reconcile, returnAfterAcceptance).finally(() => {
      this.running.delete(captureId);
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

    job = await this.update(job, { phase: "preflighting", error: undefined });
    const preflight = await this.api.preflight(job.endpoint);
    if (!preflight.ok) {
      return this.fail(job, "preflight", preflight.message, preflight.kind === "network" || preflight.kind === "server-error");
    }
    const unsupported = unsupportedEnvelopeCapability(job, preflight.capabilities);
    if (unsupported) return this.fail(job, "preflight", unsupported, false);
    const completionAvailable = preflight.capabilities.operations.capture_completion;

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
        job = await this.update(job, {
          status,
          phase: phaseForStatus(status),
          terminal: terminalCompletion(status.completion),
          error: undefined,
        });
        if (job.terminal) return job;
        if (job.acceptance) return this.continueAccepted(job, completionAvailable);
        // GET proved the manifest exists but does not carry transfer
        // instructions. Replaying the same persisted envelope recovers the
        // immutable acceptance snapshot without creating another attempt.
      } catch (error) {
        if (!(error instanceof CaptureApiError) || error.kind !== "not_found") {
          return this.failFromError(job, "reconcile", error);
        }
        // A 404 is the only reconciliation result that authorizes resending.
      }
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
    job = await this.update(job, {
      acceptance,
      phase: hasRequestedUploads(acceptance) ? "pending_transfer" : "accepted",
      terminal: false,
      error: undefined,
    });
    if (returnAfterAcceptance && !hasRequestedUploads(acceptance)) return job;
    return this.continueAccepted(job, completionAvailable);
  }

  private async continueAccepted(
    job: CaptureJobRecord,
    completionAvailable: boolean,
  ): Promise<CaptureJobRecord> {
    if (!job.acceptance) return job;
    if (hasRequestedUploads(job.acceptance)) {
      return this.update(job, { phase: "pending_transfer", terminal: false });
    }
    if (!completionAvailable) {
      return this.fail(
        job,
        "completion",
        "Fragments Engine accepted the manifest but does not advertise capture completion. The accepted IDs were retained for retry after the server is upgraded.",
        true,
      );
    }

    const completion: FECaptureCompletionRequest = {
      schema_version: "fe.capture.completion.v1",
      idempotency_key: job.completionIdempotencyKey,
      assets: job.acceptance.asset_instructions.map(completionOutcome),
      warnings: job.acceptance.warnings,
    };
    const acceptance = job.acceptance;
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

  private async failFromError(
    job: CaptureJobRecord,
    stage: CaptureJobErrorStage,
    error: unknown,
  ): Promise<CaptureJobRecord> {
    const detail = captureError(error, stage, this.now().toISOString());
    const terminal =
      !job.acceptance && error instanceof CaptureApiError && error.kind === "conflict";
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
    if (changes.error === undefined) delete updated.error;
    await this.store.saveJob(updated);
    return updated;
  }
}
