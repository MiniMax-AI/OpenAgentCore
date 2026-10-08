import { describe, expect, it } from "vitest";

import {
  catalogAnswer,
  catalogOptions,
  catalogKeeps,
  endpointTypesFor,
  fallbackAnswer,
  isOrcarouterBase,
  modelSatisfies,
  orcarouterAuthOrigin,
  orcarouterApiOrigin,
  orcarouterConfiguration,
  orcarouterDefaultConfiguration,
  orcarouterInferenceBase,
  orcarouterVerifiedSeed,
  parseCatalog,
  type CatalogModel,
} from "./orcarouter";

/** One catalog record per shape the selectors must tell apart. */
const catalog = {
  data: [
    { id: "openai/gpt-5.5", name: "OpenAI: GPT-5.5", context_length: 272000, max_completion_tokens: 128000, supported_endpoint_types: ["openai", "openai-response"], architecture: { input_modalities: ["text", "image", "file"] } },
    { id: "text/only", supported_endpoint_types: ["openai"], architecture: { input_modalities: ["text"] } },
    { id: "silent/modalities", supported_endpoint_types: ["openai"] },
    { id: "typed/nowhere", architecture: { input_modalities: ["text"] } },
    { id: "embed/one", supported_endpoint_types: ["embeddings"], architecture: { input_modalities: ["text"] } },
    { id: "image/one", supported_endpoint_types: ["image-generation"], architecture: { input_modalities: ["text", "image"] } },
    { id: "video/one", supported_endpoint_types: ["openai-video"], architecture: { input_modalities: ["text", "image"] } },
    { id: "rerank/one", supported_endpoint_types: ["jina-rerank"], architecture: { input_modalities: ["text"] } },
  ],
};

describe("the OrcaRouter catalog", () => {
  it("keeps vendor/model identifiers exactly as the gateway spells them", () => {
    const parsed = parseCatalog(catalog);
    expect(parsed.models.map((model) => model.id)).toContain("openai/gpt-5.5");
    expect(parsed.models.find((model) => model.id === "openai/gpt-5.5")).toMatchObject({ contextWindow: 272000, maxOutputTokens: 128000 });
  });

  it("refuses a record it cannot type instead of guessing at it", () => {
    const parsed = parseCatalog({ data: [{ id: "ok" }, { name: "no id" }, { id: "   " }, { id: "ok" }, { id: `bad/${"x".repeat(210)}` }, "not an object"] });
    expect(parsed.models.map((model) => model.id)).toEqual(["ok"]);
    expect(parsed.skipped).toBe(5);
  });

  it("reads the reduced console shape as well as the raw catalog shape", () => {
    const parsed = parseCatalog({ data: [{ id: "reduced/one", input_modalities: ["text", "image"], endpoint_types: ["openai"], modalities_declared: true }] });
    expect(parsed.models[0]).toMatchObject({ inputModalities: ["text", "image"], endpointTypes: ["openai"] });
  });

  it("fails closed when the server reports the catalog did not declare modalities", () => {
    const model = parseCatalog({ data: [{ id: "undeclared/one", modalities_declared: false, supported_endpoint_types: ["openai"] }] }).models[0];
    expect(model).toBeDefined();
    expect(catalogOptions([model as CatalogModel], { capability: "chat", inputModalities: ["image"] })).toEqual([]);
  });
});

describe("each entrance filters its own capability", () => {
  const models = parseCatalog(catalog).models;

  it("offers a chat entrance only text protocols", () => {
    expect(catalogOptions(models, { capability: "chat" }).map((model) => model.id)).toEqual(["openai/gpt-5.5", "text/only", "silent/modalities"]);
  });

  it("never offers image generation, video or rerank as a chat model", () => {
    for (const capability of ["chat"] as const) {
      const offered = catalogOptions(models, { capability });
      expect(offered.map((model) => model.id)).not.toContain("image/one");
      expect(offered.map((model) => model.id)).not.toContain("video/one");
      expect(offered.map((model) => model.id)).not.toContain("rerank/one");
      expect(offered.map((model) => model.id)).not.toContain("embed/one");
    }
  });

  it("matches embedding, image, video and rerank strictly by their own endpoint", () => {
    expect(catalogOptions(models, { capability: "embedding" }).map((model) => model.id)).toEqual(["embed/one"]);
    expect(catalogOptions(models, { capability: "image" }).map((model) => model.id)).toEqual(["image/one"]);
    expect(catalogOptions(models, { capability: "video" }).map((model) => model.id)).toEqual(["video/one"]);
    expect(catalogOptions(models, { capability: "rerank" }).map((model) => model.id)).toEqual(["rerank/one"]);
  });

  it("keeps a model without declared endpoint types out of every selector", () => {
    for (const capability of ["chat", "embedding", "image", "video", "rerank"] as const) {
      expect(catalogOptions(models, { capability }).map((model) => model.id)).not.toContain("typed/nowhere");
    }
    expect(modelSatisfies({ id: "typed/nowhere", name: "typed/nowhere", endpointTypes: [] }, { capability: "chat" })).toBe(false);
  });

  it("states what each capability means in the gateway's vocabulary", () => {
    expect(endpointTypesFor("chat")).toEqual(["openai", "anthropic", "gemini", "openai-response"]);
    expect(endpointTypesFor("embedding")).toEqual(["embeddings"]);
    expect(endpointTypesFor("image")).toEqual(["image-generation"]);
    expect(endpointTypesFor("video")).toEqual(["openai-video"]);
    expect(endpointTypesFor("rerank")).toEqual(["jina-rerank"]);
  });
});

describe("a multimodal entrance fails closed", () => {
  const models = parseCatalog(catalog).models;

  it("admits only chat models that declare the modality the entrance uploads", () => {
    expect(catalogOptions(models, { capability: "chat", inputModalities: ["image"] }).map((model) => model.id)).toEqual(["openai/gpt-5.5"]);
    expect(catalogOptions(models, { capability: "chat", inputModalities: ["audio"] }).map((model) => model.id)).toEqual([]);
    expect(catalogOptions(models, { capability: "chat", inputModalities: ["image", "file"] }).map((model) => model.id)).toEqual(["openai/gpt-5.5"]);
  });

  it("drops a chat model whose modalities the catalog never stated", () => {
    const silent = models.filter((model) => model.id === "silent/modalities")[0];
    expect(silent).toBeDefined();
    expect(modelSatisfies(silent as (typeof models)[number], { capability: "chat", inputModalities: ["image"] })).toBe(false);
  });
});

describe("a chosen model is re-validated, never silently kept", () => {
  const models = parseCatalog(catalog).models;

  it("keeps a model only while the current entrance admits it", () => {
    expect(catalogKeeps(models, { capability: "chat" }, "openai/gpt-5.5")).toBe(true);
    expect(catalogKeeps(models, { capability: "chat", inputModalities: ["image"] }, "text/only")).toBe(false);
    expect(catalogKeeps(models, { capability: "image" }, "openai/gpt-5.5")).toBe(false);
    expect(catalogKeeps(models, { capability: "chat" }, "gone/model")).toBe(false);
  });
});

describe("live discovery and the verified fallback stay separate", () => {
  it("returns the live answer unfiltered by the seed", () => {
    const answer = catalogAnswer(catalog, { capability: "chat" }, orcarouterApiOrigin);
    expect(answer.degraded).toBe(false);
    expect(answer.origin).toBe(orcarouterApiOrigin);
    expect(answer.models.every((model) => model.id !== "orcarouter/auto")).toBe(true);
    expect(answer.skipped).toBe(0);
  });

  it("labels a fallback and offers only verified seed models", () => {
    const answer = fallbackAnswer({ capability: "chat" }, orcarouterApiOrigin);
    expect(answer.degraded).toBe(true);
    expect(answer.reason).toBe("catalog_unreachable");
    expect(answer.models.map((model) => model.id)).toEqual(["openai/gpt-5.5", "anthropic/claude-opus-4.8", "google/gemini-3.5-flash", "deepseek/deepseek-v4-pro", "orcarouter/auto"]);
  });

  it("carries the verified metadata of the seed, including the reasoning ladder", () => {
    const [gpt] = orcarouterVerifiedSeed.filter((model) => model.id === "openai/gpt-5.5");
    expect(gpt).toMatchObject({ contextWindow: 272000, maxOutputTokens: 128000, reasoningEfforts: ["low", "medium", "high", "xhigh"], inputModalities: ["text", "image", "file"] });
  });

  it("admits no seed model into a capability it was not verified for", () => {
    expect(catalogOptions(orcarouterVerifiedSeed, { capability: "chat" }).map((model) => model.id)).not.toContain("openai/gpt-image-1");
    expect(catalogOptions(orcarouterVerifiedSeed, { capability: "chat" }).map((model) => model.id)).not.toContain("openai/text-embedding-3-large");
    expect(catalogOptions(orcarouterVerifiedSeed, { capability: "image" }).map((model) => model.id)).toEqual(["openai/gpt-image-1"]);
  });
});

describe("the two public origins are never derived from one another", () => {
  it("keeps the auth and inference origins distinct and the inference base versioned", () => {
    expect(orcarouterAuthOrigin).toBe("https://www.orcarouter.ai");
    expect(orcarouterApiOrigin).toBe("https://api.orcarouter.ai");
    expect(orcarouterInferenceBase()).toBe("https://api.orcarouter.ai/v1");
    expect(orcarouterInferenceBase("https://relay.internal/v1")).toBe("https://relay.internal/v1");
    expect(orcarouterDefaultConfiguration.authorizeUrl).toBe("https://www.orcarouter.ai/auth");
    expect(orcarouterDefaultConfiguration.authorizeUrl.startsWith(orcarouterApiOrigin)).toBe(false);
    expect(orcarouterDefaultConfiguration.inferenceBase).toBe("https://api.orcarouter.ai/v1");
  });

  it("accepts a deployment's own origins and refuses a malformed override", () => {
    const answer = orcarouterConfiguration({ auth_origin: "https://login.internal/", api_origin: "https://relay.internal", key_console: "https://login.internal/console" });
    expect(answer).toEqual({ authOrigin: "https://login.internal", apiOrigin: "https://relay.internal", authorizeUrl: "https://login.internal/auth", inferenceBase: "https://relay.internal/v1", keyConsole: "https://login.internal/console" });
    const refused = orcarouterConfiguration({ auth_origin: "https://user:pass@login.internal", api_origin: "not a url" });
    expect(refused.authOrigin).toBe(orcarouterAuthOrigin);
    expect(refused.apiOrigin).toBe(orcarouterApiOrigin);
  });

  it("recognises a saved configuration as this gateway and nothing else", () => {
    expect(isOrcarouterBase("https://api.orcarouter.ai/v1")).toBe(true);
    expect(isOrcarouterBase("https://api.orcarouter.ai/v1/")).toBe(true);
    expect(isOrcarouterBase("https://api.orcarouter.ai")).toBe(true);
    expect(isOrcarouterBase("https://relay.internal/v1", "https://relay.internal")).toBe(true);
    expect(isOrcarouterBase("https://model.example/v1")).toBe(false);
    expect(isOrcarouterBase("https://api.orcarouter.ai.evil.example/v1")).toBe(false);
  });
});
