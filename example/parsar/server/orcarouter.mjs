// The OrcaRouter credential seam.
//
// Two adapters produce the same result: an operator-pasted API key, and an
// OAuth 2.0 + PKCE login that issues a normal `sk-orca-...` key the operator
// owns. Everything downstream — catalog discovery, the provider record, the
// Session's `x_agents_core.model_provider` bundle — reads the stored key through
// `orcaCredential` and cannot tell which adapter produced it.
//
// The login is **Flow A (loopback redirect)**: this example is a local
// single-user server on `127.0.0.1` that can open a listener, so the code comes
// back to the process that holds the verifier and the operator copies nothing.
// The consent screen can still hand a code to a person instead, so `S256` is
// sent regardless of the flow.
//
// A PKCE-issued key is durable and is **not** a refresh token. There is no
// refresh endpoint and none is invented. A relay `401` is a terminal
// reauthentication requirement for the exact credential generation that made the
// rejected request; a late failure from an old generation never marks a newly
// reauthorized credential.

import { createHash, randomBytes, timingSafeEqual } from "node:crypto";
import { createServer } from "node:http";
import { AppError, text, uuid } from "./store.mjs";
import {
  allowedOrcaOrigin,
  filterModels,
  keepsModel,
  orcaAuthorizeURL,
  orcaCatalogURL,
  orcaInferenceBase,
  orcaExchangeURL,
  orcaOrigins,
  parseCatalog,
  verifiedCatalog,
} from "../shared/orcarouter.mjs";

/** Authorization codes are single-use with a ten-minute TTL. */
const LOGIN_TTL_MS = 10 * 60 * 1000;
const CATALOG_TIMEOUT_MS = 15_000;
const CATALOG_BYTE_LIMIT = 512 * 1024;
const CATALOG_ITEM_LIMIT = 5_000;
/** Which entrance asks for the catalog and which non-text modalities it takes. */
const catalogModes = new Set(["chat", "multimodal", "embedding", "image", "video", "rerank"]);
const selectorFor = (mode) => (mode === "multimodal" ? { capability: "chat", inputModalities: ["image"] } : { capability: mode, inputModalities: [] });

const b64url = (bytes) => Buffer.from(bytes).toString("base64url");
const s256 = (verifier) => b64url(createHash("sha256").update(verifier).digest());

/** The fixed loopback hosts a redirect may use. */
const loopbackHosts = new Set(["localhost", "127.0.0.1", "[::1]", "::1"]);

function sameBytes(left, right) {
  const a = Buffer.from(left);
  const b = Buffer.from(right);
  // The length is public; comparing fixed-size digests keeps the check constant-time.
  if (a.length !== b.length) return false;
  return timingSafeEqual(a, b);
}

/** The stored account record for one OrcaRouter identity. */
function accountRow(id, previous, patch) {
  return {
    ...previous,
    id,
    provider: "orcarouter",
    name: patch.name ?? previous?.name ?? "",
    base_url: patch.base_url ?? previous?.base_url ?? "",
    api_key: patch.api_key ?? previous?.api_key ?? "",
    /** `api_key` or `pkce`; the UI keeps the two choices distinct. */
    credential_source: patch.credential_source ?? previous?.credential_source ?? "",
    /** Bumped on every credential replacement; a 401 marks one exact generation. */
    generation: patch.generation ?? previous?.generation ?? 0,
    status: patch.status ?? previous?.status ?? "ok",
    /** user_id reported by the exchange, for operator display only. */
    account: patch.account ?? previous?.account ?? "",
    revision: (previous?.revision ?? 0) + 1,
    has_api_key: Boolean(patch.api_key ?? previous?.api_key ?? ""),
  };
}

/** The browser view: never the key, never the verifier, never the pending state. */
export function publicOrcaAccount(row) {
  return {
    id: row.id,
    name: row.name,
    base_url: row.base_url,
    credential_source: row.credential_source,
    generation: row.generation,
    status: row.status,
    account: row.account,
    revision: row.revision,
    has_api_key: Boolean(row.api_key),
  };
}

/**
 * The credential interface every caller uses. It never returns a key to the
 * browser and never records one in an error.
 */
export function orcaCredential(store) {
  const read = (id) => {
    const row = store.get("providers", id);
    return row?.provider === "orcarouter" ? row : null;
  };
  return {
    /** The key a downstream request must use, or null when none is configured. */
    key: (id) => read(id)?.api_key || null,
    /** Whether the account may be used at all. */
    usable: (id) => {
      const row = read(id);
      return Boolean(row?.api_key) && row.status !== "needs_reauth";
    },
    /** The generation that a request is being made under. */
    generation: (id) => read(id)?.generation ?? 0,
    /**
     * Marks one exact generation as needing reauthentication. A response that
     * belongs to a superseded generation is ignored, so a late failure never
     * invalidates a credential the operator has since replaced.
     */
    reauthenticate: (id, generation) => {
      const row = read(id);
      if (!row || row.generation !== generation) return false;
      store.put("providers", { ...row, status: "needs_reauth", revision: row.revision + 1 });
      return true;
    },
  };
}

/**
 * The server-owned OrcaRouter surface: catalog discovery, the two credential
 * adapters, and the login lifecycle. Credentials stay in the local restricted
 * SQLite file that already holds provider keys.
 */
export function orcaRouterAPI(store, fetchImpl = fetch, env = process.env) {
  const origins = orcaOrigins(env);
  if (!allowedOrcaOrigin(origins.auth) || !allowedOrcaOrigin(origins.api))
    throw new AppError(
      400,
      "OrcaRouter 地址必须是 HTTPS，或本机的 HTTP 地址（不含路径）。",
    );
  const credential = orcaCredential(store);
  /** One pending login per account: at most one browser round-trip at a time. */
  const logins = new Map();

  const requireAccount = (id) => {
    if (typeof id !== "string" || !uuid.test(id))
      throw new AppError(400, "请选择有效的 Provider。");
    const row = store.get("providers", id);
    if (!row || row.provider !== "orcarouter")
      throw new AppError(404, "该 Provider 不是 OrcaRouter。");
    return row;
  };

  function clearLogin(id) {
    const pending = logins.get(id);
    if (!pending) return;
    logins.delete(id);
    pending.closed = true;
    clearTimeout(pending.timer);
    for (const response of pending.responses ?? []) {
      try {
        response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
        response.end(pending.document);
      } catch {
        // The browser already left; nothing to serve.
      }
    }
    pending.responses = [];
    pending.server?.close();
    pending.controller?.abort();
  }

  /** Serves the browser once, whatever the outcome, then releases the login. */
  function settle(id, pending, outcome) {
    if (pending.closed || pending.settled) return;
    pending.settled = true;
    pending.responses = [];
    pending.server?.close();
    outcome();
  }

  function record(pending, patch) {
    const row = requireAccount(pending.id);
    if (row.generation !== pending.generation) return null;
    const next = accountRow(pending.id, row, patch);
    // A replacement never deletes the previous secret before the new one is
    // stored: one atomic write holds whichever credential is current.
    store.put("providers", next);
    return next;
  }

  async function exchange(pending, code) {
    let response;
    try {
      response = await fetchImpl(orcaExchangeURL(origins.auth), {
        method: "POST",
        redirect: "manual",
        signal: pending.controller.signal,
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({
          code,
          code_verifier: pending.verifier,
          code_challenge_method: "S256",
        }),
      });
    } catch {
      // A transport failure reports what the operator can do, never the cause's
      // contents.
      throw new AppError(502, "OrcaRouter 授权地址无法访问，请稍后重试。");
    }
    if (response.status >= 300 && response.status < 400)
      throw new AppError(502, "OrcaRouter 授权地址返回了重定向。");
    if (!response.ok) {
      // The status is the operator's next step; the upstream body could carry a
      // credential and is never read into a message.
      throw new AppError(400, exchangeMessage(response.status));
    }
    const body = await response.json();
    const key = typeof body?.key === "string" ? body.key.trim() : "";
    if (!key) throw new AppError(502, "OrcaRouter 未返回密钥，请重新授权。");
    // Read the granted scope back: it is what was approved, not what was asked.
    if (body.scope !== undefined && body.scope !== "api")
      throw new AppError(400, "OrcaRouter 授予的 scope 不适用于本应用。");
    return { key, account: typeof body.user_id === "string" ? body.user_id : "" };
  }

  async function complete(pending, code) {
    let granted;
    try {
      granted = await exchange(pending, code);
    } catch (error) {
      settle(pending.id, pending, () => {
        record(pending, { status: "ok" });
        logins.delete(pending.id);
      });
      throw error;
    }
    settle(pending.id, pending, () => {
      record(pending, {
        api_key: granted.key,
        credential_source: "pkce",
        generation: pending.generation + 1,
        status: "ok",
        account: granted.account,
      });
      logins.delete(pending.id);
    });
    return publicOrcaAccount(requireAccount(pending.id));
  }
  /** Starts Flow A: listen first, so the port is known before the URL is built. */
  async function startLogin(id) {
    const row = requireAccount(id);
    if (logins.has(id)) throw new AppError(409, "已有正在进行的授权，请先取消。");
    const generation = row.generation;
    const pending = {
      id,
      generation,
      verifier: "",
      state: "",
      server: null,
      controller: new AbortController(),
      responses: [],
      document: "<!doctype html><html><body><p>已完成，可以关闭此页面。</p></body></html>",
      closed: false,
      settled: false,
      timer: null,
    };
    // Fresh cryptographic randomness for every attempt: never reused, never
    // derived from a timestamp, a username or a fixed salt.
    pending.verifier = b64url(randomBytes(32));
    const challenge = s256(pending.verifier);
    pending.state = b64url(randomBytes(16));

    const ready = new Promise((resolve, reject) => {
      const server = createServer((request, response) => {
        const url = new URL(request.url, "http://127.0.0.1");
        pending.responses.push(response);
        if (url.pathname !== "/cb") {
          response.writeHead(404, { "Content-Type": "text/plain" });
          response.end();
          return;
        }
        // Compare the state before anything else: it is the only thing between
        // this listener and a code somebody else's page dropped on it.
        const state = url.searchParams.get("state") ?? "";
        if (!pending.settled && !sameBytes(state, pending.state)) {
          response.writeHead(400, { "Content-Type": "text/html; charset=utf-8" });
          response.end("<!doctype html><html><body><p>state 不匹配，已拒绝此次授权。</p></body></html>");
          response = null;
          settle(id, pending, () => logins.delete(id));
          reject(new AppError(400, "state 不匹配，已取消此次授权。"));
          return;
        }
        const denied = url.searchParams.get("error");
        const code = url.searchParams.get("code") ?? "";
        response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
        response.end(pending.document);
        response = null;
        if (denied) {
          settle(id, pending, () => logins.delete(id));
          reject(new AppError(400, "你拒绝了 OrcaRouter 授权。"));
          return;
        }
        if (!code) {
          settle(id, pending, () => logins.delete(id));
          reject(new AppError(400, "OrcaRouter 未返回授权码。"));
          return;
        }
        complete(pending, code).then(resolve, reject);
      });
      pending.server = server;
      server.on("error", reject);
      server.listen(0, "127.0.0.1", () => {
        const port = server.address().port;
        pending.redirect = `http://127.0.0.1:${port}/cb`;
        pending.authorize_url = orcaAuthorizeURL({
          authOrigin: origins.auth,
          challenge,
          state: pending.state,
          callbackURL: pending.redirect,
        });
        resolve(pending);
      });
      // A consent screen the operator never finishes must not hold the lock.
      pending.timer = setTimeout(() => {
        if (pending.settled) return;
        settle(id, pending, () => logins.delete(id));
        reject(new AppError(408, "OrcaRouter 授权超时，请重试。"));
      }, LOGIN_TTL_MS);
      pending.timer.unref?.();
    });
    logins.set(id, pending);
    return ready;
  }

  /** Finishes Flow A and, additionally, accepts a code a person pasted. */
  function submitLogin(id, body) {
    const pending = logins.get(id);
    if (!pending) throw new AppError(404, "没有正在进行的授权。");
    const code = text(body.code, "授权码", 4096, true).trim();
    if (!pending.redirect)
      throw new AppError(
        409,
        "回调流程需要浏览器返回授权码，请等待回调或取消后重试。",
      );
    return complete(pending, code);
  }

  /** Cancels a login in any terminal path: cancel, unmount, pagehide, close. */
  function cancelLogin(id) {
    const pending = logins.get(id);
    if (!pending) return { cancelled: false };
    settle(id, pending, () => logins.delete(id));
    clearTimeout(pending.timer);
    pending.server?.close();
    pending.controller.abort();
    logins.delete(id);
    return { cancelled: true };
  }

  function closeAll() {
    for (const id of [...logins.keys()]) cancelLogin(id);
  }

  /** Reads the catalog with the account's own key and returns one entrance's options. */
  async function catalog(id, mode) {
    if (!catalogModes.has(mode)) throw new AppError(400, "未知的模型能力。");
    const row = requireAccount(id);
    const generation = row.generation;
    const key = credential.key(id);
    if (!key) throw new AppError(400, "请先填写 API Key，或使用 OrcaRouter 账户连接。");
    if (!credential.usable(id))
      throw new AppError(
        401,
        "此 OrcaRouter 凭据已被撤销，请重新连接或填写新的 API Key。",
      );
    let models;
    let degraded = false;
    let reason = "";
    try {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), CATALOG_TIMEOUT_MS);
      let response;
      try {
        response = await fetchImpl(orcaCatalogURL(origins.api, mode === "multimodal" ? "chat" : mode), {
          method: "GET",
          redirect: "manual",
          signal: controller.signal,
          headers: { Accept: "application/json", Authorization: `Bearer ${key}` },
        });
      } finally {
        clearTimeout(timer);
      }
      if (response.status === 401 || response.status === 403) {
        // A relay rejection marks the exact generation that made the request and
        // never triggers a refresh: the flow has no refresh grant.
        credential.reauthenticate(id, generation);
        throw new AppError(
          401,
          "OrcaRouter 拒绝了这个 API Key，请在设置中重新填写或重新连接。",
        );
      }
      if (!response.ok) throw new AppError(502, "catalog_status");
      const body = await readBounded(response, CATALOG_BYTE_LIMIT);
      const parsed = parseCatalog(body);
      models = filterModels(parsed.models, selectorFor(mode)).slice(0, CATALOG_ITEM_LIMIT);
    } catch (error) {
      if (error instanceof AppError && error.status === 401) throw error;
      // Live discovery is authoritative when it answers. When it does not, the
      // verified seed keeps a fresh installation usable and is labelled.
      degraded = true;
      reason = error instanceof AppError ? error.message : "catalog_unreachable";
      models = filterModels(verifiedCatalog, selectorFor(mode));
    }
    return {
      models,
      degraded,
      ...(degraded ? { reason } : {}),
      catalog_source: degraded ? "verified-seed" : orcaCatalogURL(origins.api, mode === "multimodal" ? "chat" : mode),
      // Restoring a stored model id is only allowed while it is still compatible.
      selector: { capability: mode === "multimodal" ? "chat" : mode, input_modalities: mode === "multimodal" ? ["image"] : [] },
    };
  }

  /** One account's safe view, with its stored model choices re-validated. */
  function view(id) {
    const row = requireAccount(id);
    const models = store
      .list("models")
      .filter((model) => model.provider_id === id);
    return {
      ...publicOrcaAccount(row),
      inference_base: orcaInferenceBase(origins.api),
      authorize_url: `${origins.auth.replace(/\/+$/, "")}/auth`,
      key_console: "https://www.orcarouter.ai/console/authorized-apps",
      models,
      login: logins.get(id)
        ? { authorize_url: logins.get(id).authorize_url, redirect_uri: logins.get(id).redirect }
        : null,
    };
  }

  return {
    origins,
    credential,
    closeAll,
    view,
    startLogin,
    submitLogin,
    cancelLogin,
    catalog,
    /** Whether a saved model survives a change of provider or capability. */
    keeps: (models, mode, model) => keepsModel(models, selectorFor(mode), model),
  };
}

function exchangeMessage(status) {
  switch (status) {
    case 400:
      return "OrcaRouter 拒绝了本次授权，请重新连接。";
    case 403:
      return "授权码无效、已过期或已使用，请重新连接。";
    case 429:
      return "请求过于频繁，请稍后重试。";
    default:
      return "OrcaRouter 授权失败，请稍后重试。";
  }
}

/** Reads at most `limit` bytes of a JSON response; anything larger is refused. */
async function readBounded(response, limit) {
  const declared = Number(response.headers.get?.("content-length"));
  if (Number.isFinite(declared) && declared > limit)
    throw new AppError(502, "catalog_too_large");
  if (!response.body?.getReader) {
    const text = await response.text();
    if (Buffer.byteLength(text) > limit) throw new AppError(502, "catalog_too_large");
    return JSON.parse(text);
  }
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > limit) {
      await reader.cancel().catch(() => {});
      throw new AppError(502, "catalog_too_large");
    }
    chunks.push(value);
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}
