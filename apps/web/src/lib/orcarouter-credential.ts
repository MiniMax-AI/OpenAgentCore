/**
 * The console's OrcaRouter credential seam.
 *
 * Every way of obtaining a credential — a pasted API key and `Connect with
 * OrcaRouter` (OAuth 2.0 + PKCE) — is an adapter behind one interface, and both
 * adapters answer with the same `OrcarouterCredential`. Nothing downstream
 * (the dialog, the model catalog, the `ModelProviderInput` written to Core)
 * knows which adapter produced it.
 *
 * The console server owns both OrcaRouter origins and performs the two
 * credential-bearing requests, so the verifier never leaves this process and
 * the API key never reaches browser storage.
 */
import {
  orcarouterConfiguration,
  orcarouterDefaultConfiguration,
  type OrcarouterConfiguration,
  type OrcarouterCredential,
} from "./orcarouter";
import { normalizeCode, sameState, type OrcarouterLogin } from "./orcarouter-pkce";

export type { OrcarouterLogin } from "./orcarouter-pkce";

/** One adapter: it answers with the credential every downstream request uses. */
export interface OrcarouterCredentialAdapter {
  readonly source: OrcarouterCredential["source"];
  obtain(): Promise<OrcarouterCredential>;
}

/** Adapter 1: the operator already holds a key and pastes it. */
export function apiKeyAdapter(key: string): OrcarouterCredentialAdapter {
  return {
    source: "api_key",
    async obtain() {
      const trimmed = key.trim();
      if (trimmed === "") throw new Error("credential-code-missing");
      return { source: "api_key", key: trimmed };
    },
  };
}

/**
 * Adapter 2: the operator has no key and approves one on the consent screen.
 * The code is redeemed by the console server; the granted scope is read back
 * and a narrower grant is refused rather than assumed.
 */
export function pkceAdapter(input: {
  /** The code the operator pasted back. */
  code: string;
  /** The attempt it belongs to; the state must still match. */
  login: OrcarouterLogin;
  state: string;
  signal?: AbortSignal;
  request?: typeof fetch;
}): OrcarouterCredentialAdapter {
  return {
    source: "pkce",
    async obtain() {
      const code = normalizeCode(input.code);
      // The state is compared before anything is redeemed: a code from another
      // attempt is never exchanged, whatever a paste into the wrong window did.
      if (!sameState(input.login.state, input.state)) throw new Error("credential-state-mismatch");
      if (code === "" || input.login.verifier === "") throw new Error("credential-code-missing");
      const request = input.request ?? fetch;
      const response = await request("/console/orcarouter/exchange", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code, code_verifier: input.login.verifier, code_challenge_method: "S256" }),
        signal: input.signal,
      });
      const answer = (await response.json().catch(() => ({}))) as { key?: unknown; scope?: unknown };
      if (!response.ok || typeof answer.key !== "string" || answer.key === "") throw new Error(`credential-exchange-${response.status}`);
      // The granted scope is read back; it is what was approved, not what was asked for.
      if (answer.scope !== "api") throw new Error("credential-scope-insufficient");
      return { source: "pkce", key: answer.key };
    },
  };
}

/** Why an attempt failed, as the operator's next step. */
export type OrcarouterCredentialFailure =
  | "state-mismatch"
  | "missing-code"
  | "code-expired"
  | "rate-limited"
  | "unreachable"
  | "unauthorized"
  | "unavailable";

export function credentialFailure(cause: unknown): OrcarouterCredentialFailure {
  const message = cause instanceof Error ? cause.message : "";
  if (message === "credential-state-mismatch") return "state-mismatch";
  if (message === "credential-code-missing") return "missing-code";
  if (message === "credential-scope-insufficient") return "unauthorized";
  if (message.endsWith("-429")) return "rate-limited";
  if (message.endsWith("-403")) return "code-expired";
  if (message.endsWith("-401")) return "unauthorized";
  if (message.endsWith("-400")) return "code-expired";
  if (message.endsWith("-502") || message.endsWith("-503")) return "unreachable";
  return "unavailable";
}

/**
 * The deployment's OrcaRouter origins, as the console server reports them. A
 * failed read keeps the public defaults and never invents an address.
 */
export async function readOrcarouterConfiguration(
  signal?: AbortSignal,
  request: typeof fetch = fetch,
): Promise<OrcarouterConfiguration> {
  try {
    const response = await request("/console/orcarouter/config", { credentials: "include", signal });
    if (!response.ok) return orcarouterDefaultConfiguration;
    return orcarouterConfiguration(await response.json());
  } catch {
    return orcarouterDefaultConfiguration;
  }
}
