/**
 * X/Twitter DOM details are intentionally isolated here. These selectors are
 * adapter implementation details, not transport or domain concepts.
 *
 * X ships no stable class names and no serialized page state worth reading, so
 * `data-testid` attributes are the only durable handles the client exposes.
 * Every lookup below is written to degrade to nothing rather than to match
 * something unintended.
 */

export const TWEET_SELECTOR = 'article[data-testid="tweet"]';

export const TWEET_TEXT_SELECTORS = [
  '[data-testid="tweetText"]',
  '[lang][dir]',
] as const;

export const TWEET_USER_NAME_SELECTOR = '[data-testid="User-Name"]';

/** The conversation region, used to keep sidebars and trend rails out. */
export const CONVERSATION_TIMELINE_SELECTORS = [
  '[aria-label^="Timeline: Conversation"]',
  '[data-testid="primaryColumn"] section',
  '[data-testid="primaryColumn"]',
] as const;

/**
 * Promoted content. X marks ad placements with a tracking wrapper, so an
 * article inside one is never part of the conversation.
 */
export const PROMOTED_TWEET_ANCESTOR_SELECTOR = [
  '[data-testid="placementTracking"]',
  '[data-testid="promotedIndicator"]',
].join(",");

/** Affordances that reveal replies the client has not rendered yet. */
export const MORE_REPLIES_SELECTORS = [
  '[data-testid="cellInnerDiv"] [role="button"][tabindex]',
  'button[data-testid="showMoreRepliesButton"]',
] as const;

export const SHOW_MORE_TEXT = /show (?:more|additional) repl/i;
