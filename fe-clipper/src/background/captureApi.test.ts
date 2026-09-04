import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it, vi } from "vitest";

import type {
  FECaptureCompletionRequest,
  FECaptureEnvelope,
  FECaptureManifestResponse,
  FECaptureStatus,
} from "@/contracts/fragmentsEngine";
import {
  CaptureApi,
  CaptureApiError,
  deriveAssetUploadUrl,
  deriveCaptureManifestUrl,
} from "./captureApi";

const fixtureDirectory = resolve(
  process.cwd(),
  "vendor/fragments-engine-browser-contract-v1/fixtures",
);

function fixture<T>(name: string): T {
  return JSON.parse(readFileSync(join(fixtureDirectory, name), "utf8")) as T;
}

function envelope(): FECaptureEnvelope {
  return fixture<FECaptureEnvelope>("valid-capture-youtube.json");
}

function acceptance(replay = false): FECaptureManifestResponse {
  const capture = envelope();
  return {
    schema_version: "fe.capture.result.v1",
    capture_id: capture.capture_id,
    fragment_id: "fragment-1",
    fragment_revision_id: "revision-1",
    capture_attempt_id: "attempt-1",
    completion: "accepting",
    idempotent_replay: replay,
    asset_instructions: [
      {
        client_variant_id: "video:reference",
        asset_variant_id: "variant-video",
        action: "reference_only",
      },
      {
        client_variant_id: "poster:maxresdefault",
        asset_variant_id: "variant-poster",
        action: "request_upload",
        upload_href: `/v1/captures/${capture.capture_id}/assets/poster%3Amaxresdefault/content`,
      },
    ],
    warnings: [],
    reader_item: fixture("valid-reader-video.json"),
  };
}

function status(completion: FECaptureStatus["completion"] = "accepting"): FECaptureStatus {
  const accepted = acceptance();
  return {
    schema_version: "fe.capture.status.v1",
    capture_id: accepted.capture_id,
    fragment_id: accepted.fragment_id,
    fragment_revision_id: accepted.fragment_revision_id,
    capture_attempt_id: accepted.capture_attempt_id,
    completion,
    assets: [
      { client_variant_id: "video:reference", outcome: "deferred" },
      { client_variant_id: "poster:maxresdefault", outcome: "deferred" },
    ],
    enrichment_in_progress: false,
    warnings: [],
  };
}

function jsonResponse(body: unknown, statusCode = 200): Response {
  return new Response(JSON.stringify(body), {
    status: statusCode,
    headers: { "Content-Type": "application/json" },
  });
}

describe("capture API client", () => {
  it("derives only the credential-free canonical manifest route", () => {
    expect(
      deriveCaptureManifestUrl("https://fe.example.test/old/path?token=secret#fragment"),
    ).toBe("https://fe.example.test/v1/captures");
    expect(() =>
      deriveCaptureManifestUrl("https://user:secret@fe.example.test/v1/captures"),
    ).toThrow(/credentials/);
    expect(() => deriveCaptureManifestUrl("file:///tmp/fe")).toThrow(/http/);
  });

  it.each([
    [201, false],
    [200, true],
  ])("submits valid manifests with HTTP %s replay=%s", async (httpStatus, replay) => {
    const fetchImplementation = vi.fn(
      async (_input: string | URL, _init?: RequestInit) =>
        jsonResponse(acceptance(replay), httpStatus),
    );
    const api = new CaptureApi(fetchImplementation);

    const result = await api.submitManifest(
      "https://fe.example.test/configured?token=must-not-carry#secret",
      envelope(),
    );

    expect(result.idempotent_replay).toBe(replay);
    expect(fetchImplementation).toHaveBeenCalledTimes(1);
    const [url, init] = fetchImplementation.mock.calls[0]!;
    expect(url).toBe("https://fe.example.test/v1/captures");
    expect(init).toMatchObject({
      method: "POST",
      credentials: "omit",
      cache: "no-store",
      headers: { Accept: "application/json", "Content-Type": "application/json" },
    });
    expect(JSON.stringify(init)).not.toMatch(/cookie|authorization|must-not-carry|secret/i);
    expect(JSON.parse(String(init?.body))).toEqual(envelope());
  });

  it("rejects malformed and mismatched success responses", async () => {
    const malformed = new CaptureApi(async () => jsonResponse({ capture_id: envelope().capture_id }, 201));
    await expect(malformed.submitManifest("https://fe.test", envelope())).rejects.toMatchObject({
      kind: "invalid_response",
    });

    const wrong = acceptance();
    wrong.capture_id = "different-capture";
    const mismatched = new CaptureApi(async () => jsonResponse(wrong, 201));
    await expect(mismatched.submitManifest("https://fe.test", envelope())).rejects.toThrow(
      /expected 01KCAPTURE/,
    );
  });

  it("returns typed 404/409 problems and never invents a replacement", async () => {
    const problem = (statusCode: number, code: "not_found" | "conflict") => ({
      type: "about:blank",
      title: code === "not_found" ? "Capture not found" : "Capture conflict",
      status: statusCode,
      code,
    });
    const missing = new CaptureApi(async () => jsonResponse(problem(404, "not_found"), 404));
    await expect(missing.getStatus("https://fe.test", envelope().capture_id)).rejects.toMatchObject({
      kind: "not_found",
      status: 404,
    });

    const conflict = new CaptureApi(async () => jsonResponse(problem(409, "conflict"), 409));
    const error = await conflict
      .submitManifest("https://fe.test", envelope())
      .catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(CaptureApiError);
    expect(error).toMatchObject({ kind: "conflict", retryable: false, status: 409 });

    const malformed = new CaptureApi(async () => jsonResponse({ error: "gateway" }, 502));
    await expect(malformed.getStatus("https://fe.test", envelope().capture_id)).rejects.toMatchObject({
      kind: "invalid_response",
      retryable: false,
      status: 502,
    });
  });

  it("requires exact 200 and valid stable IDs for status and completion", async () => {
    const noContentStatus = new CaptureApi(async () => jsonResponse(status(), 201));
    await expect(noContentStatus.getStatus("https://fe.test", envelope().capture_id)).rejects.toThrow(
      /expected 200/,
    );

    const completion: FECaptureCompletionRequest = {
      schema_version: "fe.capture.completion.v1",
      idempotency_key: "completion-1",
      assets: [],
      warnings: [],
    };
    const wrongStatus = status("complete");
    wrongStatus.capture_id = "wrong";
    const mismatched = new CaptureApi(async () => jsonResponse(wrongStatus));
    await expect(
      mismatched.completeCapture("https://fe.test", envelope().capture_id, completion),
    ).rejects.toThrow(/expected 01KCAPTURE/);

    const wrongCompletionStatus = new CaptureApi(async () => jsonResponse(status("complete"), 201));
    await expect(
      wrongCompletionStatus.completeCapture(
        "https://fe.test",
        envelope().capture_id,
        completion,
      ),
    ).rejects.toThrow(/completion HTTP 201; expected 200/);
  });

  it("semantically binds upload hrefs while accepting FE's real colon path form", () => {
    const captureId = envelope().capture_id;
    const endpoint = "https://fe.test/v1/captures";
    expect(deriveAssetUploadUrl(
      endpoint,
      captureId,
      "poster:maxresdefault",
      `/v1/captures/${captureId}/assets/poster:maxresdefault/content`,
    )).toBe(`https://fe.test/v1/captures/${captureId}/assets/poster:maxresdefault/content`);
    expect(deriveAssetUploadUrl(
      endpoint,
      captureId,
      "poster:maxresdefault",
      `/v1/captures/${captureId}/assets/poster%3Amaxresdefault/content`,
    )).toContain("poster%3Amaxresdefault/content");

    for (const href of [
      `https://evil.test/v1/captures/${captureId}/assets/poster:maxresdefault/content`,
      `/v1/captures/${captureId}/assets/poster:maxresdefault/content?token=secret`,
      `/v1/captures/${captureId}/assets/poster:maxresdefault/content#fragment`,
      `/v1/captures/${captureId}/assets/poster%2Fmaxresdefault/content`,
      `/v1/captures/${captureId}/assets/poster%253Amaxresdefault/content`,
      `/v1/captures/../assets/poster:maxresdefault/content`,
    ]) {
      expect(() => deriveAssetUploadUrl(endpoint, captureId, "poster:maxresdefault", href)).toThrow(
        /unsafe upload route/,
      );
    }
  });

  it("uploads exact bytes with RFC 9530 digest and no credentials or unrelated headers", async () => {
    const fetchImplementation = vi.fn(
      async (_input: string | URL, _init?: RequestInit) => new Response(null, { status: 204 }),
    );
    const api = new CaptureApi(fetchImplementation);
    const capture = envelope();
    const poster = capture.media[0]!.variants.find(
      (candidate) => candidate.client_variant_id === "poster:maxresdefault",
    )!;
    const bytes = new Uint8Array([0, 1, 2, 255]);
    const digest = { algorithm: "sha256" as const, value: "00".repeat(32) };

    await api.uploadAsset(
      "https://fe.test/v1/captures",
      capture.capture_id,
      poster,
      `/v1/captures/${capture.capture_id}/assets/poster:maxresdefault/content`,
      bytes,
      "image/png",
      digest,
    );

    const [url, init] = fetchImplementation.mock.calls[0]!;
    expect(url).toContain("/assets/poster:maxresdefault/content");
    expect(init).toMatchObject({
      method: "PUT",
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
      referrerPolicy: "no-referrer",
      headers: {
        "Content-Type": "image/png",
        Digest: "sha-256=:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=:",
      },
    });
    expect([...new Uint8Array(init?.body as ArrayBuffer)]).toEqual([...bytes]);
    expect(JSON.stringify(init?.headers)).not.toMatch(/authorization|cookie|bearer|secret/i);

    const wrongStatus = new CaptureApi(async () => jsonResponse({}, 200));
    await expect(wrongStatus.uploadAsset(
      "https://fe.test",
      capture.capture_id,
      poster,
      `/v1/captures/${capture.capture_id}/assets/poster:maxresdefault/content`,
      bytes,
      "application/octet-stream",
      digest,
    )).rejects.toThrow(/expected 204/);
  });
});
