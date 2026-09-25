import type { SandboxDeployment } from "@agents-core-web/agents-client";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { FleetState } from "../fleet/use-sandbox-fleet";
import { checklistStorageKey, checklistView, gettingStartedSteps, rememberInstallation } from "./getting-started";
import { node, project } from "./test-fixtures";

const deployment = (overrides: Partial<SandboxDeployment> = {}): SandboxDeployment => ({
  installation_id: "i", provider: "docker", core_url: "http://core", maintenance: false, owner_epoch: 1, generation: 1, mode: "nodes",
  resources: { allocations: 0, pending: 0 }, suspension: null, ...overrides,
});
const fleet = (value: SandboxDeployment, nodes = [node("n1")]): FleetState => ({ status: "ready", snapshot: { deployment: value, nodes, allocations: [], loadedAt: 0 }, refreshing: false, error: null });
const sandboxes = (state: FleetState) => gettingStartedSteps({ fleet: state, projects: [], sessions: 0 }).sandboxes;

describe("Getting started steps", () => {
  it("counts sandboxes ready with a saved deployment and a ready node, or a saved E2B deployment whose build is not reported unready", () => {
    expect(sandboxes(fleet(deployment({ provider: "", mode: "" }), []))).toMatchObject({ state: "todo", action: "setup" });
    expect(sandboxes(fleet(deployment(), []))).toMatchObject({ state: "todo", action: "add-node" });
    expect(sandboxes(fleet(deployment(), [node("n1", { provider_ready: false }), node("n2", { online: false })]))).toMatchObject({ state: "todo", action: "nodes" });
    expect(sandboxes(fleet(deployment()))).toMatchObject({ state: "done" });
    const e2b = (status: string | null) => deployment({ provider: "e2b", mode: "direct", e2b: { template: "t", credential_configured: true, template_build: { status, resources: { cpus: 2, memory_mib: 2048, root_disk_mib: null } } } });
    expect(sandboxes(fleet(e2b("building"), []))).toMatchObject({ state: "todo", cloud: true });
    expect(sandboxes(fleet(e2b("ready"), []))).toMatchObject({ state: "done", cloud: true });
    // Saved before Core recorded the build: Core admitted it, so it counts as ready.
    expect(sandboxes(fleet(e2b(null), []))).toMatchObject({ state: "done", cloud: true });
    expect(sandboxes({ status: "loading" }).state).toBeNull();
    expect(sandboxes({ status: "failed", error: new Error("down") }).state).toBe("unknown");
  });

  it("needs an active project with an active key, and any Session", () => {
    const steps = (projects: Parameters<typeof gettingStartedSteps>[0]["projects"], sessions: number | "failed" | null = 0) => gettingStartedSteps({ fleet: { status: "loading" }, projects, sessions });
    expect(steps([]).key).toEqual({ state: "todo", project: null });
    const older = project("p1", { active_key_count: 0, created_at: 1 });
    const newer = project("p2", { active_key_count: 0, created_at: 2 });
    expect(steps([older, newer, project("p3", { status: "archived", active_key_count: 0, created_at: 3 })]).key).toEqual({ state: "todo", project: newer });
    expect(steps([older, project("p4")]).key.state).toBe("done");
    expect(steps(undefined).key.state).toBeNull();
    expect(steps("failed").key.state).toBe("unknown");
    expect([steps([], 0).session, steps([], 2).session, steps([], null).session, steps([], "failed").session]).toEqual(["todo", "done", null, "unknown"]);
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
