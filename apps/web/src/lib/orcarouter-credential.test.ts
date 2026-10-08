import { afterEach, describe, expect, it, vi } from "vitest";

import { orcarouterDefaultConfiguration, type OrcarouterCredential } from "./orcarouter";
import {
  apiKeyAdapter,
  credentialFailure,
  pkceAdapter,
  readOrcarouterConfiguration,
  type OrcarouterLogin,
} from "./orcarouter-credential";
import { looksLikeOrcarouterKey, normalizeCode, pkceChallenge, sameState, startOrcarouterLogin } from "./orcarouter-pkce";

/** A pasted key and a PKCE result must reach the downstream request identically. */
const pasted = (key: string): OrcarouterCredential => ({ source: "api_key", key });

function answer(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function attempt(): { login: Promise<OrcarouterLogin>; code: string } {
  return { login: startOrcarouterLogin(orcarouterDefaultConfiguration), code: "one-time-code" };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("PKCE material", () => {
  it("derives an unpadded base64url S256 challenge from the verifier", async () => {
    // A fixed verifier proves the exact transformation; it is never a real one.
    const challenge = await pkceChallenge("fixture-verifier-not-a-real-one");
    // Cross-checked against `sha256` in node:crypto, base64url and unpadded.
    expect(challenge).toBe("g6nGTwndBfMLFnVyLF6MSEoHj1i5MddZ2d3XbIA5fig");
    expect(challenge).not.toMatch(/[+/=]/u);
  });

  it("generates a fresh verifier and state for every attempt", async () => {
    const first = await startOrcarouterLogin(orcarouterDefaultConfiguration);
    const second = await startOrcarouterLogin(orcarouterDefaultConfiguration);
    expect(first.verifier).not.toBe(second.verifier);
    expect(first.state).not.toBe(second.state);
    expect(first.verifier.length).toBeGreaterThanOrEqual(43);
    expect(first.state.length).toBeGreaterThanOrEqual(16);
    expect(second.verifier).not.toBe(first.verifier);
  });

  it("asks for an out-of-band code with S256, a state and the api scope", async () => {
    const login = await startOrcarouterLogin(orcarouterDefaultConfiguration);
    const url = new URL(login.authorizeUrl);
    expect(url.origin).toBe("https://www.orcarouter.ai");
    expect(url.pathname).toBe("/auth");
    expect(url.searchParams.get("callback_url")).toBe("oob");
    expect(url.searchParams.get("code_challenge_method")).toBe("S256");
    expect(url.searchParams.get("code_challenge")).toBe(await pkceChallenge(login.verifier));
    expect(url.searchParams.get("state")).toBe(login.state);
    expect(url.searchParams.get("scope")).toBe("api");
    expect(url.searchParams.get("app_name")).toBe("OpenAgentCore Console");
  });

  it("never puts the verifier in the authorize URL", async () => {
    const login = await startOrcarouterLogin(orcarouterDefaultConfiguration);
    expect(login.authorizeUrl).not.toContain(login.verifier);
    expect(login.authorizeUrl).not.toContain("code_verifier");
  });

  it("uses the deployment's own auth origin and never the inference origin", async () => {
    const login = await startOrcarouterLogin({ authOrigin: "https://login.internal", apiOrigin: "https://relay.internal", authorizeUrl: "https://login.internal/auth", inferenceBase: "https://relay.internal/v1", keyConsole: "https://login.internal/console" });
    const url = new URL(login.authorizeUrl);
    expect(url.origin).toBe("https://login.internal");
    expect(url.pathname).toBe("/auth");
  });

  it("compares state in constant time and refuses an empty expectation", () => {
    expect(sameState("abcd", "abcd")).toBe(true);
    expect(sameState("abcd", "abce")).toBe(false);
    expect(sameState("abcd", "abc")).toBe(false);
    expect(sameState("", "")).toBe(false);
  });
});

describe("the code a person pastes back", () => {
  it("accepts a printable code and refuses whitespace or control characters", () => {
    expect(normalizeCode("  abc-123  ")).toBe("abc-123");
    expect(normalizeCode("")).toBe("");
    expect(normalizeCode("abc def")).toBe("");
    expect(normalizeCode("abc\ndef")).toBe("");
    expect(normalizeCode(`x${"\u0000"}y`)).toBe("");
  });

  it("recognises a gateway key by shape only", () => {
    expect(looksLikeOrcarouterKey("sk-orca-abcdefgh")).toBe(true);
    expect(looksLikeOrcarouterKey("sk-orca-")).toBe(false);
    expect(looksLikeOrcarouterKey("sk-other-abcdefgh")).toBe(false);
  });
});

describe("the two credential adapters", () => {
  it("trims a pasted key and refuses an empty one without any request", async () => {
    expect(await apiKeyAdapter("  sk-orca-pasted  ").obtain()).toEqual({ source: "api_key", key: "sk-orca-pasted" });
    await expect(apiKeyAdapter("   ").obtain()).rejects.toThrow("credential-code-missing");
  });

  it("exchanges only at the console's own route and returns the issued key", async () => {
    const { login, code } = attempt();
    const resolved = await login;
    const calls: Array<{ url: string; body: unknown }> = [];
    const request = (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), body: init?.body });
      return answer({ key: "sk-orca-issued", scope: "api" });
    }) as typeof fetch;
    const credential = await pkceAdapter({ code, login: resolved, state: resolved.state, request }).obtain();
    expect(credential).toEqual({ source: "pkce", key: "sk-orca-issued" });
    expect(calls).toHaveLength(1);
    expect(calls[0]?.url).toBe("/console/orcarouter/exchange");
    expect(JSON.parse(String(calls[0]?.body))).toEqual({ code: "one-time-code", code_verifier: resolved.verifier, code_challenge_method: "S256" });
    // The verifier travels to our own server in a body, never in a URL.
    expect(String(calls[0]?.url)).not.toContain(resolved.verifier);
  });

  it("refuses a code from another attempt before redeeming anything", async () => {
    const { login, code } = attempt();
    const resolved = await login;
    let calls = 0;
    const request = (async () => {
      calls += 1;
      return answer({ key: "sk-orca-should-not-exist", scope: "api" });
    }) as typeof fetch;
    await expect(pkceAdapter({ code, login: resolved, state: "another-attempt", request }).obtain()).rejects.toThrow("credential-state-mismatch");
    expect(calls).toBe(0);
    expect(credentialFailure(new Error("credential-state-mismatch"))).toBe("state-mismatch");
  });

  it("refuses an empty paste without calling the server", async () => {
    const { login } = attempt();
    const resolved = await login;
    let calls = 0;
    const request = (async () => {
      calls += 1;
      return answer({ key: "sk-orca-should-not-exist", scope: "api" });
    }) as typeof fetch;
    await expect(pkceAdapter({ code: "   ", login: resolved, state: resolved.state, request }).obtain()).rejects.toThrow("credential-code-missing");
    expect(calls).toBe(0);
  });

  it.each([
    [403, "code-expired"],
    [429, "rate-limited"],
    [400, "code-expired"],
    [401, "unauthorized"],
    [502, "unreachable"],
    [500, "unavailable"],
  ] as const)("reports HTTP %s as %s and never the upstream body", async (status, expected) => {
    const { login, code } = attempt();
    const resolved = await login;
    const request = (async () => answer({ error: "leaked-detail sk-orca-secret" }, status)) as typeof fetch;
    const failure = await pkceAdapter({ code, login: resolved, state: resolved.state, request }).obtain().catch((cause: unknown) => credentialFailure(cause));
    expect(failure).toBe(expected);
    expect(String(failure)).not.toContain("sk-orca-secret");
  });

  it("refuses a granted scope other than the one this application asked for", async () => {
    const { login, code } = attempt();
    const resolved = await login;
    const request = (async () => answer({ key: "sk-orca-issued", scope: "connector" })) as typeof fetch;
    await expect(pkceAdapter({ code, login: resolved, state: resolved.state, request }).obtain()).rejects.toThrow("credential-scope-insufficient");
  });

  it("treats a response with no key as a failure rather than an empty credential", async () => {
    const { login, code } = attempt();
    const resolved = await login;
    const request = (async () => answer({ scope: "api" })) as typeof fetch;
    await expect(pkceAdapter({ code, login: resolved, state: resolved.state, request }).obtain()).rejects.toThrow("credential-exchange-200");
  });

  it("surfaces a network failure without inventing a credential", async () => {
    const { login, code } = attempt();
    const resolved = await login;
    const request = (async () => {
      throw new TypeError("fetch failed");
    }) as typeof fetch;
    const failure = await pkceAdapter({ code, login: resolved, state: resolved.state, request }).obtain().catch((cause: unknown) => credentialFailure(cause));
    expect(failure).toBe("unavailable");
  });

  it("keeps the verifier, the state and the code out of every error it produces", async () => {
    const { login, code } = attempt();
    const resolved = await login;
    const request = (async () => answer({ error: "no" }, 403)) as typeof fetch;
    const error = (await pkceAdapter({ code, login: resolved, state: resolved.state, request }).obtain().catch((cause: unknown) => cause)) as Error;
    expect(error.message).not.toContain(resolved.verifier);
    expect(error.message).not.toContain(resolved.state);
    expect(error.message).not.toContain(code);
  });

  it("hands the downstream request the same shape from a pasted key and from PKCE", async () => {
    const { login, code } = attempt();
    const resolved = await login;
    const request = (async () => answer({ key: "sk-orca-same", scope: "api" })) as typeof fetch;
    const viaPkce = await pkceAdapter({ code, login: resolved, state: resolved.state, request }).obtain();
    const viaPaste = await apiKeyAdapter("sk-orca-same").obtain();
    // Only the recorded source differs; every downstream field is identical.
    expect(viaPkce).toEqual({ ...viaPaste, source: "pkce" });
    expect(Object.keys(viaPkce).sort()).toEqual(Object.keys(viaPaste).sort());
    expect(viaPkce.key).toBe(viaPaste.key);
  });
});

describe("reading the deployment's origins", () => {
  it("uses the console's answer", async () => {
    const request = (async () => answer({ object: "console.orcarouter", auth_origin: "https://login.internal", api_origin: "https://relay.internal", authorize_url: "https://login.internal/auth", key_console: "https://login.internal/console" })) as typeof fetch;
    const configuration = await readOrcarouterConfiguration(undefined, request);
    expect(configuration.authOrigin).toBe("https://login.internal");
    expect(configuration.authorizeUrl).toBe("https://login.internal/auth");
  });

  it("falls back to the public origins when the read fails", async () => {
    const request = (async () => {
      throw new TypeError("offline");
    }) as typeof fetch;
    expect(await readOrcarouterConfiguration(undefined, request)).toEqual(orcarouterDefaultConfiguration);
  });
});
