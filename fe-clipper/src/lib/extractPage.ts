import { Readability } from "@mozilla/readability";
import TurndownService from "turndown";
import type { AdapterDocumentObservation } from "@/capture/types";
import { sanitizeCapturePageUrl } from "@/capture/urlSafety";

const turndown = new TurndownService({
  headingStyle: "atx",
  codeBlockStyle: "fenced",
  bulletListMarker: "-",
});

export interface ExtractPageOptions {
  /** Page URL at capture time (document.location isn't reliable off a clone). */
  url: string;
  /** window.getSelection().toString() at capture time. */
  selectionText: string;
}

/**
 * Picks the page's own meta description.
 *
 * Precedence: `meta[name="description"]` before `meta[property="og:description"]`.
 * A plain `querySelector('meta[name="description"], meta[property="og:description"]')`
 * would instead return whichever tag happens to appear first in the DOM --
 * many real pages put Open Graph tags before the standard description tag,
 * which would flip the intended precedence. Checking each selector
 * separately keeps the precedence deterministic regardless of tag order.
 */
export function pickDescription(doc: Document): string {
  const standard = doc
    .querySelector('meta[name="description"]')
    ?.getAttribute("content")
    ?.trim();
  if (standard) return standard;

  const openGraph = doc
    .querySelector('meta[property="og:description"]')
    ?.getAttribute("content")
    ?.trim();
  if (openGraph) return openGraph;

  return "";
}

interface ExtractedArticle {
  title: string;
  contentHtml: string;
}

function sanitizeDocumentUrl(raw: string, baseUrl: string): string {
  try {
    const resolved = new URL(raw, baseUrl);
    if (resolved.protocol !== "http:" && resolved.protocol !== "https:") return raw;
    const sanitized = new URL(sanitizeCapturePageUrl(resolved.href));
    if (sanitized.href === resolved.href) return raw;
    if (/^https?:\/\//i.test(raw) || raw.startsWith("//")) return sanitized.href;
    if (raw.startsWith("#")) return sanitized.hash;
    if (raw.startsWith("?")) return `${sanitized.search}${sanitized.hash}`;
    const path = raw.split(/[?#]/, 1)[0] ?? "";
    return `${path}${sanitized.search}${sanitized.hash}`;
  } catch {
    return raw;
  }
}

function sanitizeResolvedContentUrls(doc: Document, contentHtml: string, baseUrl: string): string {
  const container = doc.createElement("div");
  container.innerHTML = contentHtml;
  for (const element of container.querySelectorAll<HTMLElement>("[href], [src]")) {
    for (const attribute of ["href", "src"] as const) {
      const raw = element.getAttribute(attribute);
      if (!raw) continue;
      const sanitized = sanitizeDocumentUrl(raw, baseUrl);
      if (!sanitized) element.removeAttribute(attribute);
      else if (sanitized !== raw) element.setAttribute(attribute, sanitized);
    }
  }
  return container.innerHTML;
}

/**
 * Runs Readability against a cloned copy of `doc` (Readability mutates the
 * document it's given, and we don't want to disturb the live page the
 * content script is running against). Returns null if Readability can't
 * find an article-shaped region.
 */
function extractArticle(doc: Document): ExtractedArticle | null {
  const clone = doc.cloneNode(true) as Document;
  const reader = new Readability(clone);
  const article = reader.parse();
  if (!article || !article.content || !article.content.trim()) {
    return null;
  }
  return { title: article.title ?? "", contentHtml: article.content };
}

/**
 * Full capture pipeline: Readability extraction -> Turndown markdown
 * conversion, plus meta description, selection, title, and url. Falls back
 * to converting the raw <body> when Readability can't find an article (e.g.
 * non-article pages), so a clip is still produced rather than failing
 * outright -- `usedFallback` flags that case for the caller.
 */
export function extractPage(doc: Document, opts: ExtractPageOptions): AdapterDocumentObservation {
  const article = extractArticle(doc);

  const contentHtml = sanitizeResolvedContentUrls(
    doc,
    article?.contentHtml ?? doc.body?.innerHTML ?? "",
    opts.url,
  );
  const contentMarkdown = turndown.turndown(contentHtml).trim();

  const readabilityTitle = article?.title?.trim();
  const title = readabilityTitle || doc.title?.trim() || opts.url;
  const language = doc.documentElement?.lang.trim();

  return {
    title,
    contentMarkdown,
    description: pickDescription(doc),
    selection: opts.selectionText.trim(),
    usedFallback: article === null,
    ...(language ? { language } : {}),
  };
}
