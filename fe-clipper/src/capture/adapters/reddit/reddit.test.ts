import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";

import { PageCaptureAdapterRegistry, toPageCaptureLocation } from "../../registry";
import type { AdapterMetadataValue, PageCaptureContext } from "../../types";
import { buildCaptureEnvelope } from "../../envelope";
import credentialExclusionHtml from "./fixtures/credential-exclusion.html?raw";
import noCommentsHtml from "./fixtures/no-comments.html?raw";
import oldRedditHtml from "./fixtures/old-reddit-thread.html?raw";
import shredditHtml from "./fixtures/shreddit-thread.html?raw";
import {
  REDDIT_ADAPTER_VERSION,
  applyCommentBudget,
  canonicalRedditPostUrl,
  isRedditHostname,
  postIdFromUrl,
  redditPageCaptureAdapter,
  renderCommentsMarkdown,
  type RedditCommentObservation,
} from ".";

const registry = new PageCaptureAdapterRegistry([redditPageCaptureAdapter]);

const SHREDDIT_URL =
  "https://www.reddit.com/r/houseplants/comments/abc123/which_soil_mix_actually_drains/";
const OLD_REDDIT_URL =
  "https://old.reddit.com/r/metalworking/comments/def456/best_way_to_anneal_brass/";

function context(html: string, url: string, selectionText = ""): PageCaptureContext {
  return {
    document: new JSDOM(html, { url }).window.document,
    location: toPageCaptureLocation(url),
    selectionText,
  };
}

function capture(html: string, url: string, selectionText = "") {
  return registry.capture(context(html, url, selectionText));
}

function redditExtension(result: ReturnType<typeof capture>) {
  const extension = result.extensions.find((item) => item.namespace === "provider.reddit");
  if (!extension) throw new Error("Expected a provider.reddit extension.");
  return extension;
}

function comment(id: string, depth: number, body: string, replies: RedditCommentObservation[] = []) {
  return { id, author: `author-${id}`, depth, body, replies } satisfies RedditCommentObservation;
}

describe("reddit identity", () => {
  it("recognizes reddit hosts and post routes", () => {
    expect(isRedditHostname("www.reddit.com")).toBe(true);
    expect(isRedditHostname("old.reddit.com")).toBe(true);
    expect(isRedditHostname("reddit.com.example.test")).toBe(false);
    expect(postIdFromUrl(SHREDDIT_URL)).toBe("abc123");
    expect(postIdFromUrl("https://www.reddit.com/comments/abc123/")).toBe("abc123");
    expect(postIdFromUrl("https://www.reddit.com/r/houseplants/")).toBeUndefined();
  });

  it("canonicalizes to the subreddit-free comments route", () => {
    const result = capture(shredditHtml, SHREDDIT_URL);
    expect(result.adapter.id).toBe("reddit");
    expect(result.adapter.version).toBe(REDDIT_ADAPTER_VERSION);
    expect(result.identity.provider).toBe("reddit");
    expect(result.identity.providerItemId).toBe("abc123");
    expect(result.identity.canonicalUrl).toBe(canonicalRedditPostUrl("abc123"));
  });

  it("leaves subreddit listings to the generic adapter", () => {
    const listing = context("<html><body><h1>r/houseplants</h1></body></html>",
      "https://www.reddit.com/r/houseplants/");
    expect(redditPageCaptureAdapter.matches(listing)).toBe(0);
  });
});

describe("shreddit comment extraction", () => {
  it("re-nests the flat comment list by parent and depth", () => {
    const extension = redditExtension(capture(shredditHtml, SHREDDIT_URL));
    const data = extension.data as Record<string, AdapterMetadataValue>;
    expect(data.surface).toBe("shreddit");
    expect(data.commentsObserved).toBe(4);
    expect(data.commentsCaptured).toBe(4);
    expect(data.commentsTruncated).toBe(false);

    const comments = data.comments as ReadonlyArray<Record<string, AdapterMetadataValue>>;
    expect(comments).toHaveLength(2);
    expect(comments[0]?.author).toBe("bark_and_pumice");
    expect(comments[0]?.score).toBe(412);

    const firstReplies = comments[0]?.replies as ReadonlyArray<Record<string, AdapterMetadataValue>>;
    expect(firstReplies).toHaveLength(1);
    expect(firstReplies[0]?.author).toBe("repot_regret");

    const nested = firstReplies[0]?.replies as ReadonlyArray<Record<string, AdapterMetadataValue>>;
    expect(nested[0]?.author).toBe("mod_of_dirt");
    expect(nested[0]?.distinguished).toBe("moderator");
    expect(nested[0]?.depth).toBe(2);

    expect(comments[1]?.author).toBe("terracotta_only");
    expect(comments[1]?.collapsed).toBe(true);
  });

  it("captures post metadata alongside the thread", () => {
    const extension = redditExtension(capture(shredditHtml, SHREDDIT_URL));
    expect(extension.data.post).toMatchObject({
      subreddit: "houseplants",
      author: "planter_prime",
      score: 482,
      commentCount: 3,
    });
  });

  it("appends the thread to the document body so FE can index it", () => {
    const result = capture(shredditHtml, SHREDDIT_URL);
    const body = result.document.contentMarkdown;
    expect(body).toContain("root rot");
    expect(body).toContain("## Comments");
    expect(body).toContain("**u/bark_and_pumice** · 412 points");
    expect(body).toContain("Cut any bagged mix with pumice");
    expect(body).toContain("  - **u/repot_regret** · 88 points");
    expect(body).toContain("    - **u/mod_of_dirt** · 35 points · moderator");
    expect(result.capabilities.observed).toContain("body");
    expect(result.capabilities.observed).toContain("provider_metadata");
  });
});

describe("old.reddit comment extraction", () => {
  it("walks nested child listings and excludes load-more affordances", () => {
    const result = capture(oldRedditHtml, OLD_REDDIT_URL);
    const data = redditExtension(result).data as Record<string, AdapterMetadataValue>;
    expect(data.surface).toBe("old_reddit");
    expect(data.commentsObserved).toBe(2);
    expect(data.post).toMatchObject({ subreddit: "metalworking", author: "cold_work" });

    const comments = data.comments as ReadonlyArray<Record<string, AdapterMetadataValue>>;
    expect(comments).toHaveLength(1);
    expect(comments[0]?.author).toBe("torch_and_tongs");
    expect(comments[0]?.score).toBe(164);

    const replies = comments[0]?.replies as ReadonlyArray<Record<string, AdapterMetadataValue>>;
    expect(replies).toHaveLength(1);
    expect(replies[0]?.author).toBe("water_quench");
    expect(result.document.contentMarkdown).not.toContain("load more comments");
  });

  it("warns that the page still had unexpanded replies", () => {
    const result = capture(oldRedditHtml, OLD_REDDIT_URL);
    expect(result.warnings.map((warning) => warning.code)).toContain("reddit.comments_unexpanded");
    expect(result.document.contentMarkdown).toContain("unexpanded replies");
  });
});

describe("reddit thread edge cases", () => {
  it("captures a post with no rendered comments without inventing a section", () => {
    const result = capture(
      noCommentsHtml,
      "https://www.reddit.com/r/synthesizers/comments/ghi789/just_posted_no_replies_yet/",
    );
    expect(result.document.contentMarkdown).toContain("cutoff tracking");
    expect(result.document.contentMarkdown).not.toContain("## Comments");
    const codes = result.warnings.map((warning) => warning.code);
    expect(codes).toContain("reddit.comments_absent");
    expect(codes).toContain("reddit.comments_unexpanded");
    expect(redditExtension(result).data.commentsObserved).toBe(0);
  });

  it("keeps credential sentinels out of adapter output and the envelope", async () => {
    const result = capture(
      credentialExclusionHtml,
      "https://www.reddit.com/r/testing/comments/jkl012/credential_boundary_fixture/?access_token=QUERY_CREDENTIAL",
    );
    const envelope = await buildCaptureEnvelope(
      result,
      { title: "", tags: [], note: "" },
      {
        captureId: "reddit-credentials",
        capturedAt: "2026-09-04T00:00:00.000Z",
        principalId: "local-user",
        clientVersion: "test",
        createId: () => "annotation-1",
        digestIdentifier: async (value) => `digest-${value.length}`,
      },
    );
    const serialized = `${JSON.stringify(result)}\n${JSON.stringify(envelope)}`;
    for (const sentinel of [
      "QUERY_CREDENTIAL",
      "TEST_ONLY_ACCESS_SECRET_MUST_NOT_LEAK",
      "TEST_ONLY_CSRF_SECRET_MUST_NOT_LEAK",
      "TEST_ONLY_COOKIE_SECRET_MUST_NOT_LEAK",
      "TEST_ONLY_BEARER_SECRET_MUST_NOT_LEAK",
      "TEST_ONLY_LOCAL_STORAGE_MUST_NOT_LEAK",
      "TEST_ONLY_HEADER_MUST_NOT_LEAK",
      "TEST_ONLY_RAW_STATE_MUST_NOT_LEAK",
      "TEST_ONLY_SESSION_STORAGE_MUST_NOT_LEAK",
    ]) {
      expect(serialized, `reddit leaked ${sentinel}`).not.toContain(sentinel);
    }
  });

  it("routes the thread to document extensions, which is the field FE keeps on the fragment", async () => {
    const result = capture(shredditHtml, SHREDDIT_URL);
    const envelope = await buildCaptureEnvelope(
      result,
      { title: "", tags: [], note: "" },
      {
        captureId: "reddit-placement",
        capturedAt: "2026-09-04T00:00:00.000Z",
        principalId: "local-user",
        clientVersion: "test",
        createId: () => "annotation-1",
        digestIdentifier: async (value) => `digest-${value.length}`,
      },
    );
    expect(envelope.document.extensions?.["provider.reddit"]).toBeDefined();
    expect(envelope.extraction.extensions?.["provider.reddit"]).toBeUndefined();
    // The envelope root is schema-valid but FE never reads it, so nothing
    // should be spending manifest bytes there.
    expect(envelope.extensions).toBeUndefined();
    expect(envelope.document.content?.body).toContain("**u/bark_and_pumice**");
  });
});

describe("comment size budget", () => {
  const tree = [
    comment("a", 0, "alpha", [comment("b", 1, "beta", [comment("c", 2, "gamma")])]),
    comment("d", 0, "delta"),
  ];

  it("keeps the whole thread when it fits", () => {
    const budget = applyCommentBudget(tree, 10_000);
    expect(budget.kept).toBe(4);
    expect(budget.observed).toBe(4);
    expect(budget.truncated).toBe(false);
  });

  it("drops the tail and reports what it dropped", () => {
    const budget = applyCommentBudget(tree, 200);
    expect(budget.observed).toBe(4);
    expect(budget.kept).toBeLessThan(4);
    expect(budget.truncated).toBe(true);
    // A dropped comment never leaves orphaned replies behind.
    const flattened = JSON.stringify(budget.comments);
    if (!flattened.includes('"id":"b"')) expect(flattened).not.toContain('"id":"c"');
  });

  it("announces truncation in the rendered heading", () => {
    const budget = applyCommentBudget(tree, 200);
    const markdown = renderCommentsMarkdown(budget, false);
    expect(markdown).toContain(`${budget.kept} of 4 captured`);
  });

  it("renders nothing when no comments were retained", () => {
    expect(renderCommentsMarkdown(applyCommentBudget([], 100), false)).toBe("");
  });
});
