import { AgentCoreError } from "@agents-core-web/agents-client";
import { describe, expect, it } from "vitest";
import { type AdminIssuedKey, type AdminKey, type Project } from "../../lib/admin-view";

import {
  activeKeyNames,
  curlExample,
  DEFAULT_PROJECT_NAME,
  flowError,
  idleFlow,
  isUsableName,
  keyFlowReducer,
  keyNameProblem,
  matchesProject,
  pendingPlaintext,
  projectNameProblem,
  sortKeys,
  type KeyFlow,
  type KeyFlowEvent,
} from "./key-flows";

const project: Project = { id: "proj_7f3a91c2", name: "Production", status: "active", created_at: 100, archived_at: null, active_key_count: 1 };
const archived: Project = { ...project, id: "proj_0b533e99", name: "Legacy", status: "archived", archived_at: 200, active_key_count: 0 };
const secret = "pc_live_" + "x".repeat(40);
const issued: AdminIssuedKey = { id: "9f0e1d2c-3b4a-4c5d-8e6f-7a8b9c0d1e2f", project_id: "proj_7f3a91c2", name: "bob-laptop", prefix: "pc_live_Zq8", created_at: 300, revoked_at: null, key: secret };

const run = (events: KeyFlowEvent[], from: KeyFlow = idleFlow) => events.reduce(keyFlowReducer, from);

describe("project and key names", () => {
  it("accepts trimmed names up to the limit, counting characters rather than bytes", () => {
    expect(projectNameProblem("  Production  ")).toBeNull();
    expect(projectNameProblem("数".repeat(128))).toBeNull();
    expect(projectNameProblem("数".repeat(129))).toBe("tooLong");
    expect(keyNameProblem("k".repeat(80))).toBeNull();
    expect(keyNameProblem("k".repeat(81))).toBe("tooLong");
  });

  it("leaves an empty name to the disabled submit instead of showing an error", () => {
    expect(projectNameProblem("")).toBeNull();
    expect(keyNameProblem("   ")).toBeNull();
    expect(isUsableName("   ", null)).toBe(false);
    expect(isUsableName("alice", null)).toBe(true);
  });

  it("rejects control characters and names already in use", () => {
    expect(projectNameProblem("line\nbreak")).toBe("invalid");
    expect(keyNameProblem("tab\there")).toBe("invalid");
    expect(projectNameProblem(" Production", ["Production", "Data team"])).toBe("taken");
    expect(keyNameProblem("alice", activeKeyNames([
      { id: "a", project_id: "proj_7f3a91c2", name: "alice", prefix: "pc_a", created_at: 1, revoked_at: 2 },
    ]))).toBeNull();
    expect(isUsableName("Production", projectNameProblem("Production", ["Production"]))).toBe(false);
  });
});

describe("write outcomes", () => {
  it("separates rejected and uncertain results, keeping Core's reason on a conflict", () => {
    expect(flowError(new AgentCoreError("Name is invalid.", 400))).toEqual({ kind: "rejected", status: 400, message: "Name is invalid." });
    expect(flowError(new AgentCoreError("The target Project is archived.", 409))).toEqual({ kind: "rejected", status: 409, message: "The target Project is archived." });
    // A timeout, a server error or a lost connection may have written anyway.
    expect(flowError(new AgentCoreError("Timeout.", 408))).toEqual({ kind: "uncertain" });
    expect(flowError(new AgentCoreError("Down.", 502))).toEqual({ kind: "uncertain" });
    expect(flowError(new TypeError("Failed to fetch"))).toEqual({ kind: "uncertain" });
    // The client's own name validation never reached Core.
    expect(flowError(new TypeError("A key name has 1–80 characters without leading or trailing spaces."))).toMatchObject({ kind: "rejected" });
  });
});

describe("issuing a key", () => {
  it("shows the plaintext only after Core issued it for the same project", () => {
    const opened = run([{ type: "openIssue", project }, { type: "setName", name: "bob-laptop" }]);
    expect(opened).toMatchObject({ step: "issue", project, name: "bob-laptop", busy: false });
    // A response that arrives when nothing is in flight, or for another project, is ignored.
    expect(keyFlowReducer(opened, { type: "issued", projectId: project.id, key: issued })).toBe(opened);
    const busy = keyFlowReducer(opened, { type: "started" });
    expect(keyFlowReducer(busy, { type: "issued", projectId: "proj_other", key: issued })).toBe(busy);
    expect(keyFlowReducer(busy, { type: "issued", projectId: project.id, key: issued })).toEqual({ step: "issued", project, issued, open: true });
  });

  it("keeps the plaintext until the operator confirms it was saved, then drops it", () => {
    const shown = run([{ type: "openIssue", project }, { type: "setName", name: "bob-laptop" }, { type: "started" }, { type: "issued", projectId: project.id, key: issued }]);
    expect(pendingPlaintext(shown)?.key).toBe(secret);
    // Cancelling or closing the dialog never discards a key shown only once.
    expect(keyFlowReducer(shown, { type: "cancel" })).toBe(shown);
    const hidden = keyFlowReducer(shown, { type: "hideIssued" });
    expect(hidden).toMatchObject({ step: "issued", open: false });
    expect(pendingPlaintext(hidden)?.key).toBe(secret);
    // A second issuance cannot start while this one waits.
    expect(keyFlowReducer(hidden, { type: "openIssue", project })).toBe(hidden);
    const saved = keyFlowReducer(hidden, { type: "saved" });
    expect(saved).toEqual(idleFlow);
    expect(JSON.stringify(saved)).not.toContain(secret);
  });

  it("does not cancel a request in flight and reports a failure for correction", () => {
    const busy = run([{ type: "openIssue", project }, { type: "setName", name: "bob" }, { type: "started" }]);
    expect(keyFlowReducer(busy, { type: "cancel" })).toBe(busy);
    expect(keyFlowReducer(busy, { type: "setName", name: "changed" })).toBe(busy);
    const failed = keyFlowReducer(busy, { type: "failed", error: { kind: "rejected", status: 409, message: "The target Project is archived." } });
    expect(failed).toMatchObject({ step: "issue", busy: false, error: { kind: "rejected", status: 409 }, name: "bob" });
    expect(keyFlowReducer(failed, { type: "setName", name: "bob-2" })).toMatchObject({ name: "bob-2", error: null });
    expect(keyFlowReducer(failed, { type: "cancel" })).toEqual(idleFlow);
  });

  it("does not issue keys for archived projects", () => {
    expect(keyFlowReducer(idleFlow, { type: "openIssue", project: archived })).toBe(idleFlow);
  });
});

describe("first run", () => {
  it("creates the project once and retries only the key after a failure", () => {
    const form = run([{ type: "openFirstRun" }, { type: "setName", name: "admin-laptop" }]);
    expect(form).toMatchObject({ step: "issue", project: null, projectName: DEFAULT_PROJECT_NAME });
    const created = run([{ type: "setProjectName", name: "Main" }, { type: "started" }, { type: "projectCreated", project: { ...project, name: "Main" } }], form);
    expect(created).toMatchObject({ busy: true, project: { name: "Main" } });
    const failed = keyFlowReducer(created, { type: "failed", error: { kind: "uncertain" } });
    // The created project is kept, so a retry cannot create a second one.
    expect(failed).toMatchObject({ step: "issue", busy: false, project: { id: project.id } });
    expect(keyFlowReducer(failed, { type: "setProjectName", name: "Other" })).toBe(failed);
    const retried = run([{ type: "started" }, { type: "issued", projectId: project.id, key: issued }], failed);
    expect(retried).toMatchObject({ step: "issued", project: { id: project.id } });
  });
});

describe("helpers", () => {
  it("lists active keys first, newest first", () => {
    const key = (id: string, created: number, revoked: number | null = null): AdminKey => ({ id, project_id: "proj_7f3a91c2", name: id, prefix: `pc_${id}`, created_at: created, revoked_at: revoked });
    expect(sortKeys([key("old", 1), key("gone", 5, 6), key("new", 3)]).map((entry) => entry.id)).toEqual(["new", "old", "gone"]);
  });

  it("matches projects by name or ID", () => {
    expect(matchesProject(project, "prod")).toBe(true);
    expect(matchesProject(project, "7F3A")).toBe(true);
    expect(matchesProject(project, "data")).toBe(false);
  });

  it("builds a request the caller runs with their own key variable", () => {
    const command = curlExample("https://core.example/");
    expect(command).toContain("curl https://core.example/v1/agents");
    expect(command).toContain('"Authorization: Bearer $PROJECT_API_KEY"');
    expect(command).not.toContain("pc_live");
    // Anything that is not a plain origin falls back to a variable instead of reaching the shell.
    expect(curlExample('https://core.example/$(id)')).toContain('"$CORE_URL/v1/agents"');
    expect(curlExample(null)).toContain('"$CORE_URL/v1/agents"');
  });
});
