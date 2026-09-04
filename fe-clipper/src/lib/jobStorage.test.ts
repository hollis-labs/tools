import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it, vi } from "vitest";

import type { FECaptureEnvelope } from "@/contracts/fragmentsEngine";
import {
  CaptureJobStorageQuotaError,
  CaptureJobStore,
  type CaptureJobRecord,
  type StorageAreaLike,
} from "./jobStorage";

const JOB_PREFIX = "feClipperCaptureJob.v1.";

const fixtureDirectory = resolve(
  process.cwd(),
  "vendor/fragments-engine-browser-contract-v1/fixtures",
);

class MemoryStorage implements StorageAreaLike {
  values: Record<string, unknown> = {};
  readonly removed: string[] = [];

  async get(keys?: string | null): Promise<Record<string, unknown>> {
    if (typeof keys === "string") return keys in this.values ? { [keys]: this.values[keys] } : {};
    return { ...this.values };
  }

  async set(items: Record<string, unknown>): Promise<void> {
    Object.assign(this.values, structuredClone(items));
  }

  async remove(keys: string | string[]): Promise<void> {
    for (const key of typeof keys === "string" ? [keys] : keys) {
      this.removed.push(key);
      delete this.values[key];
    }
  }
}

class QuotaStorage extends MemoryStorage {
  readonly blockers = new Set<string>();
  alwaysFull = false;

  override async set(items: Record<string, unknown>): Promise<void> {
    if (this.alwaysFull || [...this.blockers].some((key) => key in this.values)) {
      const error = new Error("QUOTA_BYTES quota exceeded");
      error.name = "QuotaExceededError";
      throw error;
    }
    await super.set(items);
  }
}

function job(captureId = "01KCAPTURE0000000000000000"): CaptureJobRecord {
  const envelope = JSON.parse(
    readFileSync(join(fixtureDirectory, "valid-capture-youtube.json"), "utf8"),
  ) as FECaptureEnvelope;
  envelope.capture_id = captureId;
  return {
    schemaVersion: 1,
    captureId,
    endpoint: "http://127.0.0.1:8091/v1/captures",
    principalId: envelope.principal_id,
    envelope,
    completionIdempotencyKey: `${captureId}:complete`,
    phase: "pending_manifest",
    terminal: false,
    manifestAttempts: 0,
    createdAt: "2026-09-03T20:00:00.000Z",
    updatedAt: "2026-09-03T20:00:00.000Z",
  };
}

function terminalJob(captureId: string, timestamp: string): CaptureJobRecord {
  return {
    ...job(captureId),
    phase: "complete",
    terminal: true,
    createdAt: timestamp,
    updatedAt: timestamp,
  };
}

describe("durable capture job storage", () => {
  it("round-trips one exact envelope and settings snapshot per capture key", async () => {
    const storage = new MemoryStorage();
    const store = new CaptureJobStore(storage);
    const expected = job();

    await store.saveJob(expected);

    expect(await store.getJob(expected.captureId)).toEqual(expected);
    expect(await store.listJobs()).toEqual([expected]);
  });

  it("rejects unsafe or internally inconsistent durable state", async () => {
    const store = new CaptureJobStore(new MemoryStorage());
    await expect(
      store.saveJob({ ...job(), endpoint: "https://user:secret@fe.example/v1/captures" }),
    ).rejects.toThrow(/unsafe/);
    await expect(
      store.saveJob({ ...job(), manifestAttempts: -1 }),
    ).rejects.toThrow(/metadata/);
    await expect(
      store.saveJob({ ...job(), terminal: true, phase: "pending_manifest" }),
    ).rejects.toThrow(/nonterminal phase/);
    await expect(
      store.saveJob({
        ...job(),
        error: { stage: "manifest", message: "", retryable: true, at: "not-a-date" },
      }),
    ).rejects.toThrow(/error is invalid/);
    const mismatched = job();
    mismatched.envelope.capture_id = "another-capture";
    await expect(store.saveJob(mismatched)).rejects.toThrow(/do not match/);
  });

  it("isolates corrupt records so healthy restart jobs still resume", async () => {
    const storage = new MemoryStorage();
    const healthy = job();
    await new CaptureJobStore(storage).saveJob(healthy);
    storage.values["feClipperCaptureJob.v1.corrupt"] = { schemaVersion: 1, phase: "invented" };
    const report = vi.fn();
    const store = new CaptureJobStore(storage, report);

    expect(await store.listJobs()).toEqual([healthy]);
    expect(report).toHaveBeenCalledOnce();
    expect(report.mock.calls[0]?.[0]).toContain("corrupt");
  });

  it("rejects a job stored under a different capture key", async () => {
    const storage = new MemoryStorage();
    storage.values[`${JOB_PREFIX}wrong-id`] = job("real-id");
    const report = vi.fn();
    const store = new CaptureJobStore(storage, report);

    await expect(store.getJob("wrong-id")).rejects.toThrow(/key does not match/);
    expect(await store.listJobs()).toEqual([]);
    expect(report).toHaveBeenCalledWith(
      `${JOB_PREFIX}wrong-id`,
      expect.objectContaining({ message: expect.stringMatching(/key does not match/) }),
    );
  });

  it("bounds terminal retention by age and count without removing active jobs", async () => {
    const storage = new MemoryStorage();
    const active = job("active-transfer");
    storage.values[`${JOB_PREFIX}${active.captureId}`] = active;
    for (let index = 0; index < 24; index += 1) {
      const captureId = `terminal-${index.toString().padStart(2, "0")}`;
      const timestamp = new Date(Date.UTC(2026, 8, 3, 20, 0, index)).toISOString();
      storage.values[`${JOB_PREFIX}${captureId}`] = terminalJob(captureId, timestamp);
    }
    const expired = terminalJob("terminal-expired", "2026-07-01T00:00:00.000Z");
    storage.values[`${JOB_PREFIX}${expired.captureId}`] = expired;
    const store = new CaptureJobStore(storage, undefined, () => new Date("2026-09-04T00:00:00Z"));

    const retained = await store.listJobs();

    expect(retained.filter((item) => item.terminal)).toHaveLength(20);
    expect(retained).toContainEqual(active);
    expect(storage.values[`${JOB_PREFIX}${active.captureId}`]).toEqual(active);
    expect(storage.values[`${JOB_PREFIX}terminal-expired`]).toBeUndefined();
    expect(storage.values[`${JOB_PREFIX}terminal-00`]).toBeUndefined();
    expect(storage.values[`${JOB_PREFIX}terminal-23`]).toBeDefined();
  });

  it("does not turn a successful durable save into failure when retention cleanup fails", async () => {
    class RemovalFailureStorage extends MemoryStorage {
      override async remove(): Promise<void> {
        throw new Error("temporary storage cleanup failure");
      }
    }
    const storage = new RemovalFailureStorage();
    const saved = terminalJob("completed-action", "2026-07-01T00:00:00.000Z");
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const store = new CaptureJobStore(
      storage,
      undefined,
      () => new Date("2026-09-04T00:00:00.000Z"),
    );

    await expect(store.saveJob(saved)).resolves.toBeUndefined();

    expect(await store.getJob(saved.captureId)).toEqual(saved);
    expect(consoleError).toHaveBeenCalledWith(
      "Could not prune completed durable capture jobs:",
      expect.any(Error),
    );
    consoleError.mockRestore();
  });

  it("recovers a quota write by pruning the oldest terminal job and preserves active jobs", async () => {
    const storage = new QuotaStorage();
    const active = job("active-transfer");
    const oldest = terminalJob("terminal-oldest", "2026-09-01T00:00:00.000Z");
    const newer = terminalJob("terminal-newer", "2026-09-02T00:00:00.000Z");
    for (const existing of [active, oldest, newer]) {
      storage.values[`${JOB_PREFIX}${existing.captureId}`] = existing;
    }
    storage.blockers.add(`${JOB_PREFIX}${oldest.captureId}`);
    const incoming = job("new-action");

    await new CaptureJobStore(storage).saveJob(incoming);

    expect(await new CaptureJobStore(storage).getJob(incoming.captureId)).toEqual(incoming);
    expect(storage.removed).toContain(`${JOB_PREFIX}${oldest.captureId}`);
    expect(storage.values[`${JOB_PREFIX}${newer.captureId}`]).toEqual(newer);
    expect(storage.values[`${JOB_PREFIX}${active.captureId}`]).toEqual(active);
  });

  it("uses corrupt and mis-keyed records for quota recovery without touching active jobs", async () => {
    const storage = new QuotaStorage();
    const active = job("active-transfer");
    const corruptKey = `${JOB_PREFIX}mis-keyed`;
    storage.values[`${JOB_PREFIX}${active.captureId}`] = active;
    storage.values[corruptKey] = job("different-id");
    storage.blockers.add(corruptKey);
    const report = vi.fn();
    const incoming = job("new-action");

    await new CaptureJobStore(storage, report).saveJob(incoming);

    expect(storage.removed).toContain(corruptKey);
    expect(storage.values[`${JOB_PREFIX}${active.captureId}`]).toEqual(active);
    expect(storage.values[`${JOB_PREFIX}${incoming.captureId}`]).toEqual(incoming);
    expect(report).toHaveBeenCalledWith(corruptKey, expect.any(Error));
  });

  it("fails actionably when active jobs exhaust quota and never evicts them", async () => {
    const storage = new QuotaStorage();
    const active = job("active-transfer");
    storage.values[`${JOB_PREFIX}${active.captureId}`] = active;
    storage.alwaysFull = true;

    await expect(new CaptureJobStore(storage).saveJob(job("new-action"))).rejects.toThrow(
      CaptureJobStorageQuotaError,
    );
    expect(storage.removed).toEqual([]);
    expect(storage.values[`${JOB_PREFIX}${active.captureId}`]).toEqual(active);
    expect(storage.values[`${JOB_PREFIX}new-action`]).toBeUndefined();
  });
});
