// OrcaRouter provider rules shared by the example server and its browser code.
//
// OrcaRouter is an OpenAI-compatible AI gateway that routes many providers
// behind one endpoint. Authentication and inference live on different public
// origins and neither is derived from the other: the code exchange is on the
// auth origin at `/api/v1/auth/keys`, while inference and the model catalog are
// on the API origin under `/v1`. `ORCA_BASE_URL` is a shared self-hosted
// fallback; the explicit `ORCA_AUTH_BASE_URL` and `ORCA_API_BASE_URL` overrides
// win over it.
//
// Nothing here holds a credential. The example server owns the credential seam
// described in `server/orcarouter.mjs`.

export const orcaAuthOrigin = "https://www.orcarouter.ai";
export const orcaApiOrigin = "https://api.orcarouter.ai";
/** Where an operator lists and revokes the keys this example holds. */
export const orcaKeyConsole = "https://www.orcarouter.ai/console/authorized-apps";
/** The consent screen. It is a page to open, not an API to call. */
export const orcaAuthorizePath = "/auth";
/** The exchange is on the auth origin; the inference origin's `/v1/auth/keys` is a 404. */
export const orcaExchangePath = "/api/v1/auth/keys";
/** The only scope this example asks for. */
export const orcaScope = "api";
export const orcaAppName = "Parsar Agent Workbench";

function trimmed(value) {
  return typeof value === "string" ? value.trim().replace(/\/+$/, "") : "";
}

/** The auth and API origins for this deployment. Explicit overrides win. */
export function orcaOrigins(env = {}) {
  const shared = trimmed(env.ORCA_BASE_URL);
  return {
    auth: trimmed(env.ORCA_AUTH_BASE_URL) || shared || orcaAuthOrigin,
    api: trimmed(env.ORCA_API_BASE_URL) || shared || orcaApiOrigin,
  };
}

/**
 * Whether an origin may be used. Remote origins require HTTPS; HTTP is allowed
 * only for a loopback self-hosted deployment.
 */
export function allowedOrcaOrigin(origin) {
  let url;
  try {
    url = new URL(origin);
  } catch {
    return false;
  }
  if (
    !url.hostname ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    (url.pathname !== "" && url.pathname !== "/") ||
    /[\\\u0000\r\n]/.test(origin)
  )
    return false;
  if (url.protocol === "https:") return true;
  return (
    url.protocol === "http:" &&
    ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname)
  );
}

/** The catalog endpoint: always `<api origin>/v1/models`, never doubled. */
export function orcaCatalogURL(apiOrigin, capability = "") {
  const base = trimmed(apiOrigin);
  const path = base.endsWith("/v1") ? `${base}/models` : `${base}/v1/models`;
  return capability
    ? `${path}?capability=${encodeURIComponent(capability)}`
    : path;
}

/** The inference base an OrcaRouter provider record uses. */
export function orcaInferenceBase(apiOrigin = orcaApiOrigin) {
  return `${trimmed(apiOrigin)}/v1`;
}

/** The code exchange endpoint on the auth origin. */
export function orcaExchangeURL(authOrigin = orcaAuthOrigin) {
  return `${trimmed(authOrigin)}${orcaExchangePath}`;
}

/**
 * The authorize URL. `code_challenge_method` is always `S256`: the consent
 * screen can hand the code to a person, and a `plain` challenge would travel on
 * this URL together with the verifier.
 */
export function orcaAuthorizeURL({ authOrigin, challenge, state, callbackURL, appName = orcaAppName, scope = orcaScope }) {
  const url = new URL(`${trimmed(authOrigin)}${orcaAuthorizePath}`);
  url.searchParams.set("callback_url", callbackURL);
  url.searchParams.set("code_challenge", challenge);
  url.searchParams.set("code_challenge_method", "S256");
  url.searchParams.set("state", state);
  url.searchParams.set("app_name", appName);
  url.searchParams.set("scope", scope);
  return url.toString();
}

/** The endpoint types that express one capability, in the gateway's vocabulary. */
export const capabilityEndpoints = {
  chat: ["openai", "anthropic", "gemini", "openai-response"],
  embedding: ["embeddings"],
  image: ["image-generation"],
  video: ["openai-video"],
  rerank: ["jina-rerank"],
};

/** Endpoints that only serve a non-text modality and never a chat entrance. */
export const nonTextEndpoints = [
  "image-generation",
  "openai-video",
  "jina-rerank",
  "embeddings",
];

function strings(value, maxItems, maxLength) {
  if (!Array.isArray(value)) return null;
  const out = [];
  for (const entry of value) {
    if (typeof entry !== "string") continue;
    const text = entry.trim();
    if (!text || text.length > maxLength) continue;
    if (out.length >= maxItems) break;
    out.push(text);
  }
  return out.length ? out : null;
}

function positive(value) {
  return typeof value === "number" && Number.isInteger(value) && value > 0
    ? value
    : null;
}

/**
 * Reads the OpenAI-shaped catalog into the metadata a model selector filters on.
 * A record without a usable ID is refused rather than guessed at.
 */
export function parseCatalog(payload) {
  const data = payload?.data;
  if (!Array.isArray(data)) return { models: [], refused: 0 };
  const models = [];
  const seen = new Set();
  let refused = 0;
  for (const entry of data) {
    if (entry === null || typeof entry !== "object" || Array.isArray(entry)) {
      refused += 1;
      continue;
    }
    const id = typeof entry.id === "string" ? entry.id.trim() : "";
    if (!id || id.length > 200 || /[\u0000\r\n]/.test(id) || seen.has(id)) {
      refused += 1;
      continue;
    }
    seen.add(id);
    const name =
      typeof entry.name === "string" && entry.name.trim()
        ? entry.name.trim().slice(0, 200)
        : id;
    const architecture =
      entry.architecture !== null &&
      typeof entry.architecture === "object" &&
      !Array.isArray(entry.architecture)
        ? entry.architecture
        : null;
    models.push({
      id,
      name,
      context_window: positive(entry.context_length),
      max_output_tokens: positive(entry.max_completion_tokens),
      // `null` means the catalog did not declare modalities; an entry that
      // declares them is retained verbatim so a non-text entrance can filter.
      input_modalities: architecture
        ? strings(architecture.input_modalities, 16, 32)
        : null,
      supported_endpoint_types: strings(entry.supported_endpoint_types, 32, 64),
    });
  }
  return { models, refused };
}

/** The endpoint types a capability must declare. */
export function endpointsFor(capability) {
  return capabilityEndpoints[capability] ?? [];
}

/**
 * Whether a catalog record satisfies one entrance. A record that declares no
 * endpoint types cannot be typed and never enters a selector; a record that does
 * not declare a modality the entrance uploads is refused, because the catalog's
 * silence is not evidence of support.
 */
export function modelSatisfies(model, { capability, inputModalities = [] }) {
  const declared = model.supported_endpoint_types;
  if (!Array.isArray(declared) || declared.length === 0) return false;
  if (!declared.some((type) => endpointsFor(capability).includes(type)))
    return false;
  if (capability === "chat" && declared.every((type) => nonTextEndpoints.includes(type)))
    return false;
  if (!inputModalities.length) return true;
  const modalities = model.input_modalities ?? [];
  return inputModalities.every((modality) => modalities.includes(modality));
}

/** The model options one entrance binds, in catalog order. */
export function filterModels(models, selector) {
  return models.filter((model) => modelSatisfies(model, selector));
}

/** Whether a stored model choice is still valid for the entrance's current selector. */
export function keepsModel(models, selector, model) {
  return filterModels(models, selector).some((entry) => entry.id === model);
}

/**
 * The cold-start catalog, used only while live discovery is unavailable. Every
 * entry and every number was read from
 * `GET https://api.orcarouter.ai/v1/models?capability=chat` on 2026-10-08. The
 * verified reasoning ladder of `openai/gpt-5.5` is retained so a fallback model
 * is not silently reduced.
 */
export const verifiedCatalog = [
  { id: "openai/gpt-5.5", name: "OpenAI: GPT-5.5", context_window: 272000, max_output_tokens: 128000, input_modalities: ["file", "image", "text"], supported_endpoint_types: ["openai", "openai-response"], reasoning_efforts: ["low", "medium", "high", "xhigh"] },
  { id: "anthropic/claude-opus-4.8", name: "Anthropic: Claude Opus 4.8", context_window: 1000000, max_output_tokens: 128000, input_modalities: ["text", "image", "file"], supported_endpoint_types: ["openai", "anthropic", "openai-response"] },
  { id: "google/gemini-3.5-flash", name: "Gemini 3.5 Flash", context_window: 1048576, max_output_tokens: 65536, input_modalities: ["text", "image", "video", "file", "audio"], supported_endpoint_types: ["openai", "gemini"] },
  { id: "deepseek/deepseek-v4-pro", name: "DeepSeek: DeepSeek V4 Pro", context_window: 1048576, max_output_tokens: 384000, input_modalities: ["text"], supported_endpoint_types: ["openai", "openai-response"] },
  { id: "orcarouter/auto", name: "OrcaRouter Auto", context_window: null, max_output_tokens: null, input_modalities: ["text"], supported_endpoint_types: ["openai", "anthropic", "gemini", "openai-response"] },
];
