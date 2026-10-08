/**
 * PKCE material for `Connect with OrcaRouter`.
 *
 * The console runs on an operator-chosen address (`OAC_PUBLIC_URL`) and cannot
 * register or listen on a loopback callback, so it asks for an out-of-band code
 * (Flow B) that the consent screen shows and the operator pastes back. S256 is
 * mandatory there — a displayed code passes through human hands — and is used
 * here unconditionally.
 *
 * Everything in this file is pure: the verifier is created from the platform's
 * cryptographic RNG, never leaves the process before the exchange, is never
 * rendered, logged or placed in a URL, and no function here talks to the
 * network. A pasted key and the key this flow returns are the same thing to
 * everything downstream.
 */
import { orcarouterAppName, type OrcarouterConfiguration } from "./orcarouter";

export interface OrcarouterLogin {
  /** The URL the operator opens; `app_name`, `state` and the S256 challenge are already on it. */
  authorizeUrl: string;
  /** The verifier for this attempt. It stays in memory and is never rendered or logged. */
  verifier: string;
  /** The state this attempt sent; compared again before the code is redeemed. */
  state: string;
}

/** base64url without padding, as `code_challenge` requires. */
function base64url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/gu, "-").replace(/\//gu, "_").replace(/=+$/u, "");
}

/** `base64url(sha256(verifier))`, with the verifier never leaving this call. */
export async function pkceChallenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
  return base64url(new Uint8Array(digest));
}

function randomValue(bytes: number): string {
  return base64url(crypto.getRandomValues(new Uint8Array(bytes)));
}

/** A constant-time comparison, so a wrong state cannot be probed byte by byte. */
export function sameState(expected: string, received: string): boolean {
  if (expected.length === 0 || expected.length !== received.length) return false;
  let different = 0;
  for (let index = 0; index < expected.length; index += 1) different |= expected.charCodeAt(index) ^ received.charCodeAt(index);
  return different === 0;
}

/**
 * One fresh authorization attempt: a new verifier and state from a
 * cryptographic RNG, and S256 over the verifier. No attempt ever reuses either.
 */
export async function startOrcarouterLogin(
  configuration: OrcarouterConfiguration,
  appName: string = orcarouterAppName,
): Promise<OrcarouterLogin> {
  const verifier = randomValue(48);
  const state = randomValue(24);
  const url = new URL(configuration.authorizeUrl);
  url.searchParams.set("callback_url", "oob");
  url.searchParams.set("code_challenge", await pkceChallenge(verifier));
  url.searchParams.set("code_challenge_method", "S256");
  url.searchParams.set("state", state);
  url.searchParams.set("scope", "api");
  if (appName !== "") url.searchParams.set("app_name", appName);
  return { authorizeUrl: url.toString(), verifier, state };
}

/** A code is shown to a person; a paste is trimmed and refused if it carries anything else. */
export function normalizeCode(value: string): string {
  const code = value.trim();
  return /^[\x21-\x7e]{1,4096}$/u.test(code) ? code : "";
}

/** Whether a value is syntactically one of this gateway's keys. */
export function looksLikeOrcarouterKey(value: string): boolean {
  return /^sk-orca-[A-Za-z0-9._-]{8,}$/u.test(value.trim());
}
