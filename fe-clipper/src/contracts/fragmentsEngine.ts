import Ajv2020, { type ErrorObject, type ValidateFunction } from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

import capabilitiesSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/capabilities.schema.json";
import captureCompletionSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/capture-completion.schema.json";
import captureEnvelopeSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/capture-envelope.schema.json";
import captureResponseSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/capture-response.schema.json";
import captureStatusSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/capture-status.schema.json";
import commonSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/common.schema.json";
import contractManifest from "../../vendor/fragments-engine-browser-contract-v1/contract-manifest.json";
import conversationRefSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/conversation-ref.schema.json";
import errorSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/error.schema.json";
import mediaSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/media.schema.json";
import playbackSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/playback.schema.json";
import readerCommandSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/reader-command.schema.json";
import readerContextSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/reader-context.schema.json";
import readerItemSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/reader-item.schema.json";
import readerListSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/reader-list.schema.json";
import readingStateSchema from "../../vendor/fragments-engine-browser-contract-v1/schema/reading-state.schema.json";
import type { components } from "@/generated/fragments-engine-api";

type FESchemas = components["schemas"];

export type FECapabilityDiscovery = FESchemas["capabilities.schema"];
export type FECaptureEnvelope = FESchemas["capture-envelope.schema"];
export type FECaptureManifestResponse = FESchemas["capture-response.schema"];
export type FECaptureStatus = FESchemas["capture-status.schema"];
export type FECaptureCompletionRequest = FESchemas["capture-completion.schema"];
export type FEAPIProblem = FESchemas["error.schema"];
export type FECaptureMediaItem = FESchemas["CaptureMediaItem"];
export type FECaptureAssetVariant = FESchemas["CaptureAssetVariant"];
export type FEAssetInstruction = FESchemas["AssetInstruction"];
export type FEAssetOutcome = FESchemas["AssetOutcome"];

interface SchemaDocument {
  $id: string;
  [key: string]: unknown;
}

const schemaDocuments = [
  capabilitiesSchema,
  captureCompletionSchema,
  captureEnvelopeSchema,
  captureResponseSchema,
  captureStatusSchema,
  commonSchema,
  conversationRefSchema,
  errorSchema,
  mediaSchema,
  playbackSchema,
  readerCommandSchema,
  readerContextSchema,
  readerItemSchema,
  readerListSchema,
  readingStateSchema,
] as unknown as readonly SchemaDocument[];

const schemasByRelativePath = new Map(
  schemaDocuments.map((schema) => [
    `schema/${schema.$id.slice(schema.$id.lastIndexOf("/") + 1)}`,
    schema,
  ]),
);

const ajv = new Ajv2020({
  allErrors: true,
  strict: true,
  validateFormats: true,
});
addFormats(ajv);

for (const schema of schemaDocuments) {
  ajv.addSchema(schema, schema.$id);
}

// Fail during module initialization if a manifest root is missing or cannot be
// compiled. This keeps a partially vendored or breaking FE contract from being
// accepted silently.
for (const [name, relativePath] of Object.entries(contractManifest.schemas)) {
  const schema = schemasByRelativePath.get(relativePath);
  if (!schema) {
    throw new Error(`Pinned FE contract root ${name} points to missing ${relativePath}.`);
  }
  if (!ajv.getSchema(schema.$id)) {
    throw new Error(`Pinned FE contract root ${name} (${schema.$id}) could not be compiled.`);
  }
}

function requireValidator<T>(schemaReference: string): ValidateFunction<T> {
  const validator = ajv.getSchema<T>(schemaReference);
  if (!validator) {
    throw new Error(`Pinned FE contract validator is unavailable for ${schemaReference}.`);
  }
  return validator;
}

const mediaSchemaId = mediaSchema.$id;

export const feContractValidators = {
  capabilities: requireValidator<FECapabilityDiscovery>(capabilitiesSchema.$id),
  captureEnvelope: requireValidator<FECaptureEnvelope>(captureEnvelopeSchema.$id),
  captureManifestResponse: requireValidator<FECaptureManifestResponse>(captureResponseSchema.$id),
  captureStatus: requireValidator<FECaptureStatus>(captureStatusSchema.$id),
  captureCompletion: requireValidator<FECaptureCompletionRequest>(captureCompletionSchema.$id),
  apiProblem: requireValidator<FEAPIProblem>(errorSchema.$id),
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
