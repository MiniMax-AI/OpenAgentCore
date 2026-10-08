// The catalog layer: parsing, per-capability filtering, provider linkage and the
// fail-closed rule for modalities the catalog did not declare. Every fixture is
// synthetic; no live credential is used here.

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  allowedOrcaOrigin,
  filterModels,
  keepsModel,
  modelSatisfies,
  orcaAuthorizeURL,
  orcaCatalogURL,
  orcaExchangeURL,
  orcaInferenceBase,
  orcaOrigins,
  parseCatalog,
  verifiedCatalog,
} from "../shared/orcarouter.mjs";

const record = (overrides) => ({
  id: "vendor/model",
  name: "Vendor Model",
  supported_endpoint_types: ["openai"],
  architecture: { input_modalities: ["text"] },
  ...overrides,
});

test("origins default to the public pair and never derive one from the other", () => {
  assert.deepEqual(orcaOrigins({}), {
    auth: "https://www.orcarouter.ai",
    api: "https://api.orcarouter.ai",
  });
  const shared = orcaOrigins({ ORCA_BASE_URL: "https://orca.internal/" });
  assert.deepEqual(shared, {
    auth: "https://orca.internal",
    api: "https://orca.internal",
  });
  const overridden = orcaOrigins({
    ORCA_BASE_URL: "https://orca.internal",
    ORCA_AUTH_BASE_URL: "https://login.internal",
    ORCA_API_BASE_URL: "https://relay.internal",
  });
  // An explicit override wins over the shared fallback.
  assert.deepEqual(overridden, {
    auth: "https://login.internal",
    api: "https://relay.internal",
  });
  // The two public origins are independent values, not one value rewritten.
  assert.notEqual(orcaOrigins({}).auth, orcaOrigins({}).api);
});

test("catalog and exchange URLs keep each origin's own path", () => {
  assert.equal(orcaCatalogURL("https://api.orcarouter.ai"), "https://api.orcarouter.ai/v1/models");
  assert.equal(orcaCatalogURL("https://api.orcarouter.ai", "embedding"), "https://api.orcarouter.ai/v1/models?capability=embedding");
  // A self-hosted value that already ends in /v1 is not doubled.
  assert.equal(orcaCatalogURL("https://relay.internal/v1"), "https://relay.internal/v1/models");
  // The relay is at /v1; the auth endpoints are not.
  assert.equal(orcaExchangeURL("https://www.orcarouter.ai"), "https://www.orcarouter.ai/api/v1/auth/keys");
  assert.notEqual(orcaExchangeURL("https://www.orcarouter.ai"), "https://www.orcarouter.ai/v1/auth/keys");
  assert.equal(orcaInferenceBase("https://api.orcarouter.ai"), "https://api.orcarouter.ai/v1");
});

test("remote origins require HTTPS and only loopback may use HTTP", () => {
  for (const origin of ["https://api.orcarouter.ai", "http://127.0.0.1:8099", "http://localhost:1", "https://relay.internal"]) {
    assert.equal(allowedOrcaOrigin(origin), true, origin);
  }
  for (const origin of ["http://remote.example", "ftp://x", "https://user:pass@x", "https://x/v1", "https://x?a=1", "not a url"]) {
    assert.equal(allowedOrcaOrigin(origin), false, origin);
  }
});

test("the authorize URL always asks for S256 and carries the state", () => {
  const url = new URL(orcaAuthorizeURL({
    authOrigin: "https://www.orcarouter.ai",
    challenge: "challenge-value",
    state: "state-value",
    callbackURL: "http://127.0.0.1:51234/cb",
  }));
  assert.equal(url.pathname, "/auth");
  assert.equal(url.searchParams.get("code_challenge"), "challenge-value");
  assert.equal(url.searchParams.get("code_challenge_method"), "S256");
  assert.equal(url.searchParams.get("state"), "state-value");
  assert.equal(url.searchParams.get("callback_url"), "http://127.0.0.1:51234/cb");
  assert.equal(url.searchParams.get("scope"), "api");
  // The verifier never travels on this URL.
  assert.equal(url.searchParams.get("code_verifier"), null);
});

test("parseCatalog reduces records and refuses unusable ones", () => {
  const { models, refused } = parseCatalog({
    data: [
      record({ id: "vendor/one", name: "One", context_length: 128000, max_completion_tokens: 8192 }),
      record({ id: "vendor/one" }),
      record({ id: "" }),
      record({ id: undefined }),
      null,
      record({ id: "x".repeat(201) }),
      record({ id: "vendor/two", context_length: -1, architecture: null }),
    ],
  });
  assert.deepEqual(models.map((model) => model.id), ["vendor/one", "vendor/two"]);
  // One duplicate, one empty id, one missing id, one null, one oversized id.
  assert.equal(refused, 5);
  assert.equal(models[0].context_window, 128000);
  assert.equal(models[0].max_output_tokens, 8192);
  assert.deepEqual(models[0].input_modalities, ["text"]);
  // A negative bound is not evidence and is dropped rather than reported.
  assert.equal(models[1].context_window, null);
  // A record with no architecture declares nothing, which is not "text only".
  assert.equal(models[1].input_modalities, null);
});

test("each capability filters its own list", () => {
  const models = parseCatalog({ data: [
    record({ id: "text/chat", supported_endpoint_types: ["openai"] }),
    record({ id: "image/out", supported_endpoint_types: ["image-generation"], architecture: { input_modalities: ["text"] } }),
    record({ id: "embed/one", supported_endpoint_types: ["embeddings"] }),
    record({ id: "video/one", supported_endpoint_types: ["openai-video"] }),
    record({ id: "rerank/one", supported_endpoint_types: ["jina-rerank"] }),
    record({ id: "null/types", supported_endpoint_types: null }),
    record({ id: "empty/types", supported_endpoint_types: [] }),
  ] }).models;
  const ids = (selector) => filterModels(models, selector).map((model) => model.id);
  assert.deepEqual(ids({ capability: "chat" }), ["text/chat"]);
  assert.deepEqual(ids({ capability: "image" }), ["image/out"]);
  assert.deepEqual(ids({ capability: "embedding" }), ["embed/one"]);
  assert.deepEqual(ids({ capability: "video" }), ["video/one"]);
  assert.deepEqual(ids({ capability: "rerank" }), ["rerank/one"]);
});

test("a chat entrance excludes models that only serve a non-text endpoint", () => {
  const models = parseCatalog({ data: [
    record({ id: "dual", supported_endpoint_types: ["openai", "image-generation"] }),
    record({ id: "image-only", supported_endpoint_types: ["image-generation"] }),
    record({ id: "video-only", supported_endpoint_types: ["openai-video"] }),
  ] }).models;
  assert.deepEqual(filterModels(models, { capability: "chat" }).map((m) => m.id), ["dual"]);
});

test("a multimodal entrance fails closed when the catalog declares no modality", () => {
  const models = parseCatalog({ data: [
    record({ id: "vision", architecture: { input_modalities: ["text", "image"] } }),
    record({ id: "text-only", architecture: { input_modalities: ["text"] } }),
    record({ id: "undeclared", architecture: undefined }),
    record({ id: "declared-null", architecture: { input_modalities: null } }),
  ] }).models;
  const withImage = filterModels(models, { capability: "chat", inputModalities: ["image"] });
  assert.deepEqual(withImage.map((m) => m.id), ["vision"]);
  // Adding the requirement removes the text-only model from the options.
  assert.equal(keepsModel(models, { capability: "chat", inputModalities: ["image"] }, "text-only"), false);
  assert.equal(keepsModel(models, { capability: "chat", inputModalities: ["image"] }, "vision"), true);
  // Without the requirement it is a normal chat model again.
  assert.equal(keepsModel(models, { capability: "chat", inputModalities: [] }, "text-only"), true);
  // A model the catalog does not describe at all never satisfies a modality.
  assert.equal(modelSatisfies(models[2], { capability: "chat", inputModalities: ["image"] }), false);
});

test("a model with no declared endpoint types never enters any selector", () => {
  const models = parseCatalog({ data: [record({ id: "unknown", supported_endpoint_types: null })] }).models;
  for (const capability of ["chat", "embedding", "image", "video", "rerank"]) {
    assert.deepEqual(filterModels(models, { capability }), [], capability);
  }
});

test("the verified seed keeps its reasoning ladder and modalities", () => {
  const seed = new Map(verifiedCatalog.map((model) => [model.id, model]));
  const gpt = seed.get("openai/gpt-5.5");
  assert.deepEqual(gpt.reasoning_efforts, ["low", "medium", "high", "xhigh"]);
  assert.deepEqual(gpt.input_modalities, ["file", "image", "text"]);
  assert.equal(gpt.context_window, 272000);
  // The seed is a catalog like any other and goes through the same filters.
  assert.deepEqual(
    filterModels(verifiedCatalog, { capability: "chat" }).map((m) => m.id),
    ["openai/gpt-5.5", "anthropic/claude-opus-4.8", "google/gemini-3.5-flash", "deepseek/deepseek-v4-pro", "orcarouter/auto"],
  );
  assert.deepEqual(
    filterModels(verifiedCatalog, { capability: "chat", inputModalities: ["image"] }).map((m) => m.id),
    ["openai/gpt-5.5", "anthropic/claude-opus-4.8", "google/gemini-3.5-flash"],
  );
});
