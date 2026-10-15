# Provider Capture and Background Transfer Execution Specification

**Status:** Accepted

**Date:** 2026-09-03

**Project:** fe-clipper (`PRJ-20260903-0001`)

**Torque plan:** `CW-20260903-0030`

**Canonical cross-project architecture:**
`/Users/chrispian/dev/hollis-labs/apps/fragments-engine/docs/browser-capture-reader-architecture.md`

## 1. Outcome

`fe-clipper` becomes a provider-aware, contract-validated capture client that
preserves readable page content and user annotations while extracting the best
available identity, metadata, ordered media, image variants, posters, and
transcript candidates from Pinterest, Instagram, YouTube, and generic articles.

FE accepts the manifest immediately. The extension then acquires preferred media
from the user's browser context in the background, survives Manifest V3 worker
restarts, and reports independent completion or failure for every asset without
losing the capture.

## 2. Ownership boundary

The extension owns the explicit user action, browser-context extraction,
client-generated capture ID, durable local capture job, browser-preferred asset
transfer, and capture status UX.

Fragments Engine owns schemas, validation semantics, stable fragment/revision
resolution, capture history, media custody, blob storage, provider completion,
enrichment, Reader projection, triage, routing, and materialization.

The extension consumes pinned generated FE contract artifacts and must not create
provider-specific transport payloads or a parallel set of hand-maintained FE
DTOs.

## 3. Locked capture behavior

- A provider adapter registry selects Pinterest, Instagram, YouTube, or generic
  extraction through one shared result contract.
- Every adapter also preserves readable page Markdown/text, title/description,
  selected text/highlight, capture note, and user tags.
- The clipper creates one capture ID for each deliberate user action. Retries of
  that job reuse the ID; a later deliberate clip creates a new capture attempt.
- Manifest acceptance is visible success. Media transfer does not block the
  popup or determine whether the text/provenance capture exists.
- The durable job resides outside popup lifetime and can reconcile with FE after
  MV3 service-worker or browser restart.
- Per-variant states are pending, transferring, available, reference-only, or
  failed. Partial capture is valid and retryable.

## 4. Provider extraction

Pinterest extraction resolves stable pin identity and canonical URL, metadata,
and the primary largest observed image plus a genuine thumbnail when available.
It excludes surrounding recommendation images.

Instagram extraction supports image posts, videos/reels, and every item in a
mixed carousel in source order, with original/thumbnail or stream-reference/poster
variants as observed. It never serializes cookies, tokens, or browser
authentication state.

YouTube extraction resolves video identity/canonical URL, metadata, posters,
caption-track/transcript candidates, and the provider media identity needed by
FE playback. It does not automatically download video bytes.

Generic article extraction retains the existing Readability/Turndown behavior and
adds meaningful hero/inline image candidates while excluding obvious chrome,
avatars, decorative repeats, and tracking pixels through deterministic heuristics.

Provider DOM, JSON-LD, and serialized page-state details remain private to their
versioned adapters and are covered by deterministic sanitized fixtures.

## 5. Contract and compatibility behavior

The client generation flow is reproducible from FE's canonical OpenAPI 3.1 and
JSON Schema artifacts. Runtime validation covers requests and responses. The
extension checks advertised FE contract/capability compatibility before capture
and presents actionable version errors.

The generic adapter remains available when no provider adapter matches or a
provider has partial data. Existing ordinary article, highlight, note, tags,
fallback, and error-copy behavior must not regress.

## 6. Media transfer and trust

Browser-preferred acquisition is a user-directed operation, not a general crawl.
The extension requests only required provider host permissions, transfers bytes
promptly when source URLs may expire, follows FE's asset instructions, computes
or verifies available integrity facts, and skips blobs FE already knows.

Cookies, bearer tokens, local storage, page authentication material, and unrelated
headers never enter capture envelopes or media uploads. A failed browser fetch
retains the source locator plus a structured outcome so FE can apply its own
fallback policy.

## 7. Scope fences

This execution does not:

- implement FE persistence, enrichment, provider fallback, Reader, routing, or
  materialization
- define a competing FE contract or provider-specific payload endpoint
- automatically download YouTube video bytes
- build a background crawler or bypass provider controls
- transmit browser credentials or session state to FE
- decide inbox disposition, FFS, folder, collection, or reading semantics
- harden the extension for general multi-tenant distribution

## 8. Exit gate

The fe-clipper track is complete only when all of the following are true:

1. The adapter registry deterministically selects provider adapters or the generic
   fallback while keeping provider internals outside transport/domain types.
2. Generated TypeScript types and runtime validators reproducibly consume the
   pinned FE contract, and incompatible FE versions/capabilities fail with an
   actionable user state.
3. Durable manifest-first capture jobs survive popup close and MV3 worker/browser
   restarts, reconcile before resending, reuse capture IDs for retries, and never
   lose accepted captures because optional assets fail.
4. Pinterest captures stable identity, metadata, page content/annotations, and
   the correct primary original/thumbnail image candidates without recommendation
   contamination.
5. Instagram captures stable post/reel identity, metadata, page
   content/annotations, and every ordered image/video carousel item without
   serializing authentication material.
6. YouTube captures stable video identity, metadata, posters, page
   content/annotations, and available transcript candidates without automatically
   downloading video bytes.
7. Generic articles preserve existing Readability/Turndown, highlight, note, tag,
   fallback, and error behavior while adding meaningful hero/inline media.
8. Browser-preferred media transfer uses scoped permissions, follows FE asset
   instructions idempotently, resumes per-variant state, skips known blobs, and
   leaves structured reference/failure outcomes for FE fallback.
9. Popup/background UX distinguishes accepted, pending, transferring, partial,
   failed, retrying, and complete work; retry resumes one capture rather than
   creating another attempt.
10. Deterministic fixtures and real-page smoke tests cover provider, sparse,
    partial, retry, deliberate recapture, contract, and credential-exclusion
    scenarios against a compatible FE instance.
11. Build, typecheck, test, lint, package, permission review, generated-contract
    reproducibility, documentation, and final review gates pass without changing
    FE-owned code or violating a scope fence.

## 9. Execution authority

This specification and the canonical FE architecture own scope and semantics.
The companion plan owns sequence. Torque plan `CW-20260903-0030` owns live task
status, dependencies, required checklists, and evidence.

Implementation details may adapt to repository reality. Changes to FE contract
ownership, credential boundaries, browser-first acquisition, stable capture ID
semantics, provider/media scope, or exit criteria require owner review and
cross-project coordination.
