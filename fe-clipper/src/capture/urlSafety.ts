const SENSITIVE_LOCATION_KEYS = new Set([
  "accesstoken",
  "apikey",
  "auth",
  "authorization",
  "authtoken",
  "bearer",
  "clientsecret",
  "cookie",
  "credential",
  "csrf",
  "csrftoken",
  "oauthtoken",
  "password",
  "passwd",
  "refreshtoken",
  "session",
  "sessionid",
  "sessiontoken",
  "token",
  "visitordata",
]);

export function isSensitiveCaptureFieldName(value: string): boolean {
  return SENSITIVE_LOCATION_KEYS.has(value.toLowerCase().replace(/[^a-z0-9]/g, ""));
}

function removeSensitiveParameters(parameters: URLSearchParams): boolean {
  let removed = false;
  for (const key of [...parameters.keys()]) {
    if (!isSensitiveCaptureFieldName(key)) continue;
    parameters.delete(key);
    removed = true;
  }
  return removed;
}

function sanitizeFragment(url: URL): void {
  const fragment = url.hash.slice(1);
  if (!fragment) return;

  const queryIndex = fragment.indexOf("?");
  if (queryIndex >= 0) {
    const prefix = fragment.slice(0, queryIndex);
    const parameters = new URLSearchParams(fragment.slice(queryIndex + 1));
    if (!removeSensitiveParameters(parameters)) return;
    const retained = parameters.toString();
    url.hash = `${prefix}${retained ? `?${retained}` : ""}`;
    return;
  }

  if (!fragment.includes("=")) {
    if (isSensitiveCaptureFieldName(fragment)) url.hash = "";
    return;
  }

  const parameters = new URLSearchParams(fragment);
  if (!removeSensitiveParameters(parameters)) return;
  url.hash = parameters.toString();
}

/** Removes browser credential material while retaining ordinary URL provenance. */
export function sanitizeCapturePageUrl(rawUrl: string): string {
  try {
    const url = new URL(rawUrl);
    url.username = "";
    url.password = "";
    removeSensitiveParameters(url.searchParams);
    sanitizeFragment(url);
    return url.href;
  } catch {
    return rawUrl;
  }
}
