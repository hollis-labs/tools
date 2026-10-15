/**
 * Reddit page inspection. Everything here observes the rendered DOM only: no
 * network, no serialized-state replay, and no reconstruction of comments the
 * page did not render. Comments Reddit hides behind "load more" are reported
 * as an explicit warning rather than silently implied to be absent.
 */

import type { AdapterExtractionWarning, PageCaptureContext } from "../../types";
import { sanitizeCapturePageUrl } from "../../urlSafety";
import type { RedditCommentObservation } from "./comments";
import {
  OLD_REDDIT_CHILD_LISTING_SELECTOR,
  OLD_REDDIT_COMMENT_BODY_SELECTORS,
  OLD_REDDIT_COMMENT_SELECTOR,
  OLD_REDDIT_EXCLUDED_COMMENT_SELECTOR,
  OLD_REDDIT_POST_BODY_SELECTORS,
  OLD_REDDIT_POST_SELECTORS,
  SHREDDIT_COMMENT_BODY_SELECTORS,
  SHREDDIT_COMMENT_SELECTOR,
  SHREDDIT_MORE_COMMENTS_SELECTOR,
  SHREDDIT_POST_SELECTOR,
} from "./selectors";

export const REDDIT_PARSER_VERSION = "reddit-dom.v1";

export type RedditSurface = "shreddit" | "old_reddit" | "unknown";

export interface RedditPostObservation {
  subreddit?: string;
  author?: string;
  score?: number;
  createdAt?: string;
  commentCount?: number;
  title?: string;
}

export interface RedditPageObservation {
  surface: RedditSurface;
  post: RedditPostObservation;
  /**
   * Inner HTML of the post's own region. Readability scores a long comment
   * thread above a short self-post and would otherwise return the comments as
   * the article, silently dropping the post the user actually clipped.
   */
  postBodyHtml?: string;
  comments: readonly RedditCommentObservation[];
  /** True when the page still had unexpanded "load more" affordances. */
  moreAvailable: boolean;
  warnings: readonly AdapterExtractionWarning[];
}

const REDDIT_HOSTS = new Set([
  "reddit.com",
  "redditmedia.com",
  "redd.it",
]);

/** `/r/<sub>/comments/<id>/<slug>` and the short `/comments/<id>` form. */
const POST_PATH = /\/comments\/([a-z0-9]{4,12})(?:\/|$)/i;
const FULLNAME = /^t(?:1|3)_([a-z0-9]{4,12})$/i;

function warning(
  code: string,
  severity: AdapterExtractionWarning["severity"],
  stage: AdapterExtractionWarning["stage"],
  message: string,
): AdapterExtractionWarning {
  return { code, severity, stage, message };
}

export function isRedditHostname(hostname: string): boolean {
  const normalized = hostname.toLowerCase().replace(/^www\./, "");
  if (REDDIT_HOSTS.has(normalized)) return true;
  return [...REDDIT_HOSTS].some((host) => normalized.endsWith(`.${host}`));
}

export function sanitizeRedditPageUrl(rawUrl: string): string | undefined {
  try {
    const url = new URL(sanitizeCapturePageUrl(rawUrl));
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    if (url.username || url.password) return undefined;
    return url.href;
  } catch {
    return undefined;
  }
}

function idFromFullname(value: string | null | undefined): string | undefined {
  const match = value?.trim().match(FULLNAME);
  return match ? match[1]!.toLowerCase() : undefined;
}

export function postIdFromUrl(rawUrl: string): string | undefined {
  try {
    const match = new URL(rawUrl).pathname.match(POST_PATH);
    return match ? match[1]!.toLowerCase() : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Resolves the post this page is about. The URL is authoritative because it is
 * the thing the user chose to clip; DOM markers only fill in for client-side
 * navigations that left the address bar on a listing route.
 */
export function resolveRedditPostId(context: PageCaptureContext): string | undefined {
  if (!isRedditHostname(context.location.hostname)) return undefined;

  const fromUrl = postIdFromUrl(context.location.href);
  if (fromUrl) return fromUrl;

  const shreddit = context.document.querySelector(SHREDDIT_POST_SELECTOR);
  const fromShreddit =
    idFromFullname(shreddit?.getAttribute("id")) ??
    postIdFromUrl(
      new URL(shreddit?.getAttribute("permalink") ?? "", context.location.href).href,
    );
  if (fromShreddit) return fromShreddit;

  for (const selector of OLD_REDDIT_POST_SELECTORS) {
    const fromOld = idFromFullname(
      context.document.querySelector(selector)?.getAttribute("data-fullname"),
    );
    if (fromOld) return fromOld;
  }
  return undefined;
}

/**
 * The subreddit-free comments route. Reddit resolves it for every post, so the
 * canonical URL stays stable even when the page never exposed its subreddit.
 */
export function canonicalRedditPostUrl(postId: string): string {
  return `https://www.reddit.com/comments/${postId}/`;
}

function text(value: string | null | undefined): string {
  return (value ?? "").replace(/\s+/g, " ").trim();
}

function integer(value: string | null | undefined): number | undefined {
  const parsed = Number.parseInt((value ?? "").trim(), 10);
  return Number.isSafeInteger(parsed) ? parsed : undefined;
}

function timestamp(value: string | null | undefined): string | undefined {
  const raw = value?.trim();
  if (!raw) return undefined;
  const parsed = Date.parse(raw);
  return Number.isNaN(parsed) ? undefined : new Date(parsed).toISOString();
}

function normalizeAuthor(value: string | null | undefined): string {
  const author = text(value).replace(/^\/?u\//i, "");
  return author || "[unknown]";
}

/**
 * Converts a comment body element to plain Markdown-ish text. Reddit bodies are
 * already short-form Markdown rendered to HTML; taking `textContent` per block
 * keeps paragraph breaks without importing a second HTML pipeline into the
 * adapter.
 */
function bodyText(element: Element | null | undefined): string {
  if (!element) return "";
  const blocks: string[] = [];
  for (const child of element.children) {
    const value = (child.textContent ?? "").trim();
    if (value) blocks.push(value.replace(/[ \t]+/g, " "));
  }
  if (blocks.length) return blocks.join("\n\n");
  return (element.textContent ?? "").trim().replace(/[ \t]+/g, " ");
}

function firstMatch(root: Element, selectors: readonly string[]): Element | null {
  for (const selector of selectors) {
    const found = root.querySelector(selector);
    if (found) return found;
  }
  return null;
}

function absoluteUrl(raw: string | null | undefined, baseUrl: string): string | undefined {
  const value = raw?.trim();
  if (!value) return undefined;
  try {
    const resolved = new URL(value, baseUrl);
    if (resolved.protocol !== "http:" && resolved.protocol !== "https:") return undefined;
    return sanitizeRedditPageUrl(resolved.href);
  } catch {
    return undefined;
  }
}

function shredditComments(
  context: PageCaptureContext,
  baseUrl: string,
): RedditCommentObservation[] {
  const elements = [...context.document.querySelectorAll(SHREDDIT_COMMENT_SELECTOR)];
  const byId = new Map<string, RedditCommentObservation>();
  const roots: RedditCommentObservation[] = [];
  // shreddit renders the tree flat and encodes structure in `depth` and
  // `parentid`, so the stack tracks the most recent ancestor at each depth.
  const openAncestors: RedditCommentObservation[] = [];

  for (const [index, element] of elements.entries()) {
    const thingId = text(element.getAttribute("thingid"));
    const id = thingId || `comment-${index}`;
    const depth = Math.max(0, integer(element.getAttribute("depth")) ?? 0);
    const comment: RedditCommentObservation = {
      id,
      author: normalizeAuthor(element.getAttribute("author")),
      depth,
      body: bodyText(firstMatch(element, SHREDDIT_COMMENT_BODY_SELECTORS)),
      replies: [],
    };
    const score = integer(element.getAttribute("score"));
    if (score !== undefined) comment.score = score;
    const permalink = absoluteUrl(element.getAttribute("permalink"), baseUrl);
    if (permalink) comment.permalink = permalink;
    const createdAt = timestamp(
      element.getAttribute("created-timestamp") ?? element.querySelector("time")?.getAttribute("datetime"),
    );
    if (createdAt) comment.createdAt = createdAt;
    const distinguished = text(element.getAttribute("distinguished"));
    if (distinguished) comment.distinguished = distinguished;
    if (element.hasAttribute("collapsed")) comment.collapsed = true;

    const parentId = idFromFullname(element.getAttribute("parentid"));
    const parentByAttribute = parentId ? byId.get(`t1_${parentId}`) : undefined;
    const parent = parentByAttribute ?? (depth > 0 ? openAncestors[depth - 1] : undefined);
    if (parent) parent.replies.push(comment);
    else roots.push(comment);

    byId.set(id, comment);
    openAncestors[depth] = comment;
    openAncestors.length = depth + 1;
  }
  return roots;
}

function oldRedditComments(
  root: Element,
  depth: number,
  baseUrl: string,
): RedditCommentObservation[] {
  const comments: RedditCommentObservation[] = [];
  for (const element of root.children) {
    if (!element.matches(OLD_REDDIT_COMMENT_SELECTOR)) continue;
    if (element.matches(OLD_REDDIT_EXCLUDED_COMMENT_SELECTOR)) continue;

    const entry = element.querySelector(".entry");
    const comment: RedditCommentObservation = {
      id: text(element.getAttribute("data-fullname")) || `comment-${depth}-${comments.length}`,
      author: normalizeAuthor(element.getAttribute("data-author")),
      depth,
      body: bodyText(firstMatch(element, OLD_REDDIT_COMMENT_BODY_SELECTORS)),
      replies: [],
    };
    const score = integer(
      element.querySelector(".score.unvoted")?.getAttribute("title") ??
        element.getAttribute("data-score"),
    );
    if (score !== undefined) comment.score = score;
    const permalink = absoluteUrl(element.getAttribute("data-permalink"), baseUrl);
    if (permalink) comment.permalink = permalink;
    const createdAt = timestamp(entry?.querySelector("time")?.getAttribute("datetime"));
    if (createdAt) comment.createdAt = createdAt;
    if (element.classList.contains("collapsed")) comment.collapsed = true;

    for (const child of element.querySelectorAll(`:scope > ${OLD_REDDIT_CHILD_LISTING_SELECTOR}`)) {
      for (const listing of child.children) {
        comment.replies.push(...oldRedditComments(listing, depth + 1, baseUrl));
      }
    }
    comments.push(comment);
  }
  return comments;
}

function shredditPost(element: Element, baseUrl: string): RedditPostObservation {
  const post: RedditPostObservation = {};
  const subreddit = text(element.getAttribute("subreddit-prefixed-name")).replace(/^r\//i, "");
  if (subreddit) post.subreddit = subreddit;
  const author = text(element.getAttribute("author"));
  if (author) post.author = normalizeAuthor(author);
  const score = integer(element.getAttribute("score"));
  if (score !== undefined) post.score = score;
  const commentCount = integer(element.getAttribute("comment-count"));
  if (commentCount !== undefined) post.commentCount = commentCount;
  const createdAt = timestamp(element.getAttribute("created-timestamp"));
  if (createdAt) post.createdAt = createdAt;
  const title = text(element.getAttribute("post-title"));
  if (title) post.title = title;
  void baseUrl;
  return post;
}

function oldRedditPost(element: Element): RedditPostObservation {
  const post: RedditPostObservation = {};
  const subreddit = text(element.getAttribute("data-subreddit"));
  if (subreddit) post.subreddit = subreddit;
  const author = text(element.getAttribute("data-author"));
  if (author) post.author = normalizeAuthor(author);
  const score = integer(element.getAttribute("data-score"));
  if (score !== undefined) post.score = score;
  const commentCount = integer(element.getAttribute("data-comments-count"));
  if (commentCount !== undefined) post.commentCount = commentCount;
  const createdAt = timestamp(element.querySelector("time")?.getAttribute("datetime"));
  if (createdAt) post.createdAt = createdAt;
  const title = text(element.querySelector("a.title")?.textContent);
  if (title) post.title = title;
  return post;
}

/** Inspects the rendered page once and reports the thread it actually showed. */
export function inspectRedditPage(context: PageCaptureContext): RedditPageObservation {
  const warnings: AdapterExtractionWarning[] = [];
  const baseUrl = sanitizeRedditPageUrl(context.location.href) ?? "https://www.reddit.com/";
  const shredditPostElement = context.document.querySelector(SHREDDIT_POST_SELECTOR);
  const oldPostElement = OLD_REDDIT_POST_SELECTORS.map((selector) =>
    context.document.querySelector(selector),
  ).find((element): element is Element => Boolean(element));

  let surface: RedditSurface = "unknown";
  let post: RedditPostObservation = {};
  let postBodyHtml: string | undefined;
  let comments: readonly RedditCommentObservation[] = [];

  if (context.document.querySelector(SHREDDIT_COMMENT_SELECTOR) || shredditPostElement) {
    surface = "shreddit";
    if (shredditPostElement) {
      post = shredditPost(shredditPostElement, baseUrl);
      postBodyHtml = shredditPostElement.innerHTML;
    }
    comments = shredditComments(context, baseUrl);
  } else if (oldPostElement || context.document.querySelector(OLD_REDDIT_COMMENT_SELECTOR)) {
    surface = "old_reddit";
    if (oldPostElement) {
      post = oldRedditPost(oldPostElement);
      postBodyHtml = firstMatch(oldPostElement, OLD_REDDIT_POST_BODY_SELECTORS)?.innerHTML;
    }
    const area = context.document.querySelector(".commentarea .sitetable") ??
      context.document.querySelector(".commentarea");
    comments = area ? oldRedditComments(area, 0, baseUrl) : [];
  }

  if (surface === "unknown") {
    warnings.push(
      warning(
        "reddit.surface_unrecognized",
        "warning",
        "provider_metadata",
        "This Reddit page did not match a known post or comment layout; only generic page extraction ran.",
      ),
    );
  } else if (!comments.length) {
    warnings.push(
      warning(
        "reddit.comments_absent",
        "info",
        "provider_metadata",
        "No rendered comments were observed on this Reddit page.",
      ),
    );
  }

  const moreAvailable =
    Boolean(context.document.querySelector(SHREDDIT_MORE_COMMENTS_SELECTOR)) ||
    Boolean(context.document.querySelector(".morecomments, .morerecursion"));
  if (moreAvailable) {
    warnings.push(
      warning(
        "reddit.comments_unexpanded",
        "info",
        "provider_metadata",
        "The page still had unexpanded replies; capture reflects only what Reddit had rendered.",
      ),
    );
  }

  return {
    surface,
    post,
    ...(postBodyHtml ? { postBodyHtml } : {}),
    comments,
    moreAvailable,
    warnings,
  };
}
