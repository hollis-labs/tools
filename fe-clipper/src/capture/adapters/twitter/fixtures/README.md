# X/Twitter adapter fixtures

These pages are synthetic, deterministic representations of stable X
conversation markup. They contain no production page captures, account
cookies, session material, or working credentials. Values in the
credential-exclusion fixture are conspicuous test sentinels used only to prove
that non-allowlisted state never reaches adapter output.

X ships no stable class names and no useful serialized page state, so the
fixtures model the `data-testid` handles the client does expose. Note that the
display name and handle are adjacent spans with no separator between them,
which is why the parser reads leaf elements rather than the block's
concatenated `textContent`.

| Fixture | Covers |
|---|---|
| `conversation.html` | Root status plus replies in display order; a promoted placement that must be excluded |
| `unrendered-replies.html` | A conversation X has not fully rendered, exposing the "Show more replies" affordance |
| `credential-exclusion.html` | Sentinel state and credential-bearing status permalinks that must not survive into adapter output or the envelope |

The scroll pre-pass is exercised against an injected fake scroll environment
rather than these fixtures, since jsdom has no layout engine; see
`twitter.test.ts`.
