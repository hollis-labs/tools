import type { CaptureJobSummary } from "@/lib/types";
import { presentCaptureJob, variantStateLabel } from "./jobView";

export class CaptureJobListRenderer {
  private snapshot = "";

  constructor(
    private readonly section: HTMLElement,
    private readonly list: HTMLElement,
  ) {}

  render(jobs: readonly CaptureJobSummary[], busy = false): boolean {
    const snapshot = JSON.stringify(jobs);
    if (snapshot === this.snapshot) {
      this.applyBusy(busy);
      return false;
    }
    const focused = this.focusedControl();
    this.snapshot = snapshot;
    this.list.replaceChildren();
    this.section.hidden = jobs.length === 0;
    for (const job of jobs) this.list.append(this.card(job, busy));
    this.applyBusy(busy);
    this.restoreFocus(focused);
    return true;
  }

  private card(job: CaptureJobSummary, busy: boolean): HTMLElement {
    const presentation = presentCaptureJob(job);
    const card = document.createElement("article");
    card.className = "capture-job";
    card.dataset.tone = presentation.tone;
    card.dataset.captureId = job.captureId;
    card.setAttribute("aria-label", presentation.ariaLabel);

    const heading = document.createElement("h3");
    heading.className = "capture-job__heading";
    heading.textContent = job.title || "Untitled capture";
    card.append(heading);

    const meta = document.createElement("p");
    meta.className = "capture-job__meta";
    meta.textContent = `${job.provider} · ${new Date(job.updatedAt).toLocaleString()}`;
    card.append(meta);

    const state = document.createElement("p");
    state.className = "capture-job__status";
    state.setAttribute("role", "status");
    state.setAttribute("aria-live", "polite");
    state.textContent = presentation.stateLabel;
    card.append(state);

    const detail = document.createElement("p");
    detail.className = "capture-job__detail";
    detail.textContent = presentation.detail;
    card.append(detail);

    if (job.variants.length > 0) {
      const variants = document.createElement("ul");
      variants.className = "capture-job__variants";
      for (const variant of job.variants) {
        const item = document.createElement("li");
        item.textContent = variantStateLabel(variant);
        if (variant.reason) {
          const reason = document.createElement("span");
          reason.className = "capture-job__reason";
          const retryability = variant.retryable === true
            ? " (retryable)"
            : variant.retryable === false
              ? " (not retryable)"
              : "";
          reason.textContent = `${variant.reason}${retryability}`;
          item.append(reason);
        }
        if (variant.sourceOrigin && variant.state !== "available") {
          const origin = document.createElement("span");
          origin.className = "capture-job__origin";
          origin.textContent = variant.sourceOrigin;
          item.append(origin);
        }
        variants.append(item);
      }
      card.append(variants);
    }

    if (job.error) {
      const error = document.createElement("p");
      error.className = "capture-job__error";
      error.textContent = job.error.message;
      card.append(error);
    }

    const actions = document.createElement("div");
    actions.className = "capture-job__actions";
    if (presentation.canRetry) this.button(actions, "Retry", "retry", job.captureId, busy);
    if (presentation.canDismiss) this.button(actions, "Dismiss", "dismiss", job.captureId, busy);
    if (job.error) this.button(actions, "Copy error", "copy", job.captureId, busy);
    card.append(actions);
    return card;
  }

  private button(
    container: HTMLElement,
    label: string,
    action: "retry" | "dismiss" | "copy",
    captureId: string,
    busy: boolean,
  ): void {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = label;
    button.dataset.action = action;
    button.dataset.captureId = captureId;
    button.setAttribute("aria-disabled", String(busy && action !== "copy"));
    container.append(button);
  }

  private focusedControl(): { captureId: string; action?: string } | undefined {
    const active = document.activeElement;
    if (!(active instanceof HTMLElement) || !this.list.contains(active)) return undefined;
    const captureId = active.dataset.captureId ?? active.closest<HTMLElement>("[data-capture-id]")?.dataset.captureId;
    return captureId ? { captureId, ...(active.dataset.action ? { action: active.dataset.action } : {}) } : undefined;
  }

  private restoreFocus(focused: { captureId: string; action?: string } | undefined): void {
    if (!focused) return;
    const cards = [...this.list.querySelectorAll<HTMLElement>("[data-capture-id]")];
    const card = cards.find((candidate) => candidate.dataset.captureId === focused.captureId);
    if (!card) return;
    const buttons = [...card.querySelectorAll<HTMLButtonElement>("button[data-action]")];
    const target = buttons.find((button) => button.dataset.action === focused.action) ?? buttons[0];
    if (target) {
      target.focus();
    } else {
      card.tabIndex = -1;
      card.focus();
    }
  }

  private applyBusy(busy: boolean): void {
    for (const button of this.list.querySelectorAll<HTMLButtonElement>("button[data-capture-id]")) {
      button.setAttribute(
        "aria-disabled",
        String(busy && button.dataset.action !== "copy"),
      );
    }
  }
}
