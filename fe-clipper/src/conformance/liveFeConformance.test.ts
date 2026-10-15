import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";

import {
  assertCaptureManifestSemantics,
  assertCaptureStatusSemantics,
  CaptureApi,
} from "@/background/captureApi";
import { buildCaptureEnvelope } from "@/capture/envelope";
import { pageCaptureAdapterRegistry, toPageCaptureLocation } from "@/capture/registry";
import type { PageCaptureResult } from "@/capture/types";
import {
  assertFECaptureCompletion,
  assertFECaptureEnvelope,
  assertFECaptureManifestResponse,
  assertFECaptureStatus,
  validateFEContractSchema,
  type FEAssetOutcome,
  type FECaptureCompletionRequest,
  type FECaptureEnvelope,
  type FECaptureManifestResponse,
} from "@/contracts/fragmentsEngine";

const READER_ITEM_SCHEMA =
  "https://schemas.hollis-labs.dev/fragments-engine/browser-capture-reader/v1/reader-item.schema.json";

const endpoint = process.env.FE_CAPTURE_SMOKE_ENDPOINT;
const runId = process.env.FE_CAPTURE_SMOKE_RUN_ID;
const liveDescribe = endpoint ? describe : describe.skip;

function requireRunConfiguration(): { endpoint: string; runId: string } {
  if (!endpoint) throw new Error("FE_CAPTURE_SMOKE_ENDPOINT is required.");
  if (!runId || !/^[a-z0-9-]{4,80}$/i.test(runId)) {
    throw new Error("FE_CAPTURE_SMOKE_RUN_ID must be a unique 4-80 character slug.");
  }
  return { endpoint, runId };
}

function capturedGallery(identitySuffix: string, selection: string, reverse: boolean): PageCaptureResult {
  const fixture = readFileSync(
    resolve("src/capture/adapters/instagram/fixtures/mixed-carousel.html"),
    "utf8",
  );
  const fixtureUrl = "https://www.instagram.com/p/MIXED789/";
  const document = new JSDOM(fixture, { url: fixtureUrl }).window.document;
  const capture = pageCaptureAdapterRegistry.capture({
    document,
    location: toPageCaptureLocation(fixtureUrl),
    selectionText: selection,
  });
  const providerItemId = `MIXED789-${identitySuffix}`;
  const media = reverse
    ? [...capture.media]
        .reverse()
        .map((item, position) => ({ ...item, position }))
    : capture.media;
  return {
    ...capture,
    identity: {
      ...capture.identity,
      submittedUrl: `https://www.instagram.com/p/${providerItemId}/`,
      canonicalUrl: `https://www.instagram.com/p/${providerItemId}/`,
      providerItemId,
    },
    media,
  };
}

async function buildEnvelope(
  captureId: string,
  captured: PageCaptureResult,
  tags: string[],
  note: string,
): Promise<FECaptureEnvelope> {
  let annotation = 0;
  return buildCaptureEnvelope(
    captured,
    { title: captured.document.title, tags, note },
    {
      captureId,
      capturedAt: "2026-09-04T03:30:00.000Z",
      principalId: "local-user",
      clientVersion: "0.1.0",
      createId: () => `${captureId}-annotation-${++annotation}`,
    },
  );
}

function assertAcceptedBindings(
  envelope: FECaptureEnvelope,
  acceptance: FECaptureManifestResponse,
): void {
  assertFECaptureEnvelope(envelope);
  assertFECaptureManifestResponse(acceptance);
  assertCaptureManifestSemantics(envelope, acceptance);
  expect(acceptance.reader_item.fragment_id).toBe(acceptance.fragment_id);
  expect(acceptance.reader_item.fragment_revision_id).toBe(acceptance.fragment_revision_id);
  expect(
    acceptance.reader_item.media.map((media) => media.attachment.fragment_revision_id),
  ).toEqual(acceptance.reader_item.media.map(() => acceptance.fragment_revision_id));
  expect(
    new Set(acceptance.asset_instructions.map((instruction) => instruction.client_variant_id)),
  ).toEqual(
    new Set(
      envelope.media.flatMap((media) =>
        media.variants.map((variant) => variant.client_variant_id),
      ),
    ),
  );
}

function finalOutcomes(acceptance: FECaptureManifestResponse): FEAssetOutcome[] {
  return acceptance.asset_instructions.map((instruction) => {
    if (instruction.action === "reuse_blob") {
      return {
        client_variant_id: instruction.client_variant_id,
        outcome: "already_available",
        digest: instruction.digest,
      };
    }
    if (instruction.action === "reference_only" || instruction.action === "server_acquire") {
      return { client_variant_id: instruction.client_variant_id, outcome: "deferred" };
    }
    return {
      client_variant_id: instruction.client_variant_id,
      outcome: "not_available",
      reason: instruction.action === "rejected"
        ? instruction.reason
        : "Deterministic smoke fixture media URL is intentionally network-isolated.",
      retryable: false,
    };
  });
}

async function getReaderItem(
  configuredEndpoint: string,
  fragmentId: string,
): Promise<FECaptureManifestResponse["reader_item"]> {
  const url = new URL(
    `/v1/reader/items/${encodeURIComponent(fragmentId)}?principal_id=local-user`,
    configuredEndpoint,
  );
  const response = await fetch(url, {
    method: "GET",
    headers: { Accept: "application/json" },
    credentials: "omit",
    cache: "no-store",
    redirect: "error",
  });
  expect(response.status).toBe(200);
  const body: unknown = await response.json();
  expect(validateFEContractSchema(READER_ITEM_SCHEMA, body)).toBe(true);
  return body as FECaptureManifestResponse["reader_item"];
}

liveDescribe("live Fragments Engine capture conformance", () => {
  it("proves retry, partial completion, revision, and additive recapture semantics", async () => {
    const config = requireRunConfiguration();
    const api = new CaptureApi(fetch, async () => true);
    const preflight = await api.preflight(config.endpoint);
    expect(preflight).toMatchObject({ ok: true });

    const firstCapture = capturedGallery(config.runId, "First live highlight.", false);
    const firstEnvelope = await buildEnvelope(
      `${config.runId}-capture-1`,
      firstCapture,
      ["live-conformance", "first-tag"],
      "First live capture note.",
    );
    expect(firstEnvelope.document.content?.body).toContain("synthetic carousel");
    const first = await api.submitManifest(config.endpoint, firstEnvelope);
    assertAcceptedBindings(firstEnvelope, first);
    expect(first).toMatchObject({ completion: "accepting", idempotent_replay: false });

    const replay = await api.submitManifest(config.endpoint, firstEnvelope);
    assertAcceptedBindings(firstEnvelope, replay);
    expect(replay).toMatchObject({
      idempotent_replay: true,
      fragment_id: first.fragment_id,
      fragment_revision_id: first.fragment_revision_id,
      capture_attempt_id: first.capture_attempt_id,
    });
    expect(replay.reader_item.capture_count).toBe(first.reader_item.capture_count);

    const acceptingStatus = await api.getStatus(config.endpoint, firstEnvelope.capture_id);
    assertFECaptureStatus(acceptingStatus);
    assertCaptureStatusSemantics(firstEnvelope, acceptingStatus);
    expect(acceptingStatus).toMatchObject({
      fragment_id: first.fragment_id,
      fragment_revision_id: first.fragment_revision_id,
      capture_attempt_id: first.capture_attempt_id,
    });

    const completion: FECaptureCompletionRequest = {
      schema_version: "fe.capture.completion.v1",
      idempotency_key: `${config.runId}-completion-1`,
      assets: finalOutcomes(first),
      warnings: [
        {
          code: "capture.live_fixture_media_unavailable",
          message: "The live API smoke intentionally does not contact fixture media hosts.",
        },
      ],
    };
    assertFECaptureCompletion(completion);
    const partial = await api.completeCapture(
      config.endpoint,
      firstEnvelope.capture_id,
      completion,
    );
    assertFECaptureStatus(partial);
    assertCaptureStatusSemantics(firstEnvelope, partial);
    expect(partial.completion).toBe("partial");
    expect(partial.assets.map((asset) => asset.client_variant_id).sort()).toEqual(
      firstEnvelope.media
        .flatMap((media) => media.variants.map((variant) => variant.client_variant_id))
        .sort(),
    );

    const changedCapture = capturedGallery(config.runId, "Second live highlight.", true);
    const changedEnvelope = await buildEnvelope(
      `${config.runId}-capture-2`,
      changedCapture,
      ["live-conformance", "second-tag"],
      "Second live capture note.",
    );
    const changed = await api.submitManifest(config.endpoint, changedEnvelope);
    assertAcceptedBindings(changedEnvelope, changed);
    expect(changed.fragment_id).toBe(first.fragment_id);
    expect(changed.fragment_revision_id).not.toBe(first.fragment_revision_id);
    expect(changed.capture_attempt_id).not.toBe(first.capture_attempt_id);
    expect(changed.reader_item.capture_count).toBe(first.reader_item.capture_count + 1);
    expect(changed.reader_item.media.map((media) => media.provider_media_id)).toEqual(
      changedCapture.media.map((media) => media.providerMediaId),
    );
    expect(changed.reader_item.tags.combined).toEqual(
      expect.arrayContaining(["live-conformance", "first-tag", "second-tag"]),
    );
    expect(changed.reader_item.annotations.map((annotation) => annotation.text)).toEqual([
      "Second live highlight.",
      "Second live capture note.",
    ]);

    const repeatedEnvelope = await buildEnvelope(
      `${config.runId}-capture-3`,
      changedCapture,
      ["live-conformance", "third-tag"],
      "Third live capture note.",
    );
    const repeated = await api.submitManifest(config.endpoint, repeatedEnvelope);
    assertAcceptedBindings(repeatedEnvelope, repeated);
    expect(repeated.fragment_id).toBe(first.fragment_id);
    expect(repeated.fragment_revision_id).toBe(changed.fragment_revision_id);
    expect(repeated.capture_attempt_id).not.toBe(changed.capture_attempt_id);
    expect(repeated.reader_item.capture_count).toBe(changed.reader_item.capture_count + 1);
    expect(repeated.reader_item.tags.combined).toEqual(
      expect.arrayContaining(["first-tag", "second-tag", "third-tag"]),
    );
    expect(repeated.reader_item.annotations.map((annotation) => annotation.text)).toEqual([
      "Second live highlight.",
      "Third live capture note.",
    ]);

    const reader = await getReaderItem(config.endpoint, repeated.fragment_id);
    expect(reader.fragment_revision_id).toBe(repeated.fragment_revision_id);
    expect(reader.capture_count).toBe(repeated.reader_item.capture_count);
    expect(reader.annotations.map((annotation) => annotation.text)).toEqual(
      expect.arrayContaining([
        "First live highlight.",
        "First live capture note.",
        "Second live highlight.",
        "Second live capture note.",
        "Third live capture note.",
      ]),
    );
  }, 30_000);
});
