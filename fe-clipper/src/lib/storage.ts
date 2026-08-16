export const DEFAULT_ENDPOINT = "http://127.0.0.1:8091/v1/intake";

const STORAGE_KEY = "feClipperEndpoint";

/** Reads the configured FE intake endpoint, falling back to the default. */
export async function getEndpoint(): Promise<string> {
  const stored = await chrome.storage.local.get(STORAGE_KEY);
  const value = stored[STORAGE_KEY];
  return typeof value === "string" && value.trim() ? value.trim() : DEFAULT_ENDPOINT;
}

/** Persists the configured FE intake endpoint. */
export async function setEndpoint(endpoint: string): Promise<void> {
  await chrome.storage.local.set({ [STORAGE_KEY]: endpoint.trim() });
}
