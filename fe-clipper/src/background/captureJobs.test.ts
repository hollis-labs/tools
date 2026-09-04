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
import { MediaTransferError, type AcquiredAsset, type MediaAcquirer } from "./mediaTransfer";

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

function capturedVariants(
  variants: Array<{ id: string; url: string; expiresAt?: string }>,
  provider = "web",
): PageCaptureResult {
  return {
    ...captured(false),
    identity: { ...captured(false).identity, provider },
    media: variants.map(({ id, url, expiresAt }, index) => ({
      candidateId: `media:${id}`,
      kind: "image",
      role: "gallery_item",
      position: index,
      variants: [{
        variantId: id,
        kind: "original",
        sourceUrl: url,
        acquisitionPreference: "browser_preferred",
        ...(expiresAt ? { expiresAt } : {}),
      }],
    })),
  };
}

function requestedAcceptance(
  captureId: string,
  variants: Array<{ id: string; action?: "request_upload" | "reuse_blob" }>,
): FECaptureManifestResponse {
  return {
    ...accepted(captureId, false),
    asset_instructions: variants.map(({ id, action = "request_upload" }) => {
      const clientVariantId = `media:${id}:${id}:0`;
      return action === "reuse_blob"
        ? {
            client_variant_id: clientVariantId,
            asset_variant_id: `asset:${id}`,
            action: "reuse_blob" as const,
            digest: { algorithm: "sha256" as const, value: "a".repeat(64) },
          }
        : {
            client_variant_id: clientVariantId,
            asset_variant_id: `asset:${id}`,
            action: "request_upload" as const,
            upload_href: `/v1/captures/${captureId}/assets/${clientVariantId}/content`,
          };
    }),
  };
}

function capabilities(): FECapabilityDiscovery {
  const value = fixture<FECapabilityDiscovery>("valid-capabilities.json");
  value.operations.capture_manifest = true;
  value.operations.asset_upload = true;
  value.operations.capture_completion = true;
  value.capture.max_manifest_bytes = 1_000_000;
  value.capture.max_asset_bytes = 1_000_000;
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
      : [],
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
  lastAcceptance?: FECaptureManifestResponse;
  lookup: (captureId: string) => Promise<FECaptureStatus> = async () => {
    if (this.lastAcceptance) return captureStatus(this.lastAcceptance);
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
    const result = await this.submit(envelope);
    this.lastAcceptance = result;
    return result;
  }

  async getStatus(_endpoint: string, captureId: string): Promise<FECaptureStatus> {
    this.events.push(`api:get:${captureId}`);
    return this.lookup(captureId);
  }

  async uploadAsset(): Promise<void> {
    this.events.push("api:upload");
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

class FakeAcquirer implements MediaAcquirer {
  readonly events: string[] = [];
  result: AcquiredAsset = {
    bytes: new Uint8Array([1, 2, 3]),
    digest: { algorithm: "sha256", value: "a".repeat(64) },
    byteSize: 3,
    mimeType: "application/octet-stream",
    observedMimeType: "application/octet-stream",
  };
  acquireImplementation?: (variantId: string) => Promise<AcquiredAsset>;

  async acquire(variant: { client_variant_id: string }): Promise<AcquiredAsset> {
    this.events.push(variant.client_variant_id);
    return this.acquireImplementation?.(variant.client_variant_id) ?? this.result;
  }
}

function coordinator(store: MemoryJobs, api: FakeApi, prefix = "id", mediaAcquirer = new FakeAcquirer()) {
  let nextId = 0;
  let tick = 0;
  return new CaptureJobCoordinator(store, api, {
    createId: () => `${prefix}-${++nextId}`,
    now: () => new Date(Date.UTC(2026, 8, 3, 20, 0, tick++)),
    clientVersion: "0.1.0",
    mediaAcquirer,
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
    permittedSourceOrigins: withMedia ? ["https://cdn.example.test"] : [],
  };
}

function inputForCaptured(
  page: PageCaptureResult,
  captureId: string,
  permittedSourceOrigins: string[],
) {
  return {
    ...input(false, captureId),
    captured: page,
    permittedSourceOrigins,
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
    await vi.waitFor(() => expect(api.events).toContain(`api:get:${first.captureId}`));
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
    expect(recovered.phase).toBe("partial");
    expect(recoveredApi.events).toEqual([
      "api:preflight",
      `api:get:${failed.captureId}`,
      `api:submit:${failed.captureId}`,
      "api:upload",
      `api:complete:${failed.captureId}`,
    ]);
  });

  it("recovers accepted IDs then replays only to restore transfer instructions", async () => {
    const store = new MemoryJobs();
    const seedApi = new FakeApi();
    const seedCoordinator = coordinator(store, seedApi, "recover");
    const seeded = await seedCoordinator.start(input(true, "recover-action"));
    await vi.waitFor(() => expect(store.jobs.get(seeded.captureId)?.terminal).toBe(true));
    const uncertain = structuredClone(seeded);
    delete uncertain.acceptance;
    delete uncertain.status;
    uncertain.phase = "submitting_manifest";
    uncertain.terminal = false;
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
      "api:upload",
      `api:complete:${seeded.captureId}`,
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
      assets: [],
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

  it("returns accepted before a deferred source fetch and repeated START never waits or resubmits", async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const store = new MemoryJobs();
    const api = new FakeApi();
    const media = new FakeAcquirer();
    media.acquireImplementation = async () => {
      expect(api.events).toContain("api:get:deferred-action");
      await gate;
      return media.result;
    };
    const jobs = coordinator(store, api, "deferred", media);

    const visible = await jobs.start(input(true, "deferred-action"));
    expect(visible).toMatchObject({ phase: "pending_transfer", acceptance: { capture_id: "deferred-action" } });
    await vi.waitFor(() => expect(media.events).toHaveLength(1));

    let repeatedResolved = false;
    const repeatedPromise = jobs.start({
      ...input(true, "deferred-action"),
      edits: { title: "ignored", tags: ["ignored"], note: "ignored" },
    }).then((job) => {
      repeatedResolved = true;
      return job;
    });
    await vi.waitFor(() => expect(repeatedResolved).toBe(true));
    expect((await repeatedPromise).acceptance?.capture_id).toBe("deferred-action");
    expect(api.events.filter((event) => event === "api:submit:deferred-action")).toHaveLength(1);

    release();
    await vi.waitFor(() => expect(store.jobs.get("deferred-action")?.terminal).toBe(true));
  });

  it("uploads successfully, persists observations, and then completes", async () => {
    const store = new MemoryJobs();
    const api = new FakeApi();
    const media = new FakeAcquirer();
    const jobs = coordinator(store, api, "upload", media);
    const visible = await jobs.start(input(true, "upload-action"));

    expect(visible.phase).toBe("pending_transfer");
    await vi.waitFor(() => expect(store.jobs.get(visible.captureId)?.terminal).toBe(true));
    const completed = store.jobs.get(visible.captureId)!;
    expect(completed.acceptance?.fragment_id).toBe("fragment-upload-action");
    expect(completed.transfers?.[0]).toMatchObject({
      state: "available",
      attempts: 1,
      digest: { value: "a".repeat(64) },
      byteSize: 3,
      mimeType: "application/octet-stream",
      outcome: { outcome: "uploaded", byte_size: 3 },
    });
    expect(api.events.indexOf("api:get:upload-action")).toBeLessThan(api.events.indexOf("api:upload"));
    expect(api.completions[0]?.assets).toEqual([completed.transfers?.[0]?.outcome]);
  });

  it("keeps permission denial retryable after acceptance and never prompts or completes in background", async () => {
    const store = new MemoryJobs();
    const api = new FakeApi();
    const media = new FakeAcquirer();
    const jobs = coordinator(store, api, "permission", media);

    const visible = await jobs.start({ ...input(true, "permission-action"), permittedSourceOrigins: [] });
    expect(visible.acceptance).toBeDefined();
    await vi.waitFor(() => expect(store.jobs.get(visible.captureId)?.error?.code).toBe("media_permission"));
    const retained = store.jobs.get(visible.captureId)!;
    expect(retained).toMatchObject({ phase: "pending_transfer", terminal: false });
    expect(retained.transfers?.[0]).toMatchObject({ state: "failed", retryable: true, attempts: 0 });
    expect(retained.transfers?.[0]?.sourceUrl).toBe("https://cdn.example.test/hero.jpg");
    expect(media.events).toEqual([]);
    expect(api.completions).toEqual([]);
  });

  it("does zero source work for reuse/server/reference/rejected branches", async () => {
    const page = capturedVariants([
      { id: "reuse", url: "https://media.test/reuse" },
      { id: "server", url: "https://media.test/server" },
      { id: "reference", url: "https://media.test/reference" },
      { id: "rejected", url: "https://media.test/rejected" },
    ]);
    const store = new MemoryJobs();
    const api = new FakeApi();
    api.submit = async (capture) => {
      const result = requestedAcceptance(capture.capture_id, [{ id: "reuse", action: "reuse_blob" }]);
      result.asset_instructions.push(
        { client_variant_id: "media:server:server:0", asset_variant_id: "asset:server", action: "server_acquire" },
        { client_variant_id: "media:reference:reference:0", asset_variant_id: "asset:reference", action: "reference_only" },
        { client_variant_id: "media:rejected:rejected:0", action: "rejected", reason: "policy" },
      );
      return result;
    };
    const media = new FakeAcquirer();
    const jobs = coordinator(store, api, "branches", media);
    const visible = await jobs.start(inputForCaptured(page, "branches-action", ["https://media.test"]));
    await vi.waitFor(() => expect(store.jobs.get(visible.captureId)?.terminal).toBe(true));

    expect(media.events).toEqual([]);
    expect(api.events).not.toContain("api:upload");
    expect(api.completions[0]?.assets.map((outcome) => outcome.outcome)).toEqual([
      "already_available",
      "deferred",
      "deferred",
      "not_available",
    ]);
  });

  it("retains locator/warning for permanent failure, but does not complete retryable failure", async () => {
    const permanentStore = new MemoryJobs();
    const permanentApi = new FakeApi();
    const permanentMedia = new FakeAcquirer();
    permanentMedia.acquireImplementation = async () => {
      throw new MediaTransferError("source returned HTTP 404", "source", false);
    };
    const visible = await coordinator(permanentStore, permanentApi, "permanent", permanentMedia)
      .start(input(true, "permanent-action"));
    await vi.waitFor(() => expect(permanentStore.jobs.get(visible.captureId)?.terminal).toBe(true));
    const permanent = permanentStore.jobs.get(visible.captureId)!;
    expect(permanent.transfers?.[0]).toMatchObject({
      state: "failed",
      retryable: false,
      sourceUrl: "https://cdn.example.test/hero.jpg",
    });
    expect(permanentApi.completions[0]?.warnings).toContainEqual(expect.objectContaining({
      code: "capture.asset_transfer_failed",
      subject_id: "generic:image:0:original:0",
    }));

    const retryStore = new MemoryJobs();
    const retryApi = new FakeApi();
    const retryMedia = new FakeAcquirer();
    retryMedia.acquireImplementation = async () => {
      throw new MediaTransferError("temporary network failure", "network", true);
    };
    const retryVisible = await coordinator(retryStore, retryApi, "retryable", retryMedia)
      .start(input(true, "retryable-action"));
    await vi.waitFor(() => expect(retryStore.jobs.get(retryVisible.captureId)?.error).toBeDefined());
    expect(retryStore.jobs.get(retryVisible.captureId)).toMatchObject({ phase: "pending_transfer", terminal: false });
    expect(retryApi.completions).toEqual([]);
  });

  it("rejects expected digest mismatch without uploading", async () => {
    const store = new MemoryJobs();
    const api = new FakeApi();
    api.submit = async (capture) => {
      const result = accepted(capture.capture_id);
      const instruction = result.asset_instructions[0]!;
      if (instruction.action !== "request_upload") throw new Error("fixture mismatch");
      instruction.expected_digest = { algorithm: "sha256", value: "b".repeat(64) };
      return result;
    };
    const visible = await coordinator(store, api, "digest").start(input(true, "digest-action"));
    await vi.waitFor(() => expect(store.jobs.get(visible.captureId)?.terminal).toBe(true));
    expect(api.events).not.toContain("api:upload");
    expect(store.jobs.get(visible.captureId)?.transfers?.[0]).toMatchObject({
      state: "failed",
      retryable: false,
      reason: expect.stringMatching(/expected SHA-256/),
    });
  });

  it("fetches valid expiring locators earliest while retaining instruction-order completion", async () => {
    const page = capturedVariants([
      { id: "none", url: "https://media.test/none" },
      { id: "later", url: "https://media.test/later", expiresAt: "2026-09-03T22:00:00Z" },
      { id: "sooner", url: "https://media.test/sooner", expiresAt: "2026-09-03T21:00:00Z" },
    ]);
    const store = new MemoryJobs();
    const api = new FakeApi();
    api.submit = async (capture) => requestedAcceptance(capture.capture_id, [
      { id: "none" }, { id: "later" }, { id: "sooner" },
    ]);
    const media = new FakeAcquirer();
    const visible = await coordinator(store, api, "expiry", media).start(
      inputForCaptured(page, "expiry-action", ["https://media.test"]),
    );
    await vi.waitFor(() => expect(store.jobs.get(visible.captureId)?.terminal).toBe(true));
    expect(media.events).toEqual([
      "media:sooner:sooner:0",
      "media:later:later:0",
      "media:none:none:0",
    ]);
    expect(api.completions[0]?.assets.map((outcome) => outcome.client_variant_id)).toEqual([
      "media:none:none:0",
      "media:later:later:0",
      "media:sooner:sooner:0",
    ]);
  });

  it("reconciles an uncertain uploaded digest before refetching", async () => {
    const store = new MemoryJobs();
    const firstApi = new FakeApi();
    firstApi.uploadAsset = async () => {
      firstApi.events.push("api:upload");
      throw new CaptureApiError("connection reset after upload", "network", true);
    };
    const firstMedia = new FakeAcquirer();
    const visible = await coordinator(store, firstApi, "uncertain", firstMedia)
      .start(input(true, "uncertain-action"));
    await vi.waitFor(() => expect(store.jobs.get(visible.captureId)?.error?.stage).toBe("transfer"));
    const uncertain = store.jobs.get(visible.captureId)!;
    expect(uncertain.transfers?.[0]?.digest?.value).toBe("a".repeat(64));

    const recoveredApi = new FakeApi();
    recoveredApi.lookup = async () => ({
      ...captureStatus(uncertain.acceptance!),
      completion: "transferring",
      assets: [{
        client_variant_id: "generic:image:0:original:0",
        outcome: "already_available",
        digest: { algorithm: "sha256", value: "a".repeat(64) },
      }],
    });
    const recoveredMedia = new FakeAcquirer();
    const [recovered] = await coordinator(store, recoveredApi, "unused", recoveredMedia).resumeAll();
    expect(recovered?.terminal).toBe(true);
    expect(recoveredMedia.events).toEqual([]);
    expect(recoveredApi.events).toEqual([
      "api:preflight",
      "api:get:uncertain-action",
      "api:complete:uncertain-action",
    ]);
  });

  it.each(["pending", "transferring"] as const)(
    "resumes %s pre-upload state with GET-first reconciliation after restart",
    async (state) => {
      const seedStore = new MemoryJobs();
      const seedApi = new FakeApi();
      const visible = await coordinator(seedStore, seedApi, `restart-${state}`).start({
        ...input(true, `restart-${state}`),
        permittedSourceOrigins: [],
      });
      await vi.waitFor(() => expect(seedStore.jobs.get(visible.captureId)?.transfers?.[0]?.state).toBe("failed"));
      const restarted = structuredClone(seedStore.jobs.get(visible.captureId)!);
      restarted.permittedSourceOrigins = ["https://cdn.example.test"];
      restarted.transfers![0]!.state = state;
      restarted.transfers![0]!.attempts = state === "transferring" ? 1 : 0;
      delete restarted.transfers![0]!.outcome;
      delete restarted.transfers![0]!.reason;
      delete restarted.transfers![0]!.retryable;
      delete restarted.error;
      seedStore.jobs.set(restarted.captureId, restarted);

      const api = new FakeApi();
      api.lookup = async () => captureStatus(restarted.acceptance!);
      const media = new FakeAcquirer();
      media.acquireImplementation = async () => {
        expect(api.events).toContain(`api:get:${restarted.captureId}`);
        return media.result;
      };
      const [completed] = await coordinator(seedStore, api, "restarted", media).resumeAll();

      expect(completed?.terminal).toBe(true);
      expect(media.events).toHaveLength(1);
      expect(api.events.indexOf(`api:get:${restarted.captureId}`)).toBeLessThan(api.events.indexOf("api:upload"));
    },
  );

  it("re-plans immutable transfer fields before effects and rejects corrupted resumed state", async () => {
    const seedStore = new MemoryJobs();
    const seedApi = new FakeApi();
    const visible = await coordinator(seedStore, seedApi, "seed").start({
      ...input(true, "corrupt-action"),
      permittedSourceOrigins: [],
    });
    await vi.waitFor(() => expect(seedStore.jobs.get(visible.captureId)?.error?.code).toBe("media_permission"));
    const seeded = seedStore.jobs.get(visible.captureId)!;

    for (const mutate of [
      (transfer: NonNullable<CaptureJobRecord["transfers"]>[number]) => { transfer.action = "reuse_blob"; },
      (transfer: NonNullable<CaptureJobRecord["transfers"]>[number]) => { transfer.uploadUrl = "https://evil.test/upload"; },
      (transfer: NonNullable<CaptureJobRecord["transfers"]>[number]) => { transfer.sourceUrl = "https://evil.test/source"; },
    ]) {
      const store = new MemoryJobs();
      const corrupt = structuredClone(seeded);
      mutate(corrupt.transfers![0]!);
      store.jobs.set(corrupt.captureId, corrupt);
      const api = new FakeApi();
      const media = new FakeAcquirer();
      const [result] = await coordinator(store, api, "corrupt", media).resumeAll();
      expect(result?.error?.message).toMatch(/no longer matches/);
      expect(media.events).toEqual([]);
      expect(api.events).not.toContain("api:upload");
      expect(api.events).not.toContain("api:get:corrupt-action");
    }

    for (const preAcceptance of [false, true]) {
      const store = new MemoryJobs();
      const corrupt = structuredClone(seeded);
      corrupt.permittedSourceOrigins.push("https://evil.test");
      if (preAcceptance) {
        delete corrupt.acceptance;
        delete corrupt.transfers;
        delete corrupt.status;
        corrupt.phase = "submitting_manifest";
      }
      store.jobs.set(corrupt.captureId, corrupt);
      const api = new FakeApi();
      const media = new FakeAcquirer();
      const [result] = await coordinator(store, api, "origin-corrupt", media).resumeAll();
      expect(result?.error?.message).toMatch(/outside this capture envelope|no longer match/);
      expect(api.events).toEqual([]);
      expect(media.events).toEqual([]);
    }
  });

  it("does not strand an earlier retryable variant when a later failure is permanent", async () => {
    const page = capturedVariants([
      { id: "temporary", url: "https://media.test/temporary" },
      { id: "permanent", url: "https://media.test/permanent" },
    ]);
    const store = new MemoryJobs();
    const api = new FakeApi();
    api.submit = async (capture) => requestedAcceptance(capture.capture_id, [
      { id: "temporary" }, { id: "permanent" },
    ]);
    const media = new FakeAcquirer();
    media.acquireImplementation = async (id) => {
      throw id.includes("temporary")
        ? new MediaTransferError("temporary", "network", true)
        : new MediaTransferError("permanent", "source", false);
    };
    const jobs = coordinator(store, api, "mixed", media);
    const visible = await jobs.start(inputForCaptured(page, "mixed-action", ["https://media.test"]));
    await vi.waitFor(() => expect(store.jobs.get(visible.captureId)?.transfers?.[1]?.state).toBe("failed"));
    expect(store.jobs.get(visible.captureId)?.error?.retryable).toBe(false);
    expect(api.completions).toEqual([]);
    const attemptsBefore = media.events.filter((id) => id.includes("temporary")).length;

    const repeated = await jobs.start(inputForCaptured(page, "mixed-action", ["https://media.test"]));
    expect(repeated.acceptance).toBeDefined();
    await vi.waitFor(() => {
      expect(media.events.filter((id) => id.includes("temporary")).length).toBeGreaterThan(attemptsBefore);
    });
    expect(api.completions).toEqual([]);
  });

  it("serializes new permission grants behind active work without rolling back progress", async () => {
    const page = capturedVariants([
      { id: "first", url: "https://first.test/item" },
      { id: "second", url: "https://second.test/item" },
    ]);
    const store = new MemoryJobs();
    const api = new FakeApi();
    api.submit = async (capture) => requestedAcceptance(capture.capture_id, [
      { id: "first" }, { id: "second" },
    ]);
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const media = new FakeAcquirer();
    media.acquireImplementation = async (id) => {
      if (id.includes("first") && media.events.filter((item) => item.includes("first")).length === 1) {
        await gate;
      }
      return media.result;
    };
    const jobs = coordinator(store, api, "permission-race", media);
    const visible = await jobs.start(inputForCaptured(page, "permission-race", ["https://first.test"]));
    await vi.waitFor(() => expect(media.events).toEqual(["media:first:first:0"]));

    const retryPromise = jobs.retry(visible.captureId, ["https://second.test"]);
    release();
    const completed = await retryPromise;

    expect(completed.terminal).toBe(true);
    expect(completed.permittedSourceOrigins).toEqual(["https://first.test", "https://second.test"]);
    expect(media.events.filter((id) => id.includes("first"))).toHaveLength(1);
    expect(media.events.filter((id) => id.includes("second"))).toHaveLength(1);
  });

  it("never converts pre-PUT or post-204 persistence failures into permanent asset outcomes", async () => {
    class OneShotTransferSaveFailure extends MemoryJobs {
      failed = false;
      constructor(private readonly point: "before-put" | "after-put") {
        super();
      }

      override async saveJob(job: CaptureJobRecord): Promise<void> {
        const transfer = job.transfers?.[0];
        const shouldFail = this.point === "before-put"
          ? transfer?.state === "transferring" && Boolean(transfer.digest)
          : transfer?.state === "available";
        if (shouldFail && !this.failed) {
          this.failed = true;
          throw new CaptureJobStorageQuotaError();
        }
        await super.saveJob(job);
      }
    }

    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    for (const point of ["before-put", "after-put"] as const) {
      const store = new OneShotTransferSaveFailure(point);
      const api = new FakeApi();
      const visible = await coordinator(store, api, point).start(input(true, `${point}-action`));
      await vi.waitFor(() => expect(store.failed).toBe(true));
      const retained = store.jobs.get(visible.captureId)!;

      expect(retained.terminal).toBe(false);
      expect(retained.transfers?.[0]).toMatchObject({ state: "transferring" });
      expect(retained.transfers?.[0]?.outcome).toBeUndefined();
      expect(api.completions).toEqual([]);
      expect(api.events.filter((event) => event === "api:upload")).toHaveLength(
        point === "before-put" ? 0 : 1,
      );
    }
    consoleError.mockRestore();
  });

  it("resumes jobs sequentially so startup never holds multiple asset bodies", async () => {
    const store = new MemoryJobs();
    const seedApi = new FakeApi();
    const seed = coordinator(store, seedApi, "resume-seed");
    for (const id of ["resume-first", "resume-second"]) {
      const visible = await seed.start({ ...input(true, id), permittedSourceOrigins: [] });
      await vi.waitFor(() => expect(store.jobs.get(visible.captureId)?.transfers?.[0]?.state).toBe("failed"));
      const job = store.jobs.get(id)!;
      job.permittedSourceOrigins = ["https://cdn.example.test"];
      job.transfers![0]!.state = "pending";
      delete job.transfers![0]!.outcome;
      delete job.transfers![0]!.reason;
      delete job.transfers![0]!.retryable;
      delete job.error;
      store.jobs.set(id, job);
    }

    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const media = new FakeAcquirer();
    media.acquireImplementation = async () => {
      if (media.events.length === 1) await gate;
      return media.result;
    };
    const api = new FakeApi();
    api.lookup = async (captureId) => captureStatus(store.jobs.get(captureId)!.acceptance!);
    const resumption = coordinator(store, api, "resume", media).resumeAll();

    await vi.waitFor(() => expect(media.events).toHaveLength(1));
    await Promise.resolve();
    expect(media.events).toHaveLength(1);
    release();
    const resumed = await resumption;
    expect(resumed).toHaveLength(2);
    expect(media.events).toHaveLength(2);
  });
});
