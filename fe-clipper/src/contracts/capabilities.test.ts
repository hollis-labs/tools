import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it, vi } from "vitest";

import {
  deriveCapabilitiesUrl,
  preflightCaptureManifest,
  type CapturePreflightResult,
} from "./capabilities";

const fixtureDirectory = resolve(
  process.cwd(),
  "vendor/fragments-engine-browser-contract-v1/fixtures",
);

function fixture(name: string): Record<string, unknown> {
  return JSON.parse(readFileSync(join(fixtureDirectory, name), "utf8"));
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function expectFailure(result: CapturePreflightResult) {
  expect(result.ok).toBe(false);
  if (result.ok) throw new Error("Expected capability preflight to fail.");
  return result;
}

describe("capture manifest capability preflight", () => {
  it("derives the contract route from the configured FE origin", () => {
    expect(
      deriveCapabilitiesUrl("https://fe.example.test/v1/intake?token=must-not-carry#fragment"),
    ).toBe("https://fe.example.test/v1/capabilities");
    expect(() => deriveCapabilitiesUrl("ftp://fe.example.test/v1/intake")).toThrow(/http/);
    expect(() => deriveCapabilitiesUrl("https://user:secret@fe.example.test/v1/intake")).toThrow(
      /credentials/,
    );
  });

  it("accepts only valid fe.capture.v1 support with manifest readiness", async () => {
    const capabilities = fixture("valid-capabilities.json");
    capabilities.operations = {
      ...(capabilities.operations as Record<string, unknown>),
      capture_manifest: true,
    };
    const fetchImplementation = vi.fn(async () => jsonResponse(capabilities));

    const result = await preflightCaptureManifest(
      "http://127.0.0.1:8091/v1/intake",
      fetchImplementation,
    );

    expect(result.ok).toBe(true);
    expect(fetchImplementation).toHaveBeenCalledWith("http://127.0.0.1:8091/v1/capabilities", {
      method: "GET",
      headers: { Accept: "application/json" },
      credentials: "omit",
      cache: "no-store",
    });
  });

  it("keeps accepted contract support separate from runtime readiness", async () => {
    const result = await preflightCaptureManifest(
      "http://127.0.0.1:8091/v1/intake",
      async () => jsonResponse(fixture("valid-capabilities.json")),
    );

    const failure = expectFailure(result);
    expect(failure.kind).toBe("capture-unavailable");
    expect(failure.message).toContain("accepts fe.capture.v1");
    expect(failure.message).toContain("manifest capture is not ready");
  });

  it("returns an actionable incompatibility state", async () => {
    const capabilities = fixture("valid-capabilities.json");
    capabilities.contracts = [
      {
        name: "capture",
        accepted_versions: ["fe.capture.v2"],
        preferred_version: "fe.capture.v2",
        schema_id:
          "https://schemas.hollis-labs.dev/fragments-engine/browser-capture-reader/v1/capture-envelope.schema.json",
      },
    ];
    capabilities.operations = {
      ...(capabilities.operations as Record<string, unknown>),
      capture_manifest: true,
    };

    const result = await preflightCaptureManifest(
      "https://fe.example.test/v1/captures",
      async () => jsonResponse(capabilities),
    );

    const failure = expectFailure(result);
    expect(failure.kind).toBe("incompatible-contract");
    expect(failure.message).toContain("requires fe.capture.v1");
    expect(failure.message).toContain("fe.capture.v2");
  });

  it("rejects unknown breaking response fields with validator detail", async () => {
    const capabilities = fixture("valid-capabilities.json");
    capabilities.unknown_breaking_field = true;

    const result = await preflightCaptureManifest(
      "https://fe.example.test/v1/intake",
      async () => jsonResponse(capabilities),
    );

    const failure = expectFailure(result);
    expect(failure.kind).toBe("invalid-response");
    expect(failure.message).toContain("unknown_breaking_field");
    expect(failure.message).toContain("pinned fe.capabilities.v1 schema");
  });

  it("distinguishes network, non-JSON, and standardized server errors", async () => {
    const network = await preflightCaptureManifest("https://fe.example.test/v1/intake", async () => {
      throw new Error("connection refused");
    });
    const networkFailure = expectFailure(network);
    expect(networkFailure.kind).toBe("network");
    expect(networkFailure.message).toContain("connection refused");

    const nonJson = await preflightCaptureManifest(
      "https://fe.example.test/v1/intake",
      async () => new Response("gateway", { status: 502 }),
    );
    const nonJsonFailure = expectFailure(nonJson);
    expect(nonJsonFailure.kind).toBe("invalid-response");
    expect(nonJsonFailure.message).toContain("non-JSON");

    const problem = await preflightCaptureManifest(
      "https://fe.example.test/v1/intake",
      async () => jsonResponse(fixture("valid-api-problem.json"), 400),
    );
    const problemFailure = expectFailure(problem);
    expect(problemFailure.kind).toBe("server-error");
    expect(problemFailure.message).toContain("validation_failed");
    expect(problemFailure.message).toContain("/media/0/variants/0/kind");
  });
});
