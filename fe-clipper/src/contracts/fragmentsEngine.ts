import type { ErrorObject, ValidateFunction } from "ajv";

import { validatorsBySchemaReference } from "@/generated/fe-contract-validators.js";
import type { components } from "@/generated/fragments-engine-api";

type FESchemas = components["schemas"];

export type FECapabilityDiscovery = FESchemas["capabilities.schema"];
// OpenAPI models JSON Schema's root anyOf constraints as extra union members.
// Keep the generated transport source authoritative while selecting the actual
// envelope member by its contract discriminator for application code.
export type FECaptureEnvelope = Extract<
  FESchemas["capture-envelope.schema"],
  { schema_version: "fe.capture.v1" }
>;
export type FECaptureManifestResponse = FESchemas["capture-response.schema"];
export type FECaptureStatus = FESchemas["capture-status.schema"];
export type FECaptureCompletionRequest = FESchemas["capture-completion.schema"];
export type FEAPIProblem = FESchemas["error.schema"];
export type FECaptureMediaItem = FESchemas["CaptureMediaItem"];
export type FECaptureAssetVariant = FESchemas["CaptureAssetVariant"];
export type FEAssetInstruction = FESchemas["AssetInstruction"];
export type FEAssetOutcome = FESchemas["AssetOutcome"];

function requireValidator<T>(schemaReference: string): ValidateFunction<T> {
  const validator = validatorsBySchemaReference[schemaReference] as ValidateFunction<T> | undefined;
  if (!validator) {
    throw new Error(`Pinned FE contract validator is unavailable for ${schemaReference}.`);
  }
  return validator;
}

const schemaBase = "https://schemas.hollis-labs.dev/fragments-engine/browser-capture-reader/v1/";
const mediaSchemaId = `${schemaBase}media.schema.json`;

export const feContractValidators = {
  capabilities: requireValidator<FECapabilityDiscovery>(`${schemaBase}capabilities.schema.json`),
  captureEnvelope: requireValidator<FECaptureEnvelope>(`${schemaBase}capture-envelope.schema.json`),
  captureManifestResponse: requireValidator<FECaptureManifestResponse>(
    `${schemaBase}capture-response.schema.json`,
  ),
  captureStatus: requireValidator<FECaptureStatus>(`${schemaBase}capture-status.schema.json`),
  captureCompletion: requireValidator<FECaptureCompletionRequest>(
    `${schemaBase}capture-completion.schema.json`,
  ),
  apiProblem: requireValidator<FEAPIProblem>(`${schemaBase}error.schema.json`),
  captureMediaItem: requireValidator<FECaptureMediaItem>(`${mediaSchemaId}#/$defs/CaptureMediaItem`),
  captureAssetVariant: requireValidator<FECaptureAssetVariant>(
    `${mediaSchemaId}#/$defs/CaptureAssetVariant`,
  ),
  assetInstruction: requireValidator<FEAssetInstruction>(`${mediaSchemaId}#/$defs/AssetInstruction`),
  assetOutcome: requireValidator<FEAssetOutcome>(`${mediaSchemaId}#/$defs/AssetOutcome`),
} as const;

/** Validate another manifest-listed FE root without declaring a parallel DTO. */
export function validateFEContractSchema(schemaReference: string, value: unknown): boolean {
  return requireValidator(schemaReference)(value);
}

function escapeJSONPointerToken(token: string): string {
  return token.replaceAll("~", "~0").replaceAll("/", "~1");
}

function issuePointer(issue: ErrorObject): string {
  const property =
    issue.keyword === "required" || issue.keyword === "additionalProperties"
      ? issue.params[issue.keyword === "required" ? "missingProperty" : "additionalProperty"]
      : undefined;
  if (typeof property === "string") {
    return `${issue.instancePath}/${escapeJSONPointerToken(property)}`;
  }
  return issue.instancePath || "/";
}

export function formatFEValidationIssues(issues: readonly ErrorObject[] | null | undefined): string {
  if (!issues?.length) return "the value did not match the pinned schema";
  return issues
    .slice(0, 4)
    .map((issue) => `${issuePointer(issue)} ${issue.message ?? issue.keyword}`)
    .join("; ");
}

export class FEContractValidationError extends Error {
  readonly issues: readonly ErrorObject[];

  constructor(boundary: string, issues: readonly ErrorObject[] | null | undefined) {
    const copiedIssues = (issues ?? []).map((issue) => ({
      ...issue,
      params: { ...issue.params },
    }));
    super(`${boundary} failed pinned FE contract validation: ${formatFEValidationIssues(copiedIssues)}`);
    this.name = "FEContractValidationError";
    this.issues = copiedIssues;
  }
}

function assertContract<T>(
  boundary: string,
  value: unknown,
  validator: ValidateFunction<T>,
): asserts value is T {
  if (!validator(value)) {
    throw new FEContractValidationError(boundary, validator.errors);
  }
}

export function assertFECaptureEnvelope(value: unknown): asserts value is FECaptureEnvelope {
  assertContract("Capture request", value, feContractValidators.captureEnvelope);
}

export function assertFECaptureManifestResponse(
  value: unknown,
): asserts value is FECaptureManifestResponse {
  assertContract("Capture manifest response", value, feContractValidators.captureManifestResponse);
}

export function assertFECaptureStatus(value: unknown): asserts value is FECaptureStatus {
  assertContract("Capture status response", value, feContractValidators.captureStatus);
}

export function assertFECaptureCompletion(
  value: unknown,
): asserts value is FECaptureCompletionRequest {
  assertContract("Capture completion request", value, feContractValidators.captureCompletion);
}

export function assertFEAssetOutcome(value: unknown): asserts value is FEAssetOutcome {
  assertContract("Capture asset outcome", value, feContractValidators.assetOutcome);
}
