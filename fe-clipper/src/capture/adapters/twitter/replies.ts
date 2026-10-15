/**
 * The X/Twitter reply model, its Markdown rendering, and the size budget.
 *
 * A captured conversation is carried twice for the same reason Reddit's is:
 * rendered Markdown reaches the document body, which is the only capture field
 * FE full-text indexes, while the structured list reaches the
 * `provider.twitter` document extension, which FE keeps as fragment metadata.
 *
 * Unlike Reddit, an X conversation is captured as a flat list in display
 * order. X renders a conversation as a linear column and exposes no reliable
 * reply-depth marker, so inventing a tree here would mean inventing structure
 * the page never stated.
 */

import type { AdapterMetadataValue } from "../../types";

export interface TwitterTweetObservation {
  /** The numeric status id when observed, otherwise a positional fallback. */
  id: string;
  handle: string;
  displayName?: string;
  text: string;
  createdAt?: string;
  permalink?: string;
  /** True for the status named by the captured URL. */
  isRoot: boolean;
}

/**
 * Bytes of reply payload a single capture may contribute. Sized against FE's
 * 2 MiB manifest limit on the same reasoning as the Reddit budget: exceeding
 * the server's advertised limit fails the whole capture, so the conversation
 * yields before the post does.
 */
export const TWITTER_REPLY_BUDGET_BYTES = 1_500_000;

const encoder = new TextEncoder();

function byteLength(value: string): number {
  return encoder.encode(value).byteLength;
}

export interface TwitterReplyBudgetResult {
  replies: TwitterTweetObservation[];
  kept: number;
  observed: number;
  truncated: boolean;
}

/** Retains replies in display order until the byte budget is spent. */
export function applyReplyBudget(
  replies: readonly TwitterTweetObservation[],
  budgetBytes: number = TWITTER_REPLY_BUDGET_BYTES,
): TwitterReplyBudgetResult {
  const retained: TwitterTweetObservation[] = [];
  let spent = 0;

  for (const reply of replies) {
    const cost = byteLength(JSON.stringify(reply)) + byteLength(renderReply(reply));
    if (spent + cost > budgetBytes) break;
    spent += cost;
    retained.push(reply);
  }

  return {
    replies: retained,
    kept: retained.length,
    observed: replies.length,
    truncated: retained.length < replies.length,
  };
}

function replyByline(reply: TwitterTweetObservation): string {
  const name = reply.displayName?.trim();
  const parts = [name ? `**${name}** (@${reply.handle})` : `**@${reply.handle}**`];
  if (reply.createdAt) parts.push(reply.createdAt);
  return parts.join(" · ");
}

function renderReply(reply: TwitterTweetObservation): string {
  const lines = [`- ${replyByline(reply)}`];
  const text = reply.text.trim();
  if (text) {
    for (const line of text.split("\n")) lines.push(line.trim() ? `  ${line}` : "");
  }
  return `${lines.join("\n")}\n`;
}

/**
 * Renders retained replies as a Markdown section appended to the root tweet.
 * Returns an empty string when nothing was retained.
 */
export function renderRepliesMarkdown(
  budget: TwitterReplyBudgetResult,
  moreAvailable: boolean,
): string {
  if (!budget.replies.length) return "";
  const notes: string[] = [];
  if (budget.truncated) {
    notes.push(`${budget.kept} of ${budget.observed} captured within this capture's size budget`);
  }
  if (moreAvailable) notes.push("the conversation had replies X never rendered");
  const heading = notes.length ? `## Replies (${notes.join("; ")})` : "## Replies";
  const body = budget.replies.map(renderReply).join("").trimEnd();
  return `${heading}\n\n${body}`;
}

/** Converts retained replies into namespaced-extension metadata values. */
export function repliesMetadata(
  replies: readonly TwitterTweetObservation[],
): readonly AdapterMetadataValue[] {
  return replies.map((reply) => ({
    id: reply.id,
    handle: reply.handle,
    text: reply.text,
    ...(reply.displayName ? { displayName: reply.displayName } : {}),
    ...(reply.createdAt ? { createdAt: reply.createdAt } : {}),
    ...(reply.permalink ? { permalink: reply.permalink } : {}),
  }));
}
