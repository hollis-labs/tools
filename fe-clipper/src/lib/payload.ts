import type { CapturedPage, ClipPayload, EditableFields } from "./types";

/**
 * Maps a captured page plus the user's popup edits (title, tags) onto the
 * exact wire shape FE's POST /v1/intake expects (see this epic's sibling
 * task CW-20260816-0071 / internal/api/server.go's intakeRequest struct in
 * the fragments-engine repo):
 *
 *   { content, title, source_url, description, selection, tags }
 *
 * `content` is always the Readability->Turndown markdown -- the raw text
 * selection is kept separate in `selection` and is never merged into it.
 */
export function buildPayload(captured: CapturedPage, edits: EditableFields): ClipPayload {
  return {
    content: captured.contentMarkdown,
    title: edits.title.trim() || captured.title,
    source_url: captured.url,
    description: captured.description,
    selection: captured.selection,
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
