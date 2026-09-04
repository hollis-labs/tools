import {
  assertFECaptureEnvelope,
  assertFECaptureManifestResponse,
  assertFECaptureStatus,
  type FECaptureEnvelope,
  type FECaptureManifestResponse,
  type FECaptureStatus,
} from "@/contracts/fragmentsEngine";
import type { CaptureJobPhase } from "./types";

const JOB_STORAGE_PREFIX = "feClipperCaptureJob.v1.";
const MAX_RETAINED_TERMINAL_JOBS = 20;
const TERMINAL_RETENTION_MS = 30 * 24 * 60 * 60 * 1_000;
const JOB_PHASES = new Set<CaptureJobPhase>([
  "pending_manifest",
  "preflighting",
  "reconciling",
  "submitting_manifest",
  "accepted",
  "pending_transfer",
  "completing",
  "complete",
  "partial",
  "failed",
]);

export type CaptureJobErrorStage = "preflight" | "reconcile" | "manifest" | "completion";

export interface CaptureJobError {
  stage: CaptureJobErrorStage;
  message: string;
  retryable: boolean;
  at: string;
  httpStatus?: number;
  code?: string;
}

export interface CaptureJobRecord {
  schemaVersion: 1;
  captureId: string;
  endpoint: string;
  principalId: string;
  envelope: FECaptureEnvelope;
  completionIdempotencyKey: string;
  phase: CaptureJobPhase;
  terminal: boolean;
  manifestAttempts: number;
  createdAt: string;
  updatedAt: string;
  acceptance?: FECaptureManifestResponse;
  status?: FECaptureStatus;
  error?: CaptureJobError;
}

export interface CaptureJobPersistence {
  getJob(captureId: string): Promise<CaptureJobRecord | undefined>;
  listJobs(): Promise<CaptureJobRecord[]>;
  saveJob(job: CaptureJobRecord): Promise<void>;
}

export interface StorageAreaLike {
  get(keys?: string | null): Promise<Record<string, unknown>>;
  set(items: Record<string, unknown>): Promise<void>;
  remove(keys: string | string[]): Promise<void>;
}

export class CaptureJobStorageQuotaError extends Error {
  constructor(cause?: unknown) {
    super(
      "Chrome capture-job storage is full and no removable corrupt or completed jobs remain. " +
        "The exact job update could not be stored, and active jobs were preserved. Retry the " +
        "same action or capture to reconcile, finish pending transfers, or reduce the capture size.",
      { cause },
    );
    this.name = "CaptureJobStorageQuotaError";
  }
}

function jobKey(captureId: string): string {
  return `${JOB_STORAGE_PREFIX}${captureId}`;
}

function quotaExceeded(error: unknown): boolean {
  if (!(error instanceof Error)) return false;
  return (
    error.name === "QuotaExceededError" ||
    /quota(?:_bytes)?|storage.*full|exceeded.*storage/i.test(error.message)
  );
}

function decodeJob(value: unknown): CaptureJobRecord {
  if (!value || typeof value !== "object") throw new Error("Stored capture job is not an object.");
  const job = value as Partial<CaptureJobRecord>;
  if (
    job.schemaVersion !== 1 ||
    typeof job.captureId !== "string" ||
    typeof job.endpoint !== "string" ||
    typeof job.principalId !== "string" ||
    typeof job.completionIdempotencyKey !== "string" ||
    typeof job.phase !== "string" ||
    !JOB_PHASES.has(job.phase as CaptureJobPhase) ||
    typeof job.terminal !== "boolean" ||
    !Number.isSafeInteger(job.manifestAttempts) ||
    (job.manifestAttempts ?? -1) < 0 ||
    typeof job.createdAt !== "string" ||
    typeof job.updatedAt !== "string"
  ) {
    throw new Error("Stored capture job metadata is incomplete.");
  }
  const endpoint = new URL(job.endpoint);
  if (
    (endpoint.protocol !== "http:" && endpoint.protocol !== "https:") ||
    endpoint.username ||
    endpoint.password
  ) {
    throw new Error("Stored capture job endpoint is unsafe.");
  }
  if (
    !job.captureId.trim() ||
    job.captureId.length > 255 ||
    !job.principalId.trim() ||
    job.principalId.length > 255 ||
    !job.completionIdempotencyKey.trim() ||
    job.completionIdempotencyKey.length > 255 ||
    Number.isNaN(Date.parse(job.createdAt)) ||
    Number.isNaN(Date.parse(job.updatedAt))
  ) {
    throw new Error("Stored capture job identifiers or timestamps are invalid.");
  }
  if (job.terminal && !["complete", "partial", "failed"].includes(job.phase)) {
    throw new Error("Stored terminal capture job has a nonterminal phase.");
  }
  if (!job.terminal && ["complete", "partial"].includes(job.phase)) {
    throw new Error("Stored completed capture job is not marked terminal.");
  }
  assertFECaptureEnvelope(job.envelope);
  if (job.envelope.capture_id !== job.captureId || job.envelope.principal_id !== job.principalId) {
    throw new Error("Stored capture job identifiers do not match its envelope.");
  }
  if (job.acceptance) {
    assertFECaptureManifestResponse(job.acceptance);
    if (job.acceptance.capture_id !== job.captureId) {
      throw new Error("Stored capture acceptance belongs to another capture.");
    }
  }
  if (job.status) {
    assertFECaptureStatus(job.status);
    if (job.status.capture_id !== job.captureId) {
      throw new Error("Stored capture status belongs to another capture.");
    }
  }
  if (
    job.acceptance &&
    job.status &&
    (job.acceptance.fragment_id !== job.status.fragment_id ||
      job.acceptance.fragment_revision_id !== job.status.fragment_revision_id ||
      job.acceptance.capture_attempt_id !== job.status.capture_attempt_id)
  ) {
    throw new Error("Stored capture acceptance and status stable IDs do not match.");
  }
  if (job.error) {
    if (
      !["preflight", "reconcile", "manifest", "completion"].includes(job.error.stage) ||
      typeof job.error.message !== "string" ||
      !job.error.message.trim() ||
      typeof job.error.retryable !== "boolean" ||
      typeof job.error.at !== "string" ||
      Number.isNaN(Date.parse(job.error.at)) ||
      (job.error.httpStatus !== undefined &&
        (!Number.isInteger(job.error.httpStatus) ||
          job.error.httpStatus < 100 ||
          job.error.httpStatus > 599)) ||
      (job.error.code !== undefined &&
        (typeof job.error.code !== "string" || !job.error.code.trim()))
    ) {
      throw new Error("Stored capture job error is invalid.");
    }
  }
  return job as CaptureJobRecord;
}

function decodeStoredJob(key: string, value: unknown): CaptureJobRecord {
  const job = decodeJob(value);
  if (key !== jobKey(job.captureId)) {
    throw new Error("Stored capture job key does not match its capture ID.");
  }
  return job;
}

function terminalKeysToPrune(
  entries: readonly { key: string; job: CaptureJobRecord }[],
  now: Date,
): Set<string> {
  const cutoff = now.getTime() - TERMINAL_RETENTION_MS;
  const terminal = entries
    .filter(({ job }) => job.terminal)
    .sort((left, right) => {
      const byUpdatedAt = right.job.updatedAt.localeCompare(left.job.updatedAt);
      return byUpdatedAt || right.job.captureId.localeCompare(left.job.captureId);
    });
  return new Set(
    terminal
      .filter(({ job }, index) => Date.parse(job.updatedAt) < cutoff || index >= MAX_RETAINED_TERMINAL_JOBS)
      .map(({ key }) => key),
  );
}

export class CaptureJobStore implements CaptureJobPersistence {
  constructor(
    private readonly storage: StorageAreaLike = chrome.storage.local,
    private readonly reportCorruption: (key: string, error: Error) => void = (key, error) => {
      console.error(`Skipping corrupt durable capture job ${key}:`, error);
    },
    private readonly now: () => Date = () => new Date(),
  ) {}

  async getJob(captureId: string): Promise<CaptureJobRecord | undefined> {
    const key = jobKey(captureId);
    const values = await this.storage.get(key);
    return values[key] === undefined ? undefined : decodeStoredJob(key, values[key]);
  }

  async listJobs(): Promise<CaptureJobRecord[]> {
    const values = await this.storage.get(null);
    const entries: { key: string; job: CaptureJobRecord }[] = [];
    for (const [key, value] of Object.entries(values)) {
      if (!key.startsWith(JOB_STORAGE_PREFIX)) continue;
      try {
        entries.push({ key, job: decodeStoredJob(key, value) });
      } catch (error) {
        this.reportCorruption(key, error instanceof Error ? error : new Error(String(error)));
      }
    }
    const pruned = terminalKeysToPrune(entries, this.now());
    const removed = await this.removeTerminalJobsBestEffort(pruned);
    return entries
      .filter(({ key }) => !removed || !pruned.has(key))
      .map(({ job }) => job)
      .sort((left, right) => left.createdAt.localeCompare(right.createdAt));
  }

  async saveJob(job: CaptureJobRecord): Promise<void> {
    const validated = decodeJob(job);
    const key = jobKey(validated.captureId);
    const exactWrite = { [key]: validated };
    try {
      await this.storage.set(exactWrite);
    } catch (error) {
      if (!quotaExceeded(error)) throw error;
      await this.recoverQuotaAndSave(exactWrite, error);
    }
    await this.pruneTerminalJobs();
  }

  private async recoverQuotaAndSave(
    exactWrite: Record<string, CaptureJobRecord>,
    initialError: unknown,
  ): Promise<void> {
    const values = await this.storage.get(null);
    const corruptKeys: string[] = [];
    const terminalEntries: { key: string; job: CaptureJobRecord }[] = [];
    for (const [key, value] of Object.entries(values)) {
      if (!key.startsWith(JOB_STORAGE_PREFIX)) continue;
      try {
        const stored = decodeStoredJob(key, value);
        if (stored.terminal) terminalEntries.push({ key, job: stored });
      } catch (error) {
        corruptKeys.push(key);
        this.reportCorruption(key, error instanceof Error ? error : new Error(String(error)));
      }
    }

    let quotaError = initialError;
    if (corruptKeys.length > 0) {
      await this.storage.remove(corruptKeys);
      try {
        await this.storage.set(exactWrite);
        return;
      } catch (error) {
        if (!quotaExceeded(error)) throw error;
        quotaError = error;
      }
    }

    terminalEntries.sort((left, right) => {
      const byUpdatedAt = left.job.updatedAt.localeCompare(right.job.updatedAt);
      return byUpdatedAt || left.job.captureId.localeCompare(right.job.captureId);
    });
    for (const { key } of terminalEntries) {
      await this.storage.remove(key);
      try {
        await this.storage.set(exactWrite);
        return;
      } catch (error) {
        if (!quotaExceeded(error)) throw error;
        quotaError = error;
      }
    }
    throw new CaptureJobStorageQuotaError(quotaError);
  }

  private async pruneTerminalJobs(): Promise<void> {
    const values = await this.storage.get(null);
    const entries: { key: string; job: CaptureJobRecord }[] = [];
    for (const [key, value] of Object.entries(values)) {
      if (!key.startsWith(JOB_STORAGE_PREFIX)) continue;
      try {
        entries.push({ key, job: decodeStoredJob(key, value) });
      } catch {
        // Corrupt records are quarantined only when quota recovery needs space.
      }
    }
    const pruned = terminalKeysToPrune(entries, this.now());
    await this.removeTerminalJobsBestEffort(pruned);
  }

  private async removeTerminalJobsBestEffort(keys: ReadonlySet<string>): Promise<boolean> {
    if (keys.size === 0) return true;
    try {
      await this.storage.remove([...keys]);
      return true;
    } catch (error) {
      console.error("Could not prune completed durable capture jobs:", error);
      return false;
    }
  }
}
