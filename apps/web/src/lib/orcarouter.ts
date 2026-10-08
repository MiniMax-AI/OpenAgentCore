/**
 * OrcaRouter as a named model provider for the console.
 *
 * OrcaRouter is an OpenAI-compatible AI gateway that routes many providers
 * behind one endpoint, so one provider entry reaches every model the operator's
 * workspace may call. Authentication and inference live on different public
 * origins and are never derived from one another: `/api/v1/auth/keys` is on the
 * auth origin, inference and the model catalog are on the API origin under
 * `/v1`. A shared self-hosted origin plus explicit overrides are supported, and
 * an explicit override always wins.
 *
 * The credential is a normal OrcaRouter API key (`sk-orca-...`) that belongs to
 * the operator. The console offers both ways to obtain one — paste an existing
 * key, or `Connect with OrcaRouter` (OAuth 2.0 + PKCE) — and both reach the same
 * `ModelProviderInput` the dialog writes to Core. Everything below is pure so
 * the capability rules can be tested without a browser; the console server
 * performs the catalog request, so the key never enters browser code.
 */

/** The public OrcaRouter origins, used only when the console names no override. */
export const orcarouterAuthOrigin = "https://www.orcarouter.ai";
export const orcarouterApiOrigin = "https://api.orcarouter.ai";

/** Where an operator creates, lists and revokes keys. */
export const orcarouterKeyConsole = "https://www.orcarouter.ai/console/authorized-apps";

/** What the console's OrcaRouter config route answers. */
export interface OrcarouterConfiguration {
  authOrigin: string;
  apiOrigin: string;
  authorizeUrl: string;
  /** The address written to Core for this provider. */
  inferenceBase: string;
  keyConsole: string;
}

export const orcarouterDefaultConfiguration: OrcarouterConfiguration = {
  authOrigin: orcarouterAuthOrigin,
  apiOrigin: orcarouterApiOrigin,
  authorizeUrl: `${orcarouterAuthOrigin}/auth`,
  inferenceBase: `${orcarouterApiOrigin}/v1`,
  keyConsole: orcarouterKeyConsole,
};

/**
 * Reads that answer. The authorize URL is fixed to the auth origin's `/auth`
 * path and is never derived from the inference origin; a malformed value falls
 * back to the public default rather than sending an operator somewhere else.
 */
export function orcarouterConfiguration(payload: unknown): OrcarouterConfiguration {
  const record = (payload ?? {}) as Record<string, unknown>;
  const origin = (value: unknown, fallback: string): string => {
    if (typeof value !== "string") return fallback;
    const trimmed = value.trim().replace(/\/+$/u, "");
    if (trimmed === "") return fallback;
    try {
      const url = new URL(trimmed);
      const bare = url.pathname === "" || url.pathname === "/";
      return url.username === "" && url.password === "" && bare && url.search === "" && url.hash === "" ? trimmed : fallback;
    } catch {
      return fallback;
    }
  };
  const authOrigin = origin(record.auth_origin, orcarouterAuthOrigin);
  const apiOrigin = origin(record.api_origin, orcarouterApiOrigin);
  return {
    authOrigin,
    apiOrigin,
    authorizeUrl: `${authOrigin}/auth`,
    inferenceBase: orcarouterInferenceBase(apiOrigin),
    keyConsole: consoleURL(record.key_console, orcarouterKeyConsole),
  };
}

/** A console link, unlike an origin, may carry a path; credentials and queries may not. */
function consoleURL(value: unknown, fallback: string): string {
  if (typeof value !== "string") return fallback;
  const trimmed = value.trim();
  try {
    const url = new URL(trimmed);
    if (url.protocol !== "https:" || url.username !== "" || url.password !== "" || url.search !== "" || url.hash !== "") return fallback;
    return trimmed.replace(/\/+$/u, "");
  } catch {
    return fallback;
  }
}
/** The inference base written to Core: the API origin plus its `/v1` segment. */export function orcarouterInferenceBase(apiOrigin: string = orcarouterApiOrigin): string {
  const base = apiOrigin.trim().replace(/\/+$/u, "");
  return base.endsWith("/v1") ? base : `${base}/v1`;
}

/**
 * A capability an AI entrance asks the catalog for. Each entrance filters its
 * own list; a caller never widens another caller's list.
 */
export const catalogCapabilities = ["chat", "image", "embedding", "video", "rerank"] as const;
export type CatalogCapability = (typeof catalogCapabilities)[number];

/** A non-text modality an entrance actually accepts as input. */
export const catalogInputModalities = ["image", "audio", "video", "file"] as const;
export type CatalogInputModality = (typeof catalogInputModalities)[number];

/** One catalog record reduced to what a model selector needs. */
export interface CatalogModel {
  id: string;
  name: string;
  contextWindow?: number;
  maxOutputTokens?: number;
  /** Declared input modalities; `undefined` means the catalog did not say. */
  inputModalities?: string[];
  endpointTypes: string[];
  /** Verified reasoning-effort ladder, retained from the fallback seed. */
  reasoningEfforts?: string[];
}

/** Text protocols a chat entrance can speak, in the gateway's own vocabulary. */
const chatEndpointTypes = ["openai", "anthropic", "gemini", "openai-response"];
/** Non-text endpoints that must never appear in a text entrance's options. */
const nonTextEndpointTypes = ["image-generation", "openai-video", "jina-rerank", "embeddings"];

export interface CatalogSelector {
  capability: CatalogCapability;
  /** Non-text modalities this entrance actually uploads. Empty means text only. */
  inputModalities?: readonly CatalogInputModality[];
  /** A model must declare one of these endpoint types; omitted derives them from the capability. */
  endpointTypes?: readonly string[];
}

/** Context and output bounds are integers; anything else is not evidence. */
function positiveInteger(value: unknown): number | undefined {
  return typeof value === "number" && Number.isInteger(value) && value > 0 ? value : undefined;
}

function cleanStrings(values: readonly unknown[]): string[] {
  return values.filter((value): value is string => typeof value === "string" && value.trim() !== "");
}

/**
 * The modalities a record declares. The console server reduces records to a
 * flat `input_modalities`, so both that shape and the raw catalog's
 * `architecture.input_modalities` are read; `modalities_declared: false` means
 * the catalog did not say and the entrance must fail closed.
 */
function declaredModalities(record: Record<string, unknown>): string[] | undefined {
  if (record.modalities_declared === false) return undefined;
  if (Array.isArray(record.input_modalities)) return cleanStrings(record.input_modalities);
  const architecture = record.architecture;
  if (architecture === null || typeof architecture !== "object" || Array.isArray(architecture)) return undefined;
  const modalities = (architecture as Record<string, unknown>).input_modalities;
  return Array.isArray(modalities) ? cleanStrings(modalities) : undefined;
}

function declaredEndpointTypes(record: Record<string, unknown>): string[] {
  const value = record.supported_endpoint_types ?? record.endpoint_types;
  return Array.isArray(value) ? cleanStrings(value) : [];
}

/**
 * Reads the OpenAI-shaped catalog. A record without a usable ID or without the
 * fields the selector filters on is dropped rather than guessed at; the caller
 * sees how many records were refused.
 */
export function parseCatalog(payload: unknown): { models: CatalogModel[]; skipped: number } {
  const data = (payload as { data?: unknown } | null)?.data;
  if (!Array.isArray(data)) return { models: [], skipped: 0 };
  const models: CatalogModel[] = [];
  const seen = new Set<string>();
  let skipped = 0;
  for (const entry of data) {
    const record = entry as Record<string, unknown> | null;
    const id = typeof record?.id === "string" ? record.id.trim() : "";
    if (record === null || typeof record !== "object" || id === "" || id.length > 200 || seen.has(id)) {
      skipped += 1;
      continue;
    }
    seen.add(id);
    const name = typeof record.name === "string" && record.name.trim() !== "" ? record.name.trim() : id;
    const contextWindow = positiveInteger(record.context_length);
    const maxOutputTokens = positiveInteger(record.max_completion_tokens);
    const inputModalities = declaredModalities(record);
    models.push({
      id,
      name: name.length > 120 ? name.slice(0, 120) : name,
      ...(contextWindow === undefined ? {} : { contextWindow }),
      ...(maxOutputTokens === undefined ? {} : { maxOutputTokens }),
      ...(inputModalities === undefined ? {} : { inputModalities }),
      endpointTypes: declaredEndpointTypes(record),
    });
  }
  return { models, skipped };
}

/** The endpoint types that express one capability, in the gateway's own vocabulary. */
export function endpointTypesFor(capability: CatalogCapability): readonly string[] {
  switch (capability) {
    case "chat": return chatEndpointTypes;
    case "embedding": return ["embeddings"];
    case "image": return ["image-generation"];
    case "video": return ["openai-video"];
    case "rerank": return ["jina-rerank"];
  }
}

/**
 * Whether one catalog record satisfies an entrance. A model that does not
 * declare the capability, or does not declare an input modality the entrance
 * actually uploads, is refused: the catalog's silence is not evidence of
 * support.
 */
export function modelSatisfies(model: CatalogModel, selector: CatalogSelector): boolean {
  const allowed = selector.endpointTypes ?? endpointTypesFor(selector.capability);
  // A model with no declared endpoint types cannot be typed; it never enters a selector.
  if (model.endpointTypes.length === 0) return false;
  if (!model.endpointTypes.some((type) => allowed.includes(type))) return false;
  const wanted = selector.inputModalities ?? [];
  if (wanted.length === 0) return true;
  const modalities = model.inputModalities ?? [];
  return wanted.every((modality) => modalities.includes(modality));
}

/** What a chat entrance must exclude even when it declares a text protocol. */
export function isNonTextOnly(model: CatalogModel): boolean {
  return model.endpointTypes.length > 0 && model.endpointTypes.every((type) => nonTextEndpointTypes.includes(type));
}

/**
 * The options one selector binds. A chat entrance additionally drops models that
 * only speak a non-text endpoint, so image generation, video and rerank never
 * appear as a chat model.
 */
export function catalogOptions(models: readonly CatalogModel[], selector: CatalogSelector): CatalogModel[] {
  return models.filter((model) => modelSatisfies(model, selector) && (selector.capability !== "chat" || !isNonTextOnly(model)));
}

/** Whether a previously chosen model still satisfies the current entrance. */
export function catalogKeeps(models: readonly CatalogModel[], selector: CatalogSelector, model: string): boolean {
  return models.some((entry) => entry.id === model && catalogOptions([entry], selector).length === 1);
}

/**
 * Every way to obtain an OrcaRouter API key. The console offers both and both
 * reach the dialog's one credential seam, so providing the model provider never
 * depends on which one an operator can use.
 */
export type OrcarouterCredential = { source: OrcarouterCredentialSource; key: string };
export const orcarouterProviderLabel = "OrcaRouter";
export const orcarouterCredentialSources = ["api_key", "pkce"] as const;
export type OrcarouterCredentialSource = (typeof orcarouterCredentialSources)[number];
/** What `app_name` shows on the consent screen the operator reads. */
export const orcarouterAppName = "OpenAgentCore Console";

/**
 * Whether a saved base URL is this gateway's. The console writes the base URL
 * itself and never lets an operator type one for OrcaRouter, so a saved
 * configuration is recognised again after a reload instead of silently
 * becoming a generic OpenAI-compatible provider.
 */
export function isOrcarouterBase(baseUrl: string, apiOrigin: string = orcarouterApiOrigin): boolean {
  const base = baseUrl.trim().replace(/\/+$/u, "");
  const origin = apiOrigin.trim().replace(/\/+$/u, "");
  return base === origin || base === `${origin}/v1` || base.startsWith(`${origin}/v1/`);
}

/** What `GET <api>/v1/models` answered, reduced for one entrance. */
export interface CatalogAnswer {
  models: CatalogModel[];
  /** True when the live catalog could not be read and a verified seed is in use. */
  degraded: boolean;
  /** The API origin the catalog came from. */
  origin: string;
  /** How many records the catalog carried but the selector refused. */
  skipped: number;
}

/**
 * The live answer for one entrance: the records the entrance admits, and how
 * many were refused. A seed is never mixed into a successful live answer.
 */
export function catalogAnswer(payload: unknown, selector: CatalogSelector, origin: string): CatalogAnswer {
  const parsed = parseCatalog(payload);
  return { models: catalogOptions(parsed.models, selector), degraded: false, origin, skipped: parsed.skipped };
}

/**
 * The fallback for one entrance while live discovery is unavailable: only the
 * verified seed entries the same filter admits, and never free text.
 */
export function fallbackAnswer(selector: CatalogSelector, origin: string): CatalogAnswer & { reason: string } {
  return { models: catalogOptions(orcarouterVerifiedSeed, selector), degraded: true, origin, skipped: 0, reason: "catalog_unreachable" };
}

/**
 * The cold-start catalog, used only while live discovery is unavailable. Every
 * entry was read from `GET https://api.orcarouter.ai/v1/models` on 2026-10-08;
 * the verified reasoning ladders and input modalities are retained so a fallback
 * model is not silently reduced.
 */
export const orcarouterVerifiedSeed: readonly CatalogModel[] = [
  { id: "openai/gpt-5.5", name: "OpenAI: GPT-5.5", contextWindow: 272000, maxOutputTokens: 128000, inputModalities: ["text", "image", "file"], endpointTypes: ["openai", "openai-response"], reasoningEfforts: ["low", "medium", "high", "xhigh"] },
  { id: "anthropic/claude-opus-4.8", name: "Anthropic: Claude Opus 4.8", contextWindow: 1000000, maxOutputTokens: 128000, inputModalities: ["text", "image", "file"], endpointTypes: ["openai", "anthropic", "openai-response"] },
  { id: "google/gemini-3.5-flash", name: "Gemini 3.5 Flash", contextWindow: 1048576, maxOutputTokens: 65536, inputModalities: ["text", "image", "video", "audio", "file"], endpointTypes: ["openai", "gemini"] },
  { id: "deepseek/deepseek-v4-pro", name: "DeepSeek: DeepSeek V4 Pro", contextWindow: 1048576, maxOutputTokens: 384000, inputModalities: ["text"], endpointTypes: ["openai", "openai-response"] },
  { id: "orcarouter/auto", name: "OrcaRouter Auto", endpointTypes: ["openai", "anthropic", "gemini", "openai-response"] },
  { id: "openai/text-embedding-3-large", name: "OpenAI: Text Embedding 3 Large", inputModalities: ["text"], endpointTypes: ["embeddings"] },
  { id: "openai/gpt-image-1", name: "OpenAI: GPT Image 1", inputModalities: ["text", "image"], endpointTypes: ["image-generation"] },
];
