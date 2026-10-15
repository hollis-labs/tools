import type { AdapterExtractionWarning } from "../../types";

type UnknownRecord = Record<string, unknown>;

export type InstagramRouteKind = "post" | "reel";
export type InstagramObservedMediaKind = "image" | "video" | "unsupported";
export type InstagramObservationSource = "serialized_state" | "json_ld" | "dom";

export interface InstagramMediaObservation {
  position: number;
  kind: InstagramObservedMediaKind;
  providerMediaId?: string;
  shortcode?: string;
  originalUrl?: string;
  thumbnailUrl?: string;
  streamUrl?: string;
  posterUrl?: string;
  mimeType?: string;
  width?: number;
  height?: number;
  durationSeconds?: number;
  altText?: string;
}

export interface InstagramPageObservation {
  shortcode?: string;
  routeKind?: InstagramRouteKind;
  caption?: string;
  authorUsername?: string;
  authorName?: string;
  publishedAt?: string;
  hashtags: readonly string[];
  media: readonly InstagramMediaObservation[];
  sources: readonly InstagramObservationSource[];
}

export interface InstagramParserResult {
  observation: InstagramPageObservation;
  warnings: readonly AdapterExtractionWarning[];
}

interface ImageResource {
  url: string;
  width?: number;
  height?: number;
  mimeType?: string;
  explicitThumbnail?: boolean;
}

interface PartialInstagramObservation {
  shortcode?: string;
  routeKind?: InstagramRouteKind;
  caption?: string;
  authorUsername?: string;
  authorName?: string;
  publishedAt?: string;
  hashtags?: readonly string[];
  media?: readonly InstagramMediaObservation[];
}

const INSTAGRAM_HOSTS = new Set(["instagram.com", "www.instagram.com", "m.instagram.com"]);
const SERIALIZED_STATE_KEYS = new Set(["shortcode_media", "xdt_shortcode_media"]);
const CREDENTIAL_QUERY_PARAMETERS = new Set([
  "access_token",
  "authorization",
  "bearer",
  "cookie",
  "csrf_token",
  "oauth_token",
  "sessionid",
]);
const PAGE_CREDENTIAL_QUERY_PARAMETERS = new Set([
  ...CREDENTIAL_QUERY_PARAMETERS,
  "auth",
  "csrf",
  "session",
  "session_token",
  "token",
]);
const MAX_STATE_NODES = 20_000;
const MAX_STATE_DEPTH = 32;

function isRecord(value: unknown): value is UnknownRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asNonEmptyString(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const trimmed = value.trim();
  return trimmed || undefined;
}

function stringField(record: UnknownRecord, ...keys: readonly string[]): string | undefined {
  for (const key of keys) {
    const value = asNonEmptyString(record[key]);
    if (value) return value;
  }
  return undefined;
}

function identifierField(record: UnknownRecord, ...keys: readonly string[]): string | undefined {
  for (const key of keys) {
    const value = record[key];
    const stringValue = asNonEmptyString(value);
    if (stringValue) return stringValue;
    if (typeof value === "number" && Number.isSafeInteger(value) && value >= 0) {
      return String(value);
    }
  }
  return undefined;
}

function positiveNumber(value: unknown): number | undefined {
  const parsed = typeof value === "number" ? value : typeof value === "string" ? Number(value) : NaN;
  return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined;
}

function positiveInteger(value: unknown): number | undefined {
  const parsed = positiveNumber(value);
  return parsed === undefined ? undefined : Math.round(parsed);
}

function safeHttpUrl(value: unknown, baseUrl: string): string | undefined {
  const raw = asNonEmptyString(value);
  if (!raw) return undefined;

  try {
    const parsed = new URL(raw, baseUrl);
    const hasCredentialParameter = [...parsed.searchParams.keys()].some((key) =>
      CREDENTIAL_QUERY_PARAMETERS.has(key.toLowerCase()),
    );
    const fragment = new URLSearchParams(parsed.hash.replace(/^#/, ""));
    const hasCredentialFragment = [...fragment.keys()].some((key) =>
      CREDENTIAL_QUERY_PARAMETERS.has(key.toLowerCase()),
    );
    return (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      !parsed.username &&
      !parsed.password &&
      !hasCredentialParameter &&
      !hasCredentialFragment
      ? parsed.href
      : undefined;
  } catch {
    return undefined;
  }
}

function mimeFromUrl(url: string | undefined, expected: "image" | "video"): string | undefined {
  if (!url) return undefined;
  let pathname: string;
  try {
    pathname = new URL(url).pathname.toLowerCase();
  } catch {
    return undefined;
  }

  if (expected === "video") {
    if (pathname.endsWith(".mp4")) return "video/mp4";
    if (pathname.endsWith(".webm")) return "video/webm";
    return undefined;
  }

  if (pathname.endsWith(".jpg") || pathname.endsWith(".jpeg")) return "image/jpeg";
  if (pathname.endsWith(".png")) return "image/png";
  if (pathname.endsWith(".webp")) return "image/webp";
  if (pathname.endsWith(".avif")) return "image/avif";
  return undefined;
}

function normalizedMime(value: unknown, expected: "image" | "video"): string | undefined {
  const mime = asNonEmptyString(value)?.toLowerCase();
  return mime?.startsWith(`${expected}/`) ? mime : undefined;
}

function parseInstagramPath(url: string): { shortcode?: string; routeKind?: InstagramRouteKind } {
  try {
    const parsed = new URL(url);
    if (!INSTAGRAM_HOSTS.has(parsed.hostname.toLowerCase())) return {};
    const segments = parsed.pathname.split("/").filter(Boolean);
    const routeIndex = ["p", "reel", "reels"].includes(segments[0]?.toLowerCase() ?? "")
      ? 0
      : ["p", "reel", "reels"].includes(segments[1]?.toLowerCase() ?? "")
        ? 1
        : -1;
    const route = routeIndex >= 0 ? segments[routeIndex]?.toLowerCase() : undefined;
    const shortcode = routeIndex >= 0 ? segments[routeIndex + 1] : undefined;
    if (!route || !shortcode || !/^[A-Za-z0-9_-]+$/.test(shortcode)) return {};
    return {
      shortcode,
      routeKind: route === "p" ? "post" : "reel",
    };
  } catch {
    return {};
  }
}

/** Removes page-URL credential material without discarding ordinary provenance parameters. */
export function sanitizeInstagramPageUrl(url: string): string {
  try {
    const parsed = new URL(url);
    parsed.username = "";
    parsed.password = "";
    for (const key of [...parsed.searchParams.keys()]) {
      if (PAGE_CREDENTIAL_QUERY_PARAMETERS.has(key.toLowerCase())) {
        parsed.searchParams.delete(key);
      }
    }

    const fragment = new URLSearchParams(parsed.hash.replace(/^#/, ""));
    if ([...fragment.keys()].some((key) => PAGE_CREDENTIAL_QUERY_PARAMETERS.has(key.toLowerCase()))) {
      for (const key of [...fragment.keys()]) {
        if (PAGE_CREDENTIAL_QUERY_PARAMETERS.has(key.toLowerCase())) fragment.delete(key);
      }
      parsed.hash = fragment.toString();
    }
    return parsed.href;
  } catch {
    // A matching Instagram context is always a valid browser URL. Failing
    // closed here prevents an unexpected malformed value from being emitted.
    return "https://www.instagram.com/";
  }
}

function timestamp(value: unknown): string | undefined {
  const toISOString = (date: Date) =>
    Number.isNaN(date.getTime()) ? undefined : date.toISOString();
  if (typeof value === "number" && Number.isFinite(value) && value > 0) {
    return toISOString(new Date(value * 1000));
  }
  const raw = asNonEmptyString(value);
  if (!raw) return undefined;
  if (/^\d{10}(?:\.\d+)?$/.test(raw)) return toISOString(new Date(Number(raw) * 1000));
  const parsed = Date.parse(raw);
  return Number.isNaN(parsed) ? undefined : new Date(parsed).toISOString();
}

function isoDurationSeconds(value: unknown): number | undefined {
  const direct = positiveNumber(value);
  if (direct !== undefined) return direct;
  const raw = asNonEmptyString(value);
  if (!raw) return undefined;
  const match = raw.match(/^PT(?:(\d+(?:\.\d+)?)H)?(?:(\d+(?:\.\d+)?)M)?(?:(\d+(?:\.\d+)?)S)?$/i);
  if (!match) return undefined;
  const seconds = Number(match[1] ?? 0) * 3600 + Number(match[2] ?? 0) * 60 + Number(match[3] ?? 0);
  return seconds > 0 ? seconds : undefined;
}

function hashtagsFromText(text: string | undefined): string[] {
  if (!text) return [];
  const tags: string[] = [];
  const seen = new Set<string>();
  for (const match of text.matchAll(/#([\p{L}\p{N}_]+)/gu)) {
    const tag = match[1];
    if (!tag) continue;
    const key = tag.toLocaleLowerCase();
    if (!seen.has(key)) {
      seen.add(key);
      tags.push(tag);
    }
  }
  return tags;
}

function dimensions(record: UnknownRecord): { width?: number; height?: number } {
  const nested = isRecord(record.dimensions) ? record.dimensions : undefined;
  const width =
    positiveInteger(record.width) ??
    positiveInteger(record.original_width) ??
    positiveInteger(nested?.width);
  const height =
    positiveInteger(record.height) ??
    positiveInteger(record.original_height) ??
    positiveInteger(nested?.height);
  return {
    ...(width ? { width } : {}),
    ...(height ? { height } : {}),
  };
}

function addImageResource(
  resources: ImageResource[],
  value: unknown,
  baseUrl: string,
  details: Partial<ImageResource> = {},
): void {
  const url = safeHttpUrl(value, baseUrl);
  if (!url) return;
  const existing = resources.find((resource) => resource.url === url);
  if (existing) {
    existing.width ??= details.width;
    existing.height ??= details.height;
    existing.mimeType ??= details.mimeType;
    existing.explicitThumbnail ||= details.explicitThumbnail;
    return;
  }
  resources.push({ url, ...details });
}

function addResourceArray(
  resources: ImageResource[],
  value: unknown,
  baseUrl: string,
  explicitThumbnail = false,
): void {
  if (!Array.isArray(value)) return;
  for (const entry of value) {
    if (!isRecord(entry)) continue;
    addImageResource(resources, entry.src ?? entry.url, baseUrl, {
      width: positiveInteger(entry.config_width) ?? positiveInteger(entry.width),
      height: positiveInteger(entry.config_height) ?? positiveInteger(entry.height),
      mimeType: normalizedMime(entry.mime_type, "image"),
      explicitThumbnail,
    });
  }
}

function imageResources(
  record: UnknownRecord,
  baseUrl: string,
  includeContentUrl: boolean,
): ImageResource[] {
  const resources: ImageResource[] = [];
  const size = dimensions(record);
  addImageResource(
    resources,
    record.display_url ?? (includeContentUrl ? record.contentUrl : undefined),
    baseUrl,
    {
    ...size,
    mimeType: normalizedMime(record.mime_type ?? record.encodingFormat, "image"),
    },
  );
  addResourceArray(resources, record.display_resources, baseUrl);
  addResourceArray(resources, record.thumbnail_resources, baseUrl, true);

  if (isRecord(record.image_versions2)) {
    addResourceArray(resources, record.image_versions2.candidates, baseUrl);
  }

  addImageResource(resources, record.thumbnail_src ?? record.thumbnail_url ?? record.thumbnailUrl, baseUrl, {
    explicitThumbnail: true,
  });
  return resources;
}

function largestVideoResource(record: UnknownRecord, baseUrl: string): ImageResource | undefined {
  const resources: ImageResource[] = [];
  const size = dimensions(record);
  addImageResource(resources, record.video_url ?? record.contentUrl, baseUrl, {
    ...size,
    mimeType: normalizedMime(record.video_mime_type ?? record.encodingFormat, "video"),
  });
  if (Array.isArray(record.video_versions)) {
    for (const entry of record.video_versions) {
      if (!isRecord(entry)) continue;
      addImageResource(resources, entry.url, baseUrl, {
        width: positiveInteger(entry.width),
        height: positiveInteger(entry.height),
        mimeType: normalizedMime(entry.mime_type, "video") ?? "video/mp4",
      });
    }
  }
  return largestResource(resources);
}

function resourceArea(resource: ImageResource): number {
  if (resource.width && resource.height) return resource.width * resource.height;
  return resource.width ?? resource.height ?? 0;
}

function largestResource(resources: readonly ImageResource[]): ImageResource | undefined {
  return resources.reduce<ImageResource | undefined>((largest, candidate) => {
    if (!largest) return candidate;
    return resourceArea(candidate) > resourceArea(largest) ? candidate : largest;
  }, undefined);
}

function thumbnailResource(
  resources: readonly ImageResource[],
  original: ImageResource | undefined,
): ImageResource | undefined {
  const alternatives = resources.filter((resource) => resource.url !== original?.url);
  const explicit = alternatives.filter((resource) => resource.explicitThumbnail);
  const eligible = explicit.length > 0 ? explicit : alternatives.filter((resource) => {
    const candidateArea = resourceArea(resource);
    const originalArea = original ? resourceArea(original) : 0;
    return candidateArea > 0 && (originalArea === 0 || candidateArea < originalArea);
  });
  return eligible.reduce<ImageResource | undefined>((smallest, candidate) => {
    if (!smallest) return candidate;
    const area = resourceArea(candidate);
    const smallestArea = resourceArea(smallest);
    if (smallestArea === 0) return area > 0 ? candidate : smallest;
    return area > 0 && area < smallestArea ? candidate : smallest;
  }, undefined);
}

function mediaKind(record: UnknownRecord): InstagramObservedMediaKind {
  const typeName = stringField(record, "__typename", "@type", "type", "media_type_name")?.toLowerCase();
  const numericType = typeof record.media_type === "number" ? record.media_type : undefined;
  if (
    record.is_video === true ||
    numericType === 2 ||
    typeName?.includes("video") ||
    asNonEmptyString(record.video_url) ||
    Array.isArray(record.video_versions) ||
    asNonEmptyString(record.contentUrl) && typeName === "videoobject"
  ) {
    return "video";
  }
  if (
    record.is_video === false ||
    numericType === 1 ||
    typeName?.includes("image") ||
    asNonEmptyString(record.display_url) ||
    asNonEmptyString(record.contentUrl) && typeName === "imageobject"
  ) {
    return "image";
  }
  return "unsupported";
}

function parseMediaRecord(
  record: UnknownRecord,
  position: number,
  baseUrl: string,
): InstagramMediaObservation {
  const kind = mediaKind(record);
  const id = identifierField(record, "id", "pk", "media_id", "identifier");
  const shortcode = stringField(record, "shortcode", "code");
  const size = dimensions(record);
  const altText = stringField(record, "accessibility_caption", "alt_text", "caption");
  const resources = imageResources(record, baseUrl, kind === "image");
  const largest = largestResource(resources);
  const thumbnail = thumbnailResource(resources, largest);

  if (kind === "video") {
    const stream = largestVideoResource(record, baseUrl);
    const streamUrl = stream?.url;
    const poster = largest;
    return {
      position,
      kind,
      ...(id ? { providerMediaId: id } : {}),
      ...(shortcode ? { shortcode } : {}),
      ...(streamUrl ? { streamUrl } : {}),
      ...(poster ? { posterUrl: poster.url } : {}),
      ...(stream?.mimeType ??
      normalizedMime(record.video_mime_type ?? record.encodingFormat, "video") ??
      mimeFromUrl(streamUrl, "video")
        ? {
            mimeType:
              stream?.mimeType ??
              normalizedMime(record.video_mime_type ?? record.encodingFormat, "video") ??
              mimeFromUrl(streamUrl, "video"),
          }
        : {}),
      ...(stream?.width ?? size.width ? { width: stream?.width ?? size.width } : {}),
      ...(stream?.height ?? size.height ? { height: stream?.height ?? size.height } : {}),
      ...(isoDurationSeconds(record.video_duration ?? record.duration) !== undefined
        ? { durationSeconds: isoDurationSeconds(record.video_duration ?? record.duration) }
        : {}),
      ...(altText ? { altText } : {}),
    };
  }

  if (kind === "image") {
    return {
      position,
      kind,
      ...(id ? { providerMediaId: id } : {}),
      ...(shortcode ? { shortcode } : {}),
      ...(largest ? { originalUrl: largest.url } : {}),
      ...(thumbnail ? { thumbnailUrl: thumbnail.url } : {}),
      ...(largest?.mimeType ?? mimeFromUrl(largest?.url, "image")
        ? { mimeType: largest?.mimeType ?? mimeFromUrl(largest?.url, "image") }
        : {}),
      ...(largest?.width ?? size.width ? { width: largest?.width ?? size.width } : {}),
      ...(largest?.height ?? size.height ? { height: largest?.height ?? size.height } : {}),
      ...(altText ? { altText } : {}),
    };
  }

  return {
    position,
    kind,
    ...(id ? { providerMediaId: id } : {}),
    ...(shortcode ? { shortcode } : {}),
    ...(altText ? { altText } : {}),
  };
}

function edgeCaption(record: UnknownRecord): string | undefined {
  const connection = isRecord(record.edge_media_to_caption)
    ? record.edge_media_to_caption
    : undefined;
  const firstEdge = Array.isArray(connection?.edges) ? connection.edges[0] : undefined;
  return isRecord(firstEdge) && isRecord(firstEdge.node)
    ? stringField(firstEdge.node, "text")
    : undefined;
}

function captionText(record: UnknownRecord): string | undefined {
  const caption = record.caption;
  return (
    asNonEmptyString(caption) ??
    (isRecord(caption) ? stringField(caption, "text") : undefined) ??
    edgeCaption(record) ??
    stringField(record, "description", "articleBody")
  );
}

function stateMediaRecords(root: UnknownRecord): unknown[] {
  if (Array.isArray(root.carousel_media)) return root.carousel_media;
  const sidecar = isRecord(root.edge_sidecar_to_children)
    ? root.edge_sidecar_to_children
    : undefined;
  if (Array.isArray(sidecar?.edges)) {
    return sidecar.edges.map((edge) => (isRecord(edge) ? edge.node : undefined));
  }
  return [root];
}

function parseStateRoot(root: UnknownRecord, baseUrl: string): PartialInstagramObservation {
  const owner = isRecord(root.owner) ? root.owner : isRecord(root.user) ? root.user : undefined;
  const caption = captionText(root);
  const shortcode = stringField(root, "shortcode", "code");
  const productType = stringField(root, "product_type")?.toLowerCase();
  const routeKind: InstagramRouteKind | undefined =
    productType === "clips" || productType === "reel" ? "reel" : undefined;
  const records = stateMediaRecords(root);
  const media = records.map((record, position) =>
    isRecord(record)
      ? parseMediaRecord(record, position, baseUrl)
      : ({ position, kind: "unsupported" } satisfies InstagramMediaObservation),
  );

  return {
    ...(shortcode ? { shortcode } : {}),
    ...(routeKind ? { routeKind } : {}),
    ...(caption ? { caption } : {}),
    ...(owner && stringField(owner, "username")
      ? { authorUsername: stringField(owner, "username") }
      : {}),
    ...(owner && stringField(owner, "full_name", "name")
      ? { authorName: stringField(owner, "full_name", "name") }
      : {}),
    ...(timestamp(root.taken_at_timestamp ?? root.taken_at ?? root.datePublished)
      ? { publishedAt: timestamp(root.taken_at_timestamp ?? root.taken_at ?? root.datePublished) }
      : {}),
    hashtags: hashtagsFromText(caption),
    media,
  };
}

function looksLikeStateRoot(record: UnknownRecord): boolean {
  const shortcode = stringField(record, "shortcode", "code");
  return Boolean(
    shortcode &&
      (Array.isArray(record.carousel_media) ||
        isRecord(record.edge_sidecar_to_children) ||
        isRecord(record.image_versions2) ||
        Array.isArray(record.video_versions) ||
        record.media_type === 1 ||
        record.media_type === 2 ||
        "display_url" in record ||
        "video_url" in record),
  );
}

function collectStateRoots(value: unknown): UnknownRecord[] {
  const roots: UnknownRecord[] = [];
  const seenRoots = new Set<UnknownRecord>();
  let visited = 0;

  function add(record: UnknownRecord): void {
    if (!seenRoots.has(record)) {
      seenRoots.add(record);
      roots.push(record);
    }
  }

  function visit(node: unknown, depth: number): void {
    if (depth > MAX_STATE_DEPTH || visited >= MAX_STATE_NODES) return;
    visited += 1;
    if (Array.isArray(node)) {
      for (const item of node) visit(item, depth + 1);
      return;
    }
    if (!isRecord(node)) return;

    if (looksLikeStateRoot(node)) add(node);
    for (const [key, child] of Object.entries(node)) {
      if (SERIALIZED_STATE_KEYS.has(key) && isRecord(child)) add(child);
      visit(child, depth + 1);
    }
  }

  visit(value, 0);
  return roots;
}

function serializedStateScripts(document: Document): HTMLScriptElement[] {
  return [...document.querySelectorAll<HTMLScriptElement>('script[type="application/json"]')].filter(
    (script) => {
      const id = script.id.toLowerCase();
      const text = script.textContent ?? "";
      return (
        id.includes("data") ||
        text.includes('"shortcode_media"') ||
        text.includes('"xdt_shortcode_media"') ||
        text.includes('"edge_sidecar_to_children"') ||
        text.includes('"carousel_media"') ||
        text.includes('"image_versions2"') ||
        text.includes('"video_versions"')
      );
    },
  );
}

function parseSerializedState(
  document: Document,
  pageUrl: string,
  expectedShortcode: string | undefined,
): { observation?: PartialInstagramObservation; warnings: AdapterExtractionWarning[] } {
  const warnings: AdapterExtractionWarning[] = [];
  const roots: UnknownRecord[] = [];

  for (const script of serializedStateScripts(document)) {
    try {
      roots.push(...collectStateRoots(JSON.parse(script.textContent ?? "")));
    } catch {
      warnings.push({
        code: "instagram.serialized_state_malformed",
        severity: "warning",
        stage: "provider_metadata",
        message: "Instagram serialized page state could not be parsed; other page observations were retained.",
      });
    }
  }

  const root = expectedShortcode
    ? roots.find((candidate) => stringField(candidate, "shortcode", "code") === expectedShortcode)
    : roots[0];
  if (expectedShortcode && roots.length > 0 && !root) {
    warnings.push({
      code: "instagram.serialized_state_target_mismatch",
      severity: "warning",
      stage: "provider_metadata",
      message: "Instagram serialized page state described a different post; it was ignored.",
    });
  }
  return {
    ...(root ? { observation: parseStateRoot(root, pageUrl) } : {}),
    warnings,
  };
}

function jsonLdTypes(record: UnknownRecord): string[] {
  const value = record["@type"];
  return (Array.isArray(value) ? value : [value])
    .map(asNonEmptyString)
    .filter((type): type is string => Boolean(type));
}

function isRelevantJsonLd(record: UnknownRecord): boolean {
  return jsonLdTypes(record).some((type) =>
    ["socialmediaposting", "imageobject", "videoobject"].includes(type.toLowerCase()),
  );
}

function collectJsonLdRecords(value: unknown): UnknownRecord[] {
  const records: UnknownRecord[] = [];
  function visit(node: unknown): void {
    if (Array.isArray(node)) {
      for (const item of node) visit(item);
      return;
    }
    if (!isRecord(node)) return;
    if (isRelevantJsonLd(node)) records.push(node);
    for (const key of ["@graph", "mainEntity", "associatedMedia", "image", "video"] as const) {
      visit(node[key]);
    }
  }
  visit(value);
  return records;
}

function jsonLdMediaEntities(post: UnknownRecord): UnknownRecord[] {
  const entities: UnknownRecord[] = [];
  const add = (value: unknown, kind: "ImageObject" | "VideoObject") => {
    for (const item of Array.isArray(value) ? value : [value]) {
      if (typeof item === "string") entities.push({ "@type": kind, contentUrl: item });
      else if (isRecord(item)) entities.push(item);
    }
  };
  add(post.associatedMedia, "ImageObject");
  add(post.image, "ImageObject");
  add(post.video, "VideoObject");
  if (jsonLdTypes(post).some((type) => type.toLowerCase() === "imageobject")) add(post, "ImageObject");
  if (jsonLdTypes(post).some((type) => type.toLowerCase() === "videoobject")) add(post, "VideoObject");

  const seen = new Set<UnknownRecord>();
  return entities.filter((entity) => {
    if (seen.has(entity)) return false;
    seen.add(entity);
    return true;
  });
}

function parseJsonLd(
  document: Document,
  pageUrl: string,
  expectedShortcode: string | undefined,
): { observation?: PartialInstagramObservation; warnings: AdapterExtractionWarning[] } {
  const warnings: AdapterExtractionWarning[] = [];
  const records: UnknownRecord[] = [];
  for (const script of document.querySelectorAll<HTMLScriptElement>('script[type="application/ld+json"]')) {
    try {
      records.push(...collectJsonLdRecords(JSON.parse(script.textContent ?? "")));
    } catch {
      warnings.push({
        code: "instagram.json_ld_malformed",
        severity: "info",
        stage: "provider_metadata",
        message: "Instagram JSON-LD could not be parsed; other page observations were retained.",
      });
    }
  }
  const posts = records.filter((record) =>
    jsonLdTypes(record).some((type) => type.toLowerCase() === "socialmediaposting"),
  );
  const postShortcode = (record: UnknownRecord) => {
    const recordUrl = safeHttpUrl(record.url ?? record.mainEntityOfPage, pageUrl);
    return recordUrl ? parseInstagramPath(recordUrl).shortcode : undefined;
  };
  const scopedPosts = posts.filter((record) => postShortcode(record));
  const post = expectedShortcode
    ? posts.find((record) => postShortcode(record) === expectedShortcode) ??
      (scopedPosts.length === 0 ? posts[0] : undefined)
    : posts[0] ?? records[0];
  if (expectedShortcode && scopedPosts.length > 0 && !post) {
    warnings.push({
      code: "instagram.json_ld_target_mismatch",
      severity: "warning",
      stage: "provider_metadata",
      message: "Instagram JSON-LD described a different post; it was ignored.",
    });
  }
  if (!post) return { warnings };

  const author = isRecord(post.author) ? post.author : undefined;
  const caption = stringField(post, "articleBody", "caption", "description", "headline");
  const postUrl = safeHttpUrl(post.url ?? post.mainEntityOfPage, pageUrl);
  const pathIdentity = postUrl ? parseInstagramPath(postUrl) : {};
  const media = jsonLdMediaEntities(post).map((record, position) =>
    parseMediaRecord(record, position, pageUrl),
  );

  return {
    observation: {
      ...pathIdentity,
      ...(caption ? { caption } : {}),
      ...(author && stringField(author, "alternateName", "identifier")
        ? { authorUsername: stringField(author, "alternateName", "identifier")?.replace(/^@/, "") }
        : {}),
      ...(author && stringField(author, "name") ? { authorName: stringField(author, "name") } : {}),
      ...(timestamp(post.datePublished) ? { publishedAt: timestamp(post.datePublished) } : {}),
      hashtags: hashtagsFromText(caption),
      media,
    },
    warnings,
  };
}

function metaContent(document: Document, selector: string): string | undefined {
  return document.querySelector<HTMLMetaElement>(selector)?.content.trim() || undefined;
}

function srcsetResources(img: HTMLImageElement, baseUrl: string): ImageResource[] {
  const resources: ImageResource[] = [];
  for (const part of img.srcset.split(",")) {
    const [url, descriptor] = part.trim().split(/\s+/, 2);
    const width = descriptor?.endsWith("w") ? positiveInteger(descriptor.slice(0, -1)) : undefined;
    addImageResource(resources, url, baseUrl, { ...(width ? { width } : {}) });
  }
  return resources;
}

function isLikelyPostImage(img: HTMLImageElement): boolean {
  const alt = img.alt.toLowerCase();
  if (alt.includes("profile picture") || alt.includes("avatar")) return false;
  const width = positiveInteger(img.getAttribute("width"));
  const height = positiveInteger(img.getAttribute("height"));
  return !((width && width <= 96) || (height && height <= 96));
}

function parseDomMedia(document: Document, pageUrl: string): InstagramMediaObservation[] {
  const container =
    document.querySelector<HTMLElement>("main article") ?? document.querySelector<HTMLElement>("article");
  if (!container) return [];
  const media: InstagramMediaObservation[] = [];
  const usedUrls = new Set<string>();

  for (const element of container.querySelectorAll<HTMLImageElement | HTMLVideoElement>("img, video")) {
    const isImage = element.tagName.toLowerCase() === "img";
    if (isImage && !isLikelyPostImage(element as HTMLImageElement)) continue;
    const id = element.dataset.mediaId ?? element.closest<HTMLElement>("[data-media-id]")?.dataset.mediaId;
    if (!isImage) {
      const video = element as HTMLVideoElement;
      const streamUrl = safeHttpUrl(video.currentSrc || video.src || video.querySelector("source")?.src, pageUrl);
      const posterUrl = safeHttpUrl(video.poster, pageUrl);
      if (!streamUrl && !posterUrl) continue;
      const dedupeUrl = streamUrl ?? posterUrl;
      if (!dedupeUrl || usedUrls.has(dedupeUrl)) continue;
      usedUrls.add(dedupeUrl);
      media.push({
        position: media.length,
        kind: "video",
        ...(id ? { providerMediaId: id } : {}),
        ...(streamUrl ? { streamUrl, mimeType: mimeFromUrl(streamUrl, "video") } : {}),
        ...(posterUrl ? { posterUrl } : {}),
        ...(positiveInteger(video.getAttribute("width"))
          ? { width: positiveInteger(video.getAttribute("width")) }
          : {}),
        ...(positiveInteger(video.getAttribute("height"))
          ? { height: positiveInteger(video.getAttribute("height")) }
          : {}),
        ...(positiveNumber(video.duration) ? { durationSeconds: positiveNumber(video.duration) } : {}),
        ...(video.getAttribute("aria-label")?.trim()
          ? { altText: video.getAttribute("aria-label")?.trim() }
          : {}),
      });
      continue;
    }

    const image = element as HTMLImageElement;
    const resources = srcsetResources(image, pageUrl);
    addImageResource(resources, image.currentSrc || image.src, pageUrl, {
      width: positiveInteger(image.getAttribute("width")),
      height: positiveInteger(image.getAttribute("height")),
    });
    const original = largestResource(resources);
    if (!original || usedUrls.has(original.url)) continue;
    usedUrls.add(original.url);
    const thumbnail = thumbnailResource(resources, original);
    media.push({
      position: media.length,
      kind: "image",
      ...(id ? { providerMediaId: id } : {}),
      originalUrl: original.url,
      ...(thumbnail ? { thumbnailUrl: thumbnail.url } : {}),
      ...(original.mimeType ?? mimeFromUrl(original.url, "image")
        ? { mimeType: original.mimeType ?? mimeFromUrl(original.url, "image") }
        : {}),
      ...(original.width ? { width: original.width } : {}),
      ...(original.height ? { height: original.height } : {}),
      ...(image.alt.trim() ? { altText: image.alt.trim() } : {}),
    });
  }
  return media;
}

function parseDom(document: Document, pageUrl: string): PartialInstagramObservation {
  const canonical = document.querySelector<HTMLLinkElement>('link[rel~="canonical"]')?.href;
  const canonicalIdentity = canonical ? parseInstagramPath(canonical) : {};
  const pageIdentity = parseInstagramPath(pageUrl);
  const caption =
    metaContent(document, 'meta[property="og:description"]') ??
    metaContent(document, 'meta[name="description"]');
  const author = metaContent(document, 'meta[name="author"]');
  let media = parseDomMedia(document, pageUrl);

  if (media.length === 0) {
    const imageUrl = metaContent(document, 'meta[property="og:image"]');
    const videoUrl = metaContent(document, 'meta[property="og:video:secure_url"]') ??
      metaContent(document, 'meta[property="og:video"]');
    const poster = safeHttpUrl(imageUrl, pageUrl);
    const stream = safeHttpUrl(videoUrl, pageUrl);
    if (stream) {
      media = [{
        position: 0,
        kind: "video",
        streamUrl: stream,
        ...(poster ? { posterUrl: poster } : {}),
        ...(mimeFromUrl(stream, "video") ? { mimeType: mimeFromUrl(stream, "video") } : {}),
        ...(positiveInteger(metaContent(document, 'meta[property="og:video:width"]'))
          ? { width: positiveInteger(metaContent(document, 'meta[property="og:video:width"]')) }
          : {}),
        ...(positiveInteger(metaContent(document, 'meta[property="og:video:height"]'))
          ? { height: positiveInteger(metaContent(document, 'meta[property="og:video:height"]')) }
          : {}),
      }];
    } else if (poster) {
      media = [{
        position: 0,
        kind: "image",
        originalUrl: poster,
        ...(mimeFromUrl(poster, "image") ? { mimeType: mimeFromUrl(poster, "image") } : {}),
        ...(positiveInteger(metaContent(document, 'meta[property="og:image:width"]'))
          ? { width: positiveInteger(metaContent(document, 'meta[property="og:image:width"]')) }
          : {}),
        ...(positiveInteger(metaContent(document, 'meta[property="og:image:height"]'))
          ? { height: positiveInteger(metaContent(document, 'meta[property="og:image:height"]')) }
          : {}),
        ...(metaContent(document, 'meta[property="og:image:alt"]')
          ? { altText: metaContent(document, 'meta[property="og:image:alt"]') }
          : {}),
      }];
    }
  }

  return {
    shortcode: canonicalIdentity.shortcode ?? pageIdentity.shortcode,
    routeKind: canonicalIdentity.routeKind ?? pageIdentity.routeKind,
    ...(caption ? { caption } : {}),
    ...(author ? { authorUsername: author.replace(/^@/, "") } : {}),
    hashtags: hashtagsFromText(caption),
    media,
  };
}

function mergeHashtags(...sets: readonly (readonly string[] | undefined)[]): string[] {
  const result: string[] = [];
  const seen = new Set<string>();
  for (const tags of sets) {
    for (const tag of tags ?? []) {
      const key = tag.toLocaleLowerCase();
      if (!seen.has(key)) {
        seen.add(key);
        result.push(tag);
      }
    }
  }
  return result;
}

/**
 * Reads only normalized, allowlisted Instagram observations. Parsed objects are
 * never returned, so credentials or unrelated page-state fields cannot cross
 * the provider adapter boundary.
 */
export function parseInstagramPage(document: Document, pageUrl: string): InstagramParserResult {
  const urlIdentity = parseInstagramPath(pageUrl);
  const serialized = parseSerializedState(document, pageUrl, urlIdentity.shortcode);
  const jsonLd = parseJsonLd(document, pageUrl, urlIdentity.shortcode);
  const dom = parseDom(document, pageUrl);
  const primary = serialized.observation ?? jsonLd.observation ?? dom;
  const sources: InstagramObservationSource[] = [];
  if (serialized.observation) sources.push("serialized_state");
  if (jsonLd.observation) sources.push("json_ld");
  sources.push("dom");

  return {
    observation: {
      shortcode:
        urlIdentity.shortcode ??
        serialized.observation?.shortcode ??
        jsonLd.observation?.shortcode ??
        dom.shortcode,
      routeKind:
        urlIdentity.routeKind ??
        serialized.observation?.routeKind ??
        jsonLd.observation?.routeKind ??
        dom.routeKind,
      caption:
        serialized.observation?.caption ?? jsonLd.observation?.caption ?? dom.caption,
      authorUsername:
        serialized.observation?.authorUsername ??
        jsonLd.observation?.authorUsername ??
        dom.authorUsername,
      authorName: serialized.observation?.authorName ?? jsonLd.observation?.authorName,
      publishedAt:
        serialized.observation?.publishedAt ?? jsonLd.observation?.publishedAt,
      hashtags: mergeHashtags(
        serialized.observation?.hashtags,
        jsonLd.observation?.hashtags,
        dom.hashtags,
      ),
      media: primary.media ?? [],
      sources,
    },
    warnings: [...serialized.warnings, ...jsonLd.warnings],
  };
}

export function matchInstagramPostUrl(url: string): boolean {
  return Boolean(parseInstagramPath(url).shortcode);
}
