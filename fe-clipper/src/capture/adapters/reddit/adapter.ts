import { genericPageCaptureAdapter } from "../generic";
import type {
  AdapterCapability,
  AdapterDocumentObservation,
  AdapterMediaCandidate,
  AdapterMetadataValue,
  AdapterProviderMetadataExtension,
  AdapterStageResult,
  PageCaptureAdapter,
  PageCaptureContext,
} from "../../types";
import { ADAPTER_MATCH_CONFIDENCE } from "../../types";
import { extractPage } from "@/lib/extractPage";
import {
  applyCommentBudget,
  commentsMetadata,
  renderCommentsMarkdown,
  type RedditCommentBudgetResult,
} from "./comments";
import {
  REDDIT_PARSER_VERSION,
  canonicalRedditPostUrl,
  inspectRedditPage,
  resolveRedditPostId,
  sanitizeRedditPageUrl,
  type RedditPageObservation,
} from "./parser";

export const REDDIT_ADAPTER_VERSION = "1.0.0";
export const REDDIT_EXTENSION_SCHEMA_VERSION = "1";

const REDDIT_CAPABILITIES = [
  "canonical_identity",
  "title",
  "description",
  "body",
  "selection",
  "provider_metadata",
] as const satisfies readonly AdapterCapability[];

interface RedditCapture {
  page: RedditPageObservation;
  budget: RedditCommentBudgetResult;
}

// Both the document and provider-metadata stages need the same trimmed thread,
// and inspecting a long thread twice is measurably expensive on large posts.
const inspected = new WeakMap<Document, RedditCapture>();

function capture(context: PageCaptureContext): RedditCapture {
  const cached = inspected.get(context.document);
  if (cached) return cached;
  const page = inspectRedditPage(context);
  const result: RedditCapture = { page, budget: applyCommentBudget(page.comments) };
  inspected.set(context.document, result);
  return result;
}

/**
 * Runs the shared extraction pipeline over just the post's region. The scoped
 * document is detached, so Readability cannot reach the comment tree.
 */
function postBodyMarkdown(
  context: PageCaptureContext,
  postBodyHtml: string | undefined,
): string {
  if (!postBodyHtml?.trim()) return "";
  try {
    const scoped = context.document.implementation.createHTMLDocument("");
    scoped.body.innerHTML = postBodyHtml;
    return extractPage(scoped, {
      url: sanitizeRedditPageUrl(context.location.href) ?? context.location.href,
      selectionText: "",
    }).contentMarkdown.trim();
  } catch {
    // A scoped re-parse is an optimization over the generic body, never a
    // requirement; the caller falls back to whole-page extraction.
    return "";
  }
}

function observedDocumentCapabilities(
  document: AdapterDocumentObservation,
): readonly AdapterCapability[] {
  const observed: AdapterCapability[] = [];
  if (document.title) observed.push("title");
  if (document.description) observed.push("description");
  if (document.contentMarkdown) observed.push("body");
  if (document.selection) observed.push("selection");
  return observed;
}

function postMetadata(page: RedditPageObservation): Record<string, AdapterMetadataValue> {
  const { post } = page;
  return {
    ...(post.subreddit ? { subreddit: post.subreddit } : {}),
    ...(post.author ? { author: post.author } : {}),
    ...(post.score !== undefined ? { score: post.score } : {}),
    ...(post.commentCount !== undefined ? { commentCount: post.commentCount } : {}),
    ...(post.createdAt ? { createdAt: post.createdAt } : {}),
    ...(post.title ? { title: post.title } : {}),
  };
}

export const redditPageCaptureAdapter: PageCaptureAdapter = {
  id: "reddit",
  version: REDDIT_ADAPTER_VERSION,

  matches(context) {
    // Only post pages carry a thread. Subreddit listings and profiles stay
    // with the generic adapter, which already handles them correctly.
    return resolveRedditPostId(context)
      ? ADAPTER_MATCH_CONFIDENCE.exact
      : ADAPTER_MATCH_CONFIDENCE.none;
  },

  identify(context) {
    const postId = resolveRedditPostId(context);
    if (!postId) throw new Error("Reddit post identity was unavailable.");
    const submittedUrl = sanitizeRedditPageUrl(context.location.href);
    if (!submittedUrl) throw new Error("Reddit page URL was unavailable.");
    return {
      observation: {
        submittedUrl,
        canonicalUrl: canonicalRedditPostUrl(postId),
        provider: "reddit",
        providerItemId: postId,
        segmentKey: "root",
      },
      observedCapabilities: ["canonical_identity"],
    };
  },

  /**
   * The post body runs through the established Readability/Turndown pipeline,
   * but scoped to the post's own region: on a busy thread Readability scores
   * the comments above a short self-post and would otherwise return the
   * comments as the article, dropping the post the user actually clipped.
   *
   * The captured thread is then appended as a Markdown section, because the
   * document body is the only capture field Fragments Engine full-text
   * indexes. Comment text that stays in metadata alone is unsearchable.
   */
  extractDocument(context): AdapterStageResult<AdapterDocumentObservation> {
    const generic = genericPageCaptureAdapter.extractDocument(context);
    const { page, budget } = capture(context);
    const comments = renderCommentsMarkdown(budget, page.moreAvailable);
    const post = postBodyMarkdown(context, page.postBodyHtml);
    const body = (post || generic.observation.contentMarkdown).trim();
    if (!comments) {
      if (body === generic.observation.contentMarkdown.trim()) return generic;
      const postOnly = { ...generic.observation, contentMarkdown: body };
      return {
        observation: postOnly,
        observedCapabilities: observedDocumentCapabilities(postOnly),
        warnings: generic.warnings,
      };
    }

    const observation: AdapterDocumentObservation = {
      ...generic.observation,
      contentMarkdown: body ? `${body}\n\n${comments}` : comments,
    };
    return {
      observation,
      observedCapabilities: observedDocumentCapabilities(observation),
      warnings: generic.warnings,
    };
  },

  /** Reddit posts still carry ordinary page media; generic handling stands. */
  extractMedia(context): AdapterStageResult<readonly AdapterMediaCandidate[]> {
    return genericPageCaptureAdapter.extractMedia(context);
  },

  extractProviderMetadata(
    context,
  ): AdapterStageResult<readonly AdapterProviderMetadataExtension[]> {
    const postId = resolveRedditPostId(context);
    if (!postId) return { observation: [] };
    const { page, budget } = capture(context);
    const warnings = [...page.warnings];

    if (budget.truncated) {
      warnings.push({
        code: "reddit.comments_truncated",
        severity: "warning",
        stage: "provider_metadata",
        message: `Retained ${budget.kept} of ${budget.observed} rendered comments; the remainder exceeded this capture's size budget.`,
      });
    }

    const data: Record<string, AdapterMetadataValue> = {
      postId,
      surface: page.surface,
      parserVersion: REDDIT_PARSER_VERSION,
      post: postMetadata(page),
      commentsObserved: budget.observed,
      commentsCaptured: budget.kept,
      commentsTruncated: budget.truncated,
      commentsUnexpanded: page.moreAvailable,
      ...(budget.comments.length ? { comments: commentsMetadata(budget.comments) } : {}),
    };

    return {
      observation: [
        {
          namespace: "provider.reddit",
          schemaVersion: REDDIT_EXTENSION_SCHEMA_VERSION,
          data,
        },
      ],
      observedCapabilities: page.surface === "unknown" ? [] : ["provider_metadata"],
      warnings,
    };
  },

  reportCapabilities() {
    return REDDIT_CAPABILITIES;
  },
};
