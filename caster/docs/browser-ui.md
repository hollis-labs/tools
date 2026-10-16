# Browser UI

`caster serve` also serves a small UI at `/` (the same port you forward): list and search notes, view them rendered as markdown, edit and save (each save is a new revision), and browse history. It is another thin wrapper: it calls the same `/v1` routes as the CLI and MCP.

Pair a browser with a link instead of copying a code:

    caster pair -scope read,write,resolve -label browser -link http://localhost:8991

Open the printed URL; a pairing dialog appears with the code filled in. The code is in the URL fragment, which the browser never sends to the server, and it is removed from the address bar on load. The resulting device token lives in `sessionStorage`, so it is gone when the tab closes; pair again next time. Stored text is rendered with DOM calls (no HTML), links are limited to `http(s)` and `caster://`, and a strict CSP keeps the page to its own origin. The shell itself is served without a token and carries no data.
