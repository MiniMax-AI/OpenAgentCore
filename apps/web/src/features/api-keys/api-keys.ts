export interface ConsoleAPIKey { id: string; name: string; prefix: string; created_at: string; revoked_at: string | null }
export interface IssuedConsoleAPIKey extends ConsoleAPIKey { key: string }
export class KeyRequestError extends Error { constructor(readonly status: number) { super("API key operation failed."); } }

export function safeKey(value: unknown): ConsoleAPIKey {
  if (!value || typeof value !== "object") throw new KeyRequestError(502);
  const row = value as Record<string, unknown>;
  if (typeof row.id !== "string" || !/^[a-f0-9-]{36}$/.test(row.id) || typeof row.name !== "string" || row.name.length > 80 ||
    typeof row.prefix !== "string" || row.prefix.length > 16 || typeof row.created_at !== "string" || !Number.isFinite(Date.parse(row.created_at)) ||
    row.revoked_at != null && (typeof row.revoked_at !== "string" || !Number.isFinite(Date.parse(row.revoked_at)))) throw new KeyRequestError(502);
  return { id: row.id, name: row.name, prefix: row.prefix, created_at: row.created_at, revoked_at: row.revoked_at as string | null ?? null };
}
async function request(path: string, init: RequestInit): Promise<unknown> {
  const response = await fetch(`/console/api-keys${path}`, { ...init, credentials: "same-origin", cache: "no-store" });
  if (!response.ok) throw new KeyRequestError(response.status);
  return response.json();
}
export async function listConsoleKeys(signal?: AbortSignal): Promise<ConsoleAPIKey[]> {
  const value = await request("", { signal }) as { data?: unknown[] };
  if (!value || !Array.isArray(value.data)) throw new KeyRequestError(502);
  return value.data.map(safeKey);
}
export async function createConsoleKey(id: string, name: string, signal?: AbortSignal): Promise<IssuedConsoleAPIKey> {
  const value = await request("", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ id, name }), signal }) as { key?: string };
  const metadata = safeKey(value);
  if (metadata.id !== id || typeof value.key !== "string" || value.key.length < 32 || value.key.length > 256 || /\s/.test(value.key)) throw new KeyRequestError(502);
  return { ...metadata, key: value.key };
}
export async function revokeConsoleKey(id: string, signal?: AbortSignal): Promise<void> {
  const value = await request(`/${encodeURIComponent(id)}`, { method: "DELETE", signal }) as { id?: string; deleted?: boolean };
  if (value?.id !== id || value.deleted !== true) throw new KeyRequestError(502);
}
