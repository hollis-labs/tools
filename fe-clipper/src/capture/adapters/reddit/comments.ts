/**
 * The Reddit comment model, its Markdown rendering, and the size budget that
 * keeps a large thread from failing an otherwise good capture.
 *
 * A captured thread is carried twice on purpose, because the two destinations
 * answer different questions in Fragments Engine:
 *
 * - Rendered Markdown is appended to the document body, which is the only
 *   capture field FE puts into its full-text index. Comment text is findable
 *   only if it lands here.
 * - The structured tree is carried in the `provider.reddit` document
 *   extension, which FE persists as fragment metadata. That preserves author,
 *   score, depth, and permalink as fields rather than as prose.
 */

import type { AdapterMetadataValue } from "../../types";

export interface RedditCommentObservation {
  /** Reddit's `t1_` fullname when observed, otherwise a positional fallback. */
  id: string;
  author: string;
  /** Zero-based thread depth. Top-level comments are depth 0. */
  depth: number;
  /** The comment body as Markdown. Empty for removed or unrendered comments. */
  body: string;
  score?: number;
  permalink?: string;
  createdAt?: string;
  /** Reddit's own distinction marker, such as a moderator or admin comment. */
  distinguished?: string;
  /** True when the page rendered the comment collapsed. */
  collapsed?: boolean;
  replies: RedditCommentObservation[];
}

/**
 * Bytes of comment payload a single capture may contribute.
 *
 * Fragments Engine advertises a 2 MiB manifest limit and `captureJobs` fails a
 * capture outright when the encoded envelope exceeds the server's advertised
 * value, so an unbounded thread would lose the post as well as the comments.
 * This budget is ~72% of that limit, which leaves the post body, media
 * manifest, annotations, and warnings comfortable headroom. The server's
 * advertised limit remains the authoritative backstop at preflight.
 */
export const REDDIT_COMMENT_BUDGET_BYTES = 1_500_000;

const encoder = new TextEncoder();

function byteLength(value: string): number {
  return encoder.encode(value).byteLength;
}

export interface RedditCommentBudgetResult {
  comments: RedditCommentObservation[];
  /** Comments retained across every depth. */
  kept: number;
  /** Comments observed in the page before trimming. */
  observed: number;
  truncated: boolean;
}

function countComments(comments: readonly RedditCommentObservation[]): number {
  let total = 0;
  for (const comment of comments) {
    total += 1 + countComments(comment.replies);
  }
  return total;
}

/**
 * Serialized cost of one node, excluding its replies. Both destinations are
 * counted because a captured comment is carried twice: once as Markdown in the
 * document body and once as structured metadata.
 */
function nodeCost(comment: RedditCommentObservation): number {
  const node = { ...comment, replies: [] };
  return byteLength(JSON.stringify(node)) + byteLength(renderComment(comment));
}

/**
 * Retains comments in the page's own displayed order until the byte budget is
 * spent, then drops the remaining tail. Dropping a comment drops its replies,
 * so the retained thread is always a well-formed prefix of the observed one.
 */
export function applyCommentBudget(
  comments: readonly RedditCommentObservation[],
  budgetBytes: number = REDDIT_COMMENT_BUDGET_BYTES,
): RedditCommentBudgetResult {
  const observed = countComments(comments);
  let spent = 0;
  let kept = 0;
  let exhausted = false;

  function walk(nodes: readonly RedditCommentObservation[]): RedditCommentObservation[] {
    const retained: RedditCommentObservation[] = [];
    for (const node of nodes) {
      if (exhausted) break;
      const cost = nodeCost(node);
      if (spent + cost > budgetBytes) {
        exhausted = true;
        break;
      }
      spent += cost;
      kept += 1;
      retained.push({ ...node, replies: walk(node.replies) });
    }
    return retained;
  }

  const retained = walk(comments);
  return { comments: retained, kept, observed, truncated: kept < observed };
}

function commentByline(comment: RedditCommentObservation): string {
  const parts = [`**u/${comment.author}**`];
  if (comment.score !== undefined) {
    parts.push(`${comment.score} ${Math.abs(comment.score) === 1 ? "point" : "points"}`);
  }
  if (comment.distinguished) parts.push(comment.distinguished);
  if (comment.createdAt) parts.push(comment.createdAt);
  return parts.join(" · ");
}

/**
 * Renders one comment as a Markdown list item. Depth becomes list nesting, so
 * the reply structure survives in any Markdown renderer without relying on
 * quote nesting, which becomes unreadable past two levels.
 */
function renderComment(comment: RedditCommentObservation): string {
  const indent = "  ".repeat(comment.depth);
  const lines = [`${indent}- ${commentByline(comment)}`];
  const body = comment.body.trim();
  if (body) {
    for (const line of body.split("\n")) {
      lines.push(line.trim() ? `${indent}  ${line}` : "");
    }
  }
  return `${lines.join("\n")}\n`;
}

function renderTree(comments: readonly RedditCommentObservation[]): string {
  let rendered = "";
  for (const comment of comments) {
    rendered += renderComment(comment);
    rendered += renderTree(comment.replies);
  }
  return rendered;
}

/**
 * Renders the captured thread as a Markdown section appended to the post body.
 * Returns an empty string when nothing was retained, so the caller never
 * appends an empty heading.
 */
export function renderCommentsMarkdown(
  budget: RedditCommentBudgetResult,
  moreAvailable: boolean,
): string {
  if (!budget.comments.length) return "";
  const notes: string[] = [];
  if (budget.truncated) {
    notes.push(
      `${budget.kept} of ${budget.observed} captured within this capture's size budget`,
    );
  }
  if (moreAvailable) notes.push("the page had unexpanded replies");
  const heading = notes.length ? `## Comments (${notes.join("; ")})` : "## Comments";
  return `${heading}\n\n${renderTree(budget.comments).trimEnd()}`;
}

/** Converts the retained tree into namespaced-extension metadata values. */
export function commentsMetadata(
  comments: readonly RedditCommentObservation[],
): readonly AdapterMetadataValue[] {
  return comments.map((comment) => ({
    id: comment.id,
    author: comment.author,
    depth: comment.depth,
    body: comment.body,
    ...(comment.score !== undefined ? { score: comment.score } : {}),
    ...(comment.permalink ? { permalink: comment.permalink } : {}),
    ...(comment.createdAt ? { createdAt: comment.createdAt } : {}),
    ...(comment.distinguished ? { distinguished: comment.distinguished } : {}),
    ...(comment.collapsed ? { collapsed: true } : {}),
    ...(comment.replies.length ? { replies: commentsMetadata(comment.replies) } : {}),
  }));
}
