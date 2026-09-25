/** Whether the browser holds a console session. The console has one administrator and no usernames. */
export type ConsoleAuth = { mode: "login" } | { mode: "authenticated" };

export class ConsoleAuthError extends Error {
  constructor(readonly status: number, readonly retryAfterSeconds: number | null = null) { super("Console authentication failed."); }
}

export function parseConsoleAuth(value: unknown): ConsoleAuth {
  if (!value || typeof value !== "object" || !("mode" in value)) throw new ConsoleAuthError(502);
  if (value.mode === "login" || value.mode === "authenticated") return { mode: value.mode };
  throw new ConsoleAuthError(502);
}

/** Seconds to wait from a `Retry-After` header, given as seconds or as an HTTP date. */
export function retryAfterSeconds(header: string | null, now = Date.now()): number | null {
  const value = header?.trim();
  if (!value) return null;
  if (/^\d+$/.test(value)) return Number(value);
  const at = Date.parse(value);
  return Number.isNaN(at) ? null : Math.max(0, Math.ceil((at - now) / 1000));
}

async function readStatus(response: Response): Promise<ConsoleAuth> {
  if (!response.ok) throw new ConsoleAuthError(response.status, response.status === 429 ? retryAfterSeconds(response.headers.get("retry-after")) : null);
  try {
    return parseConsoleAuth(await response.json());
  } catch {
    throw new ConsoleAuthError(502);
  }
}

export async function readConsoleAuth(signal?: AbortSignal): Promise<ConsoleAuth> {
  return readStatus(await fetch("/console/auth", { credentials: "same-origin", cache: "no-store", signal }));
}

/**
 * Signs in with the deployment's Core key, or signs out. The key travels only
 * in this same-origin JSON body; the console server answers with a session
 * cookie and the browser never stores the key.
 */
export async function changeConsoleAuth(
  change: { action: "login"; coreKey: string } | { action: "logout" },
  signal?: AbortSignal,
): Promise<ConsoleAuth> {
  return readStatus(await fetch(`/console/auth/${change.action}`, {
    method: "POST", credentials: "same-origin", cache: "no-store", headers: { "Content-Type": "application/json" },
    body: JSON.stringify(change.action === "login" ? { core_key: change.coreKey } : {}), signal,
  }));
}
