import {
  feContractValidators,
  formatFEValidationIssues,
  type FEAPIProblem,
  type FECapabilityDiscovery,
} from "./fragmentsEngine";

export const SUPPORTED_CAPTURE_CONTRACT_VERSION = "fe.capture.v1";

export type CapturePreflightFailureKind =
  | "invalid-endpoint"
  | "permission"
  | "network"
  | "server-error"
  | "invalid-response"
  | "incompatible-contract"
  | "capture-unavailable";

export type CapturePreflightResult =
  | {
      ok: true;
      capabilitiesUrl: string;
      capabilities: FECapabilityDiscovery;
    }
  | {
      ok: false;
      kind: CapturePreflightFailureKind;
      capabilitiesUrl?: string;
      message: string;
    };

type FetchLike = (input: string | URL, init?: RequestInit) => Promise<Response>;

/**
 * Derives FE's contract-owned discovery route from the configured legacy or
 * manifest endpoint. Path/query/fragment data and embedded credentials never
 * carry into the preflight request.
 */
export function deriveCapabilitiesUrl(configuredEndpoint: string): string {
  const endpoint = new URL(configuredEndpoint);
  if (endpoint.protocol !== "http:" && endpoint.protocol !== "https:") {
    throw new Error("the configured endpoint must use http:// or https://");
  }
  if (endpoint.username || endpoint.password) {
    throw new Error("the configured endpoint must not contain credentials");
  }
  return new URL("/v1/capabilities", endpoint.origin).toString();
}

function describeProblem(problem: FEAPIProblem): string {
  const detail = problem.detail ? ` ${problem.detail}` : "";
  const fieldDetail = problem.errors?.[0]
    ? ` (${problem.errors[0].pointer || "/"}: ${problem.errors[0].message})`
    : "";
  return `${problem.title} [${problem.code}].${detail}${fieldDetail}`;
}

export async function preflightCaptureManifest(
  configuredEndpoint: string,
  fetchImplementation: FetchLike = fetch,
): Promise<CapturePreflightResult> {
  let capabilitiesUrl: string;
  try {
    capabilitiesUrl = deriveCapabilitiesUrl(configuredEndpoint);
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    return {
      ok: false,
      kind: "invalid-endpoint",
      message: `Cannot check Fragments Engine compatibility: ${detail}.`,
    };
  }

  let response: Response;
  try {
    response = await fetchImplementation(capabilitiesUrl, {
      method: "GET",
      headers: { Accept: "application/json" },
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
    });
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    return {
      ok: false,
      kind: "network",
      capabilitiesUrl,
      message: `Could not reach Fragments Engine capability discovery at ${capabilitiesUrl}: ${detail}`,
    };
  }

  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return {
      ok: false,
      kind: "invalid-response",
      capabilitiesUrl,
      message: `Fragments Engine returned a non-JSON capability response (HTTP ${response.status}). Check the configured endpoint and server version.`,
    };
  }

  if (!response.ok) {
    if (feContractValidators.apiProblem(body)) {
      return {
        ok: false,
        kind: "server-error",
        capabilitiesUrl,
        message: `Fragments Engine capability discovery failed (HTTP ${response.status}): ${describeProblem(body)}`,
      };
    }
    return {
      ok: false,
      kind: "invalid-response",
      capabilitiesUrl,
      message: `Fragments Engine capability discovery failed with HTTP ${response.status} and an invalid error body: ${formatFEValidationIssues(feContractValidators.apiProblem.errors)}.`,
    };
  }

  if (!feContractValidators.capabilities(body)) {
    return {
      ok: false,
      kind: "invalid-response",
      capabilitiesUrl,
      message: `Fragments Engine returned capabilities that do not match the pinned fe.capabilities.v1 schema: ${formatFEValidationIssues(feContractValidators.capabilities.errors)}. Update FE or this extension before capturing.`,
    };
  }

  const captureContract = body.contracts.find((contract) => contract.name === "capture");
  if (!captureContract?.accepted_versions.includes(SUPPORTED_CAPTURE_CONTRACT_VERSION)) {
    const accepted = captureContract?.accepted_versions.join(", ") || "none advertised";
    return {
      ok: false,
      kind: "incompatible-contract",
      capabilitiesUrl,
      message: `This extension requires ${SUPPORTED_CAPTURE_CONTRACT_VERSION}, but Fragments Engine accepts ${accepted}. Update FE or use a compatible extension version.`,
    };
  }

  if (!body.operations.capture_manifest) {
    return {
      ok: false,
      kind: "capture-unavailable",
      capabilitiesUrl,
      message: `Fragments Engine accepts ${SUPPORTED_CAPTURE_CONTRACT_VERSION}, but manifest capture is not ready on server ${body.server_version}. Start or upgrade the FE capture service and try again.`,
    };
  }

  return { ok: true, capabilitiesUrl, capabilities: body };
}
