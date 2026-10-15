import type { CaptureConfigurationSnapshot } from "./types";

export const DEFAULT_ENDPOINT = "http://127.0.0.1:8091/v1/captures";
export const DEFAULT_PRINCIPAL_ID = "local-user";

const STORAGE_KEY = "feClipperEndpoint";
const PRINCIPAL_STORAGE_KEY = "feClipperPrincipalId";

export function normalizeCaptureEndpoint(endpoint: string): string {
  const parsed = new URL(endpoint.trim());
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error("Endpoint must use http:// or https://");
  }
  if (parsed.username || parsed.password) {
    throw new Error("Endpoint must not contain a username or password");
  }
  return new URL("/v1/captures", parsed.origin).toString();
}

/** Reads the configured FE capture endpoint, falling back to the default. */
export async function getEndpoint(): Promise<string> {
  const stored = await chrome.storage.local.get(STORAGE_KEY);
  const value = stored[STORAGE_KEY];
  if (typeof value === "string" && value.trim()) {
    try {
      return normalizeCaptureEndpoint(value);
    } catch {
      // Never resurrect credential-bearing or malformed legacy settings.
    }
  }
  return DEFAULT_ENDPOINT;
}

/** Persists the canonical FE capture endpoint. */
export async function setEndpoint(endpoint: string): Promise<void> {
  await chrome.storage.local.set({ [STORAGE_KEY]: normalizeCaptureEndpoint(endpoint) });
}

/** Reads the local FE principal identity used for new capture actions. */
export async function getPrincipalId(): Promise<string> {
  const stored = await chrome.storage.local.get(PRINCIPAL_STORAGE_KEY);
  const value = stored[PRINCIPAL_STORAGE_KEY];
  return typeof value === "string" && value.trim() ? value.trim() : DEFAULT_PRINCIPAL_ID;
}

export async function setPrincipalId(principalId: string): Promise<void> {
  await chrome.storage.local.set({ [PRINCIPAL_STORAGE_KEY]: principalId.trim() });
}

/** Snapshots mutable settings once for a deliberate capture action. */
export async function getCaptureConfiguration(): Promise<CaptureConfigurationSnapshot> {
  const [endpoint, principalId] = await Promise.all([getEndpoint(), getPrincipalId()]);
  return { endpoint, principalId };
}
