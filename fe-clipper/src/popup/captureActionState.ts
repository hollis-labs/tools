import type { CaptureJobSummary, ExtensionMessage } from "@/lib/types";

type StartMessage = Extract<ExtensionMessage, { type: "START_CAPTURE_JOB" }>;
type JobResult = Extract<ExtensionMessage, { type: "CAPTURE_JOB_RESULT" }>;

export class CaptureActionState {
  private request?: StartMessage;

  get activeJobId(): string | undefined {
    return this.request?.captureId;
  }

  get activeStartRequest(): StartMessage | undefined {
    return this.request;
  }

  begin(request: StartMessage): void {
    this.request = request;
  }

  applyResult(result: JobResult): void {
    if (!this.request || result.captureId !== this.request.captureId) return;
    if (result.ok || result.terminal) this.request = undefined;
  }

  sync(jobs: readonly CaptureJobSummary[]): boolean {
    if (!this.request) return false;
    if (!jobs.some((job) => job.captureId === this.request!.captureId && job.terminal)) return false;
    this.request = undefined;
    return true;
  }

  clearMatching(captureId: string): boolean {
    if (this.request?.captureId !== captureId) return false;
    this.request = undefined;
    return true;
  }
}

export class CaptureMutationGuard {
  private active = false;

  get busy(): boolean {
    return this.active;
  }

  begin(): boolean {
    if (this.active) return false;
    this.active = true;
    return true;
  }

  end(): void {
    this.active = false;
  }

  allows(action: "send" | "retry" | "dismiss" | "copy"): boolean {
    return !this.active || action === "copy";
  }
}
