import { createHash } from "node:crypto";

import { describe, expect, it, vi } from "vitest";

import type { FECaptureAssetVariant } from "@/contracts/fragmentsEngine";
import { BrowserMediaAcquirer, MediaTransferError } from "./mediaTransfer";

function variant(sourceUrl = "https://media.test/item.png"): FECaptureAssetVariant {
  return {
    client_variant_id: "poster:one",
    kind: "poster",
    source_url: sourceUrl,
    transfer_preference: "browser_preferred",
    requested_custody: "mirror",
  };
}

function png(width = 640, height = 360): Uint8Array {
  const bytes = new Uint8Array(24);
  bytes.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a], 0);
  bytes.set([0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52], 8);
  new DataView(bytes.buffer).setUint32(16, width);
  new DataView(bytes.buffer).setUint32(20, height);
  return bytes;
}

describe("browser media acquisition", () => {
  it("fetches without browser credentials and computes digest, size, MIME, and dimensions", async () => {
    const bytes = png();
    const fetchImplementation = vi.fn(
      async () => new Response(new Uint8Array(bytes).buffer, { status: 200 }),
    );
    const acquired = await new BrowserMediaAcquirer(fetchImplementation).acquire(
      variant(),
      ["https://media.test"],
      1_000,
    );

    expect(acquired).toMatchObject({
      byteSize: 24,
      mimeType: "image/png",
      observedMimeType: "image/png",
      width: 640,
      height: 360,
      digest: {
        algorithm: "sha256",
        value: createHash("sha256").update(bytes).digest("hex"),
      },
    });
    expect(fetchImplementation).toHaveBeenCalledWith(
      "https://media.test/item.png",
      expect.objectContaining({
        method: "GET",
        credentials: "omit",
        redirect: "manual",
        referrerPolicy: "no-referrer",
        cache: "no-store",
        headers: { Accept: "*/*" },
        signal: expect.any(AbortSignal),
      }),
    );
    expect(JSON.stringify(fetchImplementation.mock.calls[0])).not.toMatch(/authorization|cookie|bearer/i);
  });

  it("allows only bounded redirects whose exact origins were granted", async () => {
    const fetchImplementation = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, {
        status: 302,
        headers: { Location: "https://redirect.test/final.png" },
      }))
      .mockResolvedValueOnce(new Response(new Uint8Array(png()).buffer, { status: 200 }));
    await expect(new BrowserMediaAcquirer(fetchImplementation).acquire(
      variant(),
      ["https://media.test", "https://redirect.test"],
      1_000,
    )).resolves.toMatchObject({ mimeType: "image/png" });
    expect(fetchImplementation).toHaveBeenCalledTimes(2);

    const attackFetch = vi.fn(async () => new Response(null, {
      status: 302,
      headers: { Location: "https://evil.test/steal" },
    }));
    await expect(new BrowserMediaAcquirer(attackFetch).acquire(
      variant(),
      ["https://media.test"],
      1_000,
    )).rejects.toMatchObject({ kind: "redirect", retryable: false });
    expect(attackFetch).toHaveBeenCalledOnce();
  });

  it("rejects oversized declared lengths before reading", async () => {
    let cancelled = false;
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        controller.enqueue(new Uint8Array(10));
      },
      cancel() {
        cancelled = true;
      },
    });
    const fetchImplementation = vi.fn(async () => new Response(body, {
      status: 200,
      headers: { "Content-Length": "1001" },
    }));

    await expect(new BrowserMediaAcquirer(fetchImplementation).acquire(
      variant(),
      ["https://media.test"],
      1_000,
    )).rejects.toMatchObject({ kind: "too_large", retryable: false });
    expect(cancelled).toBe(true);
  });

  it.each([null, "2"])(
    "cuts off a stream that exceeds the limit with Content-Length %s",
    async (contentLength) => {
      let cancelled = false;
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new Uint8Array(3));
          controller.enqueue(new Uint8Array(3));
        },
        cancel() {
          cancelled = true;
        },
      });
      const headers = contentLength === null ? undefined : { "Content-Length": contentLength };
      const fetchImplementation = vi.fn(async () => new Response(body, { status: 200, headers }));

      await expect(new BrowserMediaAcquirer(fetchImplementation).acquire(
        variant(),
        ["https://media.test"],
        5,
      )).rejects.toMatchObject({ kind: "too_large" });
      expect(cancelled).toBe(true);
    },
  );

  it("records unsupported detected MIME but uploads it as octet-stream", async () => {
    const pdf = new TextEncoder().encode("%PDF-1.7\nbody");
    const acquired = await new BrowserMediaAcquirer(async () => new Response(pdf)).acquire(
      { ...variant("https://media.test/doc.pdf"), kind: "original" },
      ["https://media.test"],
      1_000,
    );
    expect(acquired.observedMimeType).toBe("application/pdf");
    expect(acquired.mimeType).toBe("application/octet-stream");
  });

  it("classifies broken body streams and bounded timeouts as retryable network failures", async () => {
    const brokenBody = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new Uint8Array([1]));
        controller.error(new Error("socket reset"));
      },
    });
    await expect(new BrowserMediaAcquirer(async () => new Response(brokenBody)).acquire(
      variant(),
      ["https://media.test"],
      1_000,
    )).rejects.toMatchObject({ kind: "network", retryable: true });

    const hangingFetch = vi.fn(async (_input: string | URL, init?: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      }));
    await expect(new BrowserMediaAcquirer(hangingFetch, 5).acquire(
      variant(),
      ["https://media.test"],
      1_000,
    )).rejects.toMatchObject({ kind: "network", retryable: true });
  });

  it("does not fetch unpermitted, credential-bearing, or non-browser variants", async () => {
    const fetchImplementation = vi.fn();
    const acquirer = new BrowserMediaAcquirer(fetchImplementation);
    await expect(acquirer.acquire(variant(), [], 1_000)).rejects.toBeInstanceOf(MediaTransferError);
    await expect(acquirer.acquire(
      variant("https://user:secret@media.test/item"),
      ["https://media.test"],
      1_000,
    )).rejects.toMatchObject({ kind: "source" });
    await expect(acquirer.acquire(
      { ...variant(), transfer_preference: "reference_only" },
      ["https://media.test"],
      1_000,
    )).rejects.toMatchObject({ kind: "source" });
    expect(fetchImplementation).not.toHaveBeenCalled();
  });
});
