# Reddit adapter fixtures

These pages are synthetic, deterministic representations of stable `shreddit`
and `old.reddit` post and comment markup. They contain no production page
captures, account cookies, session material, or working credentials. Values in
the credential-exclusion fixture are conspicuous test sentinels used only to
prove that non-allowlisted state never reaches adapter output.

| Fixture | Covers |
|---|---|
| `shreddit-thread.html` | Current web client: flat `<shreddit-comment>` elements re-nested by `parentid`/`depth`, scores, moderator distinction, a collapsed comment |
| `old-reddit-thread.html` | Server-rendered client: `.thing.comment` nesting through `.child > .listing`, `title`-attribute scores, an excluded "load more comments" affordance |
| `no-comments.html` | Post page with zero rendered comments plus an unexpanded-replies affordance |
| `credential-exclusion.html` | Sentinel state and a credential-bearing comment permalink that must not survive into adapter output or the envelope |
