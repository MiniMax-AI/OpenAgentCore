/**
 * API key ownership and activity, read through the console bridge
 * (`/console/api-keys/owners` and `/console/api-keys/activity`). Core records
 * which key performed each successful `/v1` write; the public API is unchanged.
 * Responses are projected strictly: a malformed entry is dropped from the map
 * (its owner stays unknown) rather than guessed.
 */

export const ownedResourceTypes = [
  "agent",
  "session",
  "environment_template",
  "skill",
  "skill_version",
  "file",
  "vault",
  "vault_credential",
  "environment_file",
  "session_artifact",
] as const;
export type OwnedResourceType = (typeof ownedResourceTypes)[number];

export type ActorType = "project_api_key" | "static" | "console";
export interface KeyActor {
  type: ActorType;
  id: string | null;
  name: string | null;
  prefix: string | null;
  revoked_at: string | null;
}

export interface OwnerRecord {
  resource_type: OwnedResourceType;
  resource_id: string;
  /** null: Core has no creation record (the resource predates recording). */
  owner: KeyActor | null;
  created_at: string | null;
}

export interface KeyActivity {
  id: string;
  created_at: string;
  actor: KeyActor;
  action: string;
  resource_type: string;
  resource_id: string;
  parent_resource_id: string | null;
  trace_id: string | null;
}

export interface KeyActivityPage {
  data: KeyActivity[];
  has_more: boolean;
  last_id: string | null;
}

export interface ActivityQuery {
  key_id?: string;
  actor_type?: ActorType;
  resource_type?: OwnedResourceType;
  resource_id?: string;
  after?: string;
  limit?: number;
}

/** The bridge or Core does not offer ownership records; hide the feature. */
export class OwnershipUnavailableError extends Error {
  constructor(readonly status: number) {
    super("API key records are not available from this console.");
  }
}
export class OwnershipRequestError extends Error {
  constructor(readonly status: number) {
    super("API key records could not be loaded.");
  }
}

const resourceIdPattern = /^[A-Za-z0-9_.:-]{1,128}$/;
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const actorTypes = new Set<ActorType>(["project_api_key", "static", "console"]);
export const OWNER_BATCH = 100;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
function isTimestamp(value: unknown): value is string {
  return typeof value === "string" && Number.isFinite(Date.parse(value));
}
function optionalText(value: unknown, max: number): string | null | undefined {
  if (value === null || value === undefined) return null;
  return typeof value === "string" && value.length > 0 && value.length <= max ? value : undefined;
}

export function projectActor(value: unknown): KeyActor | null {
  if (!isRecord(value) || !actorTypes.has(value.type as ActorType)) return null;
  const id = value.id === null || value.id === undefined ? null : typeof value.id === "string" && uuidPattern.test(value.id) ? value.id : undefined;
  const name = optionalText(value.name, 80);
  const prefix = optionalText(value.prefix, 32);
  const revoked = value.revoked_at === null || value.revoked_at === undefined ? null : isTimestamp(value.revoked_at) ? value.revoked_at : undefined;
  if (id === undefined || name === undefined || prefix === undefined || revoked === undefined) return null;
  // An issued key is always identified; the console and static keys may not be.
  if (value.type === "project_api_key" && (id === null || name === null)) return null;
  return { type: value.type as ActorType, id, name, prefix, revoked_at: revoked };
}

export function projectOwners(value: unknown, type: OwnedResourceType, requested: readonly string[]): Map<string, OwnerRecord> {
  if (!isRecord(value) || value.object !== "list" || !Array.isArray(value.data)) throw new OwnershipRequestError(502);
  const wanted = new Set(requested);
  const owners = new Map<string, OwnerRecord>();
  for (const entry of value.data) {
    if (!isRecord(entry) || entry.resource_type !== type || typeof entry.resource_id !== "string" || !wanted.has(entry.resource_id)) continue;
    const owner = entry.owner === null ? null : projectActor(entry.owner);
    if (entry.owner !== null && owner === null) continue;
    const created = entry.created_at === null || entry.created_at === undefined ? null : isTimestamp(entry.created_at) ? entry.created_at : undefined;
    if (created === undefined) continue;
    owners.set(entry.resource_id, { resource_type: type, resource_id: entry.resource_id, owner, created_at: created });
  }
  return owners;
}

export function projectActivityPage(value: unknown): KeyActivityPage {
  if (!isRecord(value) || value.object !== "list" || !Array.isArray(value.data) || typeof value.has_more !== "boolean") {
    throw new OwnershipRequestError(502);
  }
  const data: KeyActivity[] = [];
  for (const entry of value.data) {
    if (!isRecord(entry) || typeof entry.id !== "string" || !entry.id || !isTimestamp(entry.created_at)) continue;
    const actor = projectActor(entry.actor);
    if (!actor || typeof entry.action !== "string" || !entry.action || typeof entry.resource_type !== "string" || !entry.resource_type) continue;
    if (typeof entry.resource_id !== "string" || !resourceIdPattern.test(entry.resource_id)) continue;
    const parent = entry.parent_resource_id === null || entry.parent_resource_id === undefined ? null
      : typeof entry.parent_resource_id === "string" && resourceIdPattern.test(entry.parent_resource_id) ? entry.parent_resource_id : undefined;
    const trace = optionalText(entry.trace_id, 64);
    if (parent === undefined || trace === undefined) continue;
    data.push({ id: entry.id, created_at: entry.created_at, actor, action: entry.action, resource_type: entry.resource_type, resource_id: entry.resource_id, parent_resource_id: parent, trace_id: trace });
  }
  const lastId = typeof value.last_id === "string" && value.last_id ? value.last_id : null;
  return { data, has_more: value.has_more && lastId !== null, last_id: lastId };
}

async function request(path: string, signal?: AbortSignal): Promise<unknown> {
  const response = await fetch(`/console/api-keys/${path}`, { credentials: "same-origin", cache: "no-store", signal });
  // Absent capability: an older console (400 on any query), no route (404/405), or no paired administrator (503).
  if ([400, 404, 405, 501, 503].includes(response.status)) throw new OwnershipUnavailableError(response.status);
  if (!response.ok) throw new OwnershipRequestError(response.status);
  return response.json();
}

/** Owners for up to any number of IDs, fetched in batches of 100. */
export async function fetchOwners(type: OwnedResourceType, ids: readonly string[], signal?: AbortSignal): Promise<Map<string, OwnerRecord>> {
  const unique = [...new Set(ids)].filter((id) => resourceIdPattern.test(id));
  const result = new Map<string, OwnerRecord>();
  for (let start = 0; start < unique.length; start += OWNER_BATCH) {
    const batch = unique.slice(start, start + OWNER_BATCH);
    const query = new URLSearchParams({ resource_type: type, ids: batch.join(",") });
    const page = projectOwners(await request(`owners?${query}`, signal), type, batch);
    for (const [id, record] of page) result.set(id, record);
  }
  return result;
}

export async function fetchActivity(query: ActivityQuery, signal?: AbortSignal): Promise<KeyActivityPage> {
  const params = new URLSearchParams();
  // The bridge rejects empty values, so unset filters are omitted.
  if (query.key_id) params.set("key_id", query.key_id);
  if (query.actor_type) params.set("actor_type", query.actor_type);
  if (query.resource_type) params.set("resource_type", query.resource_type);
  if (query.resource_id) params.set("resource_id", query.resource_id);
  if (query.after) params.set("after", query.after);
  params.set("limit", String(query.limit ?? 50));
  return projectActivityPage(await request(`activity?${params}`, signal));
}
