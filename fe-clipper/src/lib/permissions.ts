/**
 * Host-permission helpers. The manifest deliberately ships with
 * `host_permissions` scoped to only the *default* FE endpoint origin
 * (http://127.0.0.1:8091/*) and declares a broader `optional_host_permissions`
 * set purely as the eligible pool the Permissions API can request from --
 * that declaration does not grant anything by itself. When the user points
 * the extension at a different FE endpoint via the options page, we request
 * (with an explicit browser consent prompt) just that origin, rather than
 * shipping a standing `<all_urls>`-style grant.
 */

/** Derives a `scheme://host:port/*` match pattern from an endpoint URL. */
export function originPattern(endpointUrl: string): string {
  const url = new URL(endpointUrl);
  return `${url.protocol}//${url.host}/*`;
}

/** True if the extension already holds host permission for this endpoint's origin. */
export async function hasHostPermission(endpointUrl: string): Promise<boolean> {
  const pattern = originPattern(endpointUrl);
  return chrome.permissions.contains({ origins: [pattern] });
}

/**
 * Prompts the user (must be called from a user-gesture context, e.g. an
 * options-page save handler or a popup click handler) to grant host
 * permission for this endpoint's origin. Returns whether permission is
 * held afterward.
 */
export async function requestHostPermission(endpointUrl: string): Promise<boolean> {
  const pattern = originPattern(endpointUrl);
  if (await chrome.permissions.contains({ origins: [pattern] })) {
    return true;
  }
  return chrome.permissions.request({ origins: [pattern] });
}
