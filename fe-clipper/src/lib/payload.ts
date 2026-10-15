import type { CapturedPage, ClipPayload, EditableFields } from "./types";

/**
 * Prepends the user's popup note as a labeled Markdown blockquote ahead of
 * the extracted content, e.g. `> **Note:** remember to follow up`. There's
 * no dedicated `note` field on FE's /v1/intake (CW-20260816-0071's contract
 * is content/title/source_url/description/selection/tags only), so folding
 * it into `content` this way gets it into the fragment's actual searchable
 * body -- useful both as a personal reminder and for FE's ingest system --
 * without needing a new wire field. Returns content untouched when note is
 * blank/whitespace-only.
 */
export function prependNote(content: string, note: string): string {
  const trimmed = note.trim();
  if (!trimmed) return content;

  const quoted = trimmed
    .split("\n")
    .map((line, i) => (i === 0 ? `> **Note:** ${line}` : `> ${line}`))
    .join("\n");
  return `${quoted}\n\n${content}`;
}

/**
 * Maps a captured page plus the user's popup edits (title, tags, note) onto
 * the exact wire shape FE's POST /v1/intake expects (see this epic's
 * sibling task CW-20260816-0071 / internal/api/server.go's intakeRequest
 * struct in the fragments-engine repo):
 *
 *   { content, title, source_url, description, selection, tags }
 *
 * `content` is always the Readability->Turndown markdown (with the note
 * blockquote prepended, if any) -- the raw text selection is kept separate
 * in `selection` and is never merged into it.
 */
export function buildPayload(captured: CapturedPage, edits: EditableFields): ClipPayload {
  return {
    content: prependNote(captured.document.contentMarkdown, edits.note),
    title: edits.title.trim() || captured.document.title,
    source_url: captured.identity.submittedUrl,
    description: captured.document.description,
    selection: captured.document.selection,
    tags: edits.tags,
  };
}

/** Splits a comma/whitespace-separated tags input into a clean tag list. */
export function parseTagsInput(raw: string): string[] {
  return raw
    .split(/[,\n]/)
    .map((t) => t.trim())
    .filter((t) => t.length > 0);
}
