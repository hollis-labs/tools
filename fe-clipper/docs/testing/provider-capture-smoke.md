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
| Installed extension on real pages | PASS after W5.1 correction | The initial eight-row run used extension `c2d2770` against disposable FE `4e87a8d`. W5.1 later found that the captioned YouTube row had falsely uploaded empty responses. Extension `1116d4c` rejects those responses before digest/upload; the corrected row and Pinterest reuse replay were rerun against FE `e476827`. |

### Installed-extension evidence (UTC 2026-09-04)

| Row | Safe page identity | Capture / attempt | Fragment / revision | Result |
|---|---|---|---|---|
| Generic without media | `https://example.com/` | `2c85ee61-33d9-4bd4-93e7-075ec5d88e75` / `79d7d333...` | `1cfaab5c...` / `b323dab1...` | Complete; useful body and annotations, empty media |
| Generic deliberate recapture | `https://example.com/` | `9febe0db-125c-4bbd-9aca-533604ea6cec` / `1e6a4663...` | same / same | Complete; new attempt, capture count 2, tags unioned and annotations appended |
| Generic with media | Wikipedia `Cat` | `3c44e3af-5c92-40de-b51c-da954d98d145` / `828fb0e1...` | `e2f3d6d...` / `a929c193...` | Complete; 33 ordered media available; accepted job resumed after worker reload |
| YouTube captions | video `aircAruvnKk` | `aa0bdd18-996c-409e-90f6-cf930d373ba3` / `da4d8c34...` | `95e851a5...` / `4a311452...` | Superseded by W5.1: posters completed and original video stayed reference-only, but the subtitle uploads were empty and must not be treated as successful evidence |
| YouTube no captions | video `I_6ZcOo6pnk` | `4c96289b-a626-4cd3-8767-1e5743acba17` / `9cfb80e7...` | `948e5569...` / `038e2cfa...` | Complete; explicit captions-unavailable warning, useful playback/posters |
| Pinterest image | pin `104427285104852001` | `6959655f-f708-4ae2-b30d-393122f553bf` / `d7dc04e6...` | `a0dcb18b...` / `947ab178...` | Complete; target original + genuine thumbnail, no recommendation contamination |
| Instagram image | shortcode `DcwApJ2SUNd` | `2e42aec4-2371-4f13-a814-5701776fccb8` / `dfba31da...` | `4fbe188c...` / `38e44bb9...` | Complete; original + thumbnail, no provider warnings |
| Instagram reel | shortcode `DcuQVFLvJMX` | `4d0ed087-c9d1-4dbf-85e8-24bef161bce2` / `4821bf28...` | `26bc253d...` / `d4b976f5...` | Complete; video reference-only and poster available |
| Instagram mixed carousel | shortcode `Dc1amuWmnUY` | `ad6dc7a9-163c-40ba-958a-af7faa737d69` / `0a5320ce...` | `887ab3b6...` / `c4e10c55...` | Complete; all ten items remained ordered, including the video item |

The same-ID permission/restart probe used capture
`ac207267-c955-40ab-b50c-3d641a999c6d`: FE retained exactly one attempt while
the popup surfaced the withheld local media origin; extension reload plus exact-origin
re-grant and Retry completed that job without changing its IDs. A separate transparent
proxy capture (`faf06015-b13e-4a03-a0f0-ee06173c00cb`) observed no Cookie,
Authorization, or Proxy-Authorization on FE requests. The asset PUT carried only
content type, content length, and an RFC 9530 digest.

FE follow-up `CW-20260904-0018` investigated an apparent rejected-annotation
atomicity problem from the first live probe. FE artifact 545 and commit `cf72d8e`
disproved the suspected defect, and the issue is closed. The successful deliberate
recapture rows above remain the positive count/context evidence.

### W5.1 corrective acceptance (UTC 2026-09-04)

Extension `1116d4c` was rebuilt and reloaded against the FE endpoint at
`127.0.0.1:18091` on supplied FE commit `e476827`.

| Row | Capture / attempt | Fragment / revision | Result |
|---|---|---|---|
| YouTube captions, corrected | `41ea912f-ab81-43bf-8cb1-5be56a8c1499` / `d9c9ecce...` | `95e851a5...` / `cff969af...` | FE accepted one attempt. Six posters reused positive-size blobs and playback stayed reference-only. All 31 caption sources returned HTTP-success empty bodies; the extension rejected each before digest/upload and surfaced retryable `failed` state. The current capture bindings remain pending with no blob and created zero new empty-digest assets. |
| Pinterest richer parser observation | `09d259e5-f24c-4e3f-9763-8c03a79a4999` / `bf3e039c...` | `a0dcb18b...` / `d2651363...` | Complete with `reuse_blob` for original and thumbnail. The live page exposed a title and fuller body than the preceding capture, so FE correctly created a new revision and appended the capture note. |
| Pinterest immediate unchanged replay | `09101125-fd6d-4871-b52e-e2b3222c7d10` / `b2e6afa6...` | same / same | Complete with `fragment_outcome=skipped`, capture count 4, `reuse_blob`/`already_available` for both positive-size assets, and a distinct fourth Reader capture note. |

The live FE database still contains the 31 zero-byte subtitle variants created by
the superseded pre-fix YouTube capture, so the aggregate Reader item continues to
show that historical bad state alongside the 31 new pending variants. This W5.1
run proves that the corrected Clipper creates no additional empty blob or
available subtitle; removal or reinterpretation of already-persisted FE variants
belongs to FE data repair, not browser transfer.
