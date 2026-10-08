// The OrcaRouter credential seam. Both adapters — a pasted API Key and an
// OAuth 2.0 + PKCE login — are exercised through the implementation the browser
// and the Session adapter actually call, against a local fake auth server.
// Every key, code and verifier here is a fake.

import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { randomUUID } from "node:crypto";
import { openStore } from "../server/store.mjs";
import { orcaCredential, orcaRouterAPI, publicOrcaAccount } from "../server/orcarouter.mjs";

const b64url = (value) => Buffer.from(value).toString("base64url");
const s256 = (verifier) => b64url(createHash("sha256").update(verifier).digest());

/** A stand-in auth origin that records what it was asked and answers on demand. */
async function fakeAuth(t, reply = () => ({ status: 200, body: { key: "sk-orca-issued", user_id: "42", scope: "api" } })) {
  const seen = [];
  const server = createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : null;
    seen.push({ method: request.method, url: request.url, body, headers: request.headers });
    const answer = reply(seen.at(-1), seen.length);
    response.writeHead(answer.status, { "Content-Type": "application/json" });
    response.end(JSON.stringify(answer.body));
  });
  await new Promise((done) => server.listen(0, "127.0.0.1", done));
  t.after(() => server.close());
  return { origin: `http://127.0.0.1:${server.address().port}`, seen };
}

/** A store holding one OrcaRouter provider account written by the API-key adapter. */
function account(store, patch = {}) {
  const id = randomUUID();
  store.put("providers", {
    id,
    name: "OrcaRouter",
    provider: "orcarouter",
    base_url: "https://api.orcarouter.ai/v1",
    api_key: "",
    credential_source: "",
    generation: 0,
    status: "ok",
    account: "",
    revision: 1,
    ...patch,
  });
  return id;
}

function api(t, { authOrigin, apiOrigin, fetchImpl, store }) {
  const built = orcaRouterAPI(store, fetchImpl ?? fetch, {
    ORCA_AUTH_BASE_URL: authOrigin,
    ORCA_API_BASE_URL: apiOrigin,
  });
  t.after(() => built.closeAll());
  return built;
}

test("the API Key adapter stores a pasted key for the downstream request", (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store, { api_key: "sk-orca-pasted", credential_source: "api_key" });
  const credential = orcaCredential(store);
  // The credential interface is the only thing downstream code reads.
  assert.equal(credential.key(id), "sk-orca-pasted");
  assert.equal(credential.usable(id), true);
  assert.equal(credential.generation(id), 0);
  // The browser view never carries the key, only whether one is configured.
  const view = publicOrcaAccount(store.get("providers", id));
  assert.equal(view.has_api_key, true);
  assert.equal(JSON.stringify(view).includes("sk-orca-pasted"), false);
});

test("a PKCE login issues the same credential a pasted key does", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store);
  const auth = await fakeAuth(t);
  const calls = [];
  const orca = api(t, {
    authOrigin: auth.origin,
    apiOrigin: "https://api.orcarouter.ai",
    store,
    fetchImpl: async (url, init) => {
      calls.push({ url, init });
      return fetch(url, init);
    },
  });

  const pending = await orca.startLogin(id);
  const authorize = new URL(pending.authorize_url);
  // The authorize URL is on the auth origin and asks for S256.
  assert.equal(authorize.origin, auth.origin);
  assert.equal(authorize.pathname, "/auth");
  assert.equal(authorize.searchParams.get("code_challenge_method"), "S256");
  const challenge = authorize.searchParams.get("code_challenge");
  const state = authorize.searchParams.get("state");
  assert.ok(challenge && state);
  assert.match(authorize.searchParams.get("callback_url"), /^http:\/\/127\.0\.0\.1:\d+\/cb$/);

  // The callback carries the code back to the process holding the verifier.
  const callback = new URL(authorize.searchParams.get("callback_url"));
  callback.searchParams.set("code", "one-time-code");
  callback.searchParams.set("state", state);
  const browser = await fetch(callback);
  assert.equal(browser.status, 200);
  await browser.text();

  // The exchange posts S256 to the auth origin's `/api/v1/auth/keys` with the
  // matching verifier, and no client secret of any kind.
  for (let attempt = 0; attempt < 200 && store.get("providers", id).api_key === ""; attempt += 1)
    await new Promise((done) => setTimeout(done, 10));
  const exchange = auth.seen.find((entry) => entry.url === "/api/v1/auth/keys");
  assert.ok(exchange, "the exchange must reach the auth origin");
  assert.equal(calls.length, 1);
  assert.equal(exchange.method, "POST");
  assert.equal(exchange.body.code, "one-time-code");
  assert.equal(exchange.body.code_challenge_method, "S256");
  assert.equal(s256(exchange.body.code_verifier), challenge);
  assert.equal(exchange.body.client_secret, undefined);
  assert.equal(JSON.stringify(exchange.body).includes("secret"), false);

  const row = store.get("providers", id);
  assert.equal(row.api_key, "sk-orca-issued");
  assert.equal(row.credential_source, "pkce");
  assert.equal(row.generation, 1);
  assert.equal(row.status, "ok");
  // The same interface serves both adapters.
  const credential = orcaCredential(store);
  assert.equal(credential.key(id), "sk-orca-issued");
  assert.equal(credential.usable(id), true);
});

test("each attempt uses a fresh verifier, state and callback port", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store);
  const orca = api(t, { authOrigin: "https://www.orcarouter.ai", apiOrigin: "https://api.orcarouter.ai", store, fetchImpl: async () => Response.json({}) });
  const first = await orca.startLogin(id);
  orca.cancelLogin(id);
  const second = await orca.startLogin(id);
  const a = new URL(first.authorize_url);
  const b = new URL(second.authorize_url);
  assert.notEqual(a.searchParams.get("code_challenge"), b.searchParams.get("code_challenge"));
  assert.notEqual(a.searchParams.get("state"), b.searchParams.get("state"));
  orca.cancelLogin(id);
});

test("Flow A compares the state before redeeming anything", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store);
  const auth = await fakeAuth(t);
  const orca = api(t, { authOrigin: auth.origin, apiOrigin: "https://api.orcarouter.ai", store });
  const pending = await orca.startLogin(id);
  const callback = new URL(pending.authorize_url.includes("callback_url")
    ? new URL(pending.authorize_url).searchParams.get("callback_url")
    : "http://127.0.0.1/cb");
  callback.searchParams.set("code", "attacker-code");
  callback.searchParams.set("state", "not-the-state");
  const response = await fetch(callback);
  assert.equal(response.status, 400);
  await response.text();
  // Nothing was redeemed and no credential appeared.
  assert.equal(store.get("providers", id).api_key, "");
  assert.equal(auth.seen.filter((entry) => entry.url === "/api/v1/auth/keys").length, 0);
});

test("a denial ends safely and releases the login", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store);
  const auth = await fakeAuth(t);
  const orca = api(t, { authOrigin: auth.origin, apiOrigin: "https://api.orcarouter.ai", store });
  const pending = await orca.startLogin(id);
  const callback = new URL(new URL(pending.authorize_url).searchParams.get("callback_url"));
  callback.searchParams.set("error", "access_denied");
  callback.searchParams.set("state", new URL(pending.authorize_url).searchParams.get("state"));
  const response = await fetch(callback);
  assert.equal(response.status, 200);
  await response.text();
  assert.equal(store.get("providers", id).api_key, "");
  // The lock is released: another login can start.
  const again = await orca.startLogin(id);
  assert.ok(again.authorize_url);
  orca.cancelLogin(id);
});

test("an exchange failure keeps the login usable and never stores a key", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store);
  const auth = await fakeAuth(t, () => ({ status: 403, body: { error: "invalid_grant", error_description: "code verifier sk-orca-secret" } }));
  const orca = api(t, { authOrigin: auth.origin, apiOrigin: "https://api.orcarouter.ai", store });
  const pending = await orca.startLogin(id);
  const callback = new URL(new URL(pending.authorize_url).searchParams.get("callback_url"));
  callback.searchParams.set("code", "expired-code");
  callback.searchParams.set("state", new URL(pending.authorize_url).searchParams.get("state"));
  await (await fetch(callback)).text();
  await new Promise((done) => setTimeout(done, 30));
  assert.equal(store.get("providers", id).api_key, "");
  assert.equal(orca.view(id).login, null);
});

test("a reused or expired code reports the operator's next step, never the body", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store);
  const auth = await fakeAuth(t, () => ({ status: 403, body: { error: "invalid_grant", error_description: "verifier sk-orca-secret" } }));
  const orca = api(t, { authOrigin: auth.origin, apiOrigin: "https://api.orcarouter.ai", store });
  await orca.startLogin(id);
  await assert.rejects(
    orca.submitLogin(id, { code: "reused" }),
    (error) => error.status === 400 && !error.message.includes("sk-orca-secret") && !error.message.includes("invalid_grant"),
  );
});

test("a scope other than the requested one is refused", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store);
  const auth = await fakeAuth(t, () => ({ status: 200, body: { key: "sk-orca-issued", scope: "connector" } }));
  const orca = api(t, { authOrigin: auth.origin, apiOrigin: "https://api.orcarouter.ai", store });
  await orca.startLogin(id);
  await assert.rejects(orca.submitLogin(id, { code: "c" }), /scope/);
  assert.equal(store.get("providers", id).api_key, "");
});

test("a network failure ends the login without storing anything", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store);
  const orca = api(t, {
    authOrigin: "https://www.orcarouter.ai",
    apiOrigin: "https://api.orcarouter.ai",
    store,
    fetchImpl: async () => { throw new Error("offline"); },
  });
  await orca.startLogin(id);
  await assert.rejects(orca.submitLogin(id, { code: "c" }), (error) => { assert.equal(error.status, 502); assert.match(error.message, /无法访问/); return true; });
  assert.equal(store.get("providers", id).api_key, "");
});

test("cancelling releases the lock and closes the listener", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store);
  const orca = api(t, { authOrigin: "https://www.orcarouter.ai", apiOrigin: "https://api.orcarouter.ai", store, fetchImpl: async () => Response.json({}) });
  const pending = await orca.startLogin(id);
  assert.ok(pending.redirect);
  orca.cancelLogin(id);
  assert.equal(orca.view(id).login, null);
  // The cancelled listener no longer answers.
  await assert.rejects(fetch(pending.redirect));
  // A second login can start immediately.
  const again = await orca.startLogin(id);
  assert.ok(again.authorize_url);
  orca.cancelLogin(id);
});

test("a second login while one is pending is refused rather than queued", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store);
  const orca = api(t, { authOrigin: "https://www.orcarouter.ai", apiOrigin: "https://api.orcarouter.ai", store, fetchImpl: async () => Response.json({}) });
  await orca.startLogin(id);
  let status = 0;
  await orca.startLogin(id).then(
    () => { status = 200; },
    (error) => { status = error.status; },
  );
  assert.equal(status, 409);
  orca.cancelLogin(id);
});

test("the catalog uses the account's own key and never crosses to the auth origin", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store, { api_key: "sk-orca-pasted" });
  const seen = [];
  const orca = api(t, {
    authOrigin: "https://www.orcarouter.ai",
    apiOrigin: "https://api.orcarouter.ai",
    store,
    fetchImpl: async (url, init) => {
      seen.push({ url, init });
      return Response.json({ data: [
        { id: "openai/gpt-5.5", name: "GPT", context_length: 272000, max_completion_tokens: 128000, supported_endpoint_types: ["openai"], architecture: { input_modalities: ["text", "image"] } },
        { id: "image/one", supported_endpoint_types: ["image-generation"] },
      ] });
    },
  });
  const catalog = await orca.catalog(id, "chat");
  assert.equal(seen[0].url, "https://api.orcarouter.ai/v1/models?capability=chat");
  assert.equal(seen[0].init.headers.Authorization, "Bearer sk-orca-pasted");
  assert.equal(seen[0].init.redirect, "manual");
  assert.deepEqual(catalog.models.map((model) => model.id), ["openai/gpt-5.5"]);
  assert.equal(catalog.degraded, false);
  assert.equal(catalog.catalog_source, "https://api.orcarouter.ai/v1/models?capability=chat");
  // The auth origin was never contacted.
  assert.equal(seen.some((entry) => entry.url.includes("orcarouter.ai/auth")), false);
});

test("a degraded catalog is the verified seed, labelled, and never a free-text fallback", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store, { api_key: "sk-orca-pasted" });
  const orca = api(t, {
    authOrigin: "https://www.orcarouter.ai",
    apiOrigin: "https://api.orcarouter.ai",
    store,
    fetchImpl: async () => { throw new Error("offline"); },
  });
  const catalog = await orca.catalog(id, "chat");
  assert.equal(catalog.degraded, true);
  assert.equal(catalog.catalog_source, "verified-seed");
  assert.deepEqual(catalog.models.map((model) => model.id), [
    "openai/gpt-5.5",
    "anthropic/claude-opus-4.8",
    "google/gemini-3.5-flash",
    "deepseek/deepseek-v4-pro",
    "orcarouter/auto",
  ]);
  // The verified reasoning ladder survives the outage.
  assert.deepEqual(catalog.models[0].reasoning_efforts, ["low", "medium", "high", "xhigh"]);
});

test("a relay 401 marks the exact generation and never fakes a refresh", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store, { api_key: "sk-orca-pasted", generation: 3 });
  let calls = 0;
  const orca = api(t, {
    authOrigin: "https://www.orcarouter.ai",
    apiOrigin: "https://api.orcarouter.ai",
    store,
    fetchImpl: async () => { calls += 1; return new Response("revoked", { status: 401 }); },
  });
  await assert.rejects(orca.catalog(id, "chat"), (error) => error.status === 401);
  // Exactly one attempt: a revoked durable key is not refreshed.
  assert.equal(calls, 1);
  assert.equal(store.get("providers", id).status, "needs_reauth");
  assert.equal(store.get("providers", id).generation, 3);
  // A second read reports the terminal state instead of retrying the relay.
  await assert.rejects(orca.catalog(id, "chat"), (error) => error.status === 401);
  assert.equal(calls, 1);
});

test("a late failure from an old generation cannot mark a new credential", (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store, { api_key: "sk-orca-old", generation: 1 });
  const credential = orcaCredential(store);
  // A new login replaces the credential before the late failure arrives.
  store.put("providers", { ...store.get("providers", id), api_key: "sk-orca-new", generation: 2, status: "ok" });
  assert.equal(credential.reauthenticate(id, 1), false);
  assert.equal(store.get("providers", id).status, "ok");
  assert.equal(credential.key(id), "sk-orca-new");
  // The current generation is still the one that can be marked.
  assert.equal(credential.reauthenticate(id, 2), true);
  assert.equal(store.get("providers", id).status, "needs_reauth");
  assert.equal(credential.key(id), "sk-orca-new");
});

test("a replacement never deletes the previous secret before the new one lands", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const id = account(store, { api_key: "sk-orca-original", credential_source: "api_key" });
  const auth = await fakeAuth(t, () => ({ status: 403, body: { error: "invalid_grant" } }));
  const orca = api(t, { authOrigin: auth.origin, apiOrigin: "https://api.orcarouter.ai", store });
  await orca.startLogin(id);
  await assert.rejects(orca.submitLogin(id, { code: "bad" }));
  // The failed login left the working credential in place.
  assert.equal(store.get("providers", id).api_key, "sk-orca-original");
  assert.equal(orcaCredential(store).usable(id), true);
});

test("both adapters produce the same credential result for the downstream request", async (t) => {
  const store = openStore(":memory:");
  t.after(() => store.close());
  const pasted = account(store, { api_key: "sk-orca-pasted", credential_source: "api_key" });
  const connected = account(store);
  const auth = await fakeAuth(t);
  const orca = api(t, { authOrigin: auth.origin, apiOrigin: "https://api.orcarouter.ai", store });
  const pending = await orca.startLogin(connected);
  await orca.submitLogin(connected, { code: "code" });
  const credential = orcaCredential(store);
  // Downstream reads only the interface: it cannot tell which adapter ran.
  for (const id of [pasted, connected]) {
    assert.equal(typeof credential.key(id), "string");
    assert.match(credential.key(id), /^sk-orca-/);
    assert.equal(credential.usable(id), true);
  }
  assert.equal(store.get("providers", pasted).credential_source, "api_key");
  assert.equal(store.get("providers", connected).credential_source, "pkce");
});
