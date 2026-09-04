# Provider capture conformance and smoke matrix

This matrix separates deterministic contract evidence, direct live Fragments
Engine evidence, and installed-extension browser evidence. The first two are
repeatable commands. The browser rows remain manual because provider pages and
browser sessions are not stable CI dependencies.

## Deterministic fixture gate

Run:

```sh
npm run test:conformance
```

The scenario manifest is
`src/conformance/fixtures/provider-scenarios.json`. Every fixture is synthetic,
sanitized, committed, and network-independent. The suite runs the production
adapter registry and envelope mapper, then validates the envelope plus modeled
manifest, status, and completion responses against the pinned FE schemas and
cross-object semantic identity rules.

| Scenario | Required evidence |
|---|---|
| Pinterest image pin | Stable pin identity; readable text, highlight, note, and tags; original then genuine thumbnail; no recommendation media |
| Pinterest partial and sparse | Safe Open Graph fallback, structured warnings, original-only and no-media cases |
| Instagram image | Stable post identity; readable text and annotations; original then thumbnail |
| Instagram video/reel | Stream remains reference-only; poster remains browser-preferred |
| Instagram mixed gallery | Every image/video item remains in source order; each item's variants remain ordered |
| Instagram sparse gallery | Unsupported item is explicitly warned and omitted only at the FE-compatible envelope boundary; surviving positions compact deterministically |
| Instagram credential boundary | Cookie, storage, auth, header, URL-userinfo, and token sentinels never enter adapter output or the envelope; signed source observations remain |
| YouTube with captions | Playback identity, three ordered posters, two subtitle candidates, structured rendered transcript, body, and highlight survive; no automatic video-byte request |
| YouTube without captions | Playback identity and poster survive; transcript is absent with a structured warning |
| YouTube sparse/malformed | Playback reference survives malformed page state; missing poster/caption/metadata warnings remain explicit |
| Generic article with media | Readability/Turndown body, selection, hero, and ordered inline media survive; inherited URL credentials are removed from Markdown |
| Generic article without media | Useful text and annotations survive with an empty media manifest |

Modeled response coverage includes immediate acceptance, exact envelope ↔
instruction/status client-variant sets, outer ↔ Reader stable IDs, ordered
attachment/revision/media bindings, shared FE asset variants, rejected variants,
partial completion, and credential-free payloads.

## Direct live FE gate

Use a disposable or explicitly approved compatible FE instance. Never point this
harness at valued production data. The run ID must be unique because the test
deliberately creates three capture attempts.

```sh
FE_CAPTURE_SMOKE_ENDPOINT=http://127.0.0.1:18091 \
FE_CAPTURE_SMOKE_RUN_ID=clipper-smoke-YYYYMMDD-HHMMSS \
npm run test:conformance:live
```

The live test first checks FE capabilities. It then extracts the committed
sanitized Instagram mixed-gallery fixture through the production adapter and
uses the production API client to prove:

1. the first manifest is accepted immediately;
2. same-ID replay returns the same fragment, revision, and attempt without
   incrementing capture count;
3. status covers the exact submitted variant IDs;
4. deterministic unavailable-media outcomes produce valid partial completion;
5. a new capture ID with changed gallery order reuses the fragment and creates a
   new revision and attempt;
6. a third capture ID with unchanged source material reuses that revision;
7. capture count increments only for the two deliberate recaptures;
8. tags are unioned and capture annotations are additive in the authoritative
   Reader detail projection.

This is live API evidence, not installed-extension or real-page evidence. It
does not contact fixture media hosts or send credentials.

## Installed-extension real-page gate

Prerequisites:

1. Run `npm run contracts:check`, `npm run build`, and load `dist/` as an
   unpacked extension in a clean Chrome test profile.
2. Configure the same compatible disposable FE endpoint used for evidence.
3. Confirm `/v1/capabilities` advertises `fe.capture.v1`,
   `fe.capture.result.v1`, `fe.capture.status.v1`, and
   `fe.capture.completion.v1`, with manifest, upload, completion, and Reader
   query operations enabled.
4. Use content you are authorized to view. Do not record cookies, tokens,
   signed URLs, private page state, or source bytes in the evidence log.

For every row, select a short visible phrase, add a unique note and tag, click
Send once, and record only safe IDs/state from the popup and FE response. Verify
the popup reports acceptance before optional media completes, the Reader item is
immediately visible, body/highlight/note/tag survive, and ordered media matches
the page. A provider warning or unavailable optional asset is valid only when
the text capture and safe source reference survive.

| Row | Real-page choice | Extraction and Reader checks | Transfer/status checks |
|---|---|---|---|
| Pinterest image | One public image pin with a visible close-up and surrounding recommendations | Canonical pin ID; readable text; only target image; original/thumbnail are not invented or upscaled | Accepted → pending/transferring → complete or truthful partial |
| Instagram image | One viewable single-image post | Stable shortcode; caption/body; original and thumbnail; no unrelated feed cards | Immediate acceptance; permitted source transfers or explicit fallback |
| Instagram video | One viewable reel | Stable shortcode; video reference and poster; no cookie/auth/page-state data | Video bytes remain reference-only; poster may transfer |
| Instagram mixed gallery | One viewable image/video carousel | Every item and variant remains in visible source order | Failure of one item does not reorder or erase siblings |
| YouTube captions | One public video with a caption track | Stable video ID; body/highlight; posters; subtitles/transcript candidate | Video bytes are never automatically requested; poster/text may transfer |
| YouTube no captions | One public video with captions unavailable | Stable video ID and poster; explicit missing-transcript state | Capture remains useful and can complete/partial independently |
| Generic with media | One public article with a hero and inline image | Canonical URL; Readability body; selection; hero before inline media | Available media transfer independently of accepted text |
| Generic without media | One public text article | Full useful body and annotations; empty media is not an error | Accepted → complete without source-media work |

Cross-cutting retry and security checks:

1. For one media row, revoke only that source origin after manifest acceptance.
   Verify a retryable permission/partial state, then re-grant from the popup and
   click Retry. The capture ID, fragment ID, attempt ID, revision ID, and capture
   count must not change.
2. Deliberately clip the same page again. The new capture ID and attempt ID must
   differ, the stable fragment must match, unchanged material must reuse the
   revision, capture count must increment once, tags must union, and the new
   highlight/note must append. If source body or gallery order genuinely changed,
   require a new revision under the same fragment.
3. Inspect the extension-origin requests in DevTools. Manifest and completion
   bodies must not contain cookies, bearer/basic authorization, local/session
   storage, page userinfo, or credential-shaped query/fragment values. FE
   requests use `credentials: omit`; upload headers are limited to content type
   and RFC 9530 digest facts, with no referrer.
4. Close the popup after acceptance and restart the extension service worker.
   The job must GET status before any uncertain resend and resume the same ID.

Record each row as `PASS`, `FAIL`, or `BLOCKED`, with UTC time, extension commit,
FE commit, sanitized page identity (provider item ID or origin/path without
credential parameters), capture/fragment/revision/attempt IDs, final state, and
brief observations. A row is not `PASS` when only fixture or direct-API evidence
exists.

## Evidence from 2026-09-04

| Layer | Result | Evidence |
|---|---|---|
| Deterministic fixtures | PASS | 13 scenarios; production adapter/envelope path plus pinned manifest/status/completion validators |
| Direct compatible FE | PASS | Implementer runs `clipper-0056-agent-a` and `clipper-0056-agent-b`; FE `4e87a8d` (includes required integration `8075d14`), pinned browser contract `118b771`; manifest acceptance, replay, partial completion, changed/unchanged recapture, Reader detail |
| Installed extension on real pages | BLOCKED / not run | Browser-control discovery returned no available browser sessions in both implementer and orchestrator environments |

The live FE positive path is independent of FE issue `CW-20260904-0018`, which
tracks a server-side finding where a rejected duplicate annotation can advance
capture count before returning HTTP 409. Do not use that invalid path as positive
recapture evidence.
