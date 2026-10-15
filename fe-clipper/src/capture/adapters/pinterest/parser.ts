import type { AdapterExtractionWarning, PageCaptureContext } from "../../types";
import {
  PINTEREST_EXCLUDED_MEDIA_ANCESTOR_SELECTOR,
  PINTEREST_PAGE_STATE_SELECTORS,
  PINTEREST_PRIMARY_IMAGE_SELECTORS,
} from "./selectors";

export const PINTEREST_PAGE_STATE_PARSER_VERSION = "pws-data.v1";

export type PinterestObservationSource =
  | "serialized_page_state"
  | "json_ld"
  | "open_graph"
  | "dom";

export interface PinterestObservedText {
  value: string;
  source: PinterestObservationSource;
}

export interface PinterestCreatorObservation {
  displayName?: string;
  username?: string;
  profileUrl?: string;
  source: PinterestObservationSource;
}

export interface PinterestBoardObservation {
  name?: string;
  url?: string;
  source: PinterestObservationSource;
}

export interface PinterestImageObservation {
  url: string;
  width?: number;
  height?: number;
  mimeType?: string;
  altText?: string;
  source: PinterestObservationSource;
  /** Normalized parser location, never raw serialized state. */
  sourceLocator: string;
  sizeHint: "original" | "thumbnail" | "unspecified";
}

export interface PinterestPageObservation {
  title?: PinterestObservedText;
  description?: PinterestObservedText;
  creator?: PinterestCreatorObservation;
  board?: PinterestBoardObservation;
  providerTags: readonly string[];
  providerTagSources: readonly PinterestObservationSource[];
  images: readonly PinterestImageObservation[];
  warnings: readonly AdapterExtractionWarning[];
}

type JSONRecord = Record<string, unknown>;

const PINTEREST_BASE_HOSTS = new Set([
  "pinterest.at",
  "pinterest.ca",
  "pinterest.ch",
  "pinterest.cl",
  "pinterest.co.kr",
  "pinterest.co.uk",
  "pinterest.com",
  "pinterest.com.au",
  "pinterest.com.mx",
  "pinterest.de",
  "pinterest.dk",
  "pinterest.es",
  "pinterest.fr",
  "pinterest.ie",
  "pinterest.it",
  "pinterest.jp",
  "pinterest.nl",
  "pinterest.nz",
  "pinterest.ph",
  "pinterest.pt",
  "pinterest.se",
]);

const MAX_PAGE_STATE_NODES = 20_000;
const MAX_PAGE_STATE_DEPTH = 16;

const SENSITIVE_URL_PARAMETER_KEYS = new Set([
  "access_token",
  "api_key",
  "apikey",
  "auth",
  "auth_token",
  "authorization",
  "bearer",
  "client_secret",
  "cookie",
  "csrf",
  "csrf_token",
  "oauth_token",
  "password",
  "passwd",
  "session",
  "session_id",
  "session_token",
  "sessionid",
  "token",
]);

function asRecord(value: unknown): JSONRecord | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as JSONRecord)
    : undefined;
}

function nonEmptyString(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const normalized = value.replace(/\s+/g, " ").trim();
  return normalized || undefined;
}

function positiveInteger(value: unknown): number | undefined {
  const parsed = typeof value === "number" ? value : Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
}

function firstString(record: JSONRecord | undefined, keys: readonly string[]): string | undefined {
  if (!record) return undefined;
  for (const key of keys) {
    const value = nonEmptyString(record[key]);
    if (value) return value;
  }
  return undefined;
}

function firstPositiveInteger(
  record: JSONRecord | undefined,
  keys: readonly string[],
): number | undefined {
  if (!record) return undefined;
  for (const key of keys) {
    const value = positiveInteger(record[key]);
    if (value) return value;
  }
  return undefined;
}

function isSensitiveUrlParameter(key: string): boolean {
  return SENSITIVE_URL_PARAMETER_KEYS.has(key.trim().toLowerCase().replaceAll("-", "_"));
}

function removeSensitiveParameters(parameters: URLSearchParams): boolean {
  let removed = false;
  for (const key of [...parameters.keys()]) {
    if (!isSensitiveUrlParameter(key)) continue;
    parameters.delete(key);
    removed = true;
  }
  return removed;
}

function sanitizeUrlFragment(url: URL): void {
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

  // OAuth-style fragments often use `#key=value&...` without a `?`.
  if (!fragment.includes("=")) return;
  const parameters = new URLSearchParams(fragment);
  if (!removeSensitiveParameters(parameters)) return;
  url.hash = parameters.toString();
}

function normalizeHttpUrl(value: unknown, baseUrl: string): string | undefined {
  const text = nonEmptyString(value);
  if (!text) return undefined;

  try {
    const resolved = new URL(text, baseUrl);
    if (resolved.protocol !== "http:" && resolved.protocol !== "https:") return undefined;
    // URL userinfo is browser credential material. It is never needed for
    // stable identity or source provenance and must not enter capture output.
    resolved.username = "";
    resolved.password = "";
    removeSensitiveParameters(resolved.searchParams);
    sanitizeUrlFragment(resolved);
    return resolved.href;
  } catch {
    return undefined;
  }
}

function pinterestBaseHost(hostname: string): string | undefined {
  const normalized = hostname.toLowerCase().replace(/\.$/, "");
  for (const baseHost of PINTEREST_BASE_HOSTS) {
    if (normalized === baseHost || normalized.endsWith(`.${baseHost}`)) return baseHost;
  }
  return undefined;
}

export function isPinterestHostname(hostname: string): boolean {
  return pinterestBaseHost(hostname) !== undefined;
}

export function pinIdFromUrl(value: string): string | undefined {
  try {
    const url = new URL(value);
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    if (!isPinterestHostname(url.hostname)) return undefined;

    const pathSegments = url.pathname
      .split("/")
      .filter(Boolean)
      .map((segment) => decodeURIComponent(segment));
    const pinIndex = pathSegments.indexOf("pin");
    const pinSegment = pinIndex >= 0 ? pathSegments[pinIndex + 1] : undefined;
    if (!pinSegment) return undefined;

    const directId = pinSegment.match(/^\d+$/)?.[0];
    const slugId = pinSegment.match(/--(\d+)$/)?.[1];
    return directId ?? slugId;
  } catch {
    return undefined;
  }
}

function observedPageUrls(context: PageCaptureContext): string[] {
  const candidates = [
    context.location.href,
    context.document.querySelector('link[rel~="canonical"]')?.getAttribute("href"),
    context.document.querySelector('meta[property="og:url"]')?.getAttribute("content"),
  ];
  const urls: string[] = [];

  for (const candidate of candidates) {
    const normalized = normalizeHttpUrl(candidate, context.location.href);
    if (normalized && !urls.includes(normalized)) urls.push(normalized);
  }
  return urls;
}

export function resolvePinterestPinId(context: PageCaptureContext): string | undefined {
  if (!isPinterestHostname(context.location.hostname)) return undefined;
  for (const url of observedPageUrls(context)) {
    const pinId = pinIdFromUrl(url);
    if (pinId) return pinId;
  }
  return undefined;
}

export function canonicalPinterestPinUrl(pinId: string): string {
  return `https://www.pinterest.com/pin/${pinId}/`;
}

export function sanitizePinterestPageUrl(value: string): string | undefined {
  return normalizeHttpUrl(value, value);
}

function warning(code: string, message: string): AdapterExtractionWarning {
  return {
    code,
    severity: "warning",
    stage: "provider_metadata",
    message,
  };
}

interface ParsedScripts {
  values: unknown[];
  scriptCount: number;
  invalidCount: number;
}

function parseScripts(document: Document, selectors: readonly string[]): ParsedScripts {
  const scripts = new Set<Element>();
  for (const selector of selectors) {
    for (const script of document.querySelectorAll(selector)) scripts.add(script);
  }

  const values: unknown[] = [];
  let invalidCount = 0;
  for (const script of scripts) {
    const text = script.textContent?.trim();
    if (!text) continue;
    try {
      values.push(JSON.parse(text));
    } catch {
      invalidCount += 1;
    }
  }
  return { values, scriptCount: scripts.size, invalidCount };
}

interface RecordSearchResult {
  record?: JSONRecord;
  scanLimited: boolean;
}

function recordPinId(record: JSONRecord): string | undefined {
  for (const key of ["id", "pin_id", "pinId"]) {
    const value = record[key];
    if (typeof value === "string" || typeof value === "number") {
      const normalized = String(value);
      if (/^\d+$/.test(normalized)) return normalized;
    }
  }
  return undefined;
}

function pinRecordScore(record: JSONRecord): number {
  let score = 0;
  if (asRecord(record.images)) score += 100;
  if (asRecord(record.media)?.images) score += 80;
  if (firstString(record, ["title", "grid_title", "name"])) score += 20;
  if (firstString(record, ["description", "seo_description"])) score += 20;
  if (asRecord(record.pinner) || asRecord(record.creator)) score += 10;
  if (asRecord(record.board)) score += 10;
  return score;
}

function findPinRecord(root: unknown, pinId: string): RecordSearchResult {
  const stack: { value: unknown; parentKey?: string; depth: number }[] = [
    { value: root, depth: 0 },
  ];
  const seen = new Set<object>();
  let visited = 0;
  let best: { record: JSONRecord; score: number } | undefined;

  while (stack.length && visited < MAX_PAGE_STATE_NODES) {
    const current = stack.pop();
    if (!current) break;
    const { value, parentKey, depth } = current;
    if ((typeof value !== "object" || value === null) || seen.has(value)) continue;
    seen.add(value);
    visited += 1;

    if (Array.isArray(value)) {
      if (depth >= MAX_PAGE_STATE_DEPTH) continue;
      for (let index = value.length - 1; index >= 0; index -= 1) {
        stack.push({ value: value[index], depth: depth + 1 });
      }
      continue;
    }

    const record = value as JSONRecord;
    if (recordPinId(record) === pinId || parentKey === pinId) {
      const score = pinRecordScore(record);
      if (!best || score > best.score) best = { record, score };
    }

    if (depth >= MAX_PAGE_STATE_DEPTH) continue;
    const entries = Object.entries(record);
    for (let index = entries.length - 1; index >= 0; index -= 1) {
      const entry = entries[index];
      if (!entry) continue;
      stack.push({ value: entry[1], parentKey: entry[0], depth: depth + 1 });
    }
  }

  return { record: best?.record, scanLimited: stack.length > 0 };
}

function imageHint(key: string): PinterestImageObservation["sizeHint"] {
  const normalized = key.toLowerCase();
  if (normalized === "orig" || normalized.includes("original")) return "original";
  if (normalized.includes("thumb") || normalized.startsWith("75x") || normalized.startsWith("236x")) {
    return "thumbnail";
  }
  return "unspecified";
}

function imageFromValue(
  value: unknown,
  baseUrl: string,
  source: PinterestObservationSource,
  sourceLocator: string,
  sizeHint: PinterestImageObservation["sizeHint"],
  fallbackAlt?: string,
): PinterestImageObservation | undefined {
  const record = asRecord(value);
  const url = normalizeHttpUrl(
    record ? firstString(record, ["url", "src", "contentUrl", "content_url"]) : value,
    baseUrl,
  );
  if (!url) return undefined;

  const mimeType = firstString(record, ["mime_type", "mimeType", "content_type", "encodingFormat"]);
  return {
    url,
    source,
    sourceLocator,
    sizeHint,
    ...(firstPositiveInteger(record, ["width", "naturalWidth"]) !== undefined
      ? { width: firstPositiveInteger(record, ["width", "naturalWidth"]) }
      : {}),
    ...(firstPositiveInteger(record, ["height", "naturalHeight"]) !== undefined
      ? { height: firstPositiveInteger(record, ["height", "naturalHeight"]) }
      : {}),
    ...(mimeType?.startsWith("image/") ? { mimeType } : {}),
    ...(firstString(record, ["alt", "alt_text", "altText", "caption"]) ?? fallbackAlt
      ? { altText: firstString(record, ["alt", "alt_text", "altText", "caption"]) ?? fallbackAlt }
      : {}),
  };
}

interface NormalizedMetadata {
  title?: PinterestObservedText;
  description?: PinterestObservedText;
  creator?: PinterestCreatorObservation;
  board?: PinterestBoardObservation;
  tags: string[];
  tagSources: PinterestObservationSource[];
}

function tagsFromValue(value: unknown): string[] {
  const values = Array.isArray(value)
    ? value
    : typeof value === "string"
      ? value.split(",")
      : [];
  const tags: string[] = [];
  for (const item of values) {
    const record = asRecord(item);
    const text = nonEmptyString(record ? firstString(record, ["name", "tag", "label"]) : item);
    const normalized = text?.replace(/^#+/, "").trim();
    if (normalized && !tags.some((tag) => tag.toLowerCase() === normalized.toLowerCase())) {
      tags.push(normalized);
    }
  }
  return tags;
}

function stateMetadata(record: JSONRecord, baseUrl: string): NormalizedMetadata {
  const source = "serialized_page_state" as const;
  const title = firstString(record, ["title", "grid_title", "name"]);
  const description = firstString(record, ["description", "seo_description", "rich_summary"]);
  const creatorRecord = asRecord(record.pinner) ?? asRecord(record.creator) ?? asRecord(record.owner);
  const boardRecord = asRecord(record.board);
  const creatorName = firstString(creatorRecord, ["full_name", "display_name", "name"]);
  const creatorUsername = firstString(creatorRecord, ["username", "user_name"]);
  const creatorUrl = normalizeHttpUrl(
    firstString(creatorRecord, ["url", "profile_url", "profileUrl"]),
    baseUrl,
  );
  const boardName = firstString(boardRecord, ["name", "title"]);
  const boardUrl = normalizeHttpUrl(firstString(boardRecord, ["url"]), baseUrl);
  const tags = tagsFromValue(record.hashtags ?? record.keywords ?? record.tags);

  return {
    ...(title ? { title: { value: title, source } } : {}),
    ...(description ? { description: { value: description, source } } : {}),
    ...(creatorName || creatorUsername || creatorUrl
      ? {
          creator: {
            source,
            ...(creatorName ? { displayName: creatorName } : {}),
            ...(creatorUsername ? { username: creatorUsername } : {}),
            ...(creatorUrl ? { profileUrl: creatorUrl } : {}),
          },
        }
      : {}),
    ...(boardName || boardUrl
      ? { board: { source, ...(boardName ? { name: boardName } : {}), ...(boardUrl ? { url: boardUrl } : {}) } }
      : {}),
    tags,
    tagSources: tags.length ? [source] : [],
  };
}

function stateImages(record: JSONRecord, baseUrl: string): PinterestImageObservation[] {
  const images: PinterestImageObservation[] = [];
  const fallbackAlt = firstString(record, ["alt_text", "altText"]);
  const maps: { value: unknown; locator: string }[] = [
    { value: record.images, locator: "pin.images" },
    { value: asRecord(record.media)?.images, locator: "pin.media.images" },
  ];

  for (const imageMap of maps) {
    const map = asRecord(imageMap.value);
    if (!map) continue;
    for (const [key, value] of Object.entries(map)) {
      const image = imageFromValue(
        value,
        baseUrl,
        "serialized_page_state",
        `${imageMap.locator}.${key}`,
        imageHint(key),
        fallbackAlt,
      );
      if (image) images.push(image);
    }
  }

  const directUrl = firstString(record, ["image_url", "imageUrl"]);
  if (directUrl) {
    const directImage = imageFromValue(
      {
        url: directUrl,
        width: record.width,
        height: record.height,
        mime_type: record.mime_type,
        alt_text: fallbackAlt,
      },
      baseUrl,
      "serialized_page_state",
      "pin.image_url",
      "unspecified",
    );
    if (directImage) images.push(directImage);
  }
  return images;
}

function jsonLdEntries(value: unknown): JSONRecord[] {
  const entries: JSONRecord[] = [];
  const stack: unknown[] = [value];
  const seen = new Set<object>();
  let visited = 0;
  while (stack.length && visited < MAX_PAGE_STATE_NODES) {
    const current = stack.pop();
    if (typeof current !== "object" || current === null || seen.has(current)) continue;
    seen.add(current);
    visited += 1;
    if (Array.isArray(current)) {
      stack.push(...current);
      continue;
    }
    const record = current as JSONRecord;
    entries.push(record);
    for (const child of Object.values(record)) {
      if (typeof child === "object" && child !== null) stack.push(child);
    }
  }
  return entries;
}

function jsonLdRecordMatches(record: JSONRecord, pinId: string, baseUrl: string): boolean {
  const identifier = record.identifier;
  if (typeof identifier === "string" || typeof identifier === "number") {
    if (String(identifier) === pinId) return true;
  }
  const identifierRecord = asRecord(identifier);
  if (firstString(identifierRecord, ["value", "@id"]) === pinId) return true;

  for (const key of ["url", "@id", "mainEntityOfPage"]) {
    const value = record[key];
    const urlValue = asRecord(value) ? firstString(asRecord(value), ["@id", "url"]) : value;
    const normalized = normalizeHttpUrl(urlValue, baseUrl);
    if (normalized && pinIdFromUrl(normalized) === pinId) return true;
  }
  return false;
}

function jsonLdMetadata(record: JSONRecord, baseUrl: string): NormalizedMetadata {
  const source = "json_ld" as const;
  const title = firstString(record, ["name", "headline", "title"]);
  const description = firstString(record, ["description", "caption"]);
  const creatorValue = record.author ?? record.creator;
  const creatorRecord = asRecord(creatorValue);
  const creatorName = creatorRecord
    ? firstString(creatorRecord, ["name", "alternateName"])
    : nonEmptyString(creatorValue);
  const creatorUsername = firstString(creatorRecord, ["alternateName", "identifier"]);
  const creatorUrl = normalizeHttpUrl(firstString(creatorRecord, ["url", "@id"]), baseUrl);
  const boardRecord = asRecord(record.isPartOf);
  const boardName = firstString(boardRecord, ["name"]);
  const boardUrl = normalizeHttpUrl(firstString(boardRecord, ["url", "@id"]), baseUrl);
  const tags = tagsFromValue(record.keywords);

  return {
    ...(title ? { title: { value: title, source } } : {}),
    ...(description ? { description: { value: description, source } } : {}),
    ...(creatorName || creatorUsername || creatorUrl
      ? {
          creator: {
            source,
            ...(creatorName ? { displayName: creatorName } : {}),
            ...(creatorUsername ? { username: creatorUsername } : {}),
            ...(creatorUrl ? { profileUrl: creatorUrl } : {}),
          },
        }
      : {}),
    ...(boardName || boardUrl
      ? { board: { source, ...(boardName ? { name: boardName } : {}), ...(boardUrl ? { url: boardUrl } : {}) } }
      : {}),
    tags,
    tagSources: tags.length ? [source] : [],
  };
}

function jsonLdImages(record: JSONRecord, baseUrl: string): PinterestImageObservation[] {
  const images: PinterestImageObservation[] = [];
  const imageValues = Array.isArray(record.image) ? record.image : [record.image];
  for (const value of imageValues) {
    if (value === undefined) continue;
    const image = imageFromValue(
      value,
      baseUrl,
      "json_ld",
      "json_ld.image",
      asRecord(value)?.contentUrl || asRecord(value)?.content_url ? "original" : "unspecified",
      firstString(record, ["name", "headline"]),
    );
    if (image) images.push(image);
  }

  const thumbnailValues = Array.isArray(record.thumbnailUrl)
    ? record.thumbnailUrl
    : [record.thumbnailUrl];
  for (const value of thumbnailValues) {
    if (value === undefined) continue;
    const image = imageFromValue(
      value,
      baseUrl,
      "json_ld",
      "json_ld.thumbnailUrl",
      "thumbnail",
      firstString(record, ["name", "headline"]),
    );
    if (image) images.push(image);
  }
  return images;
}

function metaContent(document: Document, selector: string): string | undefined {
  return nonEmptyString(document.querySelector(selector)?.getAttribute("content"));
}

function openGraphMetadata(document: Document): NormalizedMetadata {
  const source = "open_graph" as const;
  const title = metaContent(document, 'meta[property="og:title"]');
  const description = metaContent(document, 'meta[property="og:description"]');
  return {
    ...(title ? { title: { value: title, source } } : {}),
    ...(description ? { description: { value: description, source } } : {}),
    tags: [],
    tagSources: [],
  };
}

function openGraphImages(document: Document, baseUrl: string): PinterestImageObservation[] {
  const imageUrl = metaContent(document, 'meta[property="og:image"]');
  if (!imageUrl) return [];
  const image = imageFromValue(
    {
      url: imageUrl,
      width: metaContent(document, 'meta[property="og:image:width"]'),
      height: metaContent(document, 'meta[property="og:image:height"]'),
      mime_type: metaContent(document, 'meta[property="og:image:type"]'),
      alt_text: metaContent(document, 'meta[property="og:image:alt"]'),
    },
    baseUrl,
    "open_graph",
    "open_graph.og_image",
    "unspecified",
  );
  return image ? [image] : [];
}

function srcsetImages(
  value: string,
  baseUrl: string,
  sourceLocator: string,
  altText?: string,
  mimeType?: string,
): PinterestImageObservation[] {
  const images: PinterestImageObservation[] = [];
  for (const candidate of value.split(",")) {
    const [urlValue, descriptor] = candidate.trim().split(/\s+/, 2);
    const url = normalizeHttpUrl(urlValue, baseUrl);
    if (!url) continue;
    const width = descriptor?.match(/^(\d+)w$/)?.[1];
    images.push({
      url,
      source: "dom",
      sourceLocator,
      sizeHint: "unspecified",
      ...(positiveInteger(width) ? { width: positiveInteger(width) } : {}),
      ...(altText ? { altText } : {}),
      ...(mimeType?.startsWith("image/") ? { mimeType } : {}),
    });
  }
  return images;
}

function domImages(document: Document, baseUrl: string): PinterestImageObservation[] {
  const elements = new Set<HTMLImageElement>();
  for (const selector of PINTEREST_PRIMARY_IMAGE_SELECTORS) {
    for (const element of document.querySelectorAll<HTMLImageElement>(selector)) {
      if (!element.closest(PINTEREST_EXCLUDED_MEDIA_ANCESTOR_SELECTOR)) elements.add(element);
    }
  }

  const images: PinterestImageObservation[] = [];
  for (const element of elements) {
    const altText = nonEmptyString(element.getAttribute("alt"));
    const sourceLocator = "dom.pin_closeup_image";
    const src = element.currentSrc || element.getAttribute("src");
    const image = imageFromValue(
      {
        url: src,
        width: element.getAttribute("data-pin-media-width") ?? element.getAttribute("width"),
        height: element.getAttribute("data-pin-media-height") ?? element.getAttribute("height"),
        alt_text: altText,
      },
      baseUrl,
      "dom",
      sourceLocator,
      "unspecified",
    );
    if (image) images.push(image);

    const srcset = element.getAttribute("srcset");
    if (srcset) images.push(...srcsetImages(srcset, baseUrl, `${sourceLocator}.srcset`, altText));

    const picture = element.closest("picture");
    for (const source of picture?.querySelectorAll("source[srcset]") ?? []) {
      const sourceSrcset = source.getAttribute("srcset");
      if (!sourceSrcset) continue;
      images.push(
        ...srcsetImages(
          sourceSrcset,
          baseUrl,
          `${sourceLocator}.picture_srcset`,
          altText,
          source.getAttribute("type") ?? undefined,
        ),
      );
    }
  }
  return images;
}

function mergeMetadata(target: NormalizedMetadata, source: NormalizedMetadata): void {
  target.title ??= source.title;
  target.description ??= source.description;
  target.creator ??= source.creator;
  target.board ??= source.board;
  for (const tag of source.tags) {
    if (!target.tags.some((existing) => existing.toLowerCase() === tag.toLowerCase())) {
      target.tags.push(tag);
    }
  }
  for (const tagSource of source.tagSources) {
    if (!target.tagSources.includes(tagSource)) target.tagSources.push(tagSource);
  }
}

function imageInformationScore(image: PinterestImageObservation): number {
  return (
    (image.width && image.height ? 8 : image.width || image.height ? 4 : 0) +
    (image.mimeType ? 2 : 0) +
    (image.altText ? 1 : 0)
  );
}

function deduplicateImages(images: readonly PinterestImageObservation[]): PinterestImageObservation[] {
  const byUrl = new Map<string, PinterestImageObservation>();
  for (const image of images) {
    const existing = byUrl.get(image.url);
    if (!existing) {
      byUrl.set(image.url, image);
      continue;
    }

    const primary = imageInformationScore(image) > imageInformationScore(existing) ? image : existing;
    const secondary = primary === image ? existing : image;
    byUrl.set(image.url, {
      ...primary,
      sizeHint:
        primary.sizeHint === "original" || secondary.sizeHint === "original"
          ? "original"
          : primary.sizeHint === "thumbnail" || secondary.sizeHint === "thumbnail"
            ? "thumbnail"
            : "unspecified",
      width: primary.width ?? secondary.width,
      height: primary.height ?? secondary.height,
      mimeType: primary.mimeType ?? secondary.mimeType,
      altText: primary.altText ?? secondary.altText,
    });
  }
  return [...byUrl.values()];
}

export function inspectPinterestPage(
  context: PageCaptureContext,
  pinId: string,
): PinterestPageObservation {
  const metadata: NormalizedMetadata = { tags: [], tagSources: [] };
  const images: PinterestImageObservation[] = [];
  const warnings: AdapterExtractionWarning[] = [];

  const pageState = parseScripts(context.document, PINTEREST_PAGE_STATE_SELECTORS);
  let pageStateMatched = false;
  let scanLimited = false;
  for (const value of pageState.values) {
    const found = findPinRecord(value, pinId);
    scanLimited ||= found.scanLimited;
    if (!found.record) continue;
    pageStateMatched = true;
    mergeMetadata(metadata, stateMetadata(found.record, context.location.href));
    images.push(...stateImages(found.record, context.location.href));
  }
  if (pageState.invalidCount) {
    warnings.push(
      warning(
        "pinterest.page_state_invalid",
        "Pinterest serialized page state could not be parsed; other page observations were used.",
      ),
    );
  }
  if (pageState.values.length && !pageStateMatched) {
    warnings.push(
      warning(
        "pinterest.page_state_unrecognized",
        "Pinterest serialized page state did not contain the current pin in a recognized shape.",
      ),
    );
  }
  if (scanLimited) {
    warnings.push(
      warning(
        "pinterest.page_state_scan_limited",
        "Pinterest serialized page state exceeded the bounded parser scan limit.",
      ),
    );
  }

  const jsonLd = parseScripts(context.document, ['script[type="application/ld+json"]']);
  let matchedJsonLd: JSONRecord | undefined;
  for (const value of jsonLd.values) {
    const candidates = jsonLdEntries(value).filter((record) =>
      jsonLdRecordMatches(record, pinId, context.location.href),
    );
    const candidate = candidates.sort((left, right) => pinRecordScore(right) - pinRecordScore(left))[0];
    if (candidate && (!matchedJsonLd || pinRecordScore(candidate) > pinRecordScore(matchedJsonLd))) {
      matchedJsonLd = candidate;
    }
  }
  if (matchedJsonLd) {
    mergeMetadata(metadata, jsonLdMetadata(matchedJsonLd, context.location.href));
    images.push(...jsonLdImages(matchedJsonLd, context.location.href));
  }
  if (jsonLd.invalidCount) {
    warnings.push(
      warning(
        "pinterest.json_ld_invalid",
        "Pinterest JSON-LD could not be parsed; other page observations were used.",
      ),
    );
  }

  mergeMetadata(metadata, openGraphMetadata(context.document));
  images.push(...domImages(context.document, context.location.href));
  images.push(...openGraphImages(context.document, context.location.href));

  return {
    ...(metadata.title ? { title: metadata.title } : {}),
    ...(metadata.description ? { description: metadata.description } : {}),
    ...(metadata.creator ? { creator: metadata.creator } : {}),
    ...(metadata.board ? { board: metadata.board } : {}),
    providerTags: metadata.tags,
    providerTagSources: metadata.tagSources,
    images: deduplicateImages(images),
    warnings,
  };
}
