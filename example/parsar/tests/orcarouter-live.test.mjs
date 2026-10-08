// A live read of the real OrcaRouter gateway through the example's own provider
// path. It runs only when ORCAROUTER_API_KEY is present, so the regular suite
// stays offline; every other test here uses fake credentials and a local
// stand-in origin.
//
// This is the same `orcaRouterAPI` the server hands the browser and the Session
// adapter, so a pass proves the shipped discovery path — not a bare curl — talks
// to the gateway and filters its catalog.

import { test } from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { openStore } from "../server/store.mjs";
import { orcaRouterAPI } from "../server/orcarouter.mjs";

test("the shipped provider path reads the real OrcaRouter catalog", async (t) => {
  const key = (process.env.ORCAROUTER_API_KEY ?? "").trim();
  if (!key) {
    t.skip("ORCAROUTER_API_KEY is not set");
    return;
  }
  assert.match(key, /^sk-orca-/, "ORCAROUTER_API_KEY is an OrcaRouter key");
  const store = openStore(":memory:");
  t.after(() => store.close?.());
  const id = randomUUID();
  store.put("providers", {
    id,
    name: "OrcaRouter",
    provider: "orcarouter",
    base_url: "https://api.orcarouter.ai/v1",
    api_key: key,
    credential_source: "api_key",
    generation: 0,
    status: "ok",
  });
  const api = orcaRouterAPI(store);

  const chat = await api.catalog(id, "chat");
  assert.equal(chat.degraded, false, "the live catalog answered");
  assert.equal(chat.catalog_source, "https://api.orcarouter.ai/v1/models?capability=chat");
  assert.ok(chat.models.length > 0, "the live catalog carried chat models");
  for (const model of chat.models) {
    assert.match(model.id, /\S/, "every model keeps its vendor/model id");
    const endpoints = model.supported_endpoint_types ?? [];
    assert.ok(endpoints.length > 0, `${model.id} reached the selector with no endpoint types`);
    assert.ok(
      endpoints.some((type) => ["openai", "anthropic", "gemini", "openai-response"].includes(type)),
      `${model.id} is not a text protocol`,
    );
    assert.ok(
      !endpoints.every((type) => ["image-generation", "openai-video", "jina-rerank", "embeddings"].includes(type)),
      `${model.id} is not a text protocol`,
    );
  }

  // A multimodal selector is a chat selector that additionally requires the
  // declared image input, and it never widens the text set.
  const multimodal = await api.catalog(id, "multimodal");
  assert.equal(multimodal.degraded, false, "the live multimodal catalog answered");
  for (const model of multimodal.models) {
    assert.ok(
      (model.input_modalities ?? []).includes("image"),
      `${model.id} entered a multimodal selector without a declared image input`,
    );
    assert.ok(
      chat.models.some((entry) => entry.id === model.id),
      `${model.id} entered the multimodal selector but not the chat one`,
    );
  }

  t.diagnostic(
    `live OrcaRouter catalog: ${chat.models.length} chat, ${multimodal.models.length} with a declared image input`,
  );
});
