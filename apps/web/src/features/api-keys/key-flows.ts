import { AgentCoreError } from "@agents-core-web/agents-client";
import { type AdminIssuedKey, type AdminKey, type Project } from "../../lib/admin-view";

/**
 * Pure state for projects and their named API keys. A project owns the assets
 * shared by all of its keys. Issuing a key ends in a plaintext that is shown
 * once, kept only in memory, and dropped as soon as the operator confirms it
 * was saved. Nothing here touches browser storage.
 */

export const PROJECT_NAME_MAX = 128;
export const KEY_NAME_MAX = 80;
export const DEFAULT_PROJECT_NAME = "Default";

export type NameProblem = "tooLong" | "invalid" | "taken";

/** Names are trimmed before use; Core rejects surrounding spaces. */
export function normalizeName(value: string): string {
  return value.trim();
}

function nameProblem(value: string, max: number, taken: Iterable<string>): NameProblem | null {
  const name = normalizeName(value);
  if (!name) return null;
  if ([...name].length > max) return "tooLong";
  if (/[\u0000-\u001f\u007f]/.test(name)) return "invalid";
  for (const other of taken) if (normalizeName(other) === name) return "taken";
  return null;
}

/** Why a project name cannot be used; null when it can (or when it is still empty). */
export function projectNameProblem(value: string, taken: Iterable<string> = []): NameProblem | null {
  return nameProblem(value, PROJECT_NAME_MAX, taken);
}

/** Why a key name cannot be used; `taken` are the names of the project's active keys. */
export function keyNameProblem(value: string, taken: Iterable<string> = []): NameProblem | null {
  return nameProblem(value, KEY_NAME_MAX, taken);
}

export function isUsableName(value: string, problem: NameProblem | null): boolean {
  return normalizeName(value).length > 0 && problem === null;
}

/**
 * A write either failed before Core acted ("rejected": Core's reason, such as
 * an archived project on a 409; correct and retry) or has an unknown outcome
 * ("uncertain": check the list first; never retried automatically).
 */
export type FlowError =
  | { kind: "rejected"; status: number; message: string }
  | { kind: "uncertain" };

export function flowError(error: unknown): FlowError {
  if (error instanceof AgentCoreError) {
    if (error.status >= 400 && error.status < 500 && error.status !== 408) return { kind: "rejected", status: error.status, message: error.message };
    return { kind: "uncertain" };
  }
  // The client validates names before sending; nothing reached Core.
  if (error instanceof TypeError && /name has/i.test(error.message)) return { kind: "rejected", status: 400, message: error.message };
  return { kind: "uncertain" };
}

export function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

export type KeyFlow =
  | { step: "idle" }
  /**
   * Issue a named key. With `project: null` the project is created first
   * (first run); once created it is kept, so a retry only issues the key.
   */
  | { step: "issue"; project: Project | null; projectName: string; name: string; busy: boolean; error: FlowError | null }
  /** The plaintext is on screen until the operator confirms it was saved. */
  | { step: "issued"; project: Project; issued: AdminIssuedKey; open: boolean };

export type KeyFlowEvent =
  | { type: "openIssue"; project: Project }
  | { type: "openFirstRun"; projectName?: string }
  | { type: "setProjectName"; name: string }
  | { type: "setName"; name: string }
  | { type: "started" }
  | { type: "projectCreated"; project: Project }
  | { type: "failed"; error: FlowError }
  | { type: "issued"; projectId: string; key: AdminIssuedKey }
  | { type: "hideIssued" }
  | { type: "saved" }
  | { type: "cancel" };

export const idleFlow: KeyFlow = { step: "idle" };

export function keyFlowReducer(flow: KeyFlow, event: KeyFlowEvent): KeyFlow {
  switch (event.type) {
    case "openIssue":
      // Never start a second issuance while a plaintext key is waiting to be saved.
      if (flow.step !== "idle" || event.project.status !== "active") return flow;
      return { step: "issue", project: event.project, projectName: event.project.name, name: "", busy: false, error: null };
    case "openFirstRun":
      return flow.step === "idle" ? { step: "issue", project: null, projectName: event.projectName ?? DEFAULT_PROJECT_NAME, name: "", busy: false, error: null } : flow;
    case "setProjectName":
      return flow.step === "issue" && !flow.busy && flow.project === null ? { ...flow, projectName: event.name, error: null } : flow;
    case "setName":
      return flow.step === "issue" && !flow.busy ? { ...flow, name: event.name, error: null } : flow;
    case "started":
      return flow.step === "issue" && !flow.busy ? { ...flow, busy: true, error: null } : flow;
    case "projectCreated":
      return flow.step === "issue" && flow.busy && flow.project === null ? { ...flow, project: event.project, projectName: event.project.name } : flow;
    case "failed":
      return flow.step === "issue" && flow.busy ? { ...flow, busy: false, error: event.error } : flow;
    case "issued":
      if (flow.step !== "issue" || !flow.busy || flow.project?.id !== event.projectId) return flow;
      return { step: "issued", project: flow.project, issued: event.key, open: true };
    case "hideIssued":
      // Closing the dialog keeps the key on the page; only "saved" discards it.
      return flow.step === "issued" ? { ...flow, open: false } : flow;
    case "saved":
      return flow.step === "issued" ? idleFlow : flow;
    case "cancel":
      // A plaintext key cannot be cancelled away, and a request in flight finishes first.
      if (flow.step === "issued" || (flow.step === "issue" && flow.busy)) return flow;
      return idleFlow;
  }
}

/** The plaintext currently held by a flow, if any. */
export function pendingPlaintext(flow: KeyFlow): AdminIssuedKey | null {
  return flow.step === "issued" ? flow.issued : null;
}

/** Active keys first, then newest first. */
export function sortKeys(keys: readonly AdminKey[]): AdminKey[] {
  return [...keys].sort((a, b) => Number(a.revoked_at !== null) - Number(b.revoked_at !== null) || b.created_at - a.created_at || a.id.localeCompare(b.id));
}

export function activeKeyNames(keys: readonly AdminKey[] | null | undefined): string[] {
  return (keys ?? []).filter((key) => key.revoked_at === null).map((key) => key.name);
}

/** Display form of a key prefix: never the secret, always marked as truncated. */
export function prefixLabel(prefix: string | null | undefined): string {
  return prefix ? `${prefix}…` : "—";
}

export function matchesProject(project: Project, query: string): boolean {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return true;
  return [project.name, project.id].some((value) => value.toLocaleLowerCase().includes(needle));
}

/**
 * A short request the caller runs themselves to check the key. The console
 * never sends it, and the key itself stays out of the command.
 */
export function curlExample(coreOrigin: string | null): string {
  const trimmed = coreOrigin?.replace(/\/+$/, "") ?? "";
  const url = /^https?:\/\/[^\s/?#"'\\$`]+$/.test(trimmed) ? `${trimmed}/v1/agents` : '"$CORE_URL/v1/agents"';
  return [
    `curl ${url} \\`,
    '  -H "Authorization: Bearer $CORE_API_KEY" \\',
    '  -H "OpenAI-Beta: agents=v1"',
  ].join("\n");
}
