/**
 * X/Twitter page inspection. Observes the rendered DOM only: no network and no
 * serialized-state replay. What the client has not mounted does not exist here,
 * which is exactly why the adapter runs a scroll pre-pass first.
 */

import type { AdapterExtractionWarning, PageCaptureContext } from "../../types";
import { sanitizeCapturePageUrl } from "../../urlSafety";
import type { TwitterTweetObservation } from "./replies";
import {
  CONVERSATION_TIMELINE_SELECTORS,
  MORE_REPLIES_SELECTORS,
  PROMOTED_TWEET_ANCESTOR_SELECTOR,
  SHOW_MORE_TEXT,
  TWEET_SELECTOR,
  TWEET_TEXT_SELECTORS,
  TWEET_USER_NAME_SELECTOR,
} from "./selectors";

export const TWITTER_PARSER_VERSION = "x-dom.v1";

const TWITTER_HOSTS = new Set(["x.com", "twitter.com"]);
const STATUS_PATH = /\/status(?:es)?\/(\d{5,25})(?:\/|$)/;
const HANDLE = /^[A-Za-z0-9_]{1,15}$/;

export interface TwitterPageObservation {
  root?: TwitterTweetObservation;
  /** Inner HTML of the root tweet's article, for scoped body extraction. */
  rootHtml?: string;
  replies: readonly TwitterTweetObservation[];
  /** True when the client still showed an unrendered-replies affordance. */
  moreAvailable: boolean;
  warnings: readonly AdapterExtractionWarning[];
}

export function isTwitterHostname(hostname: string): boolean {
  const normalized = hostname.toLowerCase().replace(/^(?:www|mobile|m)\./, "");
  return TWITTER_HOSTS.has(normalized);
}

export function sanitizeTwitterPageUrl(rawUrl: string): string | undefined {
  try {
    const url = new URL(sanitizeCapturePageUrl(rawUrl));
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    if (url.username || url.password) return undefined;
    return url.href;
  } catch {
    return undefined;
  }
}

export function statusIdFromUrl(rawUrl: string): string | undefined {
  try {
    return new URL(rawUrl).pathname.match(STATUS_PATH)?.[1];
  } catch {
    return undefined;
  }
}

export function resolveTwitterStatusId(context: PageCaptureContext): string | undefined {
  if (!isTwitterHostname(context.location.hostname)) return undefined;
  return statusIdFromUrl(context.location.href);
}

/**
 * The account-independent permalink. X resolves `/i/web/status/<id>` for every
 * status, so the canonical URL survives a handle change, which an
 * account-scoped URL would not.
 */
export function canonicalTwitterStatusUrl(statusId: string): string {
  return `https://x.com/i/web/status/${statusId}`;
}

function text(value: string | null | undefined): string {
  return (value ?? "").replace(/\s+/g, " ").trim();
}

function firstMatch(root: Element, selectors: readonly string[]): Element | null {
  for (const selector of selectors) {
    const found = root.querySelector(selector);
    if (found) return found;
  }
  return null;
}

/**
 * Reads a tweet body. X splits text across spans, emoji images, and link
 * anchors inside one container, so `textContent` on that container is the
 * faithful reading; per-block splitting would fragment ordinary sentences.
 */
function tweetText(article: Element): string {
  const container = firstMatch(article, TWEET_TEXT_SELECTORS);
  if (!container) return "";
  return (container.textContent ?? "").trim().replace(/[ \t]+/g, " ");
}

/**
 * Reads the display name and handle.
 *
 * These are read from leaf elements rather than from the block's `textContent`.
 * X renders the name and handle as adjacent spans with no separator between
 * them, so the concatenated text of the block is "Display Name@handle" — one
 * token that no split can reliably divide. Each leaf, in contrast, holds
 * exactly one value.
 */
function tweetAuthor(article: Element): { handle: string; displayName?: string } {
  const block = article.querySelector(TWEET_USER_NAME_SELECTOR);
  if (!block) return { handle: "unknown" };

  let handle = "";
  let displayName = "";
  for (const leaf of block.querySelectorAll("span, div, a")) {
    if (leaf.childElementCount > 0) continue;
    const value = text(leaf.textContent);
    if (!value || value === "·") continue;
    if (value.startsWith("@")) {
      const candidate = value.slice(1);
      if (!handle && HANDLE.test(candidate)) handle = candidate;
      continue;
    }
    if (!displayName) displayName = value;
  }

  if (!handle) {
    // Fall back to a profile link, whose first path segment is the handle.
    for (const anchor of block.querySelectorAll('a[href^="/"]')) {
      const candidate = (anchor.getAttribute("href") ?? "").split("/").filter(Boolean)[0] ?? "";
      if (HANDLE.test(candidate)) {
        handle = candidate;
        break;
      }
    }
  }

  return {
    handle: handle || "unknown",
    ...(displayName ? { displayName } : {}),
  };
}

function tweetPermalink(article: Element, baseUrl: string): string | undefined {
  for (const anchor of article.querySelectorAll("a[href]")) {
    const href = anchor.getAttribute("href") ?? "";
    if (!STATUS_PATH.test(href)) continue;
    try {
      const resolved = new URL(href, baseUrl);
      if (resolved.protocol !== "http:" && resolved.protocol !== "https:") continue;
      return sanitizeTwitterPageUrl(resolved.href);
    } catch {
      continue;
    }
  }
  return undefined;
}

function tweetTimestamp(article: Element): string | undefined {
  const value = article.querySelector("time[datetime]")?.getAttribute("datetime")?.trim();
  if (!value) return undefined;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? undefined : new Date(parsed).toISOString();
}

function isPromoted(article: Element): boolean {
  return Boolean(article.closest(PROMOTED_TWEET_ANCESTOR_SELECTOR));
}

/** Reads one mounted article. Returns undefined for ads and empty shells. */
export function readTweet(
  article: Element,
  baseUrl: string,
  rootStatusId: string,
  index: number,
): TwitterTweetObservation | undefined {
  if (isPromoted(article)) return undefined;

  const permalink = tweetPermalink(article, baseUrl);
  const statusId = permalink ? statusIdFromUrl(permalink) : undefined;
  const body = tweetText(article);
  const author = tweetAuthor(article);

  // An article with neither identity nor text is a placeholder shell that X
  // mounted before its data arrived.
  if (!statusId && !body) return undefined;

  const createdAt = tweetTimestamp(article);
  return {
    id: statusId ?? `tweet-${index}`,
    handle: author.handle,
    ...(author.displayName ? { displayName: author.displayName } : {}),
    text: body,
    ...(createdAt ? { createdAt } : {}),
    ...(permalink ? { permalink } : {}),
    isRoot: statusId === rootStatusId,
  };
}

/** The conversation region, falling back to the whole document. */
export function conversationRoot(context: PageCaptureContext): ParentNode {
  return (
    CONVERSATION_TIMELINE_SELECTORS.map((selector) =>
      context.document.querySelector(selector),
    ).find((element): element is Element => Boolean(element)) ?? context.document
  );
}

export function hasUnrenderedReplies(context: PageCaptureContext): boolean {
  for (const selector of MORE_REPLIES_SELECTORS) {
    for (const element of context.document.querySelectorAll(selector)) {
      if (SHOW_MORE_TEXT.test(element.textContent ?? "")) return true;
    }
  }
  return false;
}

/** Reads every currently mounted tweet in display order. */
export function readMountedTweets(
  context: PageCaptureContext,
  rootStatusId: string,
): TwitterTweetObservation[] {
  const baseUrl = sanitizeTwitterPageUrl(context.location.href) ?? "https://x.com/";
  const tweets: TwitterTweetObservation[] = [];
  for (const [index, article] of [
    ...conversationRoot(context).querySelectorAll(TWEET_SELECTOR),
  ].entries()) {
    const tweet = readTweet(article, baseUrl, rootStatusId, index);
    if (tweet) tweets.push(tweet);
  }
  return tweets;
}

/** Inner HTML of the root tweet's article, for scoped body extraction. */
export function rootTweetHtml(
  context: PageCaptureContext,
  rootStatusId: string,
): string | undefined {
  const baseUrl = sanitizeTwitterPageUrl(context.location.href) ?? "https://x.com/";
  for (const [index, article] of [
    ...conversationRoot(context).querySelectorAll(TWEET_SELECTOR),
  ].entries()) {
    const tweet = readTweet(article, baseUrl, rootStatusId, index);
    if (tweet?.isRoot) return article.innerHTML;
  }
  return undefined;
}
