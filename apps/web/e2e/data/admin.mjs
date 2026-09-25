// Synthetic management-plane data (/core/v1/admin/**) for the browser acceptance fixture.
// Projects own isolated assets shared by their named keys; the base demo's
// resources are split across projects so every page can be filtered.
import { agentProject } from "./agents.mjs";
const iso = (seconds) => new Date(seconds * 1000).toISOString().replace(/\.\d{3}Z$/, "Z");

export function buildAdmin(now, base, resources) {
  const key = (id, name, prefix, createdDaysAgo, revokedDaysAgo = null) => ({ id, name, prefix, created_at: now - 86400 * createdDaysAgo, revoked_at: revokedDaysAgo === null ? null : now - 86400 * revokedDaysAgo });
  const projects = [
    { id: "proj_7f3a91c2", name: "Production", created_at: now - 86400 * 21, archived_at: null, keys: [
      key("fb533e99-524f-4e44-94bc-8f6e571646a7", "web-backend", "pc_live_7Hq", 21),
      key("3c1d9e20-7a41-4b8e-9f02-5d6e7f8a9b10", "ci-pipeline", "pc_live_Qm4", 16),
      key("5a2b3c4d-6e7f-4a1b-8c2d-3e4f5a6b7c8d", "alice", "pc_live_Al1", 12),
      key("6e2f1a30-8b52-4c9f-a013-6e7f8a9b0c21", "old-ci", "pc_live_Bv8", 30, 16),
    ] },
    { id: "proj_a47e2b19", name: "Data team", created_at: now - 86400 * 9, archived_at: null, keys: [
      key("a47e2b19-0c3d-4e5f-8a6b-7c8d9e0f1a2b", "notebook", "pc_live_k9T", 9),
      key("b58f3c2a-1d4e-4f60-9b7c-8d9e0f1a2b3c", "bob", "pc_live_Bo2", 5),
    ] },
    { id: "proj_2c8d4e10", name: "Operations", created_at: now - 86400 * 40, archived_at: null, keys: [
      key("c69a4d3b-2e5f-4071-8c8d-9e0f1a2b3c4d", "on-call", "pc_live_Oc3", 20),
    ] },
    { id: "proj_0b533e99", name: "Legacy", created_at: now - 86400 * 30, archived_at: now - 86400 * 6, keys: [
      key("0b533e99-524f-4e44-94bc-8f6e571646a7", "staging", "pc_live_2Xa", 30, 6),
    ] },
  ];
  const active = projects.slice(0, 3);
  const owner = new Map();
  const assign = (items, type, pick) => items.forEach((item, index) => owner.set(`${type}:${item.id}`, pick(item, index)));
  assign(base.agents, "agent", (_, index) => projects[agentProject[index] ?? index % 3].id);
  assign(base.sessions, "session", (session) => owner.get(`agent:${session.agent.id}`));
  assign(resources.skills, "skill", (_, index) => active[index % 3].id);
  assign(resources.files, "file", (_, index) => (index === 8 ? projects[3].id : active[index % 3].id));
  assign(resources.templates, "environment_template", (_, index) => active[index % 3].id);
  assign(resources.vaults, "vault", (_, index) => (index === 3 ? projects[3].id : active[index % 3].id));
  const of = (type, projectId) => (item) => owner.get(`${type}:${item.id}`) === projectId;

  const collections = (projectId) => ({
    agents: base.agents.filter(of("agent", projectId)),
    sessions: base.sessions.filter(of("session", projectId)),
    skills: resources.skills.filter(of("skill", projectId)),
    files: resources.files.filter(of("file", projectId)),
    templates: resources.templates.filter(of("environment_template", projectId)),
    vaults: resources.vaults.filter(of("vault", projectId)),
  });

  const keyRef = (project, k) => ({ id: k.id, name: k.name, prefix: k.prefix, kind: "issued", revoked_at: k.revoked_at ? iso(k.revoked_at) : null });
  // Deterministic creator per resource: rotate through the project's keys; one in seven is unknown.
  const creators = new Map();
  let turn = 0;
  const creatorFor = (project, type, id) => {
    const cacheKey = `${type}:${id}`;
    if (!creators.has(cacheKey)) {
      turn += 1;
      creators.set(cacheKey, turn % 11 === 0 ? { copy: true } : turn % 7 === 0 ? null : keyRef(project, project.keys[turn % project.keys.length]));
    }
    return creators.get(cacheKey);
  };

  const operations = new Map(projects.map((project) => {
    const own = collections(project.id);
    const list = [];
    const push = (at, action, type, id, parent = "", creatorType = type) => {
      const creator = action === "create" ? creatorFor(project, creatorType, id) : keyRef(project, project.keys[list.length % project.keys.length]);
      if (creator?.copy) return; // Administrator copies are in the audit log, not in key write history.
      list.push({ id: `op_${project.id.slice(5)}_${list.length}`, created_at: iso(at), api_key: creator, action, resource_type: type, resource_id: id, parent_id: parent, request_id: `req_${list.length}`, trace_id: `${list.length}`.padStart(32, "0") });
    };
    for (const agent of own.agents) { push(agent.created_at, "create", "agent", agent.id); if (agent.updated_at > agent.created_at) push(agent.updated_at, "update", "agent", agent.id); }
    for (const session of own.sessions) { push(session.created_at, "create", "session", session.id); if (session.last_active_at > session.created_at + 60) push(session.last_active_at, "send_events", "session", session.id); }
    for (const skill of own.skills) push(skill.created_at, "create", "skill", skill.id);
    for (const file of own.files) push(file.created_at, "create", "file", file.id);
    for (const template of own.templates) push(template.created_at, "create", "environment_template", template.id);
    for (const vault of own.vaults) {
      push(vault.created_at, "create", "vault", vault.id);
      for (const credential of resources.credentials.get(vault.id) ?? []) push(credential.created_at, "create", "credential", credential.id, vault.id);
    }
    list.sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
    return [project.id, list];
  }));

  function resourceOwners(project, url) {
    const type = url.searchParams.get("resource_type");
    const ids = (url.searchParams.get("resource_ids") ?? "").split(",").filter(Boolean).slice(0, 100);
    return { data: ids.map((id) => {
      const creator = creatorFor(project, type, id);
      if (creator?.copy) return { resource_id: id, api_key: null, source: "admin_copy", admin_audit_id: `audit_${id.slice(-8)}` };
      return creator ? { resource_id: id, api_key: creator, source: "api_key", admin_audit_id: null } : { resource_id: id, api_key: null, source: null, admin_audit_id: null };
    }) };
  }

  function summarize(sessions) {
    const counts = { total: sessions.length, idle: 0, in_progress: 0, requires_action: 0, failed: 0 };
    let reported = 0; let last = null;
    const usage = { input_tokens: 0, output_tokens: 0, total_tokens: 0, input_tokens_details: { cached_tokens: 0 }, output_tokens_details: { reasoning_tokens: 0 } };
    for (const session of sessions) {
      if (session.status in counts) counts[session.status] += 1;
      last = Math.max(last ?? 0, session.last_active_at);
      if (!session.usage) continue;
      reported += 1;
      usage.input_tokens += session.usage.input_tokens;
      usage.output_tokens += session.usage.output_tokens;
      usage.total_tokens += session.usage.total_tokens;
      usage.input_tokens_details.cached_tokens += session.usage.input_tokens_details?.cached_tokens ?? 0;
      usage.output_tokens_details.reasoning_tokens += session.usage.output_tokens_details?.reasoning_tokens ?? 0;
    }
    return { sessions: counts, usage, coverage: { measured_sessions: reported, total_sessions: sessions.length, ratio: sessions.length ? reported / sessions.length : null }, last_active_at: last };
  }

  function summary(url) {
    const after = url.searchParams.get("created_after"); const before = url.searchParams.get("created_before");
    const lower = after ? Date.parse(after) / 1000 : -Infinity; const upper = before ? Date.parse(before) / 1000 : Infinity;
    const only = url.searchParams.get("project_id");
    const groupBy = url.searchParams.get("group_by") ?? "project";
    const rows = [];
    for (const project of projects) {
      if (only && project.id !== only) continue;
      const own = collections(project.id);
      const sessions = own.sessions.filter((session) => session.created_at >= lower && session.created_at < upper);
      if (groupBy === "agent") {
        for (const agent of own.agents) rows.push({ project_id: project.id, key_id: null, agent_id: agent.id, assets: null, ...summarize(sessions.filter((session) => session.agent.id === agent.id)) });
      } else if (groupBy === "key") {
        const groups = new Map();
        for (const session of sessions) {
          const creator = creatorFor(project, "session", session.id);
          const id = creator?.id ?? "";
          if (!groups.has(id)) groups.set(id, { key: creator?.id ?? null, sessions: [] });
          groups.get(id).sessions.push(session);
        }
        for (const group of groups.values()) rows.push({ project_id: project.id, key_id: group.key, agent_id: null, assets: null, ...summarize(group.sessions) });
      } else {
        const credentials = own.vaults.reduce((sum, vault) => sum + (resources.credentials.get(vault.id)?.length ?? 0), 0);
        rows.push({ project_id: project.id, key_id: null, agent_id: null, assets: { agents: own.agents.length, skills: own.skills.length, environment_templates: own.templates.length, files: own.files.length, vaults: own.vaults.length, credentials }, ...summarize(sessions) });
      }
    }
    return { data: rows, has_more: false, next_cursor: "" };
  }

  const publicProject = (project) => ({
    id: project.id, name: project.name, created_at: iso(project.created_at), archived_at: project.archived_at ? iso(project.archived_at) : null,
    active_key_count: project.keys.filter((k) => !k.revoked_at).length,
  });
  const publicKey = (project, k) => ({ id: k.id, project_id: project.id, name: k.name, prefix: k.prefix, created_at: iso(k.created_at), revoked_at: k.revoked_at ? iso(k.revoked_at) : null });
  const auditLog = () => {
    const entries = [];
    let n = 0;
    const add = (at, action, project, type, id, results = []) => entries.push({ id: `audit_${String(++n).padStart(4, "0")}`, created_at: iso(at), admin_credential_id: "a1b2c3d4", actor_label: "admin", action, project_id: project.id, resource_type: type, resource_id: id, result_ids: results, request_id: `req_admin_${n}`, trace_id: `${n}`.padStart(32, "a") });
    for (const project of projects) {
      add(project.created_at, "create_project", project, "project", project.id);
      for (const k of project.keys) { add(k.created_at, "issue_key", project, "api_key", k.id); if (k.revoked_at) add(k.revoked_at, "revoke_key", project, "api_key", k.id); }
      if (project.archived_at) add(project.archived_at, "archive_project", project, "project", project.id);
    }
    return entries.sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
  };

  return {
    projects, owner, collections, operations, summary, publicProject, publicKey, resourceOwners, auditLog,
    runtimeObservations: () => base.observations.map((observation) => ({ project_id: owner.get(`session:${observation.session_id}`), observation })),
  };
}
