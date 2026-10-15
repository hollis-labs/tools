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

import type { CapturedPage } from "./types";

export interface CapturePermissionResult {
  feGranted: boolean;
  feOrigin: string;
  permittedSourceOrigins: string[];
  deniedSourceOrigins: string[];
}

export interface PermissionsApiLike {
  contains(permissions: chrome.permissions.Permissions): Promise<boolean>;
  request(permissions: chrome.permissions.Permissions): Promise<boolean>;
}

function safeHttpOrigin(rawUrl: string): string | undefined {
  try {
    const url = new URL(rawUrl);
    if (
      (url.protocol !== "http:" && url.protocol !== "https:") ||
      url.username ||
      url.password
    ) {
      return undefined;
    }
    return url.origin;
  } catch {
    return undefined;
  }
}

/** Derives a `scheme://host:port/*` match pattern from an endpoint URL. */
export function originPattern(endpointUrl: string): string {
  const url = new URL(endpointUrl);
  if (
    (url.protocol !== "http:" && url.protocol !== "https:") ||
    url.username ||
    url.password
  ) {
    throw new Error("Host permission origins must be credential-free HTTP(S) URLs.");
  }
  return `${url.protocol}//${url.host}/*`;
}

/** Exact source origins for browser-preferred candidates only. */
export function browserPreferredSourceOrigins(captured: CapturedPage): string[] {
  const origins = new Set<string>();
  for (const media of captured.media) {
    for (const variant of media.variants) {
      if (variant.acquisitionPreference !== "browser_preferred") continue;
      if (
        captured.identity.provider === "youtube" &&
        media.kind === "video" &&
        !["poster", "subtitles", "transcript"].includes(variant.kind)
      ) {
        continue;
      }
      const origin = safeHttpOrigin(variant.sourceUrl);
      if (origin) origins.add(origin);
    }
  }
  return [...origins].sort();
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

/**
 * Resolves all FE and browser-media grants with at most one permission prompt.
 * This function must only be called from the popup's explicit Send gesture.
 */
export async function requestCapturePermissions(
  endpointUrl: string,
  captured: CapturedPage,
  permissions: PermissionsApiLike = chrome.permissions,
): Promise<CapturePermissionResult> {
  const feOrigin = new URL(endpointUrl).origin;
  const sourceOrigins = browserPreferredSourceOrigins(captured);
  const allOrigins = [...new Set([feOrigin, ...sourceOrigins])];
  const patterns = new Map(allOrigins.map((origin) => [origin, originPattern(origin)]));
  const initiallyGranted = new Map(
    await Promise.all(
      allOrigins.map(async (origin) => [
        origin,
        await permissions.contains({ origins: [patterns.get(origin)!] }),
      ] as const),
    ),
  );
  const missingPatterns = allOrigins
    .filter((origin) => !initiallyGranted.get(origin))
    .map((origin) => patterns.get(origin)!);

  if (missingPatterns.length > 0) {
    // One prompt preserves user-gesture eligibility for the entire requested
    // set. The boolean is not trusted as a per-origin result; re-check below.
    await permissions.request({ origins: missingPatterns });
  }

  const granted = new Map(
    await Promise.all(
      allOrigins.map(async (origin) => [
        origin,
        await permissions.contains({ origins: [patterns.get(origin)!] }),
      ] as const),
    ),
  );
  return {
    feGranted: granted.get(feOrigin) === true,
    feOrigin,
    permittedSourceOrigins: sourceOrigins.filter((origin) => granted.get(origin) === true),
    deniedSourceOrigins: sourceOrigins.filter((origin) => granted.get(origin) !== true),
  };
}

/**
 * Re-checks and requests only the FE origin plus incomplete media origins
 * supplied by the redacted durable-job view. Must run inside the Retry click.
 */
export async function requestRetryPermissions(
  feOrigin: string,
  sourceOrigins: readonly string[],
  permissions: PermissionsApiLike = chrome.permissions,
): Promise<CapturePermissionResult> {
  const canonicalFeOrigin = new URL(feOrigin).origin;
  if (canonicalFeOrigin !== feOrigin) {
    throw new Error("The retry FE origin is invalid.");
  }
  const canonicalSources = [...new Set(sourceOrigins)].sort();
  for (const origin of canonicalSources) {
    if (new URL(origin).origin !== origin) {
      throw new Error("A retry media origin is invalid.");
    }
  }
  const allOrigins = [...new Set([canonicalFeOrigin, ...canonicalSources])];
  const patterns = new Map(allOrigins.map((origin) => [origin, originPattern(origin)]));
  const initiallyGranted = new Map(
    await Promise.all(
      allOrigins.map(async (origin) => [
        origin,
        await permissions.contains({ origins: [patterns.get(origin)!] }),
      ] as const),
    ),
  );
  const missingPatterns = allOrigins
    .filter((origin) => !initiallyGranted.get(origin))
    .map((origin) => patterns.get(origin)!);
  if (missingPatterns.length > 0) {
    await permissions.request({ origins: missingPatterns });
  }
  const granted = new Map(
    await Promise.all(
      allOrigins.map(async (origin) => [
        origin,
        await permissions.contains({ origins: [patterns.get(origin)!] }),
      ] as const),
    ),
  );
  return {
    feGranted: granted.get(canonicalFeOrigin) === true,
    feOrigin: canonicalFeOrigin,
    permittedSourceOrigins: canonicalSources.filter((origin) => granted.get(origin) === true),
    deniedSourceOrigins: canonicalSources.filter((origin) => granted.get(origin) !== true),
  };
}
