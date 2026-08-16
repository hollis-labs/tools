import { requestHostPermission } from "@/lib/permissions";
import { DEFAULT_ENDPOINT, getEndpoint, setEndpoint } from "@/lib/storage";

const formEl = document.getElementById("options-form") as HTMLFormElement;
const endpointInput = document.getElementById("endpoint-input") as HTMLInputElement;
const saveButton = document.getElementById("save-button") as HTMLButtonElement;
const statusEl = document.getElementById("status") as HTMLDivElement;

function setStatus(message: string, kind: "error" | "success" | "idle" = "idle") {
  statusEl.textContent = message;
  statusEl.dataset.kind = kind;
}

async function loadCurrentEndpoint() {
  endpointInput.value = await getEndpoint();
}

async function handleSubmit(event: SubmitEvent) {
  event.preventDefault();
  const value = endpointInput.value.trim();

  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    setStatus("Enter a full URL, e.g. http://127.0.0.1:8091/v1/intake", "error");
    return;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    setStatus("Endpoint must be http:// or https://", "error");
    return;
  }

  saveButton.disabled = true;
  setStatus("Requesting permission for this origin...", "idle");
  try {
    const granted = await requestHostPermission(value);
    if (!granted) {
      setStatus(`Permission to reach ${parsed.origin} was denied; endpoint not saved.`, "error");
      return;
    }
    await setEndpoint(value);
    setStatus(`Saved. Clips will POST to ${value}`, "success");
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
void loadCurrentEndpoint();
