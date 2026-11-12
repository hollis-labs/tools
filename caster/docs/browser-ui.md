# Browser UI

`caster serve` serves a small hand-written page at `/` (the same port you forward): list and search notes, view them
rendered as markdown, edit and save (each save is a new revision), and browse history. It calls the same `/api` routes
as the CLI and MCP. Sign in with the owner's username and password (`POST /api/login`): a session cookie, with
`X-Requested-With` and a same-origin `Origin` required for writes. There is no pairing dialog and no token in the
page. Create the owner offline first: `caster identity setup --username <owner>`.

Stored text is rendered with DOM calls (no HTML), links are limited to `http(s)` and `caster://`, a CSP (no inline script or style,
no third-party origin) and `nosniff` ride on every response outside `/api/` and `/healthz`, and raw `get` is forced to `text/plain`.
The page is deliberately small and is not to grow: a UI on the shared kits is a later task for the GUI track.
