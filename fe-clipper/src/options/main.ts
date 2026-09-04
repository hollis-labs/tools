import { requestHostPermission } from "@/lib/permissions";
import {
  DEFAULT_ENDPOINT,
  DEFAULT_PRINCIPAL_ID,
  getCaptureConfiguration,
  normalizeCaptureEndpoint,
  setEndpoint,
  setPrincipalId,
} from "@/lib/storage";

const formEl = document.getElementById("options-form") as HTMLFormElement;
const endpointInput = document.getElementById("endpoint-input") as HTMLInputElement;
const principalInput = document.getElementById("principal-input") as HTMLInputElement;
const saveButton = document.getElementById("save-button") as HTMLButtonElement;
const statusEl = document.getElementById("status") as HTMLDivElement;

function setStatus(message: string, kind: "error" | "success" | "idle" = "idle") {
  statusEl.textContent = message;
  statusEl.dataset.kind = kind;
}

async function loadCurrentEndpoint() {
  const configuration = await getCaptureConfiguration();
  endpointInput.value = configuration.endpoint;
  principalInput.value = configuration.principalId;
}

async function handleSubmit(event: SubmitEvent) {
  event.preventDefault();
  const value = endpointInput.value.trim();
  const principalId = principalInput.value.trim();

  if (!principalId || principalId.length > 255) {
    setStatus("Principal ID must contain 1–255 characters.", "error");
    return;
  }

  let parsed: URL;
  try {
    parsed = new URL(normalizeCaptureEndpoint(value));
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error);
    setStatus(`Enter a safe full URL, e.g. http://127.0.0.1:8091/v1/captures. ${detail}`, "error");
    return;
  }

  saveButton.disabled = true;
  setStatus("Requesting permission for this origin...", "idle");
  try {
    const granted = await requestHostPermission(parsed.toString());
    if (!granted) {
      setStatus(`Permission to reach ${parsed.origin} was denied; endpoint not saved.`, "error");
      return;
    }
    await Promise.all([setEndpoint(parsed.toString()), setPrincipalId(principalId)]);
    setStatus(`Saved. New clips will use ${principalId} at ${parsed.origin}.`, "success");
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    setStatus(`Failed to save: ${message}`, "error");
  } finally {
    saveButton.disabled = false;
  }
}

formEl.addEventListener("submit", (event) => {
  void handleSubmit(event);
});

endpointInput.placeholder = DEFAULT_ENDPOINT;
principalInput.placeholder = DEFAULT_PRINCIPAL_ID;
void loadCurrentEndpoint();
