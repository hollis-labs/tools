import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it } from "vitest";

import type {
  FECaptureEnvelope,
  FECaptureManifestResponse,
  FECaptureStatus,
} from "@/contracts/fragmentsEngine";
import {
  assertTransferRuntimeState,
  planCaptureTransfers,
  reconcileTransferStatus,
  transfersReadyForCompletion,
} from "./captureTransfer";

const fixtureDirectory = resolve(process.cwd(), "vendor/fragments-engine-browser-contract-v1/fixtures");

function envelope(): FECaptureEnvelope {
  return JSON.parse(
    readFileSync(join(fixtureDirectory, "valid-capture-youtube.json"), "utf8"),
  ) as FECaptureEnvelope;
}

function acceptance(capture: FECaptureEnvelope): FECaptureManifestResponse {
  return {
    schema_version: "fe.capture.result.v1",
    capture_id: capture.capture_id,
    fragment_id: "fragment-1",
    fragment_revision_id: "revision-1",
    capture_attempt_id: "attempt-1",
    completion: "accepting",
    idempotent_replay: false,
    asset_instructions: [
      {
        client_variant_id: "video:reference",
        asset_variant_id: "asset-video",
        action: "reference_only",
      },
      {
        client_variant_id: "poster:maxresdefault",
        asset_variant_id: "asset-poster",
        action: "request_upload",
        upload_href: `/v1/captures/${capture.capture_id}/assets/poster:maxresdefault/content`,
      },
    ],
    warnings: [],
    reader_item: JSON.parse(
      readFileSync(join(fixtureDirectory, "valid-reader-video.json"), "utf8"),
    ),
  };
}

describe("capture transfer semantic binding", () => {
  it("plans every instruction in deterministic order before effects", () => {
    const capture = envelope();
    const transfers = planCaptureTransfers(
      capture,
      "https://fe.test/v1/captures",
      acceptance(capture),
      "2026-09-03T20:00:00.000Z",
    );
    expect(transfers.map(({ clientVariantId, state }) => [clientVariantId, state])).toEqual([
      ["video:reference", "reference_only"],
      ["poster:maxresdefault", "pending"],
    ]);
  });

  it("rejects unknown, duplicate, missing, and transfer-policy-mismatched instructions", () => {
    const capture = envelope();
    const base = acceptance(capture);
    const unknown = structuredClone(base);
    unknown.asset_instructions[0]!.client_variant_id = "unknown";
    expect(() => planCaptureTransfers(capture, "https://fe.test", unknown, new Date().toISOString()))
      .toThrow(/unknown variant/);

    const duplicate = structuredClone(base);
    duplicate.asset_instructions[1]!.client_variant_id = "video:reference";
    expect(() => planCaptureTransfers(capture, "https://fe.test", duplicate, new Date().toISOString()))
      .toThrow(/duplicate instructions/);

    const missing = structuredClone(base);
    missing.asset_instructions.pop();
    expect(() => planCaptureTransfers(capture, "https://fe.test", missing, new Date().toISOString()))
      .toThrow(/omitted an instruction/);

    const mismatched = structuredClone(base);
    mismatched.asset_instructions[0] = {
      client_variant_id: "video:reference",
      asset_variant_id: "asset-video",
      action: "request_upload",
      upload_href: `/v1/captures/${capture.capture_id}/assets/video:reference/content`,
    };
    expect(() => planCaptureTransfers(capture, "https://fe.test", mismatched, new Date().toISOString()))
      .toThrow(/transfer policy forbids/);
  });

  it("locks YouTube video bytes even if an inconsistent envelope/server asks for upload", () => {
    const capture = envelope();
    const video = capture.media[0]!.variants.find((variant) => variant.client_variant_id === "video:reference")!;
    video.transfer_preference = "browser_preferred";
    video.requested_custody = "mirror";
    const response = acceptance(capture);
    response.asset_instructions[0] = {
      client_variant_id: "video:reference",
      asset_variant_id: "asset-video",
      action: "request_upload",
      upload_href: `/v1/captures/${capture.capture_id}/assets/video:reference/content`,
    };
    expect(() => planCaptureTransfers(capture, "https://fe.test", response, new Date().toISOString()))
      .toThrow(/transfer policy forbids/);
  });

  it("binds FE digests to envelope integrity and fills omitted expected_digest", () => {
    const capture = envelope();
    const poster = capture.media[0]!.variants.find((variant) => variant.client_variant_id === "poster:maxresdefault")!;
    poster.digest = { algorithm: "sha256", value: "a".repeat(64) };
    const response = acceptance(capture);
    const transfers = planCaptureTransfers(capture, "https://fe.test", response, new Date().toISOString());
    expect(transfers[1]?.expectedDigest).toEqual(poster.digest);

    const conflict = acceptance(capture);
    const upload = conflict.asset_instructions[1]!;
    if (upload.action !== "request_upload") throw new Error("test fixture mismatch");
    upload.expected_digest = { algorithm: "sha256", value: "b".repeat(64) };
    expect(() => planCaptureTransfers(capture, "https://fe.test", conflict, new Date().toISOString()))
      .toThrow(/contradicts/);

    const reuse = acceptance(capture);
    reuse.asset_instructions[1] = {
      client_variant_id: "poster:maxresdefault",
      asset_variant_id: "asset-poster",
      action: "reuse_blob",
      digest: { algorithm: "sha256", value: "c".repeat(64) },
    };
    expect(() => planCaptureTransfers(capture, "https://fe.test", reuse, new Date().toISOString()))
      .toThrow(/contradicts/);
  });

  it("accepts status proof only when its digest matches persisted upload facts", () => {
    const capture = envelope();
    const transfers = planCaptureTransfers(
      capture,
      "https://fe.test",
      acceptance(capture),
      "2026-09-03T20:00:00.000Z",
    );
    transfers[1]!.state = "transferring";
    transfers[1]!.digest = { algorithm: "sha256", value: "a".repeat(64) };
    const status: FECaptureStatus = {
      schema_version: "fe.capture.status.v1",
      capture_id: capture.capture_id,
      fragment_id: "fragment-1",
      fragment_revision_id: "revision-1",
      capture_attempt_id: "attempt-1",
      completion: "transferring",
      assets: [
        { client_variant_id: "video:reference", outcome: "deferred" },
        {
          client_variant_id: "poster:maxresdefault",
          outcome: "already_available",
          digest: { algorithm: "sha256", value: "b".repeat(64) },
        },
      ],
      enrichment_in_progress: false,
      warnings: [],
    };
    expect(() => reconcileTransferStatus(transfers, status, new Date().toISOString()))
      .toThrow(/persisted digest/);

    const omitted = { ...status, assets: status.assets.slice(0, 1) };
    expect(() => reconcileTransferStatus(transfers, omitted, new Date().toISOString()))
      .toThrow(/omitted variant outcome/);

    status.assets[1] = {
      client_variant_id: "poster:maxresdefault",
      outcome: "already_available",
      digest: { algorithm: "sha256", value: "a".repeat(64) },
    };
    const reconciled = reconcileTransferStatus(transfers, status, new Date().toISOString());
    expect(reconciled[1]).toMatchObject({ state: "available", outcome: { outcome: "already_available" } });
    expect(transfersReadyForCompletion(reconciled)).toBe(true);
  });

  it("requires explicit final states and leaves retryable failures nonterminal", () => {
    const capture = envelope();
    const transfers = planCaptureTransfers(capture, "https://fe.test", acceptance(capture), new Date().toISOString());
    transfers[1]!.outcome = {
      client_variant_id: "poster:maxresdefault",
      outcome: "failed",
      reason: "network",
      retryable: true,
    };
    expect(() => transfersReadyForCompletion(transfers)).toThrow(/inconsistent/);
    transfers[1]!.state = "failed";
    transfers[1]!.retryable = true;
    transfers[1]!.reason = "network";
    expect(transfersReadyForCompletion(transfers)).toBe(false);
    transfers[1]!.retryable = false;
    if (transfers[1]!.outcome?.outcome === "failed") {
      transfers[1]!.outcome.retryable = false;
    }
    expect(transfersReadyForCompletion(transfers)).toBe(true);
  });

  it("rejects action/state/outcome combinations the coordinator never creates", () => {
    const capture = envelope();
    const fresh = () => planCaptureTransfers(
      capture,
      "https://fe.test",
      acceptance(capture),
      new Date().toISOString(),
    );

    const referenceAsUploaded = fresh();
    referenceAsUploaded[0]!.state = "available";
    referenceAsUploaded[0]!.digest = { algorithm: "sha256", value: "a".repeat(64) };
    referenceAsUploaded[0]!.outcome = {
      client_variant_id: "video:reference",
      outcome: "uploaded",
      digest: { algorithm: "sha256", value: "a".repeat(64) },
      byte_size: 1,
    };
    expect(() => assertTransferRuntimeState(referenceAsUploaded)).toThrow(/inconsistent/);

    const requestAsDeferred = fresh();
    requestAsDeferred[1]!.state = "reference_only";
    requestAsDeferred[1]!.outcome = {
      client_variant_id: "poster:maxresdefault",
      outcome: "deferred",
    };
    expect(() => assertTransferRuntimeState(requestAsDeferred)).toThrow(/inconsistent/);

    const availableAsFailed = fresh();
    availableAsFailed[1]!.state = "available";
    availableAsFailed[1]!.digest = { algorithm: "sha256", value: "a".repeat(64) };
    availableAsFailed[1]!.outcome = {
      client_variant_id: "poster:maxresdefault",
      outcome: "failed",
      reason: "tampered",
      retryable: false,
    };
    expect(() => assertTransferRuntimeState(availableAsFailed)).toThrow(/inconsistent/);

    const missingProof = fresh();
    missingProof[1]!.state = "available";
    missingProof[1]!.digest = { algorithm: "sha256", value: "a".repeat(64) };
    missingProof[1]!.outcome = {
      client_variant_id: "poster:maxresdefault",
      outcome: "already_available",
    };
    expect(() => assertTransferRuntimeState(missingProof)).toThrow(/inconsistent/);
  });
});
