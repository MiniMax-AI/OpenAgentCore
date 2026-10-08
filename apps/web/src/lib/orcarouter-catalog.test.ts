import { afterEach, describe, expect, it, vi } from "vitest";

import { orcarouterApiOrigin } from "./orcarouter";
import { readOrcarouterCatalog } from "./orcarouter-catalog";

function answer(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

const chat = { capability: "chat" } as const;

describe("reading the catalog for one entrance", () => {
  it("asks the console's own route for the entrance's capability and keeps the key out of the URL", async () => {
    const calls: Array<{ url: string; headers: unknown }> = [];
    const request = (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), headers: init?.headers });
      return answer({ models: [{ id: "openai/gpt-5.5", supported_endpoint_types: ["openai"], architecture: { input_modalities: ["text", "image"] } }], catalog_origin: orcarouterApiOrigin, degraded: false });
    }) as typeof fetch;
    const catalog = await readOrcarouterCatalog({ selector: chat, key: "sk-orca-fixture", request });
    expect(calls[0]?.url).toBe("/console/orcarouter/catalog?capability=chat");
    expect(calls[0]?.url).not.toContain("sk-orca-fixture");
    expect((calls[0]?.headers as Record<string, string>)["X-OrcaRouter-Key"]).toBe("sk-orca-fixture");
    expect(catalog).toMatchObject({ degraded: false, unauthorized: false, origin: orcarouterApiOrigin });
    expect(catalog.models.map((model) => model.id)).toEqual(["openai/gpt-5.5"]);
  });

  it("filters the live answer for the entrance it was asked for", async () => {
    const request = (async () => answer({
      models: [
        { id: "openai/gpt-5.5", supported_endpoint_types: ["openai"], architecture: { input_modalities: ["text"] } },
        { id: "openai/gpt-image-1", supported_endpoint_types: ["image-generation"] },
        { id: "silent/one", supported_endpoint_types: ["openai"] },
      ],
      catalog_origin: orcarouterApiOrigin,
      degraded: false,
    })) as typeof fetch;
    const image = await readOrcarouterCatalog({ selector: { capability: "chat", inputModalities: ["image"] }, key: "sk-orca-fixture", request });
    expect(image.models.map((model) => model.id)).toEqual([]);
    const text = await readOrcarouterCatalog({ selector: chat, key: "sk-orca-fixture", request });
    expect(text.models.map((model) => model.id)).toEqual(["openai/gpt-5.5", "silent/one"]);
  });

  it("reports a refused key without offering a fallback as if it were live", async () => {
    const request = (async () => answer({ error: "OrcaRouter rejected this API key" }, 401)) as typeof fetch;
    const catalog = await readOrcarouterCatalog({ selector: chat, key: "sk-orca-revoked", request });
    expect(catalog.unauthorized).toBe(true);
    expect(catalog.degraded).toBe(true);
  });

  it("offers only the labelled verified fallback when the catalog cannot be read", async () => {
    const request = (async () => answer({ models: [], degraded: true, reason: "catalog_unreachable", catalog_origin: orcarouterApiOrigin })) as typeof fetch;
    const catalog = await readOrcarouterCatalog({ selector: chat, key: "sk-orca-fixture", request });
    expect(catalog.degraded).toBe(true);
    expect(catalog.reason).toBe("catalog_unreachable");
    expect(catalog.models.map((model) => model.id)).toEqual(["openai/gpt-5.5", "anthropic/claude-opus-4.8", "google/gemini-3.5-flash", "deepseek/deepseek-v4-pro", "orcarouter/auto"]);
    // The live answer is never mixed into the fallback.
    expect(catalog.models.every((model) => model.id !== "silent/one")).toBe(true);
  });

  it("falls back rather than throwing when the route answers something unusable", async () => {
    const request = (async () => answer({ unexpected: true })) as typeof fetch;
    const catalog = await readOrcarouterCatalog({ selector: chat, key: "sk-orca-fixture", request });
    expect(catalog.degraded).toBe(true);
    expect(catalog.models.length).toBeGreaterThan(0);
    expect(catalog.unauthorized).toBe(false);
  });

  it("keeps a refused key out of the fallback too", async () => {
    const request = (async () => answer({ error: "no" }, 403)) as typeof fetch;
    const catalog = await readOrcarouterCatalog({ selector: chat, key: "sk-orca-revoked", request });
    expect(JSON.stringify(catalog)).not.toContain("sk-orca-revoked");
  });
});
