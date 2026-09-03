/**
 * YouTube page internals are intentionally isolated in this adapter-local
 * module. They are observations of the current rendered site, not transport
 * or domain contracts.
 */
export const YOUTUBE_SELECTORS = {
  jsonLdScripts: 'script[type="application/ld+json"]',
  serializedStateScripts: "script",
  titleMeta: [
    'meta[property="og:title"]',
    'meta[name="twitter:title"]',
    'meta[itemprop="name"]',
  ],
  descriptionMeta: [
    'meta[name="description"]',
    'meta[property="og:description"]',
    'meta[name="twitter:description"]',
  ],
  channelIdMeta: ['meta[itemprop="channelId"]'],
  channelNameMeta: ['meta[itemprop="author"]'],
  posterMeta: [
    'meta[property="og:image"]',
    'meta[name="twitter:image"]',
    'link[itemprop="thumbnailUrl"]',
  ],
  posterWidthMeta: 'meta[property="og:image:width"]',
  posterHeightMeta: 'meta[property="og:image:height"]',
  posterMimeMeta: 'meta[property="og:image:type"]',
  captionTracks: 'track[kind="captions"], track[kind="subtitles"]',
  transcriptSegments:
    'ytd-transcript-segment-renderer, [data-youtube-transcript-segment]',
  transcriptTimestamp:
    '.segment-timestamp, [data-youtube-transcript-timestamp], [data-start-ms]',
  transcriptText:
    'yt-formatted-string.segment-text, .segment-text, [data-youtube-transcript-text]',
} as const;
