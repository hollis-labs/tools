import { describe, expect, it } from "vitest";

import { normalizeCaptureEndpoint } from "./storage";

describe("capture configuration", () => {
  it("canonicalizes endpoint paths without retaining query or fragment secrets", () => {
    expect(
      normalizeCaptureEndpoint("https://fe.example.test:8443/legacy?token=secret#session=secret"),
    ).toBe("https://fe.example.test:8443/v1/captures");
  });

  it("rejects embedded endpoint credentials and non-HTTP schemes", () => {
    expect(() => normalizeCaptureEndpoint("https://user:secret@fe.example.test/v1/captures")).toThrow(
      /username or password/,
    );
    expect(() => normalizeCaptureEndpoint("file:///tmp/fragments-engine")).toThrow(/http/);
  });
});
