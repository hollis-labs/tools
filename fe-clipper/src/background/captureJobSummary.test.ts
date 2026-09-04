import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it } from "vitest";

import type { FECaptureEnvelope } from "@/contracts/fragmentsEngine";
import type { CaptureJobRecord, CaptureVariantTransfer } from "@/lib/jobStorage";
import { captureJobSummary, publicCaptureMessage } from "./captureJobSummary";

function job(): CaptureJobRecord {
  const envelope = JSON.parse(readFileSync(join(
    resolve(process.cwd(), "vendor/fragments-engine-browser-contract-v1/fixtures"),
    "valid-capture-youtube.json",
  ), "utf8")) as FECaptureEnvelope;
  return {
    schemaVersion: 2,
    captureId: envelope.capture_id,
    endpoint: "https://fe.test/v1/captures",
    principalId: envelope.principal_id,
    envelope,
    completionIdempotencyKey: "complete-key",
    phase: "pending_transfer",
    terminal: false,
    manifestAttempts: 1,
    permittedSourceOrigins: ["https://i.ytimg.com"],
    createdAt: "2026-09-03T20:00:00.000Z",
    updatedAt: "2026-09-03T20:01:00.000Z",
  };
}

function posterTransfer(overrides: Partial<CaptureVariantTransfer> = {}): CaptureVariantTransfer {
  return {
    clientVariantId: "poster:maxresdefault",
    action: "request_upload",
    state: "pending",
    attempts: 0,
    updatedAt: "2026-09-03T20:01:00.000Z",
    sourceUrl: "https://i.ytimg.com/secret?access_token=SOURCE_SECRET",
    sourceOrigin: "https://evil.test",
    uploadUrl: "https://fe.test/signed?token=UPLOAD_SECRET",
    expectedDigest: { algorithm: "sha256", value: "a".repeat(64) },
    ...overrides,
  };
}

describe("redacted capture job summaries", () => {
  it("allowlists status fields and re-derives retry origins from the envelope", () => {
    const value = job();
    value.envelope.media[0]!.variants[1]!.source_url =
      `https://i.ytimg.com/${"x".repeat(300)}?access_token=ENVELOPE_SECRET`;
    value.transfers = [posterTransfer({
      state: "failed",
      retryable: true,
      reason: "Fetch https://user:pass@i.ytimg.com/private?token=REASON_SECRET failed; api_key=KEY_SECRET",
      outcome: {
        client_variant_id: "poster:maxresdefault",
        outcome: "failed",
        reason: "raw internal outcome",
        retryable: true,
      },
    })];
    value.error = {
      stage: "transfer",
      message: "Authorization: Bearer AUTH_SECRET; cookie=sid%3DCOOKIE_SECRET",
      retryable: true,
      at: value.updatedAt,
      code: "api_key=CODE_SECRET",
    };

    const summary = captureJobSummary(value);
    const serialized = JSON.stringify(summary);

    expect(summary.retryPermissionOrigins).toEqual(["https://i.ytimg.com"]);
    expect(summary.variants[1]).toMatchObject({
      clientVariantId: "poster:maxresdefault",
      sourceOrigin: "https://i.ytimg.com",
      state: "failed",
      retryable: true,
    });
    for (const secret of ["ENVELOPE_SECRET", "SOURCE_SECRET", "UPLOAD_SECRET", "REASON_SECRET", "KEY_SECRET", "AUTH_SECRET", "COOKIE_SECRET", "CODE_SECRET", "evil.test"]) {
      expect(serialized).not.toContain(secret);
    }
    for (const privateField of ["envelope", "sourceUrl", "uploadUrl", "expectedDigest", "digest", "principalId"]) {
      expect(serialized).not.toContain(privateField);
    }
  });

  it("rechecks revoked grants but excludes finished, reference, and permanent work", () => {
    const value = job();
    value.transfers = [posterTransfer()];
    expect(captureJobSummary(value).retryPermissionOrigins).toEqual(["https://i.ytimg.com"]);

    value.transfers = [posterTransfer({ state: "available", outcome: {
      client_variant_id: "poster:maxresdefault",
      outcome: "uploaded",
      digest: { algorithm: "sha256", value: "b".repeat(64) },
      byte_size: 10,
    } })];
    expect(captureJobSummary(value).retryPermissionOrigins).toEqual([]);

    value.transfers = [posterTransfer({ state: "failed", retryable: false, reason: "HTTP 404", outcome: {
      client_variant_id: "poster:maxresdefault",
      outcome: "failed",
      reason: "HTTP 404",
      retryable: false,
    } })];
    expect(captureJobSummary(value).retryPermissionOrigins).toEqual([]);
  });

  it("never projects an automatic YouTube video-byte permission origin", () => {
    const value = job();
    const video = value.envelope.media[0]!.variants[0]!;
    video.transfer_preference = "browser_preferred";
    video.source_url = "https://redirector.googlevideo.com/videoplayback?signature=SECRET";

    const summary = captureJobSummary(value);

    expect(summary.retryPermissionOrigins).toEqual(["https://i.ytimg.com"]);
    expect(summary.variants[0]).toMatchObject({ clientVariantId: "video:reference" });
    expect(summary.variants[0]).not.toHaveProperty("sourceOrigin");
    expect(JSON.stringify(summary)).not.toContain("googlevideo");
  });

  it("projects status-only terminal recovery without pending variants or retry origins", () => {
    const value = job();
    value.phase = "partial";
    value.terminal = true;
    value.status = {
      schema_version: "fe.capture.status.v1",
      capture_id: value.captureId,
      fragment_id: "fragment-1",
      fragment_revision_id: "revision-1",
      capture_attempt_id: "attempt-1",
      completion: "partial",
      assets: [
        { client_variant_id: "video:reference", outcome: "deferred" },
        {
          client_variant_id: "poster:maxresdefault",
          outcome: "failed",
          reason: "Fetch https://cdn.test/signed?token=STATUS_SECRET returned 404.",
          retryable: false,
        },
      ],
      enrichment_in_progress: false,
      warnings: [],
    };

    const summary = captureJobSummary(value);

    expect(summary.retryPermissionOrigins).toEqual([]);
    expect(summary.variants).toEqual([
      expect.objectContaining({ clientVariantId: "video:reference", state: "reference_only" }),
      expect.objectContaining({
        clientVariantId: "poster:maxresdefault",
        state: "failed",
        retryable: false,
        reason: "Fetch https://cdn.test returned 404.",
      }),
    ]);
    expect(JSON.stringify(summary)).not.toContain("STATUS_SECRET");

    value.status.assets[1] = {
      client_variant_id: "poster:maxresdefault",
      outcome: "deferred",
    };
    expect(captureJobSummary(value).variants[1]?.state).toBe("pending");
    expect(captureJobSummary(value).retryPermissionOrigins).toEqual([]);
  });

  it("redacts URL, header, standalone bearer/basic, token, key, and cookie credentials", () => {
    const redacted = publicCaptureMessage(
      "https://user:pass@example.test/path?token=URL Authorization=Bearer AUTH " +
      "Bearer STANDALONE Basic BASIC access_token=ACCESS api_key=KEY Cookie: sid=COOKIE",
    );
    for (const secret of ["user:pass", "/path", "URL", "AUTH", "STANDALONE", "BASIC", "ACCESS", "KEY", "COOKIE"]) {
      expect(redacted).not.toContain(secret);
    }
    expect(redacted).toContain("https://example.test");
  });
});
