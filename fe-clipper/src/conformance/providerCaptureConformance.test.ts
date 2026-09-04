import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";

import {
  assertCaptureManifestSemantics,
  assertCaptureStatusSemantics,
} from "@/background/captureApi";
import { buildCaptureEnvelope } from "@/capture/envelope";
import { pageCaptureAdapterRegistry, toPageCaptureLocation } from "@/capture/registry";
import type { PageCaptureResult } from "@/capture/types";
import {
  assertFECaptureCompletion,
  assertFECaptureEnvelope,
  assertFECaptureManifestResponse,
  assertFECaptureStatus,
  feContractValidators,
  type FECaptureCompletionRequest,
  type FECaptureEnvelope,
  type FECaptureManifestResponse,
  type FECaptureStatus,
} from "@/contracts/fragmentsEngine";
import scenarioData from "./fixtures/provider-scenarios.json";

const ARTICLE_TEXT =
  "This is substantial readable article prose with enough natural-language content for deterministic Readability extraction. ".repeat(
    14,
  );

const FORBIDDEN_CREDENTIAL_VALUES = [
  "QUERY_CREDENTIAL",
  "FRAGMENT_CREDENTIAL",
  "fixture-user",
  "fixture-password",
  "DOM_COOKIE_CREDENTIAL",
  "LOCAL_STORAGE_CREDENTIAL",
  "SESSION_STORAGE_CREDENTIAL",
  "SUPER_SECRET_ACCESS_TOKEN",
  "SECRET_COOKIE",
  "SECRET_VISITOR_DATA",
  "SECRET_BEARER",
  "FRAGMENT_TRACK_SECRET",
  "SECRET_PAGE_STATE_TOKEN",
  "TEST_ONLY_ACCESS_SECRET_MUST_NOT_LEAK",
  "TEST_ONLY_CSRF_SECRET_MUST_NOT_LEAK",
  "TEST_ONLY_COOKIE_SECRET_MUST_NOT_LEAK",
  "TEST_ONLY_COOKIE_JAR_MUST_NOT_LEAK",
  "TEST_ONLY_BEARER_SECRET_MUST_NOT_LEAK",
  "TEST_ONLY_LOCAL_STORAGE_MUST_NOT_LEAK",
  "TEST_ONLY_SESSION_STORAGE_MUST_NOT_LEAK",
  "TEST_ONLY_AUTH_STATE_MUST_NOT_LEAK",
  "TEST_ONLY_URL_USER_MUST_NOT_LEAK",
  "TEST_ONLY_URL_PASSWORD_MUST_NOT_LEAK",
  "TEST_ONLY_URL_TOKEN_MUST_NOT_LEAK",
  "TEST_ONLY_URL_FRAGMENT_TOKEN_MUST_NOT_LEAK",
  "TEST_ONLY_PAGE_USER_MUST_NOT_LEAK",
  "TEST_ONLY_PAGE_PASSWORD_MUST_NOT_LEAK",
  "TEST_ONLY_PAGE_QUERY_TOKEN_MUST_NOT_LEAK",
  "TEST_ONLY_HEADER_MUST_NOT_LEAK",
  "TEST_ONLY_RAW_STATE_MUST_NOT_LEAK",
  "TEST_ONLY_NESTED_AUTH_STATE_MUST_NOT_LEAK",
] as const;

interface ExpectedMedia {
  kind: "image" | "video" | "audio" | "document" | "timed_text" | "other";
  role: "primary" | "gallery_item" | "hero" | "inline" | "poster" | "transcript" | "other";
  variants: Array<
    | "original"
    | "preview"
    | "thumbnail"
    | "poster"
    | "stream_reference"
    | "audio"
    | "subtitles"
    | "transcript"
    | "other"
  >;
}

interface ProviderScenario {
  id: string;
  fixture: string;
  url: string;
  adapter: string;
  provider: string;
  selection: string;
  body_includes: string;
  media: ExpectedMedia[];
  warning_codes: string[];
}

const scenarios = scenarioData as ProviderScenario[];

function loadScenario(scenario: ProviderScenario): PageCaptureResult {
  const fixturePath = resolve("src/capture/adapters", scenario.fixture);
  const html = readFileSync(fixturePath, "utf8").replace("{{ARTICLE_TEXT}}", ARTICLE_TEXT);
  const dom = new JSDOM(html, { url: scenario.url });
  dom.window.document.cookie = "SID=DOM_COOKIE_CREDENTIAL";
  dom.window.localStorage.setItem("authToken", "LOCAL_STORAGE_CREDENTIAL");
  dom.window.sessionStorage.setItem("session", "SESSION_STORAGE_CREDENTIAL");
  return pageCaptureAdapterRegistry.capture({
    document: dom.window.document,
    location: toPageCaptureLocation(scenario.url),
    selectionText: scenario.selection,
  });
}

async function envelopeFor(
  scenario: ProviderScenario,
  capture: PageCaptureResult,
): Promise<FECaptureEnvelope> {
  let annotation = 0;
  return buildCaptureEnvelope(
    capture,
    {
      title: capture.document.title,
      tags: ["conformance", scenario.adapter, "conformance"],
      note: `Capture note for ${scenario.id}.`,
    },
    {
      captureId: `conformance-${scenario.id}`,
      capturedAt: "2026-09-04T03:00:00.000Z",
      principalId: "local-user",
      clientVersion: "0.1.0",
      createId: () => `${scenario.id}-annotation-${++annotation}`,
    },
  );
}

function mockAcceptance(
  scenario: ProviderScenario,
  envelope: FECaptureEnvelope,
): FECaptureManifestResponse {
  const fragmentId = `fragment-${scenario.id}`;
  const revisionId = `revision-${scenario.id}`;
  const instructionByClientVariant = new Map<string, {
    instruction: FECaptureManifestResponse["asset_instructions"][number];
    assetVariantId: string;
  }>();
  let rejectedBrowserVariant = false;
  const instructions = envelope.media.flatMap((media, mediaIndex) =>
    media.variants.map((variant, variantIndex) => {
      const assetVariantId = `asset-variant-${mediaIndex}-${variantIndex}`;
      const rejected = scenario.warning_codes.length > 0 &&
        variant.transfer_preference === "browser_preferred" &&
        !rejectedBrowserVariant;
      rejectedBrowserVariant ||= rejected;
      const instruction: FECaptureManifestResponse["asset_instructions"][number] = rejected
        ? {
            client_variant_id: variant.client_variant_id,
            action: "rejected",
            reason: "Deterministic partial fixture rejection.",
          }
        : variant.transfer_preference === "reference_only"
          ? {
              client_variant_id: variant.client_variant_id,
              asset_variant_id: assetVariantId,
              action: "reference_only",
            }
          : {
              client_variant_id: variant.client_variant_id,
              asset_variant_id: assetVariantId,
              action: "request_upload",
              upload_href: `/v1/captures/${envelope.capture_id}/assets/${encodeURIComponent(variant.client_variant_id)}/content`,
            };
      instructionByClientVariant.set(variant.client_variant_id, { instruction, assetVariantId });
      return instruction;
    }),
  );

  const readerMedia = envelope.media.map((media, mediaIndex) => ({
    attachment: {
      attachment_id: `attachment-${mediaIndex}`,
      fragment_revision_id: revisionId,
      media_asset_id: `asset-${mediaIndex}`,
      role: media.role,
      position: media.position,
      ...(media.caption ? { caption: media.caption } : {}),
      ...(media.source_context ? { source_context: media.source_context } : {}),
    },
    media_asset_id: `asset-${mediaIndex}`,
    ...(media.provider_media_id ? { provider_media_id: media.provider_media_id } : {}),
    kind: media.kind,
    ...(media.alt_text ? { alt_text: media.alt_text } : {}),
    variants: media.variants.map((variant, variantIndex) => {
      const binding = instructionByClientVariant.get(variant.client_variant_id)!;
      const rejected = binding.instruction.action === "rejected";
      return {
        asset_variant_id: rejected
          ? `rejected-asset-variant-${mediaIndex}-${variantIndex}`
          : binding.assetVariantId,
        kind: variant.kind,
        custody: variant.requested_custody,
        acquisition_state: rejected
          ? "failed"
          : binding.instruction.action === "reference_only"
            ? "reference_only"
            : "pending",
        ...(variant.mime_type ? { mime_type: variant.mime_type } : {}),
        ...(variant.width ? { width: variant.width } : {}),
        ...(variant.height ? { height: variant.height } : {}),
        ...(variant.duration_seconds ? { duration_seconds: variant.duration_seconds } : {}),
        ...(variant.source_url ? { source_url: variant.source_url } : {}),
        ...(rejected
          ? {
              failure: {
                code: "fixture_rejected",
                message: "Deterministic partial fixture rejection.",
                retryable: false,
              },
            }
          : {}),
      };
    }),
  }));
  const acquisitionStates = readerMedia.flatMap((media) =>
    media.variants.map((variant) => variant.acquisition_state),
  );
  const title = envelope.document.title ?? envelope.source.canonical_url ?? scenario.id;
  const summary = envelope.document.description ?? envelope.document.content?.body.slice(0, 200) ?? title;
  const response: unknown = {
    schema_version: "fe.capture.result.v1",
    capture_id: envelope.capture_id,
    fragment_id: fragmentId,
    fragment_revision_id: revisionId,
    capture_attempt_id: `attempt-${scenario.id}`,
    completion: "accepting",
    idempotent_replay: false,
    asset_instructions: instructions,
    warnings: envelope.extraction.warnings,
    reader_item: {
      schema_version: "fe.reader.item.v1",
      fragment_id: fragmentId,
      fragment_revision_id: revisionId,
      revision: 0,
      source: envelope.source,
      renderer: envelope.media.length === 0
        ? "article"
        : envelope.media.length > 1
          ? "gallery"
          : envelope.media[0]!.kind,
      display: {
        title: { value: title, source: "source" },
        ...(envelope.document.description
          ? { description: { value: envelope.document.description, source: "source" } }
          : {}),
        summary: { value: summary, source: "deterministic" },
      },
      article: {
        preview_markdown: envelope.document.content?.body.slice(0, 4000) ?? "",
        full_content_available: Boolean(envelope.document.content),
        ...(envelope.document.content
          ? { full_content_href: `/v1/reader/items/${fragmentId}/content?revision_id=${revisionId}` }
          : {}),
      },
      media: readerMedia,
      tags: {
        combined: envelope.tags,
        attributed: envelope.tags.map((value) => ({
          value,
          source: "user",
          observation_id: envelope.capture_id,
        })),
      },
      annotations: envelope.annotations.map((annotation) => ({
        ...annotation,
        capture_id: envelope.capture_id,
        captured_at: envelope.captured_at,
      })),
      capture_count: 1,
      reading_state: {
        principal_id: envelope.principal_id,
        fragment_id: fragmentId,
        state: "unread",
        position: { kind: "none" },
        revision: 0,
      },
      operations: {
        triage: { case_ids: [], unresolved_count: 0 },
        routing: { state: "none", references: [] },
        materialization: { state: "none", references: [] },
        enrichment: [],
        acquisition: {
          pending: acquisitionStates.filter((state) => state === "pending").length,
          available: 0,
          reference_only: acquisitionStates.filter((state) => state === "reference_only").length,
          failed: acquisitionStates.filter((state) => state === "failed").length,
        },
      },
      actions: [],
    },
  };
  assertFECaptureManifestResponse(response);
  return response;
}

function mockCompletion(
  acceptance: FECaptureManifestResponse,
): { request: FECaptureCompletionRequest; status: FECaptureStatus } {
  const assets = acceptance.asset_instructions.map((instruction) => {
    if (instruction.action === "rejected") {
      return {
        client_variant_id: instruction.client_variant_id,
        outcome: "not_available" as const,
        reason: instruction.reason,
        retryable: false,
      };
    }
    if (instruction.action === "reference_only" || instruction.action === "server_acquire") {
      return { client_variant_id: instruction.client_variant_id, outcome: "deferred" as const };
    }
    if (instruction.action === "reuse_blob") {
      return {
        client_variant_id: instruction.client_variant_id,
        outcome: "already_available" as const,
        digest: instruction.digest,
      };
    }
    return {
      client_variant_id: instruction.client_variant_id,
      outcome: "uploaded" as const,
      digest: { algorithm: "sha256" as const, value: "a".repeat(64) },
      byte_size: 3,
    };
  });
  const request: FECaptureCompletionRequest = {
    schema_version: "fe.capture.completion.v1",
    idempotency_key: `completion-${acceptance.capture_id}`,
    assets,
    warnings: acceptance.warnings,
  };
  const status: FECaptureStatus = {
    schema_version: "fe.capture.status.v1",
    capture_id: acceptance.capture_id,
    fragment_id: acceptance.fragment_id,
    fragment_revision_id: acceptance.fragment_revision_id,
    capture_attempt_id: acceptance.capture_attempt_id,
    completion: assets.some((asset) => asset.outcome === "not_available")
      ? "partial"
      : "complete",
    assets,
    enrichment_in_progress: false,
    warnings: acceptance.warnings,
  };
  assertFECaptureCompletion(request);
  assertFECaptureStatus(status);
  return { request, status };
}

describe("cross-provider capture conformance", () => {
  it.each(scenarios)("maps $id through one sanitized adapter and FE contract path", async (scenario) => {
    const capture = loadScenario(scenario);
    expect(capture.adapter.id).toBe(scenario.adapter);
    expect(capture.identity.provider).toBe(scenario.provider);
    expect(capture.document.contentMarkdown).toContain(scenario.body_includes);
    expect(capture.document.selection).toBe(scenario.selection);
    expect(
      capture.media.map((media) => ({
        kind: media.kind,
        role: media.role,
        variants: media.variants.map((variant) => variant.kind),
      })),
    ).toEqual(scenario.media);
    expect(capture.media.map((media) => media.position)).toEqual(
      capture.media.map((_, index) => index),
    );
    expect(capture.warnings.map((warning) => warning.code)).toEqual(
      expect.arrayContaining(scenario.warning_codes),
    );

    const envelope = await envelopeFor(scenario, capture);
    assertFECaptureEnvelope(envelope);
    expect(envelope.document.content?.body).toContain(scenario.body_includes);
    expect(envelope.tags).toEqual(["conformance", scenario.adapter]);
    expect(envelope.annotations.map((annotation) => annotation.kind)).toEqual([
      "highlight",
      "capture_note",
    ]);
    expect(envelope.annotations[0]?.text).toBe(scenario.selection);
    expect(envelope.annotations[1]?.text).toBe(`Capture note for ${scenario.id}.`);

    const compatibleAdapterMedia = capture.media.filter((media) =>
      media.variants.some((variant) => variant.kind !== "other" && Boolean(variant.sourceUrl)),
    );
    expect(envelope.media.map((media) => media.position)).toEqual(
      envelope.media.map((_, index) => index),
    );
    expect(envelope.media.map((media) => media.provider_media_id)).toEqual(
      compatibleAdapterMedia.map((media) => media.providerMediaId),
    );
    for (const media of envelope.media) {
      expect(feContractValidators.captureMediaItem(media)).toBe(true);
      for (const variant of media.variants) {
        expect(feContractValidators.captureAssetVariant(variant)).toBe(true);
      }
    }
    const variantIds = envelope.media.flatMap((media) =>
      media.variants.map((variant) => variant.client_variant_id),
    );
    expect(new Set(variantIds).size).toBe(variantIds.length);

    const acceptance = mockAcceptance(scenario, envelope);
    assertCaptureManifestSemantics(envelope, acceptance);
    const acceptingStatus: FECaptureStatus = {
      schema_version: "fe.capture.status.v1",
      capture_id: acceptance.capture_id,
      fragment_id: acceptance.fragment_id,
      fragment_revision_id: acceptance.fragment_revision_id,
      capture_attempt_id: acceptance.capture_attempt_id,
      completion: "accepting",
      assets: acceptance.asset_instructions.map((instruction) => ({
        client_variant_id: instruction.client_variant_id,
        outcome: "deferred",
      })),
      enrichment_in_progress: true,
      warnings: acceptance.warnings,
    };
    assertFECaptureStatus(acceptingStatus);
    assertCaptureStatusSemantics(envelope, acceptingStatus);
    const completion = mockCompletion(acceptance);
    assertFECaptureCompletion(completion.request);
    assertCaptureStatusSemantics(envelope, completion.status);
    expect(completion.status.completion).toBe(
      completion.status.assets.some(
        (asset) => asset.outcome === "not_available" || asset.outcome === "failed",
      )
        ? "partial"
        : "complete",
    );

    if (scenario.adapter === "youtube") {
      const automaticVideoBytes = envelope.media
        .filter((media) => media.kind === "video")
        .flatMap((media) => media.variants)
        .filter(
          (variant) =>
            variant.kind === "original" && variant.transfer_preference === "browser_preferred",
      );
      expect(automaticVideoBytes).toEqual([]);
      const youtubeExtension = envelope.extensions?.["provider.youtube"] as
        | { data?: { transcript?: unknown } }
        | undefined;
      if (scenario.id === "youtube-with-captions") {
        expect(envelope.extraction.observed_capabilities).toContain("transcript");
        expect(youtubeExtension?.data?.transcript).toMatchObject({
          source: "rendered_dom",
          language_code: "en",
        });
      } else {
        expect(envelope.extraction.observed_capabilities).not.toContain("transcript");
        expect(youtubeExtension?.data?.transcript).toBeUndefined();
      }
    }

    const serialized = `${JSON.stringify(capture)}\n${JSON.stringify(envelope)}`;
    for (const forbidden of FORBIDDEN_CREDENTIAL_VALUES) {
      expect(serialized, `${scenario.id} leaked ${forbidden}`).not.toContain(forbidden);
    }
  });
});
