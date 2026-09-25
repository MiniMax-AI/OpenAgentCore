import { AgentCoreError } from "./client";
import { isRecord } from "./response-projection";
import type { ReadOptions } from "./types";

/** Constructor options of the Core clients that take a plain bearer `token`. */
export interface CoreClientOptions {
  /** Prefix that request paths are appended to: `/core/v1/sandbox` for SandboxAdminClient, `/core/v1` for CoreMetricsClient by default. */
  baseUrl?: string;
  /** Core key for trusted server callers; console browsers use their same-origin session instead. */
  token?: string | (() => string | undefined);
  /** Fetch implementation; defaults to the global fetch. */
  fetch?: typeof fetch;
}

/**
 * The internal request layer of the `/core/v1` clients. It sends no OpenAI-Beta
 * header, attaches a bearer only when a token is supplied, keeps browser
 * requests same-origin, refuses redirects and never retries.
 */
export class CoreRequester {
  readonly #baseUrl: string;
  readonly #token: CoreClientOptions["token"];
  readonly #fetch: typeof fetch;
  readonly #invalid: () => never;

  constructor(baseUrl: string, token: CoreClientOptions["token"], fetchImpl: typeof fetch | undefined, invalid: () => never) {
    this.#baseUrl = baseUrl.replace(/\/+$/, "");
    this.#token = token;
    this.#fetch = fetchImpl ?? globalThis.fetch.bind(globalThis);
    this.#invalid = invalid;
  }

  /** Sends one request; a non-2xx response becomes an AgentCoreError with Core's error envelope. */
  async response(path: string, options?: ReadOptions, method = "GET", body?: unknown): Promise<Response> {
    const headers = new Headers({ Accept: "application/json" });
    const token = typeof this.#token === "function" ? this.#token() : this.#token;
    if (token) headers.set("Authorization", `Bearer ${token}`);
    if (body !== undefined) headers.set("Content-Type", "application/json");
    const response = await this.#fetch(`${this.#baseUrl}${path}`, {
      method, headers, signal: options?.signal, credentials: "same-origin", redirect: "error",
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    if (!response.ok) {
      let envelope: unknown;
      try { envelope = await response.json(); } catch { /* An intermediary may return a non-JSON error. */ }
      const error = isRecord(envelope) && isRecord(envelope.error) ? envelope.error : {};
      throw new AgentCoreError(
        typeof error.message === "string" ? error.message : `Core request failed (${response.status}).`,
        response.status,
        typeof error.code === "string" || error.code === null ? error.code : undefined,
        typeof error.param === "string" || error.param === null ? error.param : undefined,
        typeof error.type === "string" ? error.type : undefined,
      );
    }
    return response;
  }

  /** Sends one request and parses its JSON body; an unparsable body is an invalid response. */
  async json(path: string, options?: ReadOptions, method?: string, body?: unknown): Promise<unknown> {
    const response = await this.response(path, options, method, body);
    try { return await response.json(); } catch { return this.#invalid(); }
  }
}
