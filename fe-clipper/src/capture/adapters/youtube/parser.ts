import { YOUTUBE_SELECTORS } from "./selectors";

export const YOUTUBE_PARSER_VERSION = "1.0.0";

const VIDEO_ID_PATTERN = /^[A-Za-z0-9_-]{11}$/;
const MAX_SERIALIZED_STATE_LENGTH = 2_000_000;
const MAX_WALKED_NODES = 20_000;
const MAX_WALK_DEPTH = 30;

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

type UnknownRecord = Record<string, unknown>;

export type YouTubeObservationSource =
  | "dom_meta"
  | "json_ld"
  | "player_response"
  | "serialized_page_state"
  | "rendered_transcript";

export interface YouTubeParserWarning {
  code: string;
  message: string;
}

export interface YouTubePosterObservation {
  url: string;
  width?: number;
  height?: number;
  mimeType?: string;
}

export interface YouTubeCaptionTrackObservation {
  url: string;
  languageCode: string;
  name: string;
  kind?: string;
  isTranslatable: boolean;
  mimeType?: string;
}

export interface YouTubeChapterObservation {
  title: string;
  startSeconds: number;
}

export interface YouTubeTranscriptSegmentObservation {
  startSeconds: number;
  durationSeconds?: number;
  text: string;
}

export interface YouTubeTranscriptObservation {
  source: "rendered_dom" | "serialized_page_state";
  languageCode?: string;
  segments: readonly YouTubeTranscriptSegmentObservation[];
}

export interface YouTubePageObservation {
  title?: string;
  description?: string;
  channelId?: string;
  channelName?: string;
  tags: readonly string[];
  chapters: readonly YouTubeChapterObservation[];
  durationSeconds?: number;
  publishedAt?: string;
  posters: readonly YouTubePosterObservation[];
  captionTracks: readonly YouTubeCaptionTrackObservation[];
  transcript?: YouTubeTranscriptObservation;
  sources: readonly YouTubeObservationSource[];
  warnings: readonly YouTubeParserWarning[];
}

interface SerializedStateResult {
  value?: UnknownRecord;
  seen: boolean;
  malformed: boolean;
}

interface JsonLdStateResult {
  objects: readonly UnknownRecord[];
  malformed: boolean;
}

function asRecord(value: unknown): UnknownRecord | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as UnknownRecord)
    : undefined;
}

function asArray(value: unknown): readonly unknown[] {
  return Array.isArray(value) ? value : [];
}

function cleanString(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const cleaned = value.trim();
  return cleaned || undefined;
}

function positiveNumber(value: unknown): number | undefined {
  const number = typeof value === "number" ? value : Number.parseFloat(String(value));
  return Number.isFinite(number) && number > 0 ? number : undefined;
}

function nonNegativeNumber(value: unknown): number | undefined {
  const number = typeof value === "number" ? value : Number.parseFloat(String(value));
  return Number.isFinite(number) && number >= 0 ? number : undefined;
}

function textRuns(value: unknown): string | undefined {
  const record = asRecord(value);
  const simpleText = cleanString(record?.simpleText);
  if (simpleText) return simpleText;

  const text = asArray(record?.runs)
    .map((run) => cleanString(asRecord(run)?.text))
    .filter((part): part is string => Boolean(part))
    .join("")
    .trim();
  return text || undefined;
}

function uniqueStrings(values: readonly (string | undefined)[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values) {
    if (!value || seen.has(value)) continue;
    seen.add(value);
    result.push(value);
  }
  return result;
}

function isYouTubeHost(hostname: string): boolean {
  const host = hostname.toLowerCase();
  return host === "youtube.com" || host.endsWith(".youtube.com");
}

function isYouTubeNoCookieHost(hostname: string): boolean {
  const host = hostname.toLowerCase();
  return host === "youtube-nocookie.com" || host.endsWith(".youtube-nocookie.com");
}

function validVideoId(value: string | undefined): string | undefined {
  return value && VIDEO_ID_PATTERN.test(value) ? value : undefined;
}

function sensitiveLocationKey(value: string): boolean {
  return SENSITIVE_LOCATION_KEYS.has(value.toLowerCase().replace(/[^a-z0-9]/g, ""));
}

/** Removes browser/provider credential-shaped URL material while retaining ordinary provenance. */
export function sanitizeYouTubePageUrl(url: string): string {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return url;
  }
  parsed.username = "";
  parsed.password = "";
  for (const key of [...parsed.searchParams.keys()]) {
    if (sensitiveLocationKey(key)) parsed.searchParams.delete(key);
  }

  if (parsed.hash.length > 1) {
    const rawFragment = parsed.hash.slice(1);
    const fragment = new URLSearchParams(rawFragment);
    const hasStructuredFragment = rawFragment.includes("=") || rawFragment.includes("&");
    if (hasStructuredFragment) {
      for (const key of [...fragment.keys()]) {
        if (sensitiveLocationKey(key)) fragment.delete(key);
      }
      const sanitizedFragment = fragment.toString();
      parsed.hash = sanitizedFragment ? `#${sanitizedFragment}` : "";
    } else if (sensitiveLocationKey(rawFragment)) {
      parsed.hash = "";
    }
  }
  return parsed.href;
}

/** Resolves only stable, supported YouTube URL forms; it never inspects page state. */
export function extractYouTubeVideoId(url: string): string | undefined {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return undefined;
  }

  if (parsed.protocol !== "https:" && parsed.protocol !== "http:") return undefined;

  const host = parsed.hostname.toLowerCase();
  if (host === "youtu.be" || host === "www.youtu.be") {
    return validVideoId(parsed.pathname.split("/").filter(Boolean)[0]);
  }

  if (!isYouTubeHost(host) && !isYouTubeNoCookieHost(host)) return undefined;

  if (parsed.pathname === "/watch") {
    return validVideoId(parsed.searchParams.get("v") ?? undefined);
  }

  const [route, id] = parsed.pathname.split("/").filter(Boolean);
  if (route === "shorts" || route === "embed" || route === "live") {
    return validVideoId(id);
  }
  return undefined;
}

export function canonicalYouTubeWatchUrl(videoId: string): string {
  return `https://www.youtube.com/watch?v=${videoId}`;
}

function firstElementValue(doc: Document, selectors: readonly string[]): string | undefined {
  for (const selector of selectors) {
    const element = doc.querySelector(selector);
    const value = cleanString(element?.getAttribute("content") ?? element?.getAttribute("href"));
    if (value) return value;
  }
  return undefined;
}

function safeObservedUrl(
  value: unknown,
  kind: "poster" | "caption",
): string | undefined {
  const raw = cleanString(value);
  if (!raw) return undefined;

  let parsed: URL;
  try {
    parsed = new URL(raw, "https://www.youtube.com/");
  } catch {
    return undefined;
  }

  if (parsed.protocol !== "https:" || parsed.username || parsed.password) return undefined;
  for (const key of parsed.searchParams.keys()) {
    if (sensitiveLocationKey(key)) return undefined;
  }
  if (parsed.hash.length > 1) {
    const rawFragment = parsed.hash.slice(1);
    const fragment = new URLSearchParams(rawFragment);
    if (
      sensitiveLocationKey(rawFragment) ||
      [...fragment.keys()].some((key) => sensitiveLocationKey(key))
    ) {
      return undefined;
    }
  }

  const host = parsed.hostname.toLowerCase();
  if (kind === "poster") {
    if (
      host !== "ytimg.com" &&
      !host.endsWith(".ytimg.com") &&
      host !== "ggpht.com" &&
      !host.endsWith(".ggpht.com")
    ) {
      return undefined;
    }
  } else if (
    (!isYouTubeHost(host) && !isYouTubeNoCookieHost(host)) ||
    parsed.pathname !== "/api/timedtext"
  ) {
    return undefined;
  }

  return parsed.href;
}

function inferredImageMime(url: string): string | undefined {
  const pathname = new URL(url).pathname.toLowerCase();
  if (pathname.endsWith(".jpg") || pathname.endsWith(".jpeg")) return "image/jpeg";
  if (pathname.endsWith(".png")) return "image/png";
  if (pathname.endsWith(".webp")) return "image/webp";
  if (pathname.endsWith(".avif")) return "image/avif";
  return undefined;
}

function observedMime(value: unknown, prefix: "image/" | "text/" | "application/") {
  const mime = cleanString(value)?.toLowerCase();
  return mime?.startsWith(prefix) ? mime : undefined;
}

function captionMime(url: string, observed: unknown): string | undefined {
  const textMime = observedMime(observed, "text/");
  if (textMime) return textMime;
  const applicationMime = observedMime(observed, "application/");
  if (applicationMime) return applicationMime;

  const format = new URL(url).searchParams.get("fmt")?.toLowerCase();
  if (format === "vtt") return "text/vtt";
  if (format === "ttml") return "application/ttml+xml";
  if (format?.startsWith("srv")) return "text/xml";
  return undefined;
}

function parseISODuration(value: unknown): number | undefined {
  const duration = cleanString(value);
  if (!duration) return undefined;
  const match = /^P(?:(\d+(?:\.\d+)?)D)?(?:T(?:(\d+(?:\.\d+)?)H)?(?:(\d+(?:\.\d+)?)M)?(?:(\d+(?:\.\d+)?)S)?)?$/i.exec(
    duration,
  );
  if (!match) return undefined;
  const days = Number.parseFloat(match[1] ?? "0");
  const hours = Number.parseFloat(match[2] ?? "0");
  const minutes = Number.parseFloat(match[3] ?? "0");
  const seconds = Number.parseFloat(match[4] ?? "0");
  const total = days * 86_400 + hours * 3_600 + minutes * 60 + seconds;
  return total > 0 ? total : undefined;
}

function parseTimestamp(value: string): number | undefined {
  const pieces = value
    .trim()
    .split(":")
    .map((piece) => Number.parseInt(piece.trim(), 10));
  if ((pieces.length !== 2 && pieces.length !== 3) || pieces.some(Number.isNaN)) {
    return undefined;
  }
  if (pieces.length === 2) return (pieces[0] ?? 0) * 60 + (pieces[1] ?? 0);
  return (pieces[0] ?? 0) * 3_600 + (pieces[1] ?? 0) * 60 + (pieces[2] ?? 0);
}

function keywords(value: unknown): string[] {
  if (Array.isArray(value)) {
    return uniqueStrings(value.map(cleanString));
  }
  return uniqueStrings((cleanString(value) ?? "").split(",").map((part) => cleanString(part)));
}

function jsonObjectFromText(text: string, start: number): UnknownRecord | undefined {
  let depth = 0;
  let inString = false;
  let escaped = false;
  let objectStart = -1;

  for (let index = start; index < text.length; index += 1) {
    const character = text[index];
    if (inString) {
      if (escaped) escaped = false;
      else if (character === "\\") escaped = true;
      else if (character === '"') inString = false;
      continue;
    }
    if (character === '"') {
      inString = true;
      continue;
    }
    if (character === "{") {
      if (objectStart < 0) objectStart = index;
      depth += 1;
      continue;
    }
    if (character === "}" && objectStart >= 0) {
      depth -= 1;
      if (depth !== 0) continue;
      try {
        return asRecord(JSON.parse(text.slice(objectStart, index + 1)));
      } catch {
        return undefined;
      }
    }
  }
  return undefined;
}

function parseSerializedState(doc: Document, name: string): SerializedStateResult {
  let seen = false;
  let malformed = false;

  for (const script of doc.querySelectorAll<HTMLScriptElement>(
    YOUTUBE_SELECTORS.serializedStateScripts,
  )) {
    const text = script.textContent ?? "";
    const idMatches = script.id === name;
    const markerIndex = text.indexOf(name);
    if (!idMatches && markerIndex < 0) continue;
    seen = true;
    if (text.length > MAX_SERIALIZED_STATE_LENGTH) {
      malformed = true;
      continue;
    }

    if (idMatches) {
      try {
        const direct = asRecord(JSON.parse(text));
        if (direct) return { value: direct, seen, malformed };
      } catch {
        // Fall through to assignment-object extraction.
      }
    }

    const firstObject = text.indexOf("{", Math.max(0, markerIndex + name.length));
    const value = firstObject >= 0 ? jsonObjectFromText(text, firstObject) : undefined;
    if (value) return { value, seen, malformed };
    malformed = true;
  }
  return { seen, malformed };
}

function walkObjects(root: unknown, visit: (record: UnknownRecord) => void): void {
  const stack: Array<{ value: unknown; depth: number }> = [{ value: root, depth: 0 }];
  let visited = 0;
  while (stack.length && visited < MAX_WALKED_NODES) {
    const current = stack.pop();
    if (!current || current.depth > MAX_WALK_DEPTH) continue;
    visited += 1;
    if (Array.isArray(current.value)) {
      for (let index = current.value.length - 1; index >= 0; index -= 1) {
        stack.push({ value: current.value[index], depth: current.depth + 1 });
      }
      continue;
    }
    const record = asRecord(current.value);
    if (!record) continue;
    visit(record);
    const children = Object.values(record);
    for (let index = children.length - 1; index >= 0; index -= 1) {
      stack.push({ value: children[index], depth: current.depth + 1 });
    }
  }
}

function jsonLdIdentity(object: UnknownRecord): string | undefined {
  const identifier = cleanString(object.identifier);
  if (identifier) {
    const directId = validVideoId(identifier);
    if (directId) return directId;
  }
  for (const value of [
    object.identifier,
    object.url,
    object.embedUrl,
    object.contentUrl,
    object["@id"],
    asRecord(object.mainEntityOfPage)?.["@id"],
  ]) {
    const candidate = cleanString(value);
    if (!candidate) continue;
    const id = extractYouTubeVideoId(candidate);
    if (id) return id;
  }
  return undefined;
}

function jsonLdVideoObjects(doc: Document, expectedVideoId: string): JsonLdStateResult {
  const objects: UnknownRecord[] = [];
  let malformed = false;
  for (const script of doc.querySelectorAll<HTMLScriptElement>(YOUTUBE_SELECTORS.jsonLdScripts)) {
    const text = script.textContent?.trim();
    if (!text) continue;
    if (text.length > MAX_SERIALIZED_STATE_LENGTH) {
      malformed = true;
      continue;
    }
    try {
      walkObjects(JSON.parse(text), (record) => {
        const types = Array.isArray(record["@type"])
          ? record["@type"]
          : [record["@type"]];
        if (types.some((type) => type === "VideoObject")) objects.push(record);
      });
    } catch {
      malformed = true;
    }
  }
  const explicitlyMatching = objects.filter(
    (object) => jsonLdIdentity(object) === expectedVideoId,
  );
  if (explicitlyMatching.length) return { objects: explicitlyMatching, malformed };

  // Identity-free metadata is usable only when it is unambiguous. An object
  // explicitly naming another video is never allowed to describe this page.
  const identityFree = objects.filter((object) => jsonLdIdentity(object) === undefined);
  return {
    objects: objects.length === 1 && identityFree.length === 1 ? identityFree : [],
    malformed,
  };
}

function posterFromUnknown(value: unknown): YouTubePosterObservation[] {
  const values = Array.isArray(value) ? value : [value];
  const posters: YouTubePosterObservation[] = [];
  for (const candidate of values) {
    const record = asRecord(candidate);
    const rawUrl = record
      ? record.url ?? record.contentUrl ?? record.thumbnailUrl
      : candidate;
    const url = safeObservedUrl(rawUrl, "poster");
    if (!url) continue;
    posters.push({
      url,
      ...(positiveNumber(record?.width) ? { width: positiveNumber(record?.width) } : {}),
      ...(positiveNumber(record?.height) ? { height: positiveNumber(record?.height) } : {}),
      ...(inferredImageMime(url) ? { mimeType: inferredImageMime(url) } : {}),
    });
  }
  return posters;
}

function playerPosters(player: UnknownRecord | undefined): YouTubePosterObservation[] {
  const videoDetails = asRecord(player?.videoDetails);
  const thumbnail = asRecord(videoDetails?.thumbnail);
  const microformat = asRecord(player?.microformat);
  const renderer = asRecord(microformat?.playerMicroformatRenderer);
  const rendererThumbnail = asRecord(renderer?.thumbnail);
  return [
    ...posterFromUnknown(thumbnail?.thumbnails),
    ...posterFromUnknown(rendererThumbnail?.thumbnails),
  ];
}

function playerCaptionTracks(
  player: UnknownRecord | undefined,
  unsafeUrls: { count: number },
): YouTubeCaptionTrackObservation[] {
  const captions = asRecord(player?.captions);
  const trackList = asRecord(captions?.playerCaptionsTracklistRenderer);
  const tracks: YouTubeCaptionTrackObservation[] = [];
  for (const candidate of asArray(trackList?.captionTracks)) {
    const record = asRecord(candidate);
    if (!record) continue;
    const rawUrl = cleanString(record.baseUrl);
    const url = safeObservedUrl(rawUrl, "caption");
    if (!url) {
      if (rawUrl) unsafeUrls.count += 1;
      continue;
    }
    const languageCode = cleanString(record.languageCode);
    if (!languageCode) continue;
    tracks.push({
      url,
      languageCode,
      name: textRuns(record.name) ?? languageCode,
      ...(cleanString(record.kind) ? { kind: cleanString(record.kind) } : {}),
      isTranslatable: record.isTranslatable === true,
      ...(captionMime(url, record.mimeType) ? { mimeType: captionMime(url, record.mimeType) } : {}),
    });
  }
  return tracks;
}

function playerChapters(player: UnknownRecord | undefined): YouTubeChapterObservation[] {
  const chapters: YouTubeChapterObservation[] = [];
  walkObjects(player, (record) => {
    const renderer = asRecord(record.chapterRenderer);
    if (!renderer) return;
    const title = textRuns(renderer.title);
    const startMilliseconds = nonNegativeNumber(renderer.timeRangeStartMillis);
    if (!title || startMilliseconds === undefined) return;
    chapters.push({ title, startSeconds: startMilliseconds / 1_000 });
  });
  return chapters;
}

function descriptionChapters(description: string | undefined): YouTubeChapterObservation[] {
  if (!description) return [];
  const chapters: YouTubeChapterObservation[] = [];
  for (const line of description.split(/\r?\n/)) {
    const match = /^\s*((?:\d{1,2}:)?\d{1,2}:\d{2})\s+(.+?)\s*$/.exec(line);
    if (!match) continue;
    const startSeconds = parseTimestamp(match[1] ?? "");
    const title = cleanString(match[2]);
    if (startSeconds === undefined || !title) continue;
    chapters.push({ title, startSeconds });
  }
  return chapters;
}

function serializedTranscript(root: UnknownRecord | undefined): YouTubeTranscriptObservation | undefined {
  const segments: YouTubeTranscriptSegmentObservation[] = [];
  walkObjects(root, (record) => {
    const renderer = asRecord(record.transcriptSegmentRenderer);
    if (!renderer) return;
    const startMilliseconds = nonNegativeNumber(renderer.startMs);
    const endMilliseconds = nonNegativeNumber(renderer.endMs);
    const text = textRuns(renderer.snippet);
    if (startMilliseconds === undefined || !text) return;
    const durationSeconds =
      endMilliseconds !== undefined && endMilliseconds > startMilliseconds
        ? (endMilliseconds - startMilliseconds) / 1_000
        : undefined;
    segments.push({
      startSeconds: startMilliseconds / 1_000,
      ...(durationSeconds ? { durationSeconds } : {}),
      text,
    });
  });
  if (!segments.length) return undefined;
  return { source: "serialized_page_state", segments };
}

function renderedTranscript(doc: Document): YouTubeTranscriptObservation | undefined {
  const segments: YouTubeTranscriptSegmentObservation[] = [];
  for (const element of doc.querySelectorAll<HTMLElement>(YOUTUBE_SELECTORS.transcriptSegments)) {
    const startMilliseconds = nonNegativeNumber(element.dataset.startMs);
    const durationMilliseconds = positiveNumber(element.dataset.durationMs);
    const timestampText = cleanString(
      element.querySelector(YOUTUBE_SELECTORS.transcriptTimestamp)?.textContent,
    );
    const startSeconds =
      startMilliseconds !== undefined
        ? startMilliseconds / 1_000
        : timestampText
          ? parseTimestamp(timestampText)
          : undefined;
    const text = cleanString(
      element.querySelector(YOUTUBE_SELECTORS.transcriptText)?.textContent,
    );
    if (startSeconds === undefined || !text) continue;
    segments.push({
      startSeconds,
      ...(durationMilliseconds ? { durationSeconds: durationMilliseconds / 1_000 } : {}),
      text,
    });
  }
  if (!segments.length) return undefined;
  const languageCode = cleanString(
    doc.querySelector("[data-youtube-transcript-language]")?.getAttribute(
      "data-youtube-transcript-language",
    ),
  );
  return {
    source: "rendered_dom",
    ...(languageCode ? { languageCode } : {}),
    segments,
  };
}

function domCaptionTracks(
  doc: Document,
  unsafeUrls: { count: number },
): YouTubeCaptionTrackObservation[] {
  const tracks: YouTubeCaptionTrackObservation[] = [];
  for (const element of doc.querySelectorAll<HTMLTrackElement>(YOUTUBE_SELECTORS.captionTracks)) {
    const rawUrl = cleanString(element.getAttribute("src"));
    const url = safeObservedUrl(rawUrl, "caption");
    if (!url) {
      if (rawUrl) unsafeUrls.count += 1;
      continue;
    }
    const languageCode = cleanString(element.srclang);
    if (!languageCode) continue;
    tracks.push({
      url,
      languageCode,
      name: cleanString(element.label) ?? languageCode,
      kind: element.kind,
      isTranslatable: false,
      ...(captionMime(url, element.getAttribute("type"))
        ? { mimeType: captionMime(url, element.getAttribute("type")) }
        : {}),
    });
  }
  return tracks;
}

function dedupePosters(posters: readonly YouTubePosterObservation[]): YouTubePosterObservation[] {
  const byUrl = new Map<string, YouTubePosterObservation>();
  for (const poster of posters) {
    const current = byUrl.get(poster.url);
    if (!current) {
      byUrl.set(poster.url, poster);
      continue;
    }
    byUrl.set(poster.url, {
      url: poster.url,
      width: current.width ?? poster.width,
      height: current.height ?? poster.height,
      mimeType: current.mimeType ?? poster.mimeType,
    });
  }
  return [...byUrl.values()].sort((left, right) => {
    const leftArea = (left.width ?? 0) * (left.height ?? 0);
    const rightArea = (right.width ?? 0) * (right.height ?? 0);
    if (leftArea !== rightArea) return rightArea - leftArea;
    if ((left.width ?? 0) !== (right.width ?? 0)) return (right.width ?? 0) - (left.width ?? 0);
    return left.url.localeCompare(right.url);
  });
}

function dedupeCaptionTracks(
  tracks: readonly YouTubeCaptionTrackObservation[],
): YouTubeCaptionTrackObservation[] {
  const byUrl = new Map<string, YouTubeCaptionTrackObservation>();
  for (const track of tracks) if (!byUrl.has(track.url)) byUrl.set(track.url, track);
  return [...byUrl.values()];
}

function dedupeChapters(chapters: readonly YouTubeChapterObservation[]): YouTubeChapterObservation[] {
  const byKey = new Map<string, YouTubeChapterObservation>();
  for (const chapter of chapters) {
    const key = `${chapter.startSeconds}:${chapter.title}`;
    if (!byKey.has(key)) byKey.set(key, chapter);
  }
  return [...byKey.values()].sort((left, right) => left.startSeconds - right.startSeconds);
}

function jsonLdPosters(objects: readonly UnknownRecord[]): YouTubePosterObservation[] {
  return objects.flatMap((object) => posterFromUnknown(object.thumbnailUrl ?? object.thumbnail));
}

function jsonLdAuthor(objects: readonly UnknownRecord[]) {
  for (const object of objects) {
    const author = asRecord(object.author);
    const name = cleanString(author?.name);
    const identifier = cleanString(author?.identifier);
    if (name || identifier) return { name, identifier };
  }
  return {};
}

/**
 * Parses allowlisted normalized observations from current YouTube page inputs.
 * Raw script objects, selectors, headers, storage, and authentication material
 * never leave this module.
 */
export function parseYouTubePage(
  doc: Document,
  expectedVideoId: string,
): YouTubePageObservation {
  const warnings: YouTubeParserWarning[] = [];
  const sources = new Set<YouTubeObservationSource>();
  const unsafeCaptionUrls = { count: 0 };

  const playerState = parseSerializedState(doc, "ytInitialPlayerResponse");
  const initialDataState = parseSerializedState(doc, "ytInitialData");
  if (playerState.malformed || initialDataState.malformed) {
    warnings.push({
      code: "youtube.serialized_state_malformed",
      message: "Some serialized YouTube page state could not be parsed; retained other observations.",
    });
  }

  let player = playerState.value;
  const playerVideoId = cleanString(asRecord(player?.videoDetails)?.videoId);
  if (player && playerVideoId && playerVideoId !== expectedVideoId) {
    warnings.push({
      code: "youtube.player_video_id_mismatch",
      message: "Ignored serialized player metadata for a different video ID.",
    });
    player = undefined;
  }
  if (player) sources.add("player_response");

  const jsonLdState = jsonLdVideoObjects(doc, expectedVideoId);
  const jsonLd = jsonLdState.objects;
  if (jsonLdState.malformed) {
    warnings.push({
      code: "youtube.json_ld_malformed",
      message: "Some YouTube JSON-LD could not be parsed; retained other observations.",
    });
  }
  if (jsonLd.length) sources.add("json_ld");

  const metaTitle = firstElementValue(doc, YOUTUBE_SELECTORS.titleMeta);
  const metaDescription = firstElementValue(doc, YOUTUBE_SELECTORS.descriptionMeta);
  const metaChannelId = firstElementValue(doc, YOUTUBE_SELECTORS.channelIdMeta);
  const metaChannelName = firstElementValue(doc, YOUTUBE_SELECTORS.channelNameMeta);
  const metaPosterUrl = firstElementValue(doc, YOUTUBE_SELECTORS.posterMeta);
  if (metaTitle || metaDescription || metaChannelId || metaChannelName || metaPosterUrl) {
    sources.add("dom_meta");
  }

  const videoDetails = asRecord(player?.videoDetails);
  const microformat = asRecord(player?.microformat);
  const microformatRenderer = asRecord(microformat?.playerMicroformatRenderer);
  const firstJsonLd = jsonLd[0];
  const author = jsonLdAuthor(jsonLd);

  const title =
    cleanString(videoDetails?.title) ?? cleanString(firstJsonLd?.name) ?? metaTitle;
  const description =
    cleanString(videoDetails?.shortDescription) ??
    cleanString(firstJsonLd?.description) ??
    metaDescription;
  const channelId =
    cleanString(videoDetails?.channelId) ??
    cleanString(microformatRenderer?.externalChannelId) ??
    author.identifier ??
    metaChannelId;
  const channelName =
    cleanString(videoDetails?.author) ??
    cleanString(microformatRenderer?.ownerChannelName) ??
    author.name ??
    metaChannelName;
  const tags = uniqueStrings([
    ...keywords(videoDetails?.keywords),
    ...keywords(firstJsonLd?.keywords),
  ]);
  const durationSeconds =
    positiveNumber(videoDetails?.lengthSeconds) ?? parseISODuration(firstJsonLd?.duration);
  const publishedAt =
    cleanString(microformatRenderer?.publishDate) ??
    cleanString(microformatRenderer?.uploadDate) ??
    cleanString(firstJsonLd?.uploadDate) ??
    cleanString(firstJsonLd?.datePublished);

  const metaPoster = safeObservedUrl(metaPosterUrl, "poster");
  const metaWidth = positiveNumber(
    doc.querySelector(YOUTUBE_SELECTORS.posterWidthMeta)?.getAttribute("content"),
  );
  const metaHeight = positiveNumber(
    doc.querySelector(YOUTUBE_SELECTORS.posterHeightMeta)?.getAttribute("content"),
  );
  const metaMime = observedMime(
    doc.querySelector(YOUTUBE_SELECTORS.posterMimeMeta)?.getAttribute("content"),
    "image/",
  );
  const posters = dedupePosters([
    ...playerPosters(player),
    ...jsonLdPosters(jsonLd),
    ...(metaPoster
      ? [
          {
            url: metaPoster,
            ...(metaWidth ? { width: metaWidth } : {}),
            ...(metaHeight ? { height: metaHeight } : {}),
            ...(metaMime ?? inferredImageMime(metaPoster)
              ? { mimeType: metaMime ?? inferredImageMime(metaPoster) }
              : {}),
          },
        ]
      : []),
  ]);

  const captionTracks = dedupeCaptionTracks([
    ...playerCaptionTracks(player, unsafeCaptionUrls),
    ...domCaptionTracks(doc, unsafeCaptionUrls),
  ]);
  if (unsafeCaptionUrls.count) {
    warnings.push({
      code: "youtube.caption_url_rejected",
      message: "Ignored a caption locator that was unsafe or outside the supported timed-text boundary.",
    });
  }

  const domTranscript = renderedTranscript(doc);
  const stateTranscript = serializedTranscript(initialDataState.value);
  const transcript = domTranscript ?? stateTranscript;
  if (domTranscript) sources.add("rendered_transcript");
  else if (stateTranscript) sources.add("serialized_page_state");

  const chapters = dedupeChapters([
    ...playerChapters(player),
    ...descriptionChapters(description),
  ]);

  return {
    ...(title ? { title } : {}),
    ...(description ? { description } : {}),
    ...(channelId ? { channelId } : {}),
    ...(channelName ? { channelName } : {}),
    tags,
    chapters,
    ...(durationSeconds ? { durationSeconds } : {}),
    ...(publishedAt ? { publishedAt } : {}),
    posters,
    captionTracks,
    ...(transcript ? { transcript } : {}),
    sources: [...sources],
    warnings,
  };
}
