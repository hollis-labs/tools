import type { FECaptureAssetVariant } from "@/contracts/fragmentsEngine";
import { originPattern } from "@/lib/permissions";

const MAX_REDIRECTS = 5;
const DEFAULT_TRANSFER_TIMEOUT_MS = 30_000;
export const DEFAULT_MAX_ASSET_BYTES = 64 * 1024 * 1024;

export type MediaTransferFailureKind =
  | "permission"
  | "network"
  | "source"
  | "redirect"
  | "too_large"
  | "digest_mismatch";

export class MediaTransferError extends Error {
  constructor(
    message: string,
    readonly kind: MediaTransferFailureKind,
    readonly retryable: boolean,
  ) {
    super(message);
    this.name = "MediaTransferError";
  }
}

export interface AcquiredAsset {
  bytes: Uint8Array;
  digest: { algorithm: "sha256"; value: string };
  byteSize: number;
  /** Byte-signature observation, which may be more specific than FE accepts. */
  observedMimeType: string;
  /** Contract-allowed upload Content-Type. */
  mimeType: string;
  width?: number;
  height?: number;
}

export interface MediaAcquirer {
  acquire(
    variant: FECaptureAssetVariant,
    permittedSourceOrigins: readonly string[],
    maxAssetBytes: number,
  ): Promise<AcquiredAsset>;
}

export type SourceFetchLike = (input: string | URL, init?: RequestInit) => Promise<Response>;
export type SourcePermissionCheck = (origin: string) => Promise<boolean>;

function safeSourceUrl(raw: string): URL {
  const url = new URL(raw);
  if (
    (url.protocol !== "http:" && url.protocol !== "https:") ||
    url.username ||
    url.password
  ) {
    throw new MediaTransferError(
      "The persisted media source is not a credential-free HTTP(S) URL.",
      "source",
      false,
    );
  }
  return url;
}

function exactContentLength(value: string | null): number | undefined {
  if (value === null || !/^\d+$/.test(value)) return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) ? parsed : undefined;
}

async function readBounded(response: Response, limit: number): Promise<Uint8Array> {
  const declared = exactContentLength(response.headers.get("Content-Length"));
  if (declared !== undefined && declared > limit) {
    await response.body?.cancel();
    throw new MediaTransferError(
      `The media source declared ${declared} bytes, exceeding the ${limit}-byte transfer limit.`,
      "too_large",
      false,
    );
  }
  if (!response.body) return new Uint8Array();

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      total += value.byteLength;
      if (total > limit) {
        await reader.cancel();
        throw new MediaTransferError(
          `The media source exceeded the ${limit}-byte transfer limit while streaming.`,
          "too_large",
          false,
        );
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }

  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return bytes;
}

function startsWith(bytes: Uint8Array, signature: readonly number[]): boolean {
  return signature.every((value, index) => bytes[index] === value);
}

function jpegDimensions(bytes: Uint8Array): { width: number; height: number } | undefined {
  if (!startsWith(bytes, [0xff, 0xd8])) return undefined;
  let offset = 2;
  while (offset + 9 < bytes.length) {
    if (bytes[offset] !== 0xff) {
      offset += 1;
      continue;
    }
    const marker = bytes[offset + 1]!;
    if (marker === 0xd8 || marker === 0xd9) {
      offset += 2;
      continue;
    }
    const segmentLength = (bytes[offset + 2]! << 8) | bytes[offset + 3]!;
    if (segmentLength < 2 || offset + segmentLength + 2 > bytes.length) return undefined;
    if ((marker >= 0xc0 && marker <= 0xc3) || (marker >= 0xc5 && marker <= 0xc7) ||
      (marker >= 0xc9 && marker <= 0xcb) || (marker >= 0xcd && marker <= 0xcf)) {
      const height = (bytes[offset + 5]! << 8) | bytes[offset + 6]!;
      const width = (bytes[offset + 7]! << 8) | bytes[offset + 8]!;
      return width > 0 && height > 0 ? { width, height } : undefined;
    }
    offset += segmentLength + 2;
  }
  return undefined;
}

function sniffAsset(bytes: Uint8Array): { mimeType: string; width?: number; height?: number } {
  if (startsWith(bytes, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])) {
    if (bytes.length >= 24) {
      const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
      const width = view.getUint32(16);
      const height = view.getUint32(20);
      if (width > 0 && height > 0) return { mimeType: "image/png", width, height };
    }
    return { mimeType: "image/png" };
  }
  if (startsWith(bytes, [0x47, 0x49, 0x46, 0x38]) && bytes.length >= 10) {
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    return { mimeType: "image/gif", width: view.getUint16(6, true), height: view.getUint16(8, true) };
  }
  const jpeg = jpegDimensions(bytes);
  if (startsWith(bytes, [0xff, 0xd8])) return { mimeType: "image/jpeg", ...jpeg };
  if (
    startsWith(bytes, [0x52, 0x49, 0x46, 0x46]) &&
    bytes.length >= 12 &&
    String.fromCharCode(...bytes.slice(8, 12)) === "WEBP"
  ) {
    return { mimeType: "image/webp" };
  }
  if (startsWith(bytes, [0x25, 0x50, 0x44, 0x46, 0x2d])) return { mimeType: "application/pdf" };
  if (bytes.length >= 12 && String.fromCharCode(...bytes.slice(4, 8)) === "ftyp") {
    return { mimeType: "video/mp4" };
  }
  if (startsWith(bytes, [0x1a, 0x45, 0xdf, 0xa3])) return { mimeType: "video/webm" };
  if (startsWith(bytes, [0x49, 0x44, 0x33]) || (bytes[0] === 0xff && (bytes[1]! & 0xe0) === 0xe0)) {
    return { mimeType: "audio/mpeg" };
  }
  const textPrefix = new TextDecoder("utf-8", { fatal: false }).decode(bytes.slice(0, 64));
  if (/^\uFEFF?WEBVTT(?:\s|$)/.test(textPrefix)) return { mimeType: "text/vtt" };
  return { mimeType: "application/octet-stream" };
}

function hex(bytes: Uint8Array): string {
  return [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

export class BrowserMediaAcquirer implements MediaAcquirer {
  constructor(
    private readonly fetchImplementation: SourceFetchLike = fetch,
    private readonly timeoutMs = DEFAULT_TRANSFER_TIMEOUT_MS,
    private readonly hasCurrentPermission: SourcePermissionCheck = (origin) =>
      chrome.permissions.contains({ origins: [originPattern(origin)] }),
  ) {}

  async acquire(
    variant: FECaptureAssetVariant,
    permittedSourceOrigins: readonly string[],
    advertisedMaxAssetBytes: number,
  ): Promise<AcquiredAsset> {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.timeoutMs);
    try {
      return await this.acquireWithSignal(
        variant,
        permittedSourceOrigins,
        advertisedMaxAssetBytes,
        controller.signal,
      );
    } finally {
      clearTimeout(timeout);
    }
  }

  private async acquireWithSignal(
    variant: FECaptureAssetVariant,
    permittedSourceOrigins: readonly string[],
    advertisedMaxAssetBytes: number,
    signal: AbortSignal,
  ): Promise<AcquiredAsset> {
    if (variant.transfer_preference !== "browser_preferred" || !variant.source_url) {
      throw new MediaTransferError(
        "Only persisted browser-preferred variants with source URLs may be fetched.",
        "source",
        false,
      );
    }
    const limit = Math.min(advertisedMaxAssetBytes, DEFAULT_MAX_ASSET_BYTES);
    if (!Number.isSafeInteger(limit) || limit < 1) {
      throw new MediaTransferError("The media byte limit is unavailable.", "too_large", false);
    }
    const allowed = new Set(permittedSourceOrigins);
    let source = safeSourceUrl(variant.source_url);

    for (let redirects = 0; ; redirects += 1) {
      if (!allowed.has(source.origin)) {
        throw new MediaTransferError(
          `Host permission is not available for media origin ${source.origin}.`,
          "permission",
          true,
        );
      }
      let currentlyPermitted = false;
      try {
        currentlyPermitted = await this.hasCurrentPermission(source.origin);
      } catch {
        // Permission API uncertainty is never authority to fetch.
      }
      if (!currentlyPermitted) {
        throw new MediaTransferError(
          `Current host permission is not available for media origin ${source.origin}.`,
          "permission",
          true,
        );
      }
      let response: Response;
      try {
        // Native WorkerGlobalScope.fetch rejects an arbitrary receiver. Copy
        // the injected callable before invoking it so the class instance is
        // never used as `this` (which happens with `this.fetchImplementation()`).
        const fetchImplementation = this.fetchImplementation;
        response = await fetchImplementation(source.toString(), {
          method: "GET",
          credentials: "omit",
          redirect: "manual",
          referrerPolicy: "no-referrer",
          cache: "no-store",
          headers: { Accept: "*/*" },
          signal,
        });
      } catch (error) {
        const detail = error instanceof Error ? error.message : String(error);
        throw new MediaTransferError(`The media source request failed: ${detail}`, "network", true);
      }

      if (response.status >= 300 && response.status < 400) {
        await response.body?.cancel();
        if (redirects >= MAX_REDIRECTS) {
          throw new MediaTransferError("The media source exceeded five redirects.", "redirect", false);
        }
        const location = response.headers.get("Location");
        if (!location) {
          throw new MediaTransferError("The media source returned a redirect without a location.", "redirect", false);
        }
        const redirected = safeSourceUrl(new URL(location, source).toString());
        if (!allowed.has(redirected.origin)) {
          throw new MediaTransferError(
            `The media source redirected to unapproved origin ${redirected.origin}.`,
            "redirect",
            false,
          );
        }
        source = redirected;
        continue;
      }

      if (!response.ok) {
        await response.body?.cancel();
        throw new MediaTransferError(
          `The media source returned HTTP ${response.status}.`,
          "source",
          response.status === 408 || response.status === 425 || response.status === 429 || response.status >= 500,
        );
      }

      let bytes: Uint8Array;
      try {
        bytes = await readBounded(response, limit);
      } catch (error) {
        if (error instanceof MediaTransferError) throw error;
        throw new MediaTransferError(
          signal.aborted ? "The media source transfer timed out." : "The media source stream failed.",
          "network",
          true,
        );
      }
      const digestBytes = new Uint8Array(
        await crypto.subtle.digest("SHA-256", bytes.buffer as ArrayBuffer),
      );
      const observed = sniffAsset(bytes);
      const mimeType = /^(?:image|video|audio|text)\//.test(observed.mimeType)
        ? observed.mimeType
        : "application/octet-stream";
      return {
        bytes,
        digest: { algorithm: "sha256", value: hex(digestBytes) },
        byteSize: bytes.byteLength,
        observedMimeType: observed.mimeType,
        mimeType,
        ...(observed.width ? { width: observed.width } : {}),
        ...(observed.height ? { height: observed.height } : {}),
      };
    }
  }
}
