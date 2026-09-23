export type ConsoleAuth = { mode: "legacy" | "setup" | "login" } | { mode: "authenticated"; username: string };

export class ConsoleAuthError extends Error {
  constructor(readonly status: number) { super("Console authentication failed."); }
}

export function parseConsoleAuth(value: unknown): ConsoleAuth {
  if (!value || typeof value !== "object" || !("mode" in value)) throw new ConsoleAuthError(502);
  if (value.mode === "legacy" || value.mode === "setup" || value.mode === "login") return { mode: value.mode };
  if (value.mode === "authenticated" && "username" in value && typeof value.username === "string" && value.username.length > 0) {
    return { mode: "authenticated", username: value.username };
  }
  throw new ConsoleAuthError(502);
}

export async function readConsoleAuth(signal?: AbortSignal, accountExpected = false): Promise<ConsoleAuth> {
  const response = await fetch("/console/auth", { credentials: "same-origin", cache: "no-store", signal });
  // Older consoles and the standalone development server serve the SPA here.
  if (!accountExpected && (response.status === 404 || response.ok && response.headers.get("content-type")?.includes("text/html"))) {
    return { mode: "legacy" };
  }
  if (!response.ok) throw new ConsoleAuthError(response.status);
  const status = parseConsoleAuth(await response.json());
  if (accountExpected && status.mode === "legacy") throw new ConsoleAuthError(502);
  return status;
}

export async function changeConsoleAuth(action: "setup" | "login" | "logout", body: Record<string, string>, signal?: AbortSignal): Promise<ConsoleAuth> {
  const response = await fetch(`/console/auth/${action}`, {
    method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body), signal,
  });
  if (!response.ok) throw new ConsoleAuthError(response.status);
  return parseConsoleAuth(await response.json());
}
