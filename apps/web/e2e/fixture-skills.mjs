// Skills routes of the acceptance fixture Core. Shapes follow
// services/agents-api/internal/api/skills*.go: no Beta header, `files` for one
// ZIP, repeated `files[]` for a folder, an optional single `default` field on
// version uploads, never-reused version numbers, and sole-version deletion that
// deletes the Skill.
import { inflateRawSync } from "node:zlib";

const baseline = 1_789_438_800;
const invalidMessage = "Invalid resource identifier or request limits.";

let skills = [];
let calls = [];
let sequence = 0;
/** "available", "unsupported" (404 on every Skill route) or "storage-unavailable" (503). */
let availability = "available";

export function resetSkillsFixture() {
  skills = [];
  calls = [];
  sequence = 0;
  availability = "available";
}

function nextId(prefix) {
  sequence += 1;
  return `${prefix}_20000000-0000-4000-8000-${String(sequence).padStart(12, "0")}`;
}

function publicSkill(skill) {
  const latest = skill.versions.at(-1);
  const current = skill.versions.find((version) => version.version === skill.defaultVersion);
  return {
    id: skill.id,
    object: "skill",
    created_at: skill.createdAt,
    name: current.name,
    description: current.description,
    default_version: String(skill.defaultVersion),
    latest_version: String(latest.version),
  };
}

function publicVersion(skill, version) {
  return {
    id: version.id,
    object: "skill.version",
    created_at: version.createdAt,
    skill_id: skill.id,
    version: String(version.version),
    name: version.name,
    description: version.description,
  };
}

function listPage(values, url, cursorPrefix) {
  const order = url.searchParams.get("order");
  const limitValue = url.searchParams.get("limit");
  const after = url.searchParams.get("after");
  if (order !== null && order !== "asc" && order !== "desc") return null;
  const limit = limitValue === null ? 20 : Number(limitValue);
  if (!Number.isInteger(limit) || limit < 0 || limit > 100) return null;
  if (after !== null && !after.startsWith(cursorPrefix)) return null;
  const ordered = order === "asc" ? [...values] : [...values].reverse();
  const start = after === null ? 0 : ordered.findIndex((value) => value.id === after) + 1;
  if (after !== null && start === 0) return undefined;
  const data = ordered.slice(start, start + limit);
  return {
    object: "list",
    data,
    first_id: data[0]?.id ?? null,
    last_id: data.at(-1)?.id ?? null,
    has_more: start + data.length < ordered.length,
  };
}

async function readBody(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  return Buffer.concat(chunks);
}

/** Parses the Skill multipart body without dropping directory components from filenames. */
async function readSkillMultipart(request) {
  const match = /boundary=(?:"([^"]+)"|([^;]+))/i.exec(request.headers["content-type"] ?? "");
  if (!match) return null;
  const boundary = match[1] ?? match[2];
  const raw = (await readBody(request)).toString("latin1");
  const parts = raw.split(`--${boundary}`).slice(1, -1);
  const result = { zip: null, files: [], defaults: [] };
  for (const rawPart of parts) {
    const part = rawPart.replace(/^\r\n/, "").replace(/\r\n$/, "");
    const separator = part.indexOf("\r\n\r\n");
    if (separator < 0) return null;
    const header = part.slice(0, separator);
    const body = Buffer.from(part.slice(separator + 4), "latin1");
    const name = /\bname="([^"]+)"/i.exec(header)?.[1];
    const filename = /\bfilename="([^"]*)"/i.exec(header)?.[1];
    const decodedFilename = filename === undefined ? undefined : Buffer.from(filename, "latin1").toString("utf8");
    if (name === "default" && filename === undefined) result.defaults.push(body.toString("utf8"));
    else if (name === "files" && decodedFilename) result.zip = { filename: decodedFilename, data: body };
    else if (name === "files[]" && decodedFilename) result.files.push({ path: decodedFilename, data: body });
    else return null;
  }
  return result;
}

function readZip(buffer) {
  let end = -1;
  for (let index = buffer.length - 22; index >= 0; index -= 1) {
    if (buffer.readUInt32LE(index) === 0x06054b50) { end = index; break; }
  }
  if (end < 0) return null;
  const count = buffer.readUInt16LE(end + 10);
  let position = buffer.readUInt32LE(end + 16);
  const files = [];
  for (let index = 0; index < count; index += 1) {
    if (buffer.readUInt32LE(position) !== 0x02014b50) return null;
    const method = buffer.readUInt16LE(position + 10);
    const compressedSize = buffer.readUInt32LE(position + 20);
    const nameLength = buffer.readUInt16LE(position + 28);
    const extraLength = buffer.readUInt16LE(position + 30);
    const commentLength = buffer.readUInt16LE(position + 32);
    const offset = buffer.readUInt32LE(position + 42);
    const path = buffer.subarray(position + 46, position + 46 + nameLength).toString("utf8");
    position += 46 + nameLength + extraLength + commentLength;
    if (path.endsWith("/")) continue;
    const start = offset + 30 + buffer.readUInt16LE(offset + 26) + buffer.readUInt16LE(offset + 28);
    const data = buffer.subarray(start, start + compressedSize);
    if (method !== 0 && method !== 8) return null;
    files.push({ path, data: method === 8 ? inflateRawSync(data) : Buffer.from(data) });
  }
  return files;
}

/** A stored ZIP of the folder upload, as Core rebuilds it before storing. */
function writeZip(files) {
  const locals = [];
  const centrals = [];
  let offset = 0;
  for (const file of files) {
    const name = Buffer.from(file.path, "utf8");
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0x800, 6);
    local.writeUInt32LE(file.data.length, 18);
    local.writeUInt32LE(file.data.length, 22);
    local.writeUInt16LE(name.length, 26);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(20, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(0x800, 8);
    central.writeUInt32LE(file.data.length, 20);
    central.writeUInt32LE(file.data.length, 24);
    central.writeUInt16LE(name.length, 28);
    central.writeUInt32LE(offset, 42);
    locals.push(local, name, file.data);
    centrals.push(central, name);
    offset += local.length + name.length + file.data.length;
  }
  const directory = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(files.length, 8);
  end.writeUInt16LE(files.length, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}

/** The fixture's bundle rule: one top-level folder with SKILL.md whose frontmatter has name and description. */
function inspectBundle(files) {
  if (!files?.length || files.length > 500) return null;
  const paths = new Set();
  const topLevels = new Set();
  for (const file of files) {
    const segments = file.path.split("/");
    if (segments.length < 2 || segments.some((segment) => !segment || segment === "." || segment === "..") || /[\\\u0000-\u001f]/.test(file.path) || paths.has(file.path)) return null;
    paths.add(file.path);
    topLevels.add(segments[0]);
  }
  if (topLevels.size !== 1) return null;
  const [top] = topLevels;
  const manifest = files.find((file) => file.path.toLowerCase() === `${top.toLowerCase()}/skill.md`);
  const text = manifest?.data.toString("utf8").replaceAll("\r\n", "\n") ?? "";
  const frontmatter = /^---\n([\s\S]*?)\n---(?:\n|$)/.exec(text)?.[1];
  if (frontmatter === undefined) return null;
  const fields = Object.fromEntries(frontmatter.split("\n").map((line) => /^([a-z_-]+):\s*(.*)$/.exec(line)).filter(Boolean).map((line) => [line[1], line[2].trim()]));
  if (Object.keys(fields).some((key) => !["name", "description", "license", "compatibility", "metadata"].includes(key))) return null;
  if (!/^[a-z0-9]+(?:[-_][a-z0-9]+)*$/.test(fields.name ?? "") || !fields.description) return null;
  return { name: fields.name, description: fields.description };
}

function sendContent(response, version) {
  response.writeHead(200, {
    "content-type": "application/octet-stream",
    "content-disposition": `attachment; filename=${version.name}.zip`,
    "content-length": version.archive.length,
    "cache-control": "no-store",
    "x-content-type-options": "nosniff",
  });
  response.end(version.archive);
}

function coreError(sendJson, response, status, message, code = null, param = null) {
  sendJson(response, { error: { message, type: status >= 500 ? "server_error" : "invalid_request_error", code, param } }, status);
}

/**
 * Handles `/__fixture/skills*` controls and `/v1/skills*`. Returns false for
 * other paths. `record` receives a safe request summary for `/__fixture/requests`.
 */
export async function handleSkillsFixture(request, response, url, sendJson, record) {
  const path = url.pathname;
  if (path === "/__fixture/skills") {
    if (request.method === "POST") {
      const value = url.searchParams.get("availability") ?? "available";
      if (!["available", "unsupported", "storage-unavailable"].includes(value)) {
        coreError(sendJson, response, 400, "Unknown Skills availability.");
        return true;
      }
      availability = value;
    }
    sendJson(response, { availability, calls, skills: skills.map(publicSkill) });
    return true;
  }
  if (path !== "/v1/skills" && !path.startsWith("/v1/skills/")) return false;

  const beta = request.headers["openai-beta"] ?? null;
  const summary = { method: request.method, path, query: url.search, beta };
  calls.push(summary);
  record(summary);
  if (availability === "unsupported") {
    coreError(sendJson, response, 404, `No fixture route for ${request.method} ${path}`);
    return true;
  }
  if (availability === "storage-unavailable") {
    coreError(sendJson, response, 503, "Skill storage is unavailable.", "skill_storage_unavailable");
    return true;
  }
  if (beta !== null) {
    coreError(sendJson, response, 400, "Skills do not accept the Agents beta header in this fixture.");
    return true;
  }

  const segments = path.split("/").slice(3).map(decodeURIComponent);
  const [skillId, child, versionNumber, content] = segments;
  const skill = skillId === undefined ? undefined : skills.find((candidate) => candidate.id === skillId);
  // Each responder answers the request and reports it handled.
  const reply = (value) => { sendJson(response, value); return true; };
  const notFound = () => { coreError(sendJson, response, 404, "Resource not found."); return true; };
  const invalid = (code = "invalid_request", param = null, message = invalidMessage) => {
    coreError(sendJson, response, 400, message, code, param);
    return true;
  };

  const upload = async (versionUpload) => {
    const form = await readSkillMultipart(request);
    summary.multipart = form ? {
      fields: [...(form.zip ? ["files"] : []), ...form.files.map(() => "files[]"), ...form.defaults.map(() => "default")],
      filenames: form.zip ? [form.zip.filename] : form.files.map((file) => file.path),
      defaults: form.defaults,
    } : "invalid";
    if (!form || (form.zip && form.files.length) || form.defaults.length > (versionUpload ? 1 : 0)) return null;
    if (form.defaults.some((value) => value !== "true" && value !== "false")) return null;
    const files = form.zip ? readZip(form.zip.data) : form.files;
    const metadata = inspectBundle(files);
    if (!metadata) return null;
    return { metadata, archive: form.zip ? form.zip.data : writeZip(form.files), makeDefault: form.defaults[0] === "true" };
  };

  if (path === "/v1/skills") {
    if (request.method === "GET") {
      const page = listPage(skills.map(publicSkill), url, "skill_");
      if (page === null) return invalid();
      if (page === undefined) return notFound();
      return reply(page);
    }
    if (request.method === "POST") {
      const bundle = await upload(false);
      if (!bundle) return invalid();
      const id = nextId("skill");
      const createdAt = baseline + sequence;
      skills.push({
        id,
        createdAt,
        defaultVersion: 1,
        nextVersion: 2,
        versions: [{ id: nextId("skillver"), version: 1, createdAt, archive: bundle.archive, ...bundle.metadata }],
      });
      return reply(publicSkill(skills.at(-1)));
    }
  }
  if (!skill) return notFound();

  if (child === undefined) {
    if (request.method === "GET") return reply(publicSkill(skill));
    if (request.method === "DELETE") {
      skills = skills.filter((candidate) => candidate !== skill);
      return reply({ id: skill.id, object: "skill.deleted", deleted: true });
    }
    if (request.method === "POST") {
      let body;
      try { body = JSON.parse((await readBody(request)).toString("utf8")); } catch { return invalid(); }
      summary.body = body;
      const target = skill.versions.find((version) => String(version.version) === body?.default_version);
      if (!target) return notFound();
      if (Object.keys(body).length !== 1) return invalid();
      skill.defaultVersion = target.version;
      return reply(publicSkill(skill));
    }
  }
  if (child === "content" && versionNumber === undefined && request.method === "GET") {
    sendContent(response, skill.versions.find((version) => version.version === skill.defaultVersion));
    return true;
  }
  if (child !== "versions") return notFound();
  if (versionNumber === undefined) {
    if (request.method === "GET") {
      const page = listPage(skill.versions.map((version) => publicVersion(skill, version)), url, "skillver_");
      if (page === null) return invalid("invalid_value", "after");
      if (page === undefined) return notFound();
      return reply(page);
    }
    if (request.method === "POST") {
      const bundle = await upload(true);
      if (!bundle) return invalid();
      const version = { id: nextId("skillver"), version: skill.nextVersion, createdAt: baseline + sequence, archive: bundle.archive, ...bundle.metadata };
      skill.nextVersion += 1;
      skill.versions.push(version);
      if (bundle.makeDefault) skill.defaultVersion = version.version;
      return reply(publicVersion(skill, version));
    }
  }
  const version = skill.versions.find((candidate) => String(candidate.version) === versionNumber);
  if (!version) return notFound();
  if (content === "content" && request.method === "GET") {
    sendContent(response, version);
    return true;
  }
  if (content !== undefined) return notFound();
  if (request.method === "GET") return reply(publicVersion(skill, version));
  if (request.method === "DELETE") {
    if (version.version === skill.defaultVersion && skill.versions.length > 1) {
      return invalid("invalid_value", "version", "Cannot delete the default skill version.");
    }
    skill.versions = skill.versions.filter((candidate) => candidate !== version);
    // Deleting the sole version deletes the Skill; numbers are never reused.
    if (!skill.versions.length) skills = skills.filter((candidate) => candidate !== skill);
    return reply({ id: version.id, object: "skill.version.deleted", version: String(version.version), deleted: true });
  }
  return notFound();
}
