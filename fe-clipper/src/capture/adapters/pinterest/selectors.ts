/**
 * Pinterest DOM details are intentionally isolated here. These selectors are
 * adapter implementation details, not transport or domain concepts.
 */

export const PINTEREST_PAGE_STATE_SELECTORS = [
  'script#__PWS_DATA__[type="application/json"]',
  // Current Pinterest close-up pages emit the selected pin as an anonymous
  // PinResource response instead of placing it in __PWS_DATA__. Exact pin-ID
  // matching in the parser keeps unrelated anonymous resources out.
  'script[type="application/json"]:not([id])',
  'script[data-test-id="pinterest-page-state"][type="application/json"]',
  'script[data-pinterest-state][type="application/json"]',
] as const;

/**
 * Only explicit close-up markers are considered primary DOM media. Broad
 * selectors such as `main img` or pin-card selectors would admit related pins,
 * recommendation grids, avatars, and Pinterest chrome.
 */
export const PINTEREST_PRIMARY_IMAGE_SELECTORS = [
  'img[data-test-id="pin-closeup-image"]',
  '[data-test-id="pin-closeup-image"] > img',
  '[data-test-id="pin-closeup-image"] > picture > img',
  '[data-test-id="closeup-image-main"] img',
  '[data-test-id="closeup-image"] img',
  'img[elementtiming^="closeup-image-"]',
] as const;

export const PINTEREST_EXCLUDED_MEDIA_ANCESTOR_SELECTOR = [
  '[data-test-id*="recommend"]',
  '[data-test-id*="related"]',
  '[data-test-id*="pin-card"]',
  '[data-test-id*="avatar"]',
  '[aria-label*="recommend" i]',
  '[aria-label*="related" i]',
].join(",");
