import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";

import { PageCaptureAdapterRegistry, toPageCaptureLocation } from "../../registry";
import type { AdapterMetadataValue, PageCaptureContext } from "../../types";
import { buildCaptureEnvelope } from "../../envelope";
import conversationHtml from "./fixtures/conversation.html?raw";
import credentialExclusionHtml from "./fixtures/credential-exclusion.html?raw";
import unrenderedRepliesHtml from "./fixtures/unrendered-replies.html?raw";
import {
  TWITTER_ADAPTER_VERSION,
  applyReplyBudget,
  canonicalTwitterStatusUrl,
  harvestTwitterConversation,
  isTwitterHostname,
  readMountedTweets,
  recordTwitterHarvest,
  statusIdFromUrl,
  twitterPageCaptureAdapter,
  type TwitterHarvestOptions,
  type TwitterScrollEnvironment,
  type TwitterTweetObservation,
} from ".";

const registry = new PageCaptureAdapterRegistry([twitterPageCaptureAdapter]);

const CONVERSATION_URL = "https://x.com/ada_builds/status/1830000000000000001";
const UNRENDERED_URL = "https://twitter.com/rin_writes/status/1840000000000000001";

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

function twitterExtension(result: ReturnType<typeof capture>) {
  const extension = result.extensions.find((item) => item.namespace === "provider.twitter");
  if (!extension) throw new Error("Expected a provider.twitter extension.");
  return extension;
}

function envelopeOptions(captureId: string) {
  return {
    captureId,
    capturedAt: "2026-09-04T00:00:00.000Z",
    principalId: "local-user",
    clientVersion: "test",
    createId: () => "annotation-1",
    digestIdentifier: async (value: string) => `digest-${value.length}`,
  };
}

describe("twitter identity", () => {
  it("recognizes both hosts and status routes", () => {
    expect(isTwitterHostname("x.com")).toBe(true);
    expect(isTwitterHostname("www.twitter.com")).toBe(true);
    expect(isTwitterHostname("mobile.x.com")).toBe(true);
    expect(isTwitterHostname("x.com.example.test")).toBe(false);
    expect(statusIdFromUrl(CONVERSATION_URL)).toBe("1830000000000000001");
    expect(statusIdFromUrl("https://x.com/ada_builds")).toBeUndefined();
  });

  it("canonicalizes to the account-independent permalink", () => {
    const result = capture(conversationHtml, CONVERSATION_URL);
    expect(result.adapter.id).toBe("twitter");
    expect(result.adapter.version).toBe(TWITTER_ADAPTER_VERSION);
    expect(result.identity.provider).toBe("twitter");
    expect(result.identity.providerItemId).toBe("1830000000000000001");
    expect(result.identity.canonicalUrl).toBe(
      canonicalTwitterStatusUrl("1830000000000000001"),
    );
    // A handle change must not orphan the fragment.
    expect(result.identity.canonicalUrl).not.toContain("ada_builds");
  });

  it("leaves profile pages to the generic adapter", () => {
    const profile = context("<html><body><h1>Ada</h1></body></html>", "https://x.com/ada_builds");
    expect(twitterPageCaptureAdapter.matches(profile)).toBe(0);
  });
});

describe("twitter conversation extraction", () => {
  it("splits the root status from its replies and drops promoted content", () => {
    const result = capture(conversationHtml, CONVERSATION_URL);
    const data = twitterExtension(result).data as Record<string, AdapterMetadataValue>;

    expect(data.repliesObserved).toBe(2);
    expect(data.repliesCaptured).toBe(2);
    expect((data.root as Record<string, unknown>)?.handle).toBe("ada_builds");
    expect((data.root as Record<string, unknown>)?.displayName).toBe("Ada Lovelace");

    const replies = data.replies as ReadonlyArray<Record<string, AdapterMetadataValue>>;
    expect(replies.map((reply) => reply.handle)).toEqual(["queue_sre", "flagsmith_fan"]);
    expect(replies[0]?.displayName).toBe("Grace in SRE");
    expect(JSON.stringify(result)).not.toContain("some_vendor");
    expect(JSON.stringify(result)).not.toContain("Promoted content");
  });

  it("puts the root status and replies in the indexed body", () => {
    const result = capture(conversationHtml, CONVERSATION_URL);
    const body = result.document.contentMarkdown;
    expect(body).toContain("drain the queue before you flip the flag");
    expect(body).toContain("## Replies");
    expect(body).toContain("**Grace in SRE** (@queue_sre)");
    expect(body).toContain("lost about nine minutes of writes");
    expect(result.capabilities.observed).toContain("provider_metadata");
  });

  it("reports replies X never rendered instead of implying none exist", () => {
    const result = capture(unrenderedRepliesHtml, UNRENDERED_URL);
    expect(result.warnings.map((warning) => warning.code)).toContain(
      "twitter.replies_unrendered",
    );
    expect(result.document.contentMarkdown).toContain("replies X never rendered");
  });

  it("records that no scroll pre-pass ran for a plain DOM read", () => {
    const result = capture(conversationHtml, CONVERSATION_URL);
    expect(twitterExtension(result).data.scrollHarvested).toBe(false);
  });

  it("keeps credential sentinels out of adapter output and the envelope", async () => {
    const result = capture(
      credentialExclusionHtml,
      "https://x.com/boundary_check/status/1850000000000000001?access_token=QUERY_CREDENTIAL",
    );
    const envelope = await buildCaptureEnvelope(
      result,
      { title: "", tags: [], note: "" },
      envelopeOptions("twitter-credentials"),
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
      expect(serialized, `twitter leaked ${sentinel}`).not.toContain(sentinel);
    }
  });

  it("routes the conversation to the extension placement FE keeps on the fragment", async () => {
    const result = capture(conversationHtml, CONVERSATION_URL);
    const envelope = await buildCaptureEnvelope(
      result,
      { title: "", tags: [], note: "" },
      envelopeOptions("twitter-placement"),
    );
    expect(envelope.document.extensions?.["provider.twitter"]).toBeDefined();
    expect(envelope.extraction.extensions?.["provider.twitter"]).toBeUndefined();
    expect(envelope.extensions).toBeUndefined();
  });
});

/**
 * The scroll pre-pass is driven through an injected environment because jsdom
 * has no layout engine. The fake below models the property that actually
 * matters: X mounts rows only as the viewport reaches them, and remounts rows
 * it had unmounted.
 */
interface FakeScrollOptions {
  totalBatches: number;
  /** Unmount earlier batches as the walk advances, as X does. */
  virtualize?: boolean;
  /** Stop growing scroll height, modeling a page that cannot scroll further. */
  stuckAfterStep?: number;
}

function fakeConversation(options: FakeScrollOptions) {
  const dom = new JSDOM(conversationHtml, { url: CONVERSATION_URL });
  const document = dom.window.document;
  const timeline = document.querySelector('[aria-label="Timeline: Conversation"]')!;
  const pageContext: PageCaptureContext = {
    document,
    location: toPageCaptureLocation(CONVERSATION_URL),
    selectionText: "",
  };

  let y = 0;
  let step = 0;
  let mountedBatch = 0;
  let clock = 0;

  const mountBatch = (batch: number) => {
    if (options.virtualize) {
      for (const cell of timeline.querySelectorAll("[data-fake-batch]")) cell.remove();
    }
    const cell = document.createElement("div");
    cell.setAttribute("data-testid", "cellInnerDiv");
    cell.setAttribute("data-fake-batch", String(batch));
    cell.innerHTML = `
      <article data-testid="tweet">
        <div data-testid="User-Name">
          <a href="/replier${batch}"><span>Replier ${batch}</span></a>
          <a href="/replier${batch}"><span>@replier${batch}</span></a>
          <a href="/replier${batch}/status/18300000000000100${batch}"><time datetime="2026-09-01T11:0${batch}:00.000Z">Sep 1</time></a>
        </div>
        <div data-testid="tweetText" lang="en" dir="auto">Batched reply number ${batch}.</div>
      </article>`;
    timeline.appendChild(cell);
  };

  const environment: TwitterScrollEnvironment = {
    scrollY: () => y,
    scrollX: () => 0,
    viewportHeight: () => 800,
    maxScrollY: () => 10_000,
    scrollTo: (_x, next) => {
      const stuck = options.stuckAfterStep !== undefined && step >= options.stuckAfterStep;
      if (!stuck) y = next;
      step += 1;
      if (!stuck && mountedBatch < options.totalBatches) {
        mountedBatch += 1;
        mountBatch(mountedBatch);
      }
    },
    wait: async () => {
      clock += 100;
    },
    now: () => clock,
  };

  return { context: pageContext, environment, scrollY: () => y };
}

describe("twitter scroll pre-pass", () => {
  const fastOptions: TwitterHarvestOptions = {
    maxDurationMs: 60_000,
    maxSteps: 100,
    idleRoundsBeforeStop: 3,
    settleMs: 0,
  };

  it("accumulates replies that mount only as the walk advances", async () => {
    const fake = fakeConversation({ totalBatches: 5 });
    const harvest = await harvestTwitterConversation(
      fake.context,
      "1830000000000000001",
      fake.environment,
      fastOptions,
    );
    // Two replies were on screen; five more mounted during the walk.
    expect(harvest.tweets.filter((tweet) => !tweet.isRoot)).toHaveLength(7);
    expect(harvest.tweets.some((tweet) => tweet.isRoot)).toBe(true);
    expect(harvest.stoppedEarly).toBe(false);
  });

  it("keeps replies X unmounted behind the walk", async () => {
    const fake = fakeConversation({ totalBatches: 4, virtualize: true });
    const harvest = await harvestTwitterConversation(
      fake.context,
      "1830000000000000001",
      fake.environment,
      fastOptions,
    );
    const handles = harvest.tweets.map((tweet) => tweet.handle);
    // Every batch survives even though only the last is still in the DOM.
    for (const batch of [1, 2, 3, 4]) expect(handles).toContain(`replier${batch}`);
    expect(readMountedTweets(fake.context, "1830000000000000001").length).toBeLessThan(
      harvest.tweets.length,
    );
  });

  it("never double-counts a reply that remounts", async () => {
    const fake = fakeConversation({ totalBatches: 3 });
    const harvest = await harvestTwitterConversation(
      fake.context,
      "1830000000000000001",
      fake.environment,
      fastOptions,
    );
    expect(new Set(harvest.tweets.map((tweet) => tweet.id)).size).toBe(harvest.tweets.length);
  });

  it("restores the reader's scroll position", async () => {
    const fake = fakeConversation({ totalBatches: 3 });
    await harvestTwitterConversation(
      fake.context,
      "1830000000000000001",
      fake.environment,
      fastOptions,
    );
    expect(fake.scrollY()).toBe(0);
  });

  it("stops on a page that will not scroll further", async () => {
    const fake = fakeConversation({ totalBatches: 50, stuckAfterStep: 2 });
    const harvest = await harvestTwitterConversation(
      fake.context,
      "1830000000000000001",
      fake.environment,
      fastOptions,
    );
    expect(harvest.stoppedEarly).toBe(false);
    expect(harvest.scrollSteps).toBeLessThan(10);
  });

  it("gives up on an endless conversation and says so", async () => {
    const fake = fakeConversation({ totalBatches: Number.MAX_SAFE_INTEGER });
    const harvest = await harvestTwitterConversation(
      fake.context,
      "1830000000000000001",
      fake.environment,
      { ...fastOptions, maxSteps: 6 },
    );
    expect(harvest.stoppedEarly).toBe(true);
    expect(harvest.scrollSteps).toBe(6);
  });

  it("respects the time budget even while replies keep arriving", async () => {
    const fake = fakeConversation({ totalBatches: Number.MAX_SAFE_INTEGER });
    const harvest = await harvestTwitterConversation(
      fake.context,
      "1830000000000000001",
      fake.environment,
      { ...fastOptions, maxDurationMs: 500 },
    );
    expect(harvest.stoppedEarly).toBe(true);
    expect(harvest.scrollSteps).toBeLessThanOrEqual(6);
  });

  it("feeds the harvest into the synchronous stages", () => {
    const pageContext = context(conversationHtml, CONVERSATION_URL);
    const harvested: TwitterTweetObservation[] = [
      { id: "1830000000000000001", handle: "ada_builds", text: "Root", isRoot: true },
      { id: "9001", handle: "offscreen_one", text: "A reply that was never on screen.", isRoot: false },
      { id: "9002", handle: "offscreen_two", text: "Another one.", isRoot: false },
    ];
    recordTwitterHarvest(pageContext.document, {
      tweets: harvested,
      stoppedEarly: false,
      moreAvailable: false,
      scrollSteps: 4,
    });

    const result = registry.capture(pageContext);
    const data = twitterExtension(result).data as Record<string, AdapterMetadataValue>;
    expect(data.scrollHarvested).toBe(true);
    expect(data.repliesObserved).toBe(2);
    expect(result.document.contentMarkdown).toContain("@offscreen_two");
  });
});

describe("reply size budget", () => {
  const replies: TwitterTweetObservation[] = Array.from({ length: 5 }, (_, index) => ({
    id: `reply-${index}`,
    handle: `handle${index}`,
    text: "A reply body long enough to consume a measurable share of the budget.",
    isRoot: false,
  }));

  it("keeps everything when it fits", () => {
    const budget = applyReplyBudget(replies, 100_000);
    expect(budget.kept).toBe(5);
    expect(budget.truncated).toBe(false);
  });

  it("drops the tail and reports the drop", () => {
    const budget = applyReplyBudget(replies, 400);
    expect(budget.observed).toBe(5);
    expect(budget.kept).toBeLessThan(5);
    expect(budget.truncated).toBe(true);
  });
});
