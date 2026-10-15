export const ACCEPTED_AUTO_CLOSE_MS = 1600;

export interface AcceptedResultForClose {
  phase: string;
  warning?: string;
}

export function shouldAutoCloseFreshCapture(result: AcceptedResultForClose): boolean {
  return !result.warning &&
    (result.phase === "accepted" || result.phase === "pending_transfer" || result.phase === "complete");
}

export function acceptedCaptureNotice(
  result: AcceptedResultForClose,
  options: { fresh: boolean; mediaPermissionDenied?: boolean },
): { message: string; kind: "success" | "error"; autoClose: boolean } {
  const base = options.fresh
    ? "Manifest accepted by Fragments Engine."
    : "The same capture remains accepted by Fragments Engine.";
  const transfer = result.phase === "pending_transfer"
    ? " Media continues independently in the background."
    : "";
  const permission = options.mediaPermissionDenied
    ? " Some media access was denied; the accepted manifest and IDs remain safe."
    : "";
  const warning = result.warning
    ? ` Attention needed: ${result.warning}`
    : "";
  const needsAttention = Boolean(permission || warning);
  return {
    message: `${base}${transfer}${permission}${warning}`,
    kind: needsAttention ? "error" : "success",
    autoClose: options.fresh && !needsAttention && shouldAutoCloseFreshCapture(result),
  };
}

export function scheduleAcceptedPopupClose(
  closePopup: () => void,
  schedule: (callback: () => void, delay: number) => unknown = window.setTimeout.bind(window),
): unknown {
  return schedule(closePopup, ACCEPTED_AUTO_CLOSE_MS);
}
