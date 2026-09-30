import type { CoreHarness, SandboxDeployment } from "@oac/agents-client";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { FleetState } from "../fleet/use-sandbox-fleet";
import { checklistStorageKey, checklistView, gettingStartedSteps, nextStepAfterNode, rememberInstallation } from "./getting-started";
import { node, project } from "./test-fixtures";

const deployment = (overrides: Partial<SandboxDeployment> = {}): SandboxDeployment => ({
  rollout: { state: "settled", previous_generation_sandboxes: 0, nodes: { ready: 1, preparing: 0, failed: 0, update_required: 0, unknown: 0 } }, installation_id: "i", provider: "docker", core_url: "http://core", reset: null, owner_epoch: 1, generation: 1, mode: "nodes",
  resources: { allocations: 0, pending: 0 }, suspension: null, ...overrides,
});
const fleet = (value: SandboxDeployment, nodes = [node("n1")]): FleetState => ({ status: "ready", snapshot: { deployment: value, nodes, allocations: [], loadedAt: 0 }, targetGeneration: value.generation, refreshing: false, error: null });
const sandboxes = (state: FleetState) => gettingStartedSteps({ sandboxReset: false, fleet: state, localOnly: false, projects: [], sessions: 0, harnesses: [] }).sandboxes;
const provider = { object: "core.model_configuration", model: "fixture-model", harness_config: {}, model_provider: { protocol: "responses", base_url: "https://model.example/v1", api_key_configured: true }, last_used_at: null, last_error_code: null, last_error_at: null, updated_at: "2026-09-25T00:00:00Z" } as const;
const harness = (id: CoreHarness["id"], fields: Partial<CoreHarness> = {}): CoreHarness => ({ object: "core.harness", model_configuration_support: { protocols: ["responses"], accepts_harness_config: true, token_limits_required: false }, id, enabled: true, default: false, model_configuration: null, ...fields });

describe("Getting started steps", () => {
  it("uses live provider readiness independently of target state or a durable pin", () => {
    for (const state of ["preparing", "failed", "update_required", "unknown"] as const) {
      const serving = node("n1", { rollout: { state, ready_generation: 1 } });
      expect(sandboxes(fleet(deployment({ generation: 2 }), [serving])).state).toBe("done");
      expect(sandboxes(fleet(deployment({ generation: 2 }), [{ ...serving, provider_ready: false }])).state).toBe("todo");
    }
    expect(sandboxes(fleet(deployment(), [node("n1", { online: false, rollout: { state: "unknown", ready_generation: 1 } })])).state).toBe("todo");
    expect(sandboxes(fleet(deployment(), [node("n1", { rollout: { state: "unknown", ready_generation: 1 } })])).state).toBe("done");
    expect(sandboxes(fleet(deployment(), [node("n1", { provider_ready: false, rollout: { state: "preparing", ready_generation: null } })])).state).toBe("todo");
  });

  it("does not report ready during reset or an unreadable deployment, even with ready nodes", () => {
    for (const backend of ["docker", "e2b"] as const) {
      const input = { fleet: fleet(deployment({ provider: backend })), localOnly: false, projects: [project("p")], sessions: 1,
        harnesses: [harness("codex", { default: true, model_configuration: { ...provider, harness: "codex" } })] };
      for (const sandboxReset of [true, "failed", undefined] as const) {
        const steps = gettingStartedSteps({ ...input, sandboxReset });
        expect(steps.sandboxes).toMatchObject({ state: sandboxReset === true ? "todo" : sandboxReset === "failed" ? "unknown" : null, action: "nodes" });
        expect(checklistView([steps.sandboxes.state, steps.model, steps.key.state, steps.session.state], "open")).not.toBe("complete");
      }
      expect(gettingStartedSteps({ ...input, sandboxReset: false }).sandboxes.state).toBe("done");
    }
  });

  it("keeps stale fleet readiness unknown after a failed refresh", () => {
    const stale = { ...fleet(deployment()), error: new Error("node read failed") } as FleetState;
    expect(sandboxes(stale).state).toBe("unknown");
  });

  it("cannot complete onboarding while the installation read is pending or failed", () => {
    for (const localOnly of [undefined, "failed"] as const) {
      const steps = gettingStartedSteps({ sandboxReset: false, localOnly, fleet: fleet(deployment()), projects: [project("p")], sessions: 1,
        harnesses: [harness("codex", { default: true, model_configuration: { ...provider, harness: "codex" } })] });
      expect(steps.sandboxes.state).toBe(localOnly === "failed" ? "unknown" : null);
      expect(checklistView([steps.sandboxes.state, steps.model, steps.key.state, steps.session.state], "open")).toBe("full");
    }
  });
  it("keeps local-only installations to do even with a ready node or cloud deployment", () => {
    for (const provider of ["docker", "e2b"] as const) {
      const steps = gettingStartedSteps({ sandboxReset: false, fleet: fleet(deployment({ provider })), projects: [], sessions: 1, harnesses: [], localOnly: true });
      expect(steps.sandboxes).toMatchObject({ state: "todo", action: "nodes", cloud: provider === "e2b" });
    }
  });
  it("counts sandboxes ready with a saved deployment and a ready node, or a saved E2B deployment whose build is not reported unready", () => {
    expect(sandboxes(fleet(deployment({ provider: "", mode: "" }), []))).toMatchObject({ state: "todo", action: "setup" });
    expect(sandboxes(fleet(deployment(), []))).toMatchObject({ state: "todo", action: "add-node" });
    expect(sandboxes(fleet(deployment(), [node("n1", { provider_ready: false }), node("n2", { online: false })]))).toMatchObject({ state: "todo", action: "nodes" });
    expect(sandboxes(fleet(deployment()))).toMatchObject({ state: "done" });
    const e2b = (status: string | null) => deployment({ provider: "e2b", mode: "direct", e2b: { template: "t", api_url: "https://api.e2b.app", domain: "e2b.app", credential_configured: true, template_build: { status, resources: { cpus: 2, memory_mib: 2048, root_disk_mib: null } } } });
    expect(sandboxes(fleet(e2b("building"), []))).toMatchObject({ state: "todo", cloud: true });
    expect(sandboxes(fleet(e2b("ready"), []))).toMatchObject({ state: "done", cloud: true });
    // Saved before Core recorded the build: Core admitted it, so it counts as ready.
    expect(sandboxes(fleet(e2b(null), []))).toMatchObject({ state: "done", cloud: true });
    expect(sandboxes({ status: "loading" }).state).toBeNull();
    expect(sandboxes({ status: "failed", error: new Error("down") }).state).toBe("unknown");
  });

  it("needs an active project with an active key, and any Session", () => {
    const steps = (projects: Parameters<typeof gettingStartedSteps>[0]["projects"], sessions: number | "failed" | null = 0) => gettingStartedSteps({ sandboxReset: false, fleet: { status: "loading" }, projects, sessions, harnesses: undefined });
    expect(steps([]).key).toEqual({ state: "todo", project: null });
    const older = project("p1", { active_key_count: 0, created_at: 1 });
    const newer = project("p2", { active_key_count: 0, created_at: 2 });
    expect(steps([older, newer, project("p3", { status: "archived", active_key_count: 0, created_at: 3 })]).key).toEqual({ state: "todo", project: newer });
    expect(steps([older, project("p4")]).key.state).toBe("done");
    expect(steps(undefined).key.state).toBeNull();
    expect(steps("failed").key.state).toBe("unknown");
    expect([steps([], 0).session.state, steps([], 2).session.state, steps([], null).session.state, steps([], "failed").session.state]).toEqual(["todo", "done", null, "unknown"]);
  });

  it("opens the call samples of the newest active project with a key, else of the newest active project", () => {
    const call = (projects: Parameters<typeof gettingStartedSteps>[0]["projects"]) => gettingStartedSteps({ sandboxReset: false, fleet: { status: "loading" }, projects, sessions: 0, harnesses: undefined }).session.project;
    const keyed = project("p1", { created_at: 1 });
    const newer = project("p2", { active_key_count: 0, created_at: 2 });
    const archived = project("p3", { status: "archived", active_key_count: 0, created_at: 3 });
    expect(call([keyed, newer, archived])).toBe(keyed);
    expect(call([newer, archived])).toBe(newer);
    expect([call([archived]), call(undefined), call("failed")]).toEqual([null, null, null]);
  });

  it("needs a default model on the default harness, or on any enabled harness when none is default", () => {
    const model = (harnesses: Parameters<typeof gettingStartedSteps>[0]["harnesses"]) => gettingStartedSteps({ sandboxReset: false, fleet: { status: "loading" }, projects: undefined, sessions: null, harnesses }).model;
    const on = (id: CoreHarness["id"]) => ({ ...provider, harness: id });
    expect(model([harness("codex", { default: true }), harness("claude_sdk", { model_configuration: on("claude_sdk") })])).toBe("todo");
    expect(model([harness("codex", { default: true, model_configuration: on("codex") })])).toBe("done");
    expect(model([harness("codex"), harness("mcode", { enabled: false, model_configuration: on("mcode") })])).toBe("todo");
    expect(model([harness("codex"), harness("claude_sdk", { model_configuration: on("claude_sdk") })])).toBe("done");
    expect(model(undefined)).toBeNull();
    expect(model("failed")).toBe("unknown");
  });
});

describe("Getting started visibility", () => {
  it("shows while a step is to do, ends with You're set only where it was seen, and stays closed once hidden", () => {
    expect(checklistView(["done", "todo", null], null)).toBe("full");
    expect(checklistView([null, null, null], null)).toBe("hidden");
    // An open checklist waits for a step's state instead of showing every step as checking.
    expect(checklistView([null, null, null], "open")).toBe("hidden");
    expect(checklistView(["done", null, null], "open")).toBe("full");
    expect(checklistView(["done", "done", "done"], null)).toBe("hidden");
    expect(checklistView(["done", "done", "done"], "open")).toBe("complete");
    expect(checklistView(["todo", "todo", "todo"], "closed")).toBe("hidden");
    // A step added later does not reopen a checklist that was closed, by hand or after You're set.
    expect(checklistView(["done", "todo", "done", "done"], "closed")).toBe("hidden");
  });

  afterEach(() => vi.unstubAllGlobals());

  it("remembers the choice per installation, and keeps the last one while the deployment cannot be read", () => {
    const stored = new Map<string, string>();
    vi.stubGlobal("window", { localStorage: { getItem: (key: string) => stored.get(key) ?? null, setItem: (key: string, value: string) => stored.set(key, value) } });
    const down: FleetState = { status: "failed", error: new Error("down") };
    expect(checklistStorageKey(fleet(deployment({ installation_id: "inst-1" })))).toBe("agents-core-web.getting-started.inst-1");
    expect(checklistStorageKey(down)).toBe("agents-core-web.getting-started");
    rememberInstallation("inst-1");
    expect(checklistStorageKey(down)).toBe("agents-core-web.getting-started.inst-1");
    expect(checklistStorageKey({ status: "loading" })).toBeNull();
  });
});

describe("the next step after a node is ready", () => {
  it("is the default model while it is to do, else the checklist, and only while the checklist is open", () => {
    expect(nextStepAfterNode(true, "todo")).toBe("default-model");
    expect(nextStepAfterNode(true, "done")).toBe("getting-started");
    expect(nextStepAfterNode(true, null)).toBeNull();
    expect(nextStepAfterNode(false, "todo")).toBeNull();
  });
});
