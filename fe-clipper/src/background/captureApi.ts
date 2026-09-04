import {
  assertFECaptureCompletion,
  assertFECaptureEnvelope,
  assertFECaptureManifestResponse,
  assertFECaptureStatus,
  feContractValidators,
  formatFEValidationIssues,
  type FEAPIProblem,
  type FECaptureCompletionRequest,
  type FECaptureEnvelope,
  type FECaptureManifestResponse,
  type FECaptureStatus,
} from "@/contracts/fragmentsEngine";
import {
  preflightCaptureManifest,
  type CapturePreflightResult,
} from "@/contracts/capabilities";

export type CaptureApiErrorKind =
  | "network"
  | "not_found"
  | "conflict"
  | "server"
  | "invalid_response";

export class CaptureApiError extends Error {
  constructor(
    message: string,
    readonly kind: CaptureApiErrorKind,
    readonly retryable: boolean,
    readonly status?: number,
    readonly code?: string,
  ) {
    super(message);
    this.name = "CaptureApiError";
  }
}

export type FetchLike = (input: string | URL, init?: RequestInit) => Promise<Response>;

export interface CaptureApiClient {
  preflight(endpoint: string): Promise<CapturePreflightResult>;
  submitManifest(endpoint: string, envelope: FECaptureEnvelope): Promise<FECaptureManifestResponse>;
  getStatus(endpoint: string, captureId: string): Promise<FECaptureStatus>;
  completeCapture(
    endpoint: string,
    captureId: string,
    completion: FECaptureCompletionRequest,
  ): Promise<FECaptureStatus>;
}

export function deriveCaptureManifestUrl(configuredEndpoint: string): string {
  const endpoint = new URL(configuredEndpoint);
  if (endpoint.protocol !== "http:" && endpoint.protocol !== "https:") {
    throw new Error("the configured endpoint must use http:// or https://");
  }
  if (endpoint.username || endpoint.password) {
    throw new Error("the configured endpoint must not contain credentials");
  }
  return new URL("/v1/captures", endpoint.origin).toString();
}

function captureResourceUrl(endpoint: string, captureId: string, suffix = ""): string {
  const manifestUrl = new URL(deriveCaptureManifestUrl(endpoint));
  manifestUrl.pathname = `/v1/captures/${encodeURIComponent(captureId)}${suffix}`;
  return manifestUrl.toString();
}

function describeProblem(problem: FEAPIProblem): string {
  const detail = problem.detail ? ` ${problem.detail}` : "";
  const violation = problem.errors?.[0]
    ? ` (${problem.errors[0].pointer || "/"}: ${problem.errors[0].message})`
    : "";
  return `${problem.title} [${problem.code}].${detail}${violation}`;
}

async function responseBody(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    throw new CaptureApiError(
      `Fragments Engine returned non-JSON data (HTTP ${response.status}). Check the configured endpoint and FE version.`,
      "invalid_response",
      false,
      response.status,
    );
  }
}

function responseError(response: Response, body: unknown): CaptureApiError {
  if (!feContractValidators.apiProblem(body)) {
    return new CaptureApiError(
      `Fragments Engine returned HTTP ${response.status} with an invalid problem body: ${formatFEValidationIssues(feContractValidators.apiProblem.errors)}.`,
      "invalid_response",
      false,
      response.status,
    );
  }
  const kind: CaptureApiErrorKind =
    response.status === 404 ? "not_found" : response.status === 409 ? "conflict" : "server";
  const retryable = body.retryable ?? response.status >= 500;
  return new CaptureApiError(
    `Fragments Engine rejected the capture request (HTTP ${response.status}): ${describeProblem(body)}`,
    kind,
    retryable,
    response.status,
    body.code,
  );
}

async function fetchResponse(
  fetchImplementation: FetchLike,
  url: string,
  init: RequestInit,
): Promise<Response> {
  try {
    return await fetchImplementation(url, { ...init, credentials: "omit" });
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    throw new CaptureApiError(`Could not reach Fragments Engine at ${url}: ${detail}`, "network", true);
  }
}

function requireMatchingCaptureId(captureId: string, actual: string, boundary: string): void {
  if (actual !== captureId) {
    throw new CaptureApiError(
      `${boundary} returned capture_id ${actual}, expected ${captureId}. The response was not persisted.`,
      "invalid_response",
      false,
    );
  }
}

export class CaptureApi implements CaptureApiClient {
  constructor(private readonly fetchImplementation: FetchLike = fetch) {}

  preflight(endpoint: string): Promise<CapturePreflightResult> {
    return preflightCaptureManifest(endpoint, this.fetchImplementation);
  }

  async submitManifest(
    endpoint: string,
    envelope: FECaptureEnvelope,
  ): Promise<FECaptureManifestResponse> {
    assertFECaptureEnvelope(envelope);
    const url = deriveCaptureManifestUrl(endpoint);
    const response = await fetchResponse(this.fetchImplementation, url, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify(envelope),
      cache: "no-store",
    });
    const body = await responseBody(response);
    if (!response.ok) throw responseError(response, body);
    if (response.status !== 200 && response.status !== 201) {
      throw new CaptureApiError(
        `Fragments Engine returned unexpected manifest status HTTP ${response.status}; expected 200 or 201.`,
        "invalid_response",
        false,
        response.status,
      );
    }
    try {
      assertFECaptureManifestResponse(body);
    } catch (error) {
      throw new CaptureApiError(
        error instanceof Error ? error.message : String(error),
        "invalid_response",
        false,
        response.status,
      );
    }
    requireMatchingCaptureId(envelope.capture_id, body.capture_id, "Capture manifest response");
    if ((response.status === 200) !== body.idempotent_replay) {
      throw new CaptureApiError(
        `Fragments Engine manifest HTTP status and idempotent_replay flag disagree for ${envelope.capture_id}.`,
        "invalid_response",
        false,
        response.status,
      );
    }
    return body;
  }

  async getStatus(endpoint: string, captureId: string): Promise<FECaptureStatus> {
    const url = captureResourceUrl(endpoint, captureId);
    const response = await fetchResponse(this.fetchImplementation, url, {
      method: "GET",
      headers: { Accept: "application/json" },
      cache: "no-store",
    });
    const body = await responseBody(response);
    if (!response.ok) throw responseError(response, body);
    if (response.status !== 200) {
      throw new CaptureApiError(
        `Fragments Engine returned unexpected status lookup HTTP ${response.status}; expected 200.`,
        "invalid_response",
        false,
        response.status,
      );
    }
    try {
      assertFECaptureStatus(body);
    } catch (error) {
      throw new CaptureApiError(
        error instanceof Error ? error.message : String(error),
        "invalid_response",
        false,
        response.status,
      );
    }
    requireMatchingCaptureId(captureId, body.capture_id, "Capture status response");
    return body;
  }

  async completeCapture(
    endpoint: string,
    captureId: string,
    completion: FECaptureCompletionRequest,
  ): Promise<FECaptureStatus> {
    assertFECaptureCompletion(completion);
    const url = captureResourceUrl(endpoint, captureId, "/complete");
    const response = await fetchResponse(this.fetchImplementation, url, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify(completion),
      cache: "no-store",
    });
    const body = await responseBody(response);
    if (!response.ok) throw responseError(response, body);
    if (response.status !== 200) {
      throw new CaptureApiError(
        `Fragments Engine returned unexpected capture completion HTTP ${response.status}; expected 200.`,
        "invalid_response",
        false,
        response.status,
      );
    }
    try {
      assertFECaptureStatus(body);
    } catch (error) {
      throw new CaptureApiError(
        error instanceof Error ? error.message : String(error),
        "invalid_response",
        false,
        response.status,
      );
    }
    requireMatchingCaptureId(captureId, body.capture_id, "Capture completion response");
    return body;
  }
}
