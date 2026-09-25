import { type Project } from "../../lib/admin-view";
import { type FleetState } from "../fleet/use-sandbox-fleet";
import { templateBuildStatus } from "../sandbox/deployment-specification";

/**
 * Getting started on the Overview: three steps to a working deployment,
 * computed only from reads the Overview already makes. A step whose read is
 * pending is null, and "unknown" when that read failed; neither counts as done.
 */
export type StepState = "done" | "todo" | "unknown" | null;

/** Where the sandbox step leads: the setup wizard, Add node, or the Nodes page. */
export type SandboxAction = "setup" | "add-node" | "nodes";

export interface GettingStartedSteps {
  sandboxes: { state: StepState; action: SandboxAction; cloud: boolean };
  /** `project` is the active project a key would be issued for; null means create one first. */
  key: { state: StepState; project: Project | null };
  session: StepState;
}

export function gettingStartedSteps(input: {
  fleet: FleetState;
  /** Undefined until the project list is read. */
  projects: readonly Project[] | "failed" | undefined;
  /** Sessions in every project, by Core's summary; null until it is read. */
  sessions: number | "failed" | null;
}): GettingStartedSteps {
  const { sessions } = input;
  return {
    sandboxes: sandboxStep(input.fleet),
    key: keyStep(input.projects),
    session: sessions === null ? null : sessions === "failed" ? "unknown" : sessions > 0 ? "done" : "todo",
  };
}

/**
 * Own machines are ready once the deployment is saved and a node is online
 * with its provider ready; E2B once the deployment is saved, since Core admits
 * only a ready template build. Only a build Core reports as not ready leaves
 * the step to do; a selection saved before Core recorded its build has no
 * status and counts as done.
 */
function sandboxStep(fleet: FleetState): GettingStartedSteps["sandboxes"] {
  if (fleet.status !== "ready") {
    return { state: fleet.status === "failed" || fleet.status === "unconfigured" ? "unknown" : null, action: "nodes", cloud: false };
  }
  const { deployment, nodes } = fleet.snapshot;
  if (!deployment.provider) return { state: "todo", action: "setup", cloud: false };
  if (deployment.provider === "e2b") {
    return { state: templateBuildStatus(deployment.e2b?.template_build) === "notReady" ? "todo" : "done", action: "nodes", cloud: true };
  }
  if (nodes.some((node) => node.online && node.provider_ready)) return { state: "done", action: "nodes", cloud: false };
  return { state: "todo", action: nodes.length ? "nodes" : "add-node", cloud: false };
}

function keyStep(projects: readonly Project[] | "failed" | undefined): GettingStartedSteps["key"] {
  if (!projects || projects === "failed") return { state: projects ? "unknown" : null, project: null };
  const active = projects.filter((project) => project.status === "active");
  if (active.some((project) => project.active_key_count > 0)) return { state: "done", project: null };
  // The newest active project, most likely the one just created.
  const newest = active.reduce<Project | null>((best, project) => (!best || project.created_at > best.created_at ? project : best), null);
  return { state: "todo", project: newest };
}

/**
 * What this browser remembers: the checklist was shown with a step to do
 * ("open"), hidden while steps remained ("dismissed"), or closed for good
 * ("closed", after "You're set" or on a deployment that was already set up).
 */
export type ChecklistMemory = "open" | "dismissed" | "closed" | null;

export type ChecklistView = "hidden" | "full" | "compact" | "complete";

/**
 * The checklist shows while a step is to do. "You're set" follows only in a
 * browser that saw a step to do, so a deployment set up before this console
 * never shows it. A dismissed checklist stays one compact line until every
 * step is done.
 */
export function checklistView(states: readonly StepState[], memory: ChecklistMemory): ChecklistView {
  if (memory === "closed") return "hidden";
  if (states.every((state) => state === "done")) return memory === "open" ? "complete" : "hidden";
  const todo = states.includes("todo");
  if (memory === "dismissed") return todo ? "compact" : "hidden";
  return todo || memory === "open" ? "full" : "hidden";
}

const STORAGE_KEY = "agents-core-web.getting-started";

export function readChecklistMemory(): ChecklistMemory {
  try {
    const value = window.localStorage.getItem(STORAGE_KEY);
    return value === "open" || value === "dismissed" || value === "closed" ? value : null;
  } catch {
    return null;
  }
}

export function writeChecklistMemory(value: Exclude<ChecklistMemory, null>): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, value);
  } catch {
    // Storage can be unavailable; the choice then lasts until the page reloads.
  }
}
