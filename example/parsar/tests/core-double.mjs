import { randomUUID } from "node:crypto";
import { AppError } from "../server/store.mjs";

// Core's saved Agents and Environment Templates for tests, in front of a test's
// own Core for everything else. `saved` holds what Parsar wrote.
const resource = /^\/v1\/agents(\/environments\/templates)?(?:\/([^/?]+))?$/;
function handle(saved, path, method, body) {
  const match = path.match(resource);
  if (!match || ["sessions", "environments"].includes(match[2])) return;
  const [, , id] = match;
  if (!id && method === "POST") {
    const row = { id: randomUUID(), ...body };
    saved.set(row.id, row);
    return { status: 201, value: row };
  }
  const row = id && saved.get(id);
  if (!row) return { status: 404, value: { error: { message: "not found" } } };
  if (method === "DELETE") {
    saved.delete(id);
    return { status: 200, value: { id, deleted: true } };
  }
  if (method === "POST") Object.assign(row, body);
  return { status: 200, value: row };
}

export function withSavedResources(core) {
  const saved = new Map();
  const wrapped = async (path, method, body, key) => {
    const result = handle(saved, path, method, body);
    if (!result) return core(path, method, body, key);
    if (result.status >= 400) throw new AppError(result.status, result.value.error.message);
    return result.value;
  };
  wrapped.saved = saved;
  // The saved Core Agent a Session request references.
  wrapped.agentOf = (request) => saved.get(request.agent_id);
  return wrapped;
}

export function savedResourcesFetch(fetchImpl) {
  const saved = new Map();
  const wrapped = async (url, init = {}) => {
    const { pathname } = new URL(url);
    if (!resource.test(pathname)) return fetchImpl(url, init);
    const result = handle(saved, pathname, init.method || "GET", init.body ? JSON.parse(init.body) : undefined);
    return result ? Response.json(result.value, { status: result.status }) : fetchImpl(url, init);
  };
  wrapped.saved = saved;
  return wrapped;
}
