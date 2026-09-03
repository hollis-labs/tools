import type {
  AdapterCapability,
  AdapterExtractionWarning,
  AdapterMediaCandidate,
  AdapterMediaVariantCandidate,
  AdapterStageResult,
  PageCaptureContext,
} from "../types";

const DOCUMENT_POSITION_FOLLOWING = 4;
const MAX_CONTEXT_LENGTH = 320;
const MIN_LARGE_IMAGE_WIDTH = 600;
const MIN_LARGE_IMAGE_HEIGHT = 300;
const MAX_DECORATIVE_EDGE = 96;

const CHROME_ANCESTOR_SELECTOR = [
  "nav",
  "aside",
  "footer",
  '[role="navigation"]',
  '[role="banner"]',
  '[role="contentinfo"]',
  '[role="complementary"]',
].join(",");

const DECORATIVE_TOKEN =
  /(?:^|[\s_./-])(avatar|author[-_ ]?(?:avatar|photo)|badge|beacon|blank|emoji|icon|logo|pixel|profile[-_ ]?(?:avatar|image|photo)|spacer|spinner|sprite|tracker|tracking|transparent)(?:$|[\s_./-])/i;
const CHROME_TOKEN =
  /(?:^|[\s_-])(advert(?:isement)?|breadcrumb|menu|nav(?:igation)?|promo|recommend(?:ation)?|share|sidebar|social|sponsor|toolbar)(?:$|[\s_-])/i;
const TRACKING_QUERY_PARAMETER =
  /^(?:utm_.+|fbclid|gclid|dclid|msclkid|mc_cid|mc_eid|_ga)$/i;

interface ObservedImageSource {
  sourceUrl: string;
  descriptorWidth?: number;
  descriptorDensity?: number;
  mimeType?: string;
  explicitlyOriginal: boolean;
  priority: number;
  order: number;
}

interface ImageDimensions {
  width?: number;
  height?: number;
}

interface AcceptedImage {
  variant: AdapterMediaVariantCandidate;
  duplicateKey: string;
  heroHint: boolean;
  largeEnoughForHero: boolean;
  altText?: string;
  caption?: string;
  sourceContext?: string;
}

interface MetaImage {
  candidate: Omit<AdapterMediaCandidate, "candidateId" | "position">;
  duplicateKey: string;
}

interface MediaWarningCounts {
  unusableSources: number;
  failedCandidates: number;
}

function positiveInteger(value: string | null | undefined): number | undefined {
  if (!value || !/^\d+$/.test(value)) return undefined;
  const parsed = Number.parseInt(value, 10);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined;
}

function compactText(value: string | null | undefined, maxLength = MAX_CONTEXT_LENGTH) {
  const compacted = value?.replace(/\s+/g, " ").trim();
  if (!compacted) return undefined;
  return compacted.length <= maxLength ? compacted : `${compacted.slice(0, maxLength - 1)}…`;
}

function httpUrl(rawValue: string | null | undefined, baseUrl: string): string | undefined {
  const value = rawValue?.trim();
  if (!value) return undefined;

  try {
    const url = new URL(value, baseUrl);
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    if (url.username || url.password) return undefined;
    url.hash = "";
    return url.href;
  } catch {
    return undefined;
  }
}

function safePageBaseUrl(rawUrl: string): string {
  try {
    const url = new URL(rawUrl);
    url.username = "";
    url.password = "";
    return url.href;
  } catch {
    return rawUrl;
  }
}

function duplicateKey(sourceUrl: string): string {
  const url = new URL(sourceUrl);
  for (const key of [...url.searchParams.keys()]) {
    if (TRACKING_QUERY_PARAMETER.test(key)) url.searchParams.delete(key);
  }
  url.searchParams.sort();
  return url.href;
}

function mimeTypeFromUrl(sourceUrl: string): string | undefined {
  const pathname = new URL(sourceUrl).pathname.toLowerCase();
  if (/\.jpe?g$/.test(pathname)) return "image/jpeg";
  if (/\.png$/.test(pathname)) return "image/png";
  if (/\.webp$/.test(pathname)) return "image/webp";
  if (/\.gif$/.test(pathname)) return "image/gif";
  if (/\.avif$/.test(pathname)) return "image/avif";
  if (/\.svg$/.test(pathname)) return "image/svg+xml";
  return undefined;
}

function normalizedImageMimeType(value: string | null | undefined): string | undefined {
  const mimeType = value?.trim().toLowerCase();
  return mimeType && /^image\/[a-z0-9!#$&^_.+-]+$/.test(mimeType) ? mimeType : undefined;
}

function parseSrcset(srcset: string): readonly {
  url: string;
  width?: number;
  density?: number;
}[] {
  const candidates: { url: string; width?: number; density?: number }[] = [];

  for (const entry of srcset.split(",")) {
    const [url, descriptor, ...extra] = entry.trim().split(/\s+/);
    if (!url || extra.length > 0) continue;

    if (!descriptor) {
      candidates.push({ url });
      continue;
    }

    const widthMatch = /^(\d+)w$/.exec(descriptor);
    if (widthMatch) {
      const width = positiveInteger(widthMatch[1]);
      if (width) candidates.push({ url, width });
      continue;
    }

    const densityMatch = /^(\d+(?:\.\d+)?)x$/.exec(descriptor);
    if (densityMatch) {
      const density = Number.parseFloat(densityMatch[1] ?? "");
      if (Number.isFinite(density) && density > 0) candidates.push({ url, density });
    }
  }

  return candidates;
}

function imageDimensions(image: HTMLImageElement): ImageDimensions {
  const naturalWidth = image.naturalWidth > 0 ? image.naturalWidth : undefined;
  const naturalHeight = image.naturalHeight > 0 ? image.naturalHeight : undefined;
  const attributeWidth = positiveInteger(image.getAttribute("width"));
  const attributeHeight = positiveInteger(image.getAttribute("height"));
  const styleWidth = /^([1-9]\d*)px$/i.exec(image.style.width.trim())?.[1];
  const styleHeight = /^([1-9]\d*)px$/i.exec(image.style.height.trim())?.[1];
  const computedStyle = image.ownerDocument.defaultView?.getComputedStyle(image);
  const computedWidth = /^([1-9]\d*)px$/i.exec(computedStyle?.width.trim() ?? "")?.[1];
  const computedHeight = /^([1-9]\d*)px$/i.exec(computedStyle?.height.trim() ?? "")?.[1];

  return {
    width:
      naturalWidth ?? attributeWidth ?? positiveInteger(styleWidth) ?? positiveInteger(computedWidth),
    height:
      naturalHeight ??
      attributeHeight ??
      positiveInteger(styleHeight) ??
      positiveInteger(computedHeight),
  };
}

function isHidden(image: HTMLImageElement): boolean {
  for (let element: Element | null = image; element; element = element.parentElement) {
    if (
      element.hasAttribute("hidden") ||
      element.hasAttribute("inert") ||
      element.getAttribute("aria-hidden")?.toLowerCase() === "true"
    ) {
      return true;
    }

    const inlineStyle = (element as HTMLElement).style;
    const computedStyle = element.ownerDocument.defaultView?.getComputedStyle(element);
    for (const style of [inlineStyle, computedStyle]) {
      if (!style) continue;
      const { display, visibility, opacity } = style;
      if (
        display.toLowerCase() === "none" ||
        /^(?:hidden|collapse)$/i.test(visibility) ||
        Number.parseFloat(opacity) === 0
      ) {
        return true;
      }
    }
  }

  return false;
}

function hasDecorativeToken(image: HTMLImageElement, sourceUrl: string): boolean {
  const elementTokens: string[] = [];
  let current: Element | null = image;

  // Local ancestry is enough to recognize an avatar/icon wrapper without
  // letting a broad page class such as "social-article" hide real content.
  for (let depth = 0; current && depth < 3; depth += 1, current = current.parentElement) {
    elementTokens.push(
      current.id,
      current.className,
      current.getAttribute("role") ?? "",
      current.getAttribute("itemprop") ?? "",
    );
  }

  const altAndTitle = `${image.getAttribute("alt") ?? ""} ${image.getAttribute("title") ?? ""}`;
  return (
    DECORATIVE_TOKEN.test(`${elementTokens.join(" ")} ${altAndTitle}`) ||
    CHROME_TOKEN.test(elementTokens.join(" ")) ||
    DECORATIVE_TOKEN.test(new URL(sourceUrl).pathname)
  );
}

function isDecorativeBySemantics(image: HTMLImageElement): boolean {
  if (image.getAttribute("role")?.toLowerCase() === "presentation") return true;
  if (image.hasAttribute("alt") && !image.getAttribute("alt")?.trim()) return true;
  return false;
}

function isPageChrome(image: HTMLImageElement, contentRoot: Element): boolean {
  if (image.closest(CHROME_ANCESTOR_SELECTOR)) return true;
  return contentRoot.tagName === "BODY" && image.closest("header") !== null;
}

function isSmallDecorativeImage(dimensions: ImageDimensions): boolean {
  const { width, height } = dimensions;
  if ((width !== undefined && width <= 2) || (height !== undefined && height <= 2)) return true;
  return (
    width !== undefined &&
    height !== undefined &&
    width <= MAX_DECORATIVE_EDGE &&
    height <= MAX_DECORATIVE_EDGE
  );
}

function collectImageSources(
  image: HTMLImageElement,
  pageUrl: string,
): readonly ObservedImageSource[] {
  const sources: ObservedImageSource[] = [];
  let order = 0;

  const add = (
    rawUrl: string | null | undefined,
    options: Omit<ObservedImageSource, "sourceUrl" | "order">,
  ) => {
    const sourceUrl = httpUrl(rawUrl, pageUrl);
    if (!sourceUrl) return;
    sources.push({ sourceUrl, order, ...options });
    order += 1;
  };

  const picture = image.closest("picture");
  for (const source of picture?.querySelectorAll("source") ?? []) {
    const mimeType = normalizedImageMimeType(source.getAttribute("type"));
    for (const candidate of parseSrcset(source.getAttribute("srcset") ?? "")) {
      add(candidate.url, {
        descriptorWidth: candidate.width,
        descriptorDensity: candidate.density,
        mimeType,
        explicitlyOriginal: false,
        priority: 30,
      });
    }
  }

  for (const attribute of ["srcset", "data-srcset"]) {
    for (const candidate of parseSrcset(image.getAttribute(attribute) ?? "")) {
      add(candidate.url, {
        descriptorWidth: candidate.width,
        descriptorDensity: candidate.density,
        explicitlyOriginal: false,
        priority: attribute === "srcset" ? 35 : 25,
      });
    }
  }

  add(image.getAttribute("data-original"), { explicitlyOriginal: true, priority: 50 });
  add(image.getAttribute("data-original-src"), { explicitlyOriginal: true, priority: 50 });
  add(image.currentSrc, { explicitlyOriginal: false, priority: 45 });
  add(image.getAttribute("data-src"), { explicitlyOriginal: false, priority: 25 });
  add(image.getAttribute("data-lazy-src"), { explicitlyOriginal: false, priority: 25 });
  add(image.getAttribute("src"), { explicitlyOriginal: false, priority: 20 });

  return sources;
}

function compareSources(left: ObservedImageSource, right: ObservedImageSource): number {
  if (left.explicitlyOriginal !== right.explicitlyOriginal) {
    return left.explicitlyOriginal ? 1 : -1;
  }

  const leftWidth = left.descriptorWidth ?? 0;
  const rightWidth = right.descriptorWidth ?? 0;
  if (leftWidth !== rightWidth) return leftWidth - rightWidth;

  const leftDensity = left.descriptorDensity ?? 0;
  const rightDensity = right.descriptorDensity ?? 0;
  if (leftDensity !== rightDensity) return leftDensity - rightDensity;

  if (left.priority !== right.priority) return left.priority - right.priority;
  return right.order - left.order;
}

function selectedVariant(
  image: HTMLImageElement,
  pageUrl: string,
): AdapterMediaVariantCandidate | undefined {
  const selected = collectImageSources(image, pageUrl).reduce<ObservedImageSource | undefined>(
    (best, candidate) => (!best || compareSources(candidate, best) > 0 ? candidate : best),
    undefined,
  );
  if (!selected) return undefined;

  const dimensions = imageDimensions(image);
  const width = selected.descriptorWidth ?? dimensions.width;
  const height = selected.descriptorWidth ? undefined : dimensions.height;
  const mimeType = selected.mimeType ?? mimeTypeFromUrl(selected.sourceUrl);

  return {
    variantId: "original",
    kind: "original",
    sourceUrl: selected.sourceUrl,
    acquisitionPreference: "browser_preferred",
    ...(mimeType ? { mimeType } : {}),
    ...(width ? { width } : {}),
    ...(height ? { height } : {}),
  };
}

function selectContentRoot(document: Document): Element | null {
  const articles = [...document.querySelectorAll("article")];
  if (articles.length > 0) {
    return articles.reduce((best, candidate) =>
      (candidate.textContent?.trim().length ?? 0) > (best.textContent?.trim().length ?? 0)
        ? candidate
        : best,
    );
  }
  return document.querySelector("main") ?? document.querySelector('[role="main"]') ?? document.body;
}

function captionFor(image: HTMLImageElement): string | undefined {
  const figure = image.closest("figure");
  return compactText(figure?.querySelector(":scope > figcaption")?.textContent);
}

function nearestHeading(image: HTMLImageElement, contentRoot: Element): string | undefined {
  let nearest: Element | undefined;
  for (const heading of contentRoot.querySelectorAll("h1, h2, h3, h4, h5, h6")) {
    if (heading === image || heading.compareDocumentPosition(image) & DOCUMENT_POSITION_FOLLOWING) {
      nearest = heading;
      continue;
    }
    break;
  }
  return compactText(nearest?.textContent);
}

function contextFor(
  image: HTMLImageElement,
  contentRoot: Element,
  caption: string | undefined,
): string | undefined {
  const heading = nearestHeading(image, contentRoot);
  if (heading) return `Under heading: ${heading}`;
  if (caption) return `Figure: ${caption}`;

  const linkLabel = compactText(
    image.closest("a")?.getAttribute("aria-label") ?? image.closest("a")?.getAttribute("title"),
  );
  return linkLabel ? `Linked image: ${linkLabel}` : undefined;
}

function hasHeroHint(image: HTMLImageElement): boolean {
  const localTokens = `${image.id} ${image.className} ${image.closest("figure")?.className ?? ""}`;
  return /(?:^|[\s_-])(hero|lead|featured|cover)(?:$|[\s_-])/i.test(localTokens);
}

function acceptDomImage(
  image: HTMLImageElement,
  contentRoot: Element,
  pageUrl: string,
  warningCounts: MediaWarningCounts,
): AcceptedImage | undefined {
  if (isHidden(image) || isDecorativeBySemantics(image) || isPageChrome(image, contentRoot)) {
    return undefined;
  }

  const variant = selectedVariant(image, pageUrl);
  if (!variant) {
    if (
      image.hasAttribute("src") ||
      image.hasAttribute("srcset") ||
      image.hasAttribute("data-src") ||
      image.hasAttribute("data-original")
    ) {
      warningCounts.unusableSources += 1;
    }
    return undefined;
  }

  const dimensions = imageDimensions(image);
  if (
    isSmallDecorativeImage(dimensions) ||
    hasDecorativeToken(image, variant.sourceUrl)
  ) {
    return undefined;
  }

  const width = variant.width ?? dimensions.width;
  const height = variant.height ?? dimensions.height;
  const caption = captionFor(image);
  const altText = compactText(image.getAttribute("alt"));
  const sourceContext = contextFor(image, contentRoot, caption);

  return {
    variant,
    duplicateKey: duplicateKey(variant.sourceUrl),
    heroHint: hasHeroHint(image),
    largeEnoughForHero:
      width !== undefined &&
      height !== undefined &&
      width >= MIN_LARGE_IMAGE_WIDTH &&
      height >= MIN_LARGE_IMAGE_HEIGHT,
    ...(altText ? { altText } : {}),
    ...(caption ? { caption } : {}),
    ...(sourceContext ? { sourceContext } : {}),
  };
}

function metadataValues(document: Document): ReadonlyMap<string, string> {
  const values = new Map<string, string>();
  for (const meta of document.querySelectorAll("meta[property], meta[name]")) {
    const key = (meta.getAttribute("property") ?? meta.getAttribute("name"))
      ?.trim()
      .toLowerCase();
    const content = meta.getAttribute("content")?.trim();
    if (key && content && !values.has(key)) values.set(key, content);
  }
  return values;
}

function metadataHero(
  context: PageCaptureContext,
  warningCounts: MediaWarningCounts,
): MetaImage | undefined {
  const meta = metadataValues(context.document);
  const pageUrl = safePageBaseUrl(context.location.href);
  const sources = [
    ["og:image:secure_url", meta.get("og:image:secure_url")],
    ["og:image:url", meta.get("og:image:url")],
    ["og:image", meta.get("og:image")],
    ["twitter:image:src", meta.get("twitter:image:src")],
    ["twitter:image", meta.get("twitter:image")],
    [
      "image_src",
      context.document.querySelector('link[rel~="image_src"]')?.getAttribute("href") ?? undefined,
    ],
  ] as const;

  for (const [sourceName, rawSourceUrl] of sources) {
    if (!rawSourceUrl) continue;
    const sourceUrl = httpUrl(rawSourceUrl, pageUrl);
    if (!sourceUrl) {
      warningCounts.unusableSources += 1;
      continue;
    }

    const isOpenGraph = sourceName.startsWith("og:");
    const width = isOpenGraph ? positiveInteger(meta.get("og:image:width")) : undefined;
    const height = isOpenGraph ? positiveInteger(meta.get("og:image:height")) : undefined;
    if (
      isSmallDecorativeImage({ width, height }) ||
      DECORATIVE_TOKEN.test(new URL(sourceUrl).pathname)
    ) {
      continue;
    }

    const mimeType =
      (isOpenGraph ? normalizedImageMimeType(meta.get("og:image:type")) : undefined) ??
      mimeTypeFromUrl(sourceUrl);
    const altText = compactText(meta.get("og:image:alt") ?? meta.get("twitter:image:alt"));

    return {
      duplicateKey: duplicateKey(sourceUrl),
      candidate: {
        kind: "image",
        role: "hero",
        ...(altText ? { altText } : {}),
        sourceContext: `Document metadata: ${sourceName}`,
        variants: [
          {
            variantId: "original",
            kind: "original",
            sourceUrl,
            acquisitionPreference: "browser_preferred",
            ...(mimeType ? { mimeType } : {}),
            ...(width ? { width } : {}),
            ...(height ? { height } : {}),
          },
        ],
      },
    };
  }

  return undefined;
}

function warningSummary(counts: MediaWarningCounts): readonly AdapterExtractionWarning[] {
  const warnings: AdapterExtractionWarning[] = [];

  if (counts.unusableSources > 0) {
    warnings.push({
      code: "generic.media_sources_unusable",
      severity: "info",
      stage: "media",
      message: `${counts.unusableSources} observed image source${counts.unusableSources === 1 ? " was" : "s were"} omitted because ${counts.unusableSources === 1 ? "it was not a safe" : "they were not safe"} HTTP(S) URL${counts.unusableSources === 1 ? "" : "s"}.`,
    });
  }

  if (counts.failedCandidates > 0) {
    warnings.push({
      code: "generic.media_extraction_partial",
      severity: "warning",
      stage: "media",
      message: `${counts.failedCandidates} image candidate${counts.failedCandidates === 1 ? " could" : "s could"} not be inspected; the remaining article media was preserved.`,
    });
  }

  return warnings;
}

/**
 * Extracts ordinary article imagery without site-specific selectors.
 *
 * Deterministic policy:
 * - prefer article/main content over the whole body;
 * - accept only observed HTTP(S) URLs and choose the largest declared srcset
 *   member (or an explicitly observed original URL) without synthesizing one;
 * - filter semantic decoration, hidden elements, page chrome, avatar/icon/logo
 *   tokens, tiny/tracking pixels, and duplicate canonical URLs;
 * - prefer an observed metadata hero, otherwise a hero/lead hint or the first
 *   sufficiently large article image, then retain the remaining images in DOM
 *   order as inline attachments.
 */
export function extractGenericMedia(
  context: PageCaptureContext,
): AdapterStageResult<readonly AdapterMediaCandidate[]> {
  const warningCounts: MediaWarningCounts = { unusableSources: 0, failedCandidates: 0 };
  const contentRoot = selectContentRoot(context.document);
  const metadata = metadataHero(context, warningCounts);
  const accepted: AcceptedImage[] = [];
  const pageUrl = safePageBaseUrl(context.location.href);

  if (contentRoot) {
    for (const image of contentRoot.querySelectorAll("img")) {
      try {
        const candidate = acceptDomImage(image, contentRoot, pageUrl, warningCounts);
        if (candidate) accepted.push(candidate);
      } catch {
        warningCounts.failedCandidates += 1;
      }
    }
  }

  const media: AdapterMediaCandidate[] = [];
  const seen = new Set<string>();
  let domHero: AcceptedImage | undefined;

  if (metadata) {
    seen.add(metadata.duplicateKey);
    media.push({ candidateId: "generic:image:0", position: 0, ...metadata.candidate });
  } else {
    domHero = accepted.find((candidate) => candidate.heroHint) ??
      accepted.find((candidate) => candidate.largeEnoughForHero);
  }

  const append = (candidate: AcceptedImage, role: "hero" | "inline") => {
    if (seen.has(candidate.duplicateKey)) return;
    seen.add(candidate.duplicateKey);
    const position = media.length;
    media.push({
      candidateId: `generic:image:${position}`,
      kind: "image",
      role,
      position,
      ...(candidate.altText ? { altText: candidate.altText } : {}),
      ...(candidate.caption ? { caption: candidate.caption } : {}),
      ...(candidate.sourceContext ? { sourceContext: candidate.sourceContext } : {}),
      variants: [candidate.variant],
    });
  };

  if (domHero) append(domHero, "hero");
  for (const candidate of accepted) {
    if (candidate !== domHero) append(candidate, "inline");
  }

  const observedCapabilities: AdapterCapability[] = [];
  if (media.length > 0) observedCapabilities.push("gallery_manifest", "original_media");

  return {
    observation: media,
    observedCapabilities,
    warnings: warningSummary(warningCounts),
  };
}
