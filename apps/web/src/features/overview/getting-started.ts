import type { CoreHarness } from "@agents-core-web/agents-client";

import { type Project } from "../../lib/admin-view";
import { type FleetState } from "../fleet/use-sandbox-fleet";
import { templateBuildStatus } from "../sandbox/deployment-specification";

/**
 * Getting started on the Overview: four steps to a working deployment,
 * computed from the Overview's reads and the harness list. A step whose read
 * is pending is null, and "unknown" when that read failed; neither counts as
 * done.
 */
export type StepState = "done" | "todo" | "unknown" | null;

/** Where the sandbox step leads: the setup wizard, Add node, or the Nodes page. */
export type SandboxAction = "setup" | "add-node" | "nodes";

export interface GettingStartedSteps {
  sandboxes: { state: StepState; action: SandboxAction; cloud: boolean };
  model: StepState;
  /** `project` is the active project a key would be issued for; null means create one first. */
  key: { state: StepState; project: Project | null };
  /** `project` is the active project whose call samples the step opens; null leads to the project list. */
  session: { state: StepState; project: Project | null };
}

export function gettingStartedSteps(input: {
  fleet: FleetState;
  /** Undefined until the project list is read. */
  projects: readonly Project[] | "failed" | undefined;
  /** Sessions in every project, by Core's summary; null until it is read. */
  sessions: number | "failed" | null;
  /** Core's harnesses; undefined until they are read. */
  harnesses: readonly CoreHarness[] | "failed" | undefined;
}): GettingStartedSteps {
  const { sessions } = input;
  return {
    sandboxes: sandboxStep(input.fleet),
    model: modelStep(input.harnesses),
    key: keyStep(input.projects),
    session: {
      state: sessions === null ? null : sessions === "failed" ? "unknown" : sessions > 0 ? "done" : "todo",
      project: callProject(input.projects),
    },
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

/**
 * Done once the default harness has a deployment default model provider;
 * without a default harness, once any enabled harness has one.
 */
export function modelStep(harnesses: readonly CoreHarness[] | "failed" | undefined): StepState {
  if (!harnesses || harnesses === "failed") return harnesses ? "unknown" : null;
  const target = harnesses.find((harness) => harness.default);
  const set = target ? target.model_provider !== null : harnesses.some((harness) => harness.enabled && harness.model_provider !== null);
  return set ? "done" : "todo";
}

/** The newest of the projects, most likely the one just created. */
function newestOf(projects: readonly Project[]): Project | null {
  return projects.reduce<Project | null>((best, project) => (!best || project.created_at > best.created_at ? project : best), null);
}

function keyStep(projects: readonly Project[] | "failed" | undefined): GettingStartedSteps["key"] {
  if (!projects || projects === "failed") return { state: projects ? "unknown" : null, project: null };
  const active = projects.filter((project) => project.status === "active");
  if (active.some((project) => project.active_key_count > 0)) return { state: "done", project: null };
  return { state: "todo", project: newestOf(active) };
}

/**
 * Where the first Session's call samples are: the newest active project with
 * an active key, else the newest active project (whose page issues one).
 */
function callProject(projects: readonly Project[] | "failed" | undefined): Project | null {
  if (!projects || projects === "failed") return null;
  const active = projects.filter((project) => project.status === "active");
  return newestOf(active.filter((project) => project.active_key_count > 0)) ?? newestOf(active);
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

/** Whether this browser still has the installation's checklist open: shown with a step to do, and neither hidden nor finished. */
export function checklistOpenFor(installationId: string): boolean {
  return readChecklistMemory(installationId ? `${MEMORY_KEY}.${installationId}` : MEMORY_KEY) === "open";
}

/**
 * Where Add node points once its node is ready, while the checklist is open:
 * the default model while that step is to do, otherwise back to the checklist.
 * Nothing while the checklist is closed or the model step is still being read.
 */
export function nextStepAfterNode(checklistOpen: boolean, model: StepState): "default-model" | "getting-started" | null {
  if (!checklistOpen || model === null) return null;
  return model === "todo" ? "default-model" : "getting-started";
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
