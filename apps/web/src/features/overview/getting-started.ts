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
 * What this browser remembers for one installation: the checklist was shown
 * with a step to do ("open"), or it is closed ("closed": hidden, after
 * "You're set", or on a deployment that was already set up). Only Show
 * Getting started opens a closed checklist again.
 */
export type ChecklistMemory = "open" | "closed" | null;

export type ChecklistView = "hidden" | "full" | "complete";

/**
 * The checklist shows while a step is to do. "You're set" follows only in a
 * browser that saw a step to do, so a deployment set up before this console
 * never shows it. An open checklist waits for a step's state rather than
 * showing every step as checking.
 */
export function checklistView(states: readonly StepState[], memory: ChecklistMemory): ChecklistView {
  if (memory === "closed") return "hidden";
  if (states.every((state) => state === "done")) return memory === "open" ? "complete" : "hidden";
  if (states.includes("todo")) return "full";
  return memory === "open" && states.some((state) => state !== null) ? "full" : "hidden";
}

const MEMORY_KEY = "agents-core-web.getting-started";
/** The installation this browser last read, so the checklist keeps its entry while the deployment cannot be read. */
const INSTALLATION_KEY = "agents-core-web.last-installation";

/**
 * The storage entry for this installation, so a reinstall at the same origin
 * starts again. While the console cannot read the deployment it uses the
 * installation it last read (the unscoped entry if none); null while reading.
 */
export function checklistStorageKey(fleet: FleetState): string | null {
  const installation = fleet.status === "ready" ? fleet.snapshot.deployment.installation_id
    : fleet.status === "failed" || fleet.status === "unconfigured" ? readStored(INSTALLATION_KEY) ?? ""
    : null;
  if (installation === null) return null;
  return installation ? `${MEMORY_KEY}.${installation}` : MEMORY_KEY;
}

export function rememberInstallation(installationId: string): void {
  writeStored(INSTALLATION_KEY, installationId);
}

export function readChecklistMemory(key: string): ChecklistMemory {
  const value = readStored(key);
  return value === "open" || value === "closed" ? value : null;
}

export function writeChecklistMemory(key: string, value: Exclude<ChecklistMemory, null>): void {
  writeStored(key, value);
}

/**
 * Checklists whose "You're set" is on screen until dismissed, by storage entry.
 * It lives outside the Overview so it outlasts the tour, which replaces the page.
 */
const celebrating = new Set<string>();

export function isCelebrating(key: string): boolean {
  return celebrating.has(key);
}

export function setCelebrating(key: string, on: boolean): void {
  if (on) celebrating.add(key);
  else celebrating.delete(key);
}

function readStored(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeStored(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Storage can be unavailable; the choice then lasts until the page reloads.
  }
}
