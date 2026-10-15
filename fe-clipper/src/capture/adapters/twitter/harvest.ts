/**
 * The X/Twitter scroll pre-pass.
 *
 * X virtualizes its conversation: only the replies near the viewport are
 * mounted, and it unmounts them again once they are far enough away. A single
 * DOM read therefore sees whatever happened to be on screen. This walks the
 * conversation, accumulating replies as they mount, then puts the page back
 * where it was.
 *
 * Two properties matter and are enforced here rather than trusted:
 *
 * - It terminates. Time, step count, and consecutive-idle-round budgets all
 *   bound the walk, because an infinite feed will happily scroll forever.
 * - It restores the viewport. The user did not ask to lose their place.
 *
 * The environment is injected so the walk is testable without a layout engine:
 * tests supply a fake that mounts rows as it "scrolls".
 */

import type { AdapterExtractionWarning, PageCaptureContext } from "../../types";
import { hasUnrenderedReplies, readMountedTweets } from "./parser";
import type { TwitterTweetObservation } from "./replies";

export interface TwitterHarvest {
  tweets: readonly TwitterTweetObservation[];
  /** True when the walk stopped on a budget rather than reaching the end. */
  stoppedEarly: boolean;
  /** True when the client still showed an unrendered-replies affordance. */
  moreAvailable: boolean;
  scrollSteps: number;
}

export interface TwitterScrollEnvironment {
  scrollY(): number;
  scrollX(): number;
  viewportHeight(): number;
  maxScrollY(): number;
  scrollTo(x: number, y: number): void;
  wait(ms: number): Promise<void>;
  now(): number;
}

export interface TwitterHarvestOptions {
  /** Wall-clock ceiling for the whole walk. */
  maxDurationMs: number;
  /** Hard ceiling on scroll steps, independent of time. */
  maxSteps: number;
  /** Consecutive scrolls yielding no new tweet before stopping. */
  idleRoundsBeforeStop: number;
  /** Pause after each scroll, giving the client time to mount rows. */
  settleMs: number;
}

export const DEFAULT_TWITTER_HARVEST_OPTIONS: TwitterHarvestOptions = {
  maxDurationMs: 20_000,
  maxSteps: 150,
  idleRoundsBeforeStop: 3,
  settleMs: 400,
};

export function browserScrollEnvironment(view: Window): TwitterScrollEnvironment {
  return {
    scrollY: () => view.scrollY,
    scrollX: () => view.scrollX,
    viewportHeight: () => view.innerHeight,
    maxScrollY: () =>
      Math.max(
        0,
        (view.document.documentElement?.scrollHeight ?? 0) - view.innerHeight,
      ),
    scrollTo: (x, y) => view.scrollTo(x, y),
    wait: (ms) => new Promise((resolve) => view.setTimeout(resolve, ms)),
    now: () => Date.now(),
  };
}

const harvests = new WeakMap<Document, TwitterHarvest>();

/**
 * Stores a completed walk for the synchronous stages to read. The stages stay
 * pure observations; this is the seam where the async pre-pass hands off.
 */
export function recordTwitterHarvest(document: Document, harvest: TwitterHarvest): void {
  harvests.set(document, harvest);
}

export function readTwitterHarvest(document: Document): TwitterHarvest | undefined {
  return harvests.get(document);
}

/**
 * Walks the conversation and returns everything it managed to mount, in first
 * -seen order. Tweets are keyed by status id so the client's remounting cannot
 * duplicate them.
 */
export async function harvestTwitterConversation(
  context: PageCaptureContext,
  rootStatusId: string,
  environment: TwitterScrollEnvironment,
  options: TwitterHarvestOptions = DEFAULT_TWITTER_HARVEST_OPTIONS,
): Promise<TwitterHarvest> {
  const startedAt = environment.now();
  const originX = environment.scrollX();
  const originY = environment.scrollY();
  const seen = new Map<string, TwitterTweetObservation>();

  const collect = (): number => {
    let added = 0;
    for (const tweet of readMountedTweets(context, rootStatusId)) {
      if (seen.has(tweet.id)) continue;
      seen.set(tweet.id, tweet);
      added += 1;
    }
    return added;
  };

  try {
    collect();
    let idleRounds = 0;
    let steps = 0;
    let stoppedEarly = false;

    while (idleRounds < options.idleRoundsBeforeStop) {
      if (steps >= options.maxSteps) {
        stoppedEarly = true;
        break;
      }
      if (environment.now() - startedAt >= options.maxDurationMs) {
        stoppedEarly = true;
        break;
      }

      const before = environment.scrollY();
      environment.scrollTo(originX, before + Math.max(1, environment.viewportHeight() - 100));
      steps += 1;
      await environment.wait(options.settleMs);

      const added = collect();
      const moved = environment.scrollY() > before;
      // Stalling at the bottom counts as an idle round too, so a conversation
      // that ends cleanly still exits through the idle path.
      idleRounds = added > 0 && moved ? 0 : idleRounds + 1;
    }

    return {
      tweets: [...seen.values()],
      stoppedEarly,
      moreAvailable: hasUnrenderedReplies(context),
      scrollSteps: steps,
    };
  } finally {
    // Runs even if a collect throws: never strand the reader mid-conversation.
    environment.scrollTo(originX, originY);
  }
}

/**
 * Warnings about the walk itself. Whether replies remain unrendered is not
 * reported here: that is true or false whether or not a walk happened, so the
 * adapter raises it once from the resolved observation instead.
 */
export function harvestWarnings(harvest: TwitterHarvest): AdapterExtractionWarning[] {
  if (!harvest.stoppedEarly) return [];
  return [
    {
      code: "twitter.scroll_budget_exhausted",
      severity: "warning",
      stage: "provider_metadata",
      message: `Stopped walking this conversation after ${harvest.scrollSteps} scroll steps; later replies were not captured.`,
    },
  ];
}
