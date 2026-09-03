import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it } from "vitest";

import {
  assertFEAssetOutcome,
  assertFECaptureEnvelope,
  assertFECaptureManifestResponse,
  assertFECaptureStatus,
  FEContractValidationError,
  feContractValidators,
  validateFEContractSchema,
} from "./fragmentsEngine";

const fixtureDirectory = resolve(
  process.cwd(),
  "vendor/fragments-engine-browser-contract-v1/fixtures",
);

function fixture(name: string): unknown {
  return JSON.parse(readFileSync(join(fixtureDirectory, name), "utf8"));
}

const portableFixtures = [
  ["invalid-api-problem-missing-errors.json", feContractValidators.apiProblem, false],
  ["invalid-browser-capture-missing-urls.json", feContractValidators.captureEnvelope, false],
  ["invalid-capture-media-discriminator.json", feContractValidators.captureEnvelope, false],
  ["invalid-capture-unnamespaced-extension.json", feContractValidators.captureEnvelope, false],
  ["invalid-playback-html.json", "playback", false],
  ["invalid-reader-command-patch.json", "readerCommand", false],
  ["invalid-reader-context-messages.json", "readerContext", false],
  ["invalid-reading-position-discriminator.json", "readingState", false],
  ["valid-api-problem.json", feContractValidators.apiProblem, true],
  ["valid-capabilities.json", feContractValidators.capabilities, true],
  ["valid-capture-completion.json", feContractValidators.captureCompletion, true],
  ["valid-capture-youtube.json", feContractValidators.captureEnvelope, true],
  ["valid-conversation-ref.json", "conversationRef", true],
  ["valid-reader-command.json", "readerCommand", true],
  ["valid-reader-context.json", "readerContext", true],
  ["valid-reader-non-web.json", "readerItem", true],
  ["valid-reader-video.json", "readerItem", true],
] as const;

type Validator = (value: unknown) => boolean;

// The portable corpus includes Reader roots in addition to capture roots. Get
// their already-strictly-compiled validators by schema id without declaring
// Reader transport details in extension-owned code.
const schemaBase =
  "https://schemas.hollis-labs.dev/fragments-engine/browser-capture-reader/v1/";
const additionalFixtureValidators: Record<string, Validator> = {
  playback: (value) => validateFEContractSchema(`${schemaBase}playback.schema.json`, value),
  readerCommand: (value) =>
    validateFEContractSchema(`${schemaBase}reader-command.schema.json`, value),
  readerContext: (value) =>
    validateFEContractSchema(`${schemaBase}reader-context.schema.json`, value),
  readingState: (value) =>
    validateFEContractSchema(`${schemaBase}reading-state.schema.json`, value),
  conversationRef: (value) =>
    validateFEContractSchema(`${schemaBase}conversation-ref.schema.json`, value),
  readerItem: (value) =>
    validateFEContractSchema(`${schemaBase}reader-item.schema.json`, value),
};

describe("pinned Fragments Engine contract", () => {
  it("accounts for and validates every portable FE fixture", () => {
    const files = readdirSync(fixtureDirectory).filter((name) => name.endsWith(".json")).sort();
    expect(portableFixtures.map(([name]) => name).sort()).toEqual(files);

    for (const [name, configuredValidator, expected] of portableFixtures) {
      const validator =
        typeof configuredValidator === "string"
          ? additionalFixtureValidators[configuredValidator]
          : configuredValidator;
      expect(validator?.(fixture(name)), name).toBe(expected);
    }
  });

  it("validates capture result, status, completion, and per-asset outcomes", () => {
    const readerItem = fixture("valid-reader-video.json");
    const result = {
      schema_version: "fe.capture.result.v1",
      capture_id: "capture-1",
      fragment_id: "fragment-1",
      fragment_revision_id: "revision-1",
      capture_attempt_id: "attempt-1",
      completion: "accepting",
      idempotent_replay: false,
      asset_instructions: [
        {
          client_variant_id: "poster-1",
          asset_variant_id: "asset-variant-1",
          action: "reference_only",
        },
      ],
      warnings: [],
      reader_item: readerItem,
    };
    const status = {
      schema_version: "fe.capture.status.v1",
      capture_id: "capture-1",
      fragment_id: "fragment-1",
      fragment_revision_id: "revision-1",
      capture_attempt_id: "attempt-1",
      completion: "partial",
      assets: [{ client_variant_id: "poster-1", outcome: "deferred" }],
      enrichment_in_progress: false,
      warnings: [],
    };

    expect(feContractValidators.captureManifestResponse(result)).toBe(true);
    expect(feContractValidators.captureStatus(status)).toBe(true);
    expect(feContractValidators.captureCompletion(fixture("valid-capture-completion.json"))).toBe(
      true,
    );

    for (const outcome of [
      {
        client_variant_id: "original-1",
        outcome: "uploaded",
        digest: { algorithm: "sha256", value: "a".repeat(64) },
        byte_size: 42,
      },
      { client_variant_id: "thumb-1", outcome: "already_available" },
      {
        client_variant_id: "poster-1",
        outcome: "failed",
        reason: "source expired",
        retryable: true,
      },
      { client_variant_id: "transcript-1", outcome: "deferred" },
    ]) {
      expect(feContractValidators.assetOutcome(outcome)).toBe(true);
    }

    assertFECaptureManifestResponse(result);
    assertFECaptureStatus(status);
  });

  it("fails closed on breaking request, media, instruction, and outcome shapes", () => {
    const request = fixture("valid-capture-youtube.json") as Record<string, unknown>;
    expect(() => assertFECaptureEnvelope({ ...request, unexpected: true })).toThrow(
      FEContractValidationError,
    );

    expect(
      feContractValidators.captureAssetVariant({
        client_variant_id: "poster-1",
        kind: "poster",
        mime_type: "video/mp4",
        transfer_preference: "browser_preferred",
        requested_custody: "cache",
      }),
    ).toBe(false);
    expect(
      feContractValidators.assetInstruction({
        client_variant_id: "poster-1",
        asset_variant_id: "asset-variant-1",
        action: "request_upload",
      }),
    ).toBe(false);
    expect(() => assertFEAssetOutcome({ client_variant_id: "poster-1", outcome: "lost" })).toThrow(
      /Capture asset outcome failed pinned FE contract validation/,
    );
  });
});
