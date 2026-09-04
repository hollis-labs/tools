/**
 * Reddit DOM details are intentionally isolated here. These selectors are
 * adapter implementation details, not transport or domain concepts.
 *
 * Two surfaces are supported because both are in everyday use and they share
 * no markup:
 *
 * - `shreddit`: the current www.reddit.com web client, which renders posts and
 *   comments as `<shreddit-post>` / `<shreddit-comment>` custom elements whose
 *   metadata lives in attributes and whose bodies are slotted light DOM.
 * - `old.reddit.com`: the server-rendered client, which nests comments as
 *   `.thing.comment` elements inside `.child > .listing` subtrees.
 */

export const SHREDDIT_POST_SELECTOR = "shreddit-post";

export const SHREDDIT_COMMENT_SELECTOR = "shreddit-comment";

/**
 * shreddit slots a comment's rendered body into the light DOM. The id-suffixed
 * form is the stable one; the bare slot is the fallback for older markup.
 */
export const SHREDDIT_COMMENT_BODY_SELECTORS = [
  '[id$="-comment-rtjson-content"]',
  '[slot="comment"]',
  ".md",
] as const;

export const OLD_REDDIT_POST_SELECTORS = [
  '#siteTable .thing.link[data-fullname^="t3_"]',
  '.thing.link[data-fullname^="t3_"]',
] as const;

export const OLD_REDDIT_POST_BODY_SELECTORS = [
  ".expando .usertext-body .md",
  ".entry .usertext-body .md",
] as const;

export const OLD_REDDIT_COMMENT_SELECTOR = '.thing.comment[data-fullname^="t1_"]';

export const OLD_REDDIT_COMMENT_BODY_SELECTORS = [
  ":scope > .entry .usertext-body .md",
  ".entry .usertext-body .md",
] as const;

/**
 * old.reddit nests replies through `.child`. Walking only direct structural
 * children keeps a comment's own body out of its replies and preserves depth.
 */
export const OLD_REDDIT_CHILD_LISTING_SELECTOR = ".child";

/**
 * Markup that is structurally a comment but carries no authored content.
 * Excluding it keeps "load more comments" affordances and ad slots out of the
 * captured thread.
 */
export const OLD_REDDIT_EXCLUDED_COMMENT_SELECTOR = [
  ".morecomments",
  ".morerecursion",
  ".deepthread",
  ".promoted",
].join(",");

export const SHREDDIT_MORE_COMMENTS_SELECTOR = [
  "shreddit-comment-tree-more-button",
  'faceplate-partial[loading="action"]',
  'button[rel="nofollow"][class*="more-comments"]',
].join(",");
