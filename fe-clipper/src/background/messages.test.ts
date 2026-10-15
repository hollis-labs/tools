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
import type { CaptureJobPersistence, CaptureJobRecord } from "@/lib/jobStorage";
import type { CapturedPage, ExtensionMessage } from "@/lib/types";
import { CaptureApiError, type CaptureApiClient } from "./captureApi";
import { CaptureJobCoordinator } from "./captureJobs";
import type { AcquiredAsset, MediaAcquirer } from "./mediaTransfer";
import {
  registerCaptureMessages,
  type CaptureJobController,
} from "./messages";

const fixtures = resolve(process.cwd(), "vendor/fragments-engine-browser-contract-v1/fixtures");

function acceptedJob(captureId = "capture-1", phase: CaptureJobRecord["phase"] = "accepted"): CaptureJobRecord {
  const envelope = JSON.parse(readFileSync(join(fixtures, "valid-capture-youtube.json"), "utf8")) as FECaptureEnvelope;
  envelope.capture_id = captureId;
  const reader = JSON.parse(readFileSync(join(fixtures, "valid-reader-video.json"), "utf8"));
  const acceptance = {
    schema_version: "fe.capture.result.v1",
    capture_id: captureId,
    fragment_id: `fragment-${captureId}`,
    fragment_revision_id: `revision-${captureId}`,
    capture_attempt_id: `attempt-${captureId}`,
    completion: "accepting",
    idempotent_replay: false,
    asset_instructions: [],
    warnings: [],
    reader_item: reader,
  } as FECaptureManifestResponse;
  return {
    schemaVersion: 2,
    captureId,
    endpoint: "https://fe.test/v1/captures",
    principalId: envelope.principal_id,
    envelope,
    completionIdempotencyKey: "complete-key",
    phase,
    terminal: false,
    manifestAttempts: 1,
    permittedSourceOrigins: [],
    createdAt: "2026-09-03T20:00:00.000Z",
    updatedAt: "2026-09-03T20:01:00.000Z",
    acceptance,
  };
}

function page(longUrl = "https://page.test/item"): CapturedPage {
  return {
    adapter: { id: "generic", version: "1.0.0", matchConfidence: 100, usedGenericFallback: false },
    identity: {
      submittedUrl: longUrl,
      canonicalUrl: longUrl,
      provider: "web",
      segmentKey: "root",
    },
    document: {
      title: "Long URL capture",
      description: "",
      contentMarkdown: "Body",
      selection: "",
      usedFallback: false,
    },
    media: [{
      candidateId: "media-1",
      kind: "image",
      role: "hero",
      position: 0,
      variants: [{
        variantId: "original",
        kind: "original",
        sourceUrl: longUrl,
        acquisitionPreference: "browser_preferred",
      }],
    }],
    extensions: [],
    capabilities: { declared: [], observed: [] },
    warnings: [],
  };
}

function controller(): CaptureJobController & Record<"start" | "retry" | "list" | "dismiss", ReturnType<typeof vi.fn>> {
  return {
    start: vi.fn(async (input) => acceptedJob(input.captureId)),
    retry: vi.fn(async (captureId) => acceptedJob(captureId, "pending_transfer")),
    list: vi.fn(async () => [acceptedJob()]),
    dismiss: vi.fn(async () => true),
  };
}

function runtimeBoundary(control: CaptureJobController) {
  let listener!: (
    message: unknown,
    sender: chrome.runtime.MessageSender,
    sendResponse: (response: ExtensionMessage) => void,
  ) => boolean | undefined;
  const runtime = {
    id: "extension-id",
    getURL: (path: string) => `chrome-extension://extension-id/${path}`,
    onMessage: { addListener: (callback: typeof listener) => { listener = callback; } },
  };
  registerCaptureMessages(runtime, control);
  const trusted: chrome.runtime.MessageSender = {
    id: runtime.id,
    url: runtime.getURL("popup.html"),
  };
  return {
    trusted,
    dispatch(message: unknown, sender = trusted): Promise<ExtensionMessage> {
      return new Promise((resolve, reject) => {
        const handled = listener(message, sender, resolve);
        if (handled === undefined) reject(new Error("message was not handled"));
      });
    },
  };
}

function startMessage(captureId = "capture-1", captured = page()) {
  return {
    type: "START_CAPTURE_JOB",
    captureId,
    captured,
    edits: { title: "", tags: ["link"], note: "" },
    configuration: { endpoint: "https://fe.test/v1/captures", principalId: "local-user" },
    permittedSourceOrigins: [new URL(captured.media[0]!.variants[0]!.sourceUrl).origin],
  };
}

describe("capture runtime message boundary", () => {
  it("refreshes an allowlisted projection without creating, retrying, or dismissing work", async () => {
    const control = controller();
    const value = acceptedJob();
    value.envelope.media[0]!.variants[1]!.source_url = "https://cdn.test/private?access_token=SECRET";
    value.error = {
      stage: "transfer",
      message: "Authorization: Bearer AUTH_SECRET at https://cdn.test/path?token=URL_SECRET",
      retryable: true,
      at: value.updatedAt,
      code: "api_key=CODE_SECRET",
    };
    control.list.mockResolvedValue([value]);
    const boundary = runtimeBoundary(control);

    const response = await boundary.dispatch({ type: "QUERY_CAPTURE_JOBS" });
    const serialized = JSON.stringify(response);

    expect(response).toMatchObject({ type: "CAPTURE_JOBS_RESULT", ok: true });
    expect(control.list).toHaveBeenCalledOnce();
    expect(control.start).not.toHaveBeenCalled();
    expect(control.retry).not.toHaveBeenCalled();
    expect(control.dismiss).not.toHaveBeenCalled();
    for (const privateValue of ["SECRET", "AUTH_SECRET", "URL_SECRET", "CODE_SECRET", "envelope", "source_url", "reader_item", "digest"]) {
      expect(serialized).not.toContain(privateValue);
    }
  });

  it.each(["QUERY_CAPTURE_JOBS", "RETRY_CAPTURE_JOB", "DISMISS_CAPTURE_JOB", "START_CAPTURE_JOB"])(
    "rejects %s from a content-script tab before controller effects",
    async (type) => {
      const control = controller();
      const boundary = runtimeBoundary(control);
      const message = type === "QUERY_CAPTURE_JOBS"
        ? { type }
        : type === "START_CAPTURE_JOB"
          ? startMessage()
          : { type, captureId: "capture-1" };
      const sender = { ...boundary.trusted, tab: {} as chrome.tabs.Tab };

      const response = await boundary.dispatch(message, sender);

      expect(JSON.stringify(response)).toContain("only to the extension popup");
      expect(control.start).not.toHaveBeenCalled();
      expect(control.retry).not.toHaveBeenCalled();
      expect(control.list).not.toHaveBeenCalled();
      expect(control.dismiss).not.toHaveBeenCalled();
    },
  );

  it("rejects malformed actions and unsafe permission origins without controller effects", async () => {
    const control = controller();
    const boundary = runtimeBoundary(control);
    const malformed = startMessage() as Record<string, unknown>;
    delete malformed.captured;

    expect(await boundary.dispatch(malformed)).toMatchObject({ ok: false, captureId: "capture-1" });
    expect(await boundary.dispatch({
      type: "RETRY_CAPTURE_JOB",
      captureId: "capture-1",
      permittedSourceOrigins: ["https://safe.test", "https://evil.test/path"],
    })).toMatchObject({ ok: false, captureId: "capture-1" });
    expect(control.start).not.toHaveBeenCalled();
    expect(control.retry).not.toHaveBeenCalled();
  });

  it("accepts valid page and media URLs longer than 255 characters", async () => {
    const control = controller();
    const boundary = runtimeBoundary(control);
    const longUrl = `https://page.test/${"segment".repeat(60)}?tracking=allowed`;

    const response = await boundary.dispatch(startMessage("long-url-capture", page(longUrl)));

    expect(response).toMatchObject({ ok: true, captureId: "long-url-capture" });
    expect(control.start).toHaveBeenCalledOnce();
  });

  it("does not apply capture-ID length limits to a valid long FE endpoint", async () => {
    const control = controller();
    const boundary = runtimeBoundary(control);
    const message = startMessage("long-endpoint") as ReturnType<typeof startMessage>;
    const labels = ["a", "b", "c", "d"].map((value) => value.repeat(60)).join(".");
    message.configuration.endpoint = `https://${labels}.test/tenant/fragments-engine`;
    expect(message.configuration.endpoint.length).toBeGreaterThan(255);

    const response = await boundary.dispatch(message);

    expect(response).toMatchObject({ ok: true, captureId: "long-endpoint" });
    expect(control.start).toHaveBeenCalledOnce();
  });

  it("preserves the exact capture ID across repeated START and explicit retry messages", async () => {
    const control = controller();
    const boundary = runtimeBoundary(control);
    const message = startMessage("stable-capture");

    await boundary.dispatch(message);
    await boundary.dispatch(message);
    await boundary.dispatch({ type: "RETRY_CAPTURE_JOB", captureId: "stable-capture", permittedSourceOrigins: [] });

    expect(control.start.mock.calls.map(([input]) => input.captureId)).toEqual(["stable-capture", "stable-capture"]);
    expect(control.retry).toHaveBeenCalledWith("stable-capture", []);
  });

  it.each([
    { phase: "submitting_manifest" as const, accepted: false, name: "acceptance save" },
    { phase: "reconciling" as const, accepted: true, name: "reconciliation save" },
    { phase: "completing" as const, accepted: true, name: "terminal status save" },
  ])("keeps $name failures actionable and on the same ID", async ({ phase, accepted }) => {
    const control = controller();
    const persisted = acceptedJob("durable-capture", phase);
    if (!accepted) delete persisted.acceptance;
    control.retry.mockRejectedValue(
      new Error("write failed https://user:pass@fe.test/private?access_token=SECRET Authorization=Bearer AUTH"),
    );
    control.list.mockResolvedValue([persisted]);
    const boundary = runtimeBoundary(control);

    const response = await boundary.dispatch({ type: "RETRY_CAPTURE_JOB", captureId: "durable-capture" });
    const serialized = JSON.stringify(response);

    expect(response).toMatchObject({ captureId: "durable-capture", accepted });
    expect(serialized).toContain(accepted ? "accepted capture is preserved" : "outcome is unknown");
    expect(serialized).toContain("same capture ID");
    for (const secret of ["user:pass", "/private", "SECRET", "AUTH"]) expect(serialized).not.toContain(secret);
  });

  it("dismisses only the requested ID through the runtime boundary", async () => {
    const control = controller();
    const boundary = runtimeBoundary(control);

    const response = await boundary.dispatch({ type: "DISMISS_CAPTURE_JOB", captureId: "terminal-1" });

    expect(response).toEqual({ type: "CAPTURE_JOB_DISMISS_RESULT", ok: true, captureId: "terminal-1" });
    expect(control.dismiss).toHaveBeenCalledWith("terminal-1");
  });

  it("deduplicates concurrent popup START and startup resume through the real coordinator", async () => {
    class MemoryStore implements CaptureJobPersistence {
      readonly jobs = new Map<string, CaptureJobRecord>();
      async getJob(captureId: string) { return this.jobs.get(captureId); }
      async listJobs() { return [...this.jobs.values()]; }
      async saveJob(value: CaptureJobRecord) { this.jobs.set(value.captureId, structuredClone(value)); }
      async removeTerminalJob(captureId: string) { return this.jobs.delete(captureId); }
    }
    let releasePreflight!: () => void;
    const preflightGate = new Promise<void>((resolve) => { releasePreflight = resolve; });
    const events: string[] = [];
    let serverAcceptance: FECaptureManifestResponse | undefined;
    const capability = JSON.parse(
      readFileSync(join(fixtures, "valid-capabilities.json"), "utf8"),
    ) as FECapabilityDiscovery;
    capability.operations.capture_manifest = true;
    capability.operations.asset_upload = true;
    capability.operations.capture_completion = true;
    capability.capture.max_manifest_bytes = 1_000_000;
    capability.capture.max_asset_bytes = 1_000_000;
    const status = (completion: FECaptureStatus["completion"]): FECaptureStatus => ({
      schema_version: "fe.capture.status.v1",
      capture_id: serverAcceptance!.capture_id,
      fragment_id: serverAcceptance!.fragment_id,
      fragment_revision_id: serverAcceptance!.fragment_revision_id,
      capture_attempt_id: serverAcceptance!.capture_attempt_id,
      completion,
      assets: serverAcceptance!.asset_instructions.map((instruction) => ({
        client_variant_id: instruction.client_variant_id,
        outcome: "deferred" as const,
      })),
      enrichment_in_progress: false,
      warnings: [],
    });
    const api: CaptureApiClient = {
      async preflight() {
        events.push("preflight");
        if (events.filter((event) => event === "preflight").length === 1) await preflightGate;
        return { ok: true, capabilitiesUrl: "https://fe.test/v1/capabilities", capabilities: capability };
      },
      async submitManifest(_endpoint: string, envelope: FECaptureEnvelope) {
        events.push("manifest");
        const variantId = envelope.media[0]!.variants[0]!.client_variant_id;
        serverAcceptance = {
          schema_version: "fe.capture.result.v1",
          capture_id: envelope.capture_id,
          fragment_id: `fragment-${envelope.capture_id}`,
          fragment_revision_id: `revision-${envelope.capture_id}`,
          capture_attempt_id: `attempt-${envelope.capture_id}`,
          completion: "accepting",
          idempotent_replay: false,
          asset_instructions: [{
            client_variant_id: variantId,
            asset_variant_id: `asset-${variantId}`,
            action: "request_upload",
            upload_href: `/v1/captures/${envelope.capture_id}/assets/${encodeURIComponent(variantId)}/content`,
          }],
          warnings: [],
          reader_item: JSON.parse(readFileSync(join(fixtures, "valid-reader-video.json"), "utf8")),
        };
        return serverAcceptance;
      },
      async getStatus() {
        events.push("get");
        if (!serverAcceptance) throw new CaptureApiError("missing", "not_found", false, 404);
        return status("accepting");
      },
      async uploadAsset() { events.push("upload"); },
      async completeCapture(
        _endpoint: string,
        _captureId: string,
        _completion: FECaptureCompletionRequest,
      ) {
        events.push("complete");
        return status("complete");
      },
    };
    const acquired: AcquiredAsset = {
      bytes: new Uint8Array([1, 2, 3]),
      digest: { algorithm: "sha256", value: "a".repeat(64) },
      byteSize: 3,
      mimeType: "image/jpeg",
      observedMimeType: "image/jpeg",
    };
    const media: MediaAcquirer = {
      async acquire() { events.push("source"); return acquired; },
    };
    const store = new MemoryStore();
    let id = 0;
    const jobs = new CaptureJobCoordinator(store, api, {
      createId: () => `generated-${++id}`,
      now: () => new Date("2026-09-03T20:00:00.000Z"),
      clientVersion: "0.1.0",
      mediaAcquirer: media,
    });
    const boundary = runtimeBoundary(jobs);
    const message = startMessage("raced-capture", page("https://media.test/item.jpg"));

    const first = boundary.dispatch(message);
    await vi.waitFor(() => expect(events).toEqual(["preflight"]));
    const second = boundary.dispatch(message);
    const startup = jobs.resumeAll();
    releasePreflight();

    const [firstResponse, secondResponse] = await Promise.all([first, second]);
    await startup;
    await vi.waitFor(() => expect(store.jobs.get("raced-capture")?.terminal).toBe(true));

    expect(firstResponse).toMatchObject({ ok: true, captureId: "raced-capture" });
    expect(secondResponse).toMatchObject({ ok: true, captureId: "raced-capture" });
    expect(events.filter((event) => event === "manifest")).toHaveLength(1);
    expect(events.filter((event) => event === "get")).toHaveLength(1);
    expect(events.filter((event) => event === "source")).toHaveLength(1);
    expect(events.filter((event) => event === "upload")).toHaveLength(1);
    expect(events.filter((event) => event === "complete")).toHaveLength(1);
  });
});
