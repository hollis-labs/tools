import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it, vi } from "vitest";

import type {
  FECapabilityDiscovery,
  FECaptureCompletionRequest,
  FECaptureEnvelope,
  FECaptureManifestResponse,
  FECaptureStatus,
} from "@/contracts/fragmentsEngine";
import type {
  CaptureJobPersistence,
  CaptureJobRecord,
} from "@/lib/jobStorage";
import { CaptureJobStorageQuotaError } from "@/lib/jobStorage";
import type { PageCaptureResult } from "@/capture/types";
import { CaptureApiError, type CaptureApiClient } from "./captureApi";
import { CaptureJobCoordinator } from "./captureJobs";

const fixtureDirectory = resolve(
  process.cwd(),
  "vendor/fragments-engine-browser-contract-v1/fixtures",
);

function fixture<T>(name: string): T {
  return JSON.parse(readFileSync(join(fixtureDirectory, name), "utf8")) as T;
}

function captured(withMedia = false): PageCaptureResult {
  return {
    adapter: { id: "generic", version: "1.0.0", matchConfidence: 100, usedGenericFallback: false },
    identity: {
      submittedUrl: "https://example.test/article",
      canonicalUrl: "https://example.test/article",
      provider: "web",
      segmentKey: "root",
    },
    document: {
      title: "Article",
      description: "Description",
      contentMarkdown: "Body",
      selection: "",
      usedFallback: false,
    },
    media: withMedia
      ? [
          {
            candidateId: "generic:image:0",
            kind: "image",
            role: "hero",
            position: 0,
            variants: [
              {
                variantId: "original",
                kind: "original",
                sourceUrl: "https://cdn.example.test/hero.jpg",
                acquisitionPreference: "browser_preferred",
                mimeType: "image/jpeg",
              },
            ],
          },
        ]
      : [],
    extensions: [],
    capabilities: { declared: ["title", "body"], observed: ["title", "body"] },
    warnings: [],
  };
}

function capabilities(): FECapabilityDiscovery {
  const value = fixture<FECapabilityDiscovery>("valid-capabilities.json");
  value.operations.capture_manifest = true;
  value.operations.capture_completion = true;
  value.capture.max_manifest_bytes = 1_000_000;
  return value;
}

function accepted(captureId: string, upload = true): FECaptureManifestResponse {
  return {
    schema_version: "fe.capture.result.v1",
    capture_id: captureId,
    fragment_id: `fragment-${captureId}`,
    fragment_revision_id: `revision-${captureId}`,
    capture_attempt_id: `attempt-${captureId}`,
    completion: "accepting",
    idempotent_replay: false,
    asset_instructions: upload
      ? [
          {
            client_variant_id: "generic:image:0:original:0",
            asset_variant_id: `variant-${captureId}`,
            action: "request_upload",
            upload_href: `/v1/captures/${captureId}/assets/generic%3Aimage%3A0%3Aoriginal%3A0/content`,
          },
        ]
      : [
          {
            client_variant_id: "reference",
            asset_variant_id: `variant-${captureId}`,
            action: "reference_only",
          },
          {
            client_variant_id: "rejected",
            action: "rejected",
            reason: "No stable source URL",
          },
        ],
    warnings: [{ code: "capture.partial", message: "Some optional media was unavailable." }],
    reader_item: fixture("valid-reader-video.json"),
  };
}

function captureStatus(
  acceptance: FECaptureManifestResponse,
  completion: FECaptureStatus["completion"] = "accepting",
): FECaptureStatus {
  return {
    schema_version: "fe.capture.status.v1",
    capture_id: acceptance.capture_id,
    fragment_id: acceptance.fragment_id,
    fragment_revision_id: acceptance.fragment_revision_id,
    capture_attempt_id: acceptance.capture_attempt_id,
    completion,
    assets: acceptance.asset_instructions.map((instruction) => ({
      client_variant_id: instruction.client_variant_id,
      outcome: "deferred" as const,
    })),
    enrichment_in_progress: false,
    warnings: acceptance.warnings,
  };
}

class MemoryJobs implements CaptureJobPersistence {
  readonly jobs = new Map<string, CaptureJobRecord>();
  readonly events: string[] = [];

  async getJob(captureId: string): Promise<CaptureJobRecord | undefined> {
    return this.jobs.get(captureId);
  }

  async listJobs(): Promise<CaptureJobRecord[]> {
    return [...this.jobs.values()];
  }

  async saveJob(job: CaptureJobRecord): Promise<void> {
    this.events.push(`save:${job.captureId}:${job.phase}:${job.manifestAttempts}`);
    this.jobs.set(job.captureId, structuredClone(job));
  }
}

class FakeApi implements CaptureApiClient {
  readonly events: string[] = [];
  readonly completions: FECaptureCompletionRequest[] = [];
  capability = capabilities();
  submit: (envelope: FECaptureEnvelope) => Promise<FECaptureManifestResponse> = async (envelope) =>
    accepted(envelope.capture_id);
  lookup: (captureId: string) => Promise<FECaptureStatus> = async () => {
    throw new CaptureApiError("missing", "not_found", false, 404, "not_found");
  };
  complete: (
    captureId: string,
    completion: FECaptureCompletionRequest,
  ) => Promise<FECaptureStatus> = async (captureId) =>
    captureStatus(accepted(captureId, false), "partial");

  async preflight(): Promise<Awaited<ReturnType<CaptureApiClient["preflight"]>>> {
    this.events.push("api:preflight");
    return { ok: true, capabilitiesUrl: "https://fe.test/v1/capabilities", capabilities: this.capability };
  }

  async submitManifest(
    _endpoint: string,
    envelope: FECaptureEnvelope,
  ): Promise<FECaptureManifestResponse> {
    this.events.push(`api:submit:${envelope.capture_id}`);
    return this.submit(envelope);
  }

  async getStatus(_endpoint: string, captureId: string): Promise<FECaptureStatus> {
    this.events.push(`api:get:${captureId}`);
    return this.lookup(captureId);
  }

  async completeCapture(
    _endpoint: string,
    captureId: string,
    completion: FECaptureCompletionRequest,
  ): Promise<FECaptureStatus> {
    this.events.push(`api:complete:${captureId}`);
    this.completions.push(completion);
    return this.complete(captureId, completion);
  }
}

function coordinator(store: MemoryJobs, api: FakeApi, prefix = "id") {
  let nextId = 0;
  let tick = 0;
  return new CaptureJobCoordinator(store, api, {
    createId: () => `${prefix}-${++nextId}`,
    now: () => new Date(Date.UTC(2026, 8, 3, 20, 0, tick++)),
    clientVersion: "0.1.0",
  });
}

function input(withMedia = true, captureId = "action-1") {
  return {
    captureId,
    captured: captured(withMedia),
    edits: { title: "", tags: ["link"], note: "" },
    configuration: {
      endpoint: "https://fe.test/legacy?token=secret#fragment",
      principalId: "local-user",
    },
  };
}

describe("durable manifest capture coordinator", () => {
  it("surfaces active-job quota exhaustion before any network work", async () => {
    class FullJobs extends MemoryJobs {
      override async saveJob(): Promise<void> {
        throw new CaptureJobStorageQuotaError();
      }
    }
    const store = new FullJobs();
    const api = new FakeApi();

    await expect(coordinator(store, api).start(input(true, "quota-action"))).rejects.toThrow(
      /storage is full.*active jobs were preserved/,
    );
    expect(api.events).toEqual([]);
  });

  it("does not begin preflight until the initial durable write resolves", async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    class DeferredJobs extends MemoryJobs {
      blocked = false;
      override async saveJob(job: CaptureJobRecord): Promise<void> {
        if (this.events.length === 0) {
          this.blocked = true;
          await gate;
        }
        await super.saveJob(job);
      }
    }
    const store = new DeferredJobs();
    const api = new FakeApi();
    const operation = coordinator(store, api, "durable").start(input(true, "durable-action"));

    await vi.waitFor(() => expect(store.blocked).toBe(true));
    expect(api.events).toEqual([]);
    release();
    await operation;
    expect(api.events[0]).toBe("api:preflight");
  });

  it("persists before fetch, creates new IDs per action, and retries the same ID", async () => {
    const store = new MemoryJobs();
    const api = new FakeApi();
    const jobs = coordinator(store, api);

    const first = await jobs.start(input(true, "action-first"));
    expect(first.captureId).toBe("action-first");
    expect(first.phase).toBe("pending_transfer");
    expect(first.endpoint).toBe("https://fe.test/v1/captures");
    expect(store.events[0]).toBe("save:action-first:pending_manifest:0");
    expect(store.events.indexOf("save:action-first:preflighting:0")).toBeLessThan(
      store.events.indexOf("save:action-first:submitting_manifest:1"),
    );
    expect(api.events[0]).toBe("api:preflight");

    api.lookup = async () => captureStatus(first.acceptance!);
    // Repeating START covers a lost message response and must load the exact
    // persisted job rather than rebuilding an envelope from the new input.
    const retried = await jobs.start({
      ...input(true, first.captureId),
      edits: { title: "must be ignored", tags: ["different"], note: "must be ignored" },
    });
    expect(retried.captureId).toBe(first.captureId);
    expect(api.events.indexOf(`api:get:${first.captureId}`)).toBeGreaterThan(
      api.events.indexOf("api:preflight"),
    );
    expect(api.events.filter((event) => event.startsWith(`api:submit:${first.captureId}`))).toHaveLength(1);
    expect(retried.envelope).toEqual(first.envelope);

    const second = await jobs.start(input(true, "action-second"));
    expect(second.captureId).toBe("action-second");
    expect(second.captureId).not.toBe(first.captureId);
  });

  it("GET-reconciles uncertain attempts before a 404-authorized resend", async () => {
    const store = new MemoryJobs();
    const failingApi = new FakeApi();
    failingApi.submit = async () => {
      throw new CaptureApiError("connection reset", "network", true);
    };
    const firstCoordinator = coordinator(store, failingApi, "retry");
    const failed = await firstCoordinator.start(input(true, "retry-action"));
    expect(failed.phase).toBe("failed");
    expect(failed.manifestAttempts).toBe(1);

    const recoveredApi = new FakeApi();
    recoveredApi.submit = async (envelope) => ({ ...accepted(envelope.capture_id), idempotent_replay: false });
    const recreated = coordinator(store, recoveredApi, "unused");
    const recovered = await recreated.retry(failed.captureId);

    expect(recovered.captureId).toBe(failed.captureId);
    expect(recovered.phase).toBe("pending_transfer");
    expect(recoveredApi.events).toEqual([
      "api:preflight",
      `api:get:${failed.captureId}`,
      `api:submit:${failed.captureId}`,
    ]);
  });

  it("recovers accepted IDs then replays only to restore transfer instructions", async () => {
    const store = new MemoryJobs();
    const seedApi = new FakeApi();
    const seedCoordinator = coordinator(store, seedApi, "recover");
    const seeded = await seedCoordinator.start(input(true, "recover-action"));
    const uncertain = structuredClone(seeded);
    delete uncertain.acceptance;
    uncertain.phase = "submitting_manifest";
    await store.saveJob(uncertain);

    const replayApi = new FakeApi();
    const serverAcceptance = accepted(seeded.captureId);
    replayApi.lookup = async () => captureStatus(serverAcceptance);
    replayApi.submit = async () => ({ ...serverAcceptance, idempotent_replay: true });
    const recovered = await coordinator(store, replayApi, "unused").resumeAll();

    expect(recovered[0]?.acceptance?.fragment_id).toBe(serverAcceptance.fragment_id);
    expect(replayApi.events).toEqual([
      "api:preflight",
      `api:get:${seeded.captureId}`,
      `api:submit:${seeded.captureId}`,
    ]);
  });

  it("keeps a 409 on the original capture ID and never creates a replacement", async () => {
    const store = new MemoryJobs();
    const api = new FakeApi();
    api.submit = async () => {
      throw new CaptureApiError("capture conflict", "conflict", false, 409, "conflict");
    };
    const jobs = coordinator(store, api, "conflict");
    const failed = await jobs.start(input(true, "conflict-action"));

    expect(failed).toMatchObject({ captureId: "conflict-action", phase: "failed", terminal: true });
    expect(await jobs.retry(failed.captureId)).toEqual(failed);
    expect(api.events.filter((event) => event.startsWith("api:submit"))).toHaveLength(1);
  });

  it("returns after acceptance, then completes no-upload work with preserved warnings", async () => {
    const store = new MemoryJobs();
    const api = new FakeApi();
    api.submit = async (envelope) => accepted(envelope.capture_id, false);
    const jobs = coordinator(store, api, "complete");

    const visible = await jobs.start(input(false, "complete-action"));
    expect(visible.phase).toBe("accepted");
    expect(visible.acceptance).toBeDefined();

    await vi.waitFor(() => expect(store.jobs.get(visible.captureId)?.terminal).toBe(true));
    const completed = store.jobs.get(visible.captureId)!;
    expect(completed.phase).toBe("partial");
    expect(api.completions[0]).toEqual({
      schema_version: "fe.capture.completion.v1",
      idempotency_key: "complete-1",
      assets: [
        { client_variant_id: "reference", outcome: "deferred" },
        {
          client_variant_id: "rejected",
          outcome: "not_available",
          reason: "No stable source URL",
          retryable: false,
        },
      ],
      warnings: [{ code: "capture.partial", message: "Some optional media was unavailable." }],
    });
  });

  it("preserves accepted IDs under completion failure and keeps accepted conflicts consistent", async () => {
    const store = new MemoryJobs();
    const api = new FakeApi();
    api.submit = async (envelope) => accepted(envelope.capture_id, false);
    api.complete = async () => {
      throw new CaptureApiError("completion conflict", "conflict", false, 409, "conflict");
    };
    const visible = await coordinator(store, api, "partial").start(input(false, "partial-action"));

    await vi.waitFor(() => expect(store.jobs.get(visible.captureId)?.error).toBeDefined());
    const retained = store.jobs.get(visible.captureId)!;
    expect(retained.acceptance?.fragment_id).toBe(`fragment-${visible.captureId}`);
    expect(retained).toMatchObject({ phase: "accepted", terminal: false });
    expect(retained.error).toMatchObject({ stage: "completion", retryable: false, httpStatus: 409 });
  });

  it("fails before POST when the server inventory or manifest limit is incompatible", async () => {
    const store = new MemoryJobs();
    const api = new FakeApi();
    api.capability.capture.media_kinds = [];
    const incompatible = await coordinator(store, api, "caps").start(input(true, "caps-action"));
    expect(incompatible.error?.message).toContain("media kind image");
    expect(api.events.some((event) => event.startsWith("api:submit"))).toBe(false);

    const store2 = new MemoryJobs();
    const api2 = new FakeApi();
    api2.capability.capture.max_manifest_bytes = 10;
    const oversized = await coordinator(store2, api2, "size").start(input(false, "size-action"));
    expect(oversized.error?.message).toContain("exceeding");
    expect(api2.events.some((event) => event.startsWith("api:submit"))).toBe(false);
  });
});
