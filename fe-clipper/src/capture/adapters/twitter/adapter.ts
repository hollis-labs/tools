import { genericPageCaptureAdapter } from "../generic";
import type {
  AdapterCapability,
  AdapterDocumentObservation,
  AdapterExtractionWarning,
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
  browserScrollEnvironment,
  harvestTwitterConversation,
  harvestWarnings,
  readTwitterHarvest,
  recordTwitterHarvest,
  type TwitterHarvest,
} from "./harvest";
import {
  TWITTER_PARSER_VERSION,
  canonicalTwitterStatusUrl,
  hasUnrenderedReplies,
  readMountedTweets,
  resolveTwitterStatusId,
  rootTweetHtml,
  sanitizeTwitterPageUrl,
} from "./parser";
import {
  applyReplyBudget,
  renderRepliesMarkdown,
  repliesMetadata,
  type TwitterReplyBudgetResult,
  type TwitterTweetObservation,
} from "./replies";

export const TWITTER_ADAPTER_VERSION = "1.0.0";
export const TWITTER_EXTENSION_SCHEMA_VERSION = "1";

const TWITTER_CAPABILITIES = [
  "canonical_identity",
  "title",
  "description",
  "body",
  "selection",
  "provider_metadata",
] as const satisfies readonly AdapterCapability[];

interface TwitterCapture {
  root?: TwitterTweetObservation;
  budget: TwitterReplyBudgetResult;
  moreAvailable: boolean;
  harvested: boolean;
  warnings: readonly AdapterExtractionWarning[];
}

const captures = new WeakMap<Document, TwitterCapture>();

/**
 * Resolves the conversation for this capture, preferring the scroll pre-pass's
 * accumulated result. Falling back to a plain DOM read keeps the adapter
 * correct when the pre-pass was skipped or failed — in a fixture test, or when
 * the page never scrolled — rather than reporting an empty conversation.
 */
function capture(context: PageCaptureContext): TwitterCapture {
  const cached = captures.get(context.document);
  if (cached) return cached;

  const statusId = resolveTwitterStatusId(context);
  const harvest: TwitterHarvest | undefined = readTwitterHarvest(context.document);
  const tweets = harvest?.tweets ?? (statusId ? readMountedTweets(context, statusId) : []);
  const root = tweets.find((tweet) => tweet.isRoot);
  const replies = tweets.filter((tweet) => !tweet.isRoot);

  const moreAvailable = harvest?.moreAvailable ?? hasUnrenderedReplies(context);
  const warnings: AdapterExtractionWarning[] = harvest ? harvestWarnings(harvest) : [];
  if (moreAvailable) {
    warnings.push({
      code: "twitter.replies_unrendered",
      severity: "info",
      stage: "provider_metadata",
      message:
        "X still offered replies it had not rendered; capture reflects only the conversation it produced.",
    });
  }
  if (!root) {
    warnings.push({
      code: "twitter.root_tweet_absent",
      severity: "warning",
      stage: "document",
      message:
        "The captured status was not found among the rendered tweets; the page body was used instead.",
    });
  }
  if (!replies.length) {
    warnings.push({
      code: "twitter.replies_absent",
      severity: "info",
      stage: "provider_metadata",
      message: "No replies were observed in this conversation.",
    });
  }

  const result: TwitterCapture = {
    ...(root ? { root } : {}),
    budget: applyReplyBudget(replies),
    moreAvailable,
    harvested: Boolean(harvest),
    warnings,
  };
  captures.set(context.document, result);
  return result;
}

/** Runs the shared extraction pipeline over just the root tweet's article. */
function rootBodyMarkdown(context: PageCaptureContext, html: string | undefined): string {
  if (!html?.trim()) return "";
  try {
    const scoped = context.document.implementation.createHTMLDocument("");
    scoped.body.innerHTML = html;
    return extractPage(scoped, {
      url: sanitizeTwitterPageUrl(context.location.href) ?? context.location.href,
      selectionText: "",
    }).contentMarkdown.trim();
  } catch {
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

export const twitterPageCaptureAdapter: PageCaptureAdapter = {
  id: "twitter",
  version: TWITTER_ADAPTER_VERSION,

  matches(context) {
    // Only status pages carry a conversation. Profiles, home, and search stay
    // with the generic adapter.
    return resolveTwitterStatusId(context)
      ? ADAPTER_MATCH_CONFIDENCE.exact
      : ADAPTER_MATCH_CONFIDENCE.none;
  },

  /**
   * Walks the virtualized conversation so the synchronous stages below see the
   * whole thread rather than the handful of replies that happened to be on
   * screen. The page is scrolled and then put back.
   */
  async prepare(context) {
    const statusId = resolveTwitterStatusId(context);
    const view = context.document.defaultView;
    if (!statusId || !view) return [];

    const harvest = await harvestTwitterConversation(
      context,
      statusId,
      browserScrollEnvironment(view),
    );
    recordTwitterHarvest(context.document, harvest);
    // A caller that read this document before preparing it would otherwise
    // keep that pre-walk reading; the walk is always the better observation.
    captures.delete(context.document);
    return [];
  },

  identify(context) {
    const statusId = resolveTwitterStatusId(context);
    if (!statusId) throw new Error("X status identity was unavailable.");
    const submittedUrl = sanitizeTwitterPageUrl(context.location.href);
    if (!submittedUrl) throw new Error("X page URL was unavailable.");
    return {
      observation: {
        submittedUrl,
        canonicalUrl: canonicalTwitterStatusUrl(statusId),
        provider: "twitter",
        providerItemId: statusId,
        segmentKey: "root",
      },
      observedCapabilities: ["canonical_identity"],
    };
  },

  /**
   * The root tweet becomes the body, extracted from its own article so
   * Readability cannot return the reply column instead. Replies are appended
   * as a Markdown section, because the document body is the only capture field
   * Fragments Engine full-text indexes.
   */
  extractDocument(context): AdapterStageResult<AdapterDocumentObservation> {
    const generic = genericPageCaptureAdapter.extractDocument(context);
    const statusId = resolveTwitterStatusId(context);
    const { budget, moreAvailable } = capture(context);
    const root = statusId ? rootBodyMarkdown(context, rootTweetHtml(context, statusId)) : "";
    const replies = renderRepliesMarkdown(budget, moreAvailable);
    const body = (root || generic.observation.contentMarkdown).trim();

    if (!replies && body === generic.observation.contentMarkdown.trim()) return generic;

    const observation: AdapterDocumentObservation = {
      ...generic.observation,
      contentMarkdown: replies ? (body ? `${body}\n\n${replies}` : replies) : body,
    };
    return {
      observation,
      observedCapabilities: observedDocumentCapabilities(observation),
      warnings: generic.warnings,
    };
  },

  /** X statuses still carry ordinary page media; generic handling stands. */
  extractMedia(context): AdapterStageResult<readonly AdapterMediaCandidate[]> {
    return genericPageCaptureAdapter.extractMedia(context);
  },

  extractProviderMetadata(
    context,
  ): AdapterStageResult<readonly AdapterProviderMetadataExtension[]> {
    const statusId = resolveTwitterStatusId(context);
    if (!statusId) return { observation: [] };
    const observation = capture(context);
    const { budget, root } = observation;
    const warnings = [...observation.warnings];

    if (budget.truncated) {
      warnings.push({
        code: "twitter.replies_truncated",
        severity: "warning",
        stage: "provider_metadata",
        message: `Retained ${budget.kept} of ${budget.observed} observed replies; the remainder exceeded this capture's size budget.`,
      });
    }

    const data: Record<string, AdapterMetadataValue> = {
      statusId,
      parserVersion: TWITTER_PARSER_VERSION,
      // Records whether the conversation was walked or merely glimpsed, so a
      // sparse capture is later distinguishable from a sparse conversation.
      scrollHarvested: observation.harvested,
      repliesObserved: budget.observed,
      repliesCaptured: budget.kept,
      repliesTruncated: budget.truncated,
      repliesUnrendered: observation.moreAvailable,
      ...(root
        ? {
            root: {
              id: root.id,
              handle: root.handle,
              ...(root.displayName ? { displayName: root.displayName } : {}),
              ...(root.createdAt ? { createdAt: root.createdAt } : {}),
              ...(root.permalink ? { permalink: root.permalink } : {}),
            },
          }
        : {}),
      ...(budget.replies.length ? { replies: repliesMetadata(budget.replies) } : {}),
    };

    return {
      observation: [
        {
          namespace: "provider.twitter",
          schemaVersion: TWITTER_EXTENSION_SCHEMA_VERSION,
          data,
        },
      ],
      observedCapabilities: root || budget.kept ? ["provider_metadata"] : [],
      warnings,
    };
  },

  reportCapabilities() {
    return TWITTER_CAPABILITIES;
  },
};
