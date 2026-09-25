import type { SandboxNode } from "@agents-core-web/agents-client";

import type { MessageKey } from "../../lib/locale-strings";
import { nodeProviderDiagnostic } from "../../lib/sandbox-diagnostic";

/**
 * How long the installer waits, after starting the node service, for Core to
 * report the node connected and its provider ready
 * (deploy/install/node_install.py `wait_ready`, timeout=60).
 */
export const NODE_READY_WAIT_MS = 60_000;

export interface HostPrerequisite {
  label: MessageKey;
  /** A root command that prepares the host; NODE_USER stands for the node's user (`<user>` would be shell redirection). */
  command?: string;
}

/**
 * What a node host needs, exactly as the enrollment command, the installer and
 * the node's readiness probe check it (line numbers as of this revision):
 * - the command runs curl, sha256sum and python3 (enrollment-command.ts);
 *   deploy/install/node_install.py:67-69 needs Python 3.9+, Linux amd64 and a non-root user;
 * - Docker: node_install.py:73-74 runs `docker info` through /var/run/docker.sock,
 *   and the node is ready only when Docker enforces CPU and memory limits
 *   (services/agents-api/internal/sandbox/config/probe.go:37-38), which the
 *   installer waits for (node_install.py:363-364);
 * - microsandbox: read/write /dev/kvm (node_install.py:75-76);
 * - node_install.py:71-72 needs lingering. It starts the user's systemd manager,
 *   whose services, the node's included, keep the groups it started with; so the
 *   group changes above come first, or that manager is restarted after them. A
 *   shell open before the change lacks the group too, so the user signs in again;
 * - microsandbox: the host libraries its binaries link (the ldd check,
 *   node_install.py:249-253), and a home short enough for ~/.parsar/m/<12 hex> to
 *   fit in 48 bytes (node_install.py:205-209): at most 48 - len("/.parsar/m/") - 12 = 25 bytes;
 * - the host's CPUs and total memory hold one sandbox of the deployment's size
 *   (probe.go:43 and 75, config/capacity.go:9), else the node reports capacity_insufficient;
 * - node_install.py:70 needs the user's own systemd session (`systemctl --user`),
 *   which sudo -u and su don't provide;
 * - network: node files from the console (node_install.py:94-106), artifacts from
 *   the manifest's artifact_base_url, the console by default (node_install.py:139-140,
 *   distribution.py:150-154), Core's /api/v1 (node_spec.py:89, node_install.py:385),
 *   and sandboxes reach Core as well (node_install.py:213, 220-233).
 * `sized` says whether the deployment's sandbox size is known for the capacity item.
 */
export function hostPrerequisites(provider: "docker" | "microsandbox", sized: boolean): HostPrerequisite[] {
  const access: HostPrerequisite = provider === "docker"
    ? { label: "Docker at /var/run/docker.sock for that user, enforcing CPU and memory limits", command: "sudo usermod -aG docker NODE_USER" }
    : { label: "Read and write access to /dev/kvm for that user", command: "sudo usermod -aG kvm NODE_USER" };
  const microsandbox: HostPrerequisite[] = provider === "microsandbox" ? [
    { label: "The shared libraries microsandbox needs, on a glibc system" },
    { label: "A home directory of 25 bytes or less, such as /home/parsar" },
  ] : [];
  return [
    { label: "Linux amd64 with Python 3.9+, curl and sha256sum, and a non-root user to run the node (NODE_USER below)" },
    access,
    { label: "systemd lingering for that user, enabled after the group change", command: "sudo loginctl enable-linger NODE_USER" },
    ...microsandbox,
    { label: sized ? "CPUs and memory for at least one sandbox: {{size}}" : "CPUs and memory for at least one sandbox" },
    { label: "Run the command signed in as that user: over SSH, or with", command: "sudo machinectl shell NODE_USER@" },
    { label: "Can reach {{console}}, {{core}} and the release downloads; sandboxes must reach {{core}}" },
  ];
}

/** Restarts a user's systemd manager, so its services pick up a group change. */
export const USER_MANAGER_RESTART = "sudo systemctl restart user@$(id -u NODE_USER).service";

/** Time left as m:ss (h:mm:ss from an hour), rounded up so it reads 0:00 only once expired. */
export function formatCountdown(milliseconds: number): string {
  const total = Math.max(0, Math.ceil(milliseconds / 1000));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = String(total % 60).padStart(2, "0");
  return hours ? `${hours}:${String(minutes).padStart(2, "0")}:${seconds}` : `${minutes}:${seconds}`;
}

/** What identifies the node a command enrolls: nodes that existed before it, and the limits it approved. */
export interface EnrollmentTarget {
  known: ReadonlySet<string>;
  max_active: number;
  max_retained: number;
}

/**
 * The newest node registered since the command was issued with the command's
 * limits, which Core copies onto the node it enrolls
 * (services/agents-api/internal/store/runtime_nodes.go:166); a node from another
 * command with other limits is not this one. Only microsandbox keeps its own
 * retained limit; Core sets Docker's to the active one.
 */
export function enrolledNode(nodes: readonly SandboxNode[], target: EnrollmentTarget, suspends: boolean): SandboxNode | null {
  let newest: SandboxNode | null = null;
  for (const node of nodes) {
    if (target.known.has(node.id) || node.max_active !== target.max_active || (suspends && node.max_retained !== target.max_retained)) continue;
    if (!newest || Date.parse(node.created_at) > Date.parse(newest.created_at)) newest = node;
  }
  return newest;
}

export type EnrollmentStage = "waiting" | "registered" | "connected" | "ready";

export interface EnrollmentProgress {
  stage: EnrollmentStage;
  /**
   * What to show about a node that is not ready: the diagnostic code it reports,
   * or, once the installer's wait has passed, provider_unavailable for a
   * connected node and "not_connected" for one that never connected. Else "".
   */
  problem: string;
}

export type StepState = "done" | "current" | "future";

/**
 * The state of each step (registered, connected, backend ready) while the node
 * is on its way: the steps it has passed are done and the next one is current.
 * A ready node ends the list, so the last step is never done here.
 */
export function progressSteps(stage: Exclude<EnrollmentStage, "ready">): [StepState, StepState, StepState] {
  const passed = { waiting: 0, registered: 1, connected: 2 }[stage];
  return [0, 1, 2].map((index) => (index < passed ? "done" : index === passed ? "current" : "future")) as [StepState, StepState, StepState];
}

/** Registration progress of the enrolled node, `appearedAt` being when the console first saw it. */
export function enrollmentProgress(node: SandboxNode | null, appearedAt: number | undefined, now: number): EnrollmentProgress {
  if (!node) return { stage: "waiting", problem: "" };
  if (node.online && node.provider_ready) return { stage: "ready", problem: "" };
  const late = appearedAt !== undefined && now - appearedAt >= NODE_READY_WAIT_MS;
  if (!node.online) return { stage: "registered", problem: late ? "not_connected" : "" };
  return { stage: "connected", problem: node.diagnostic || late ? nodeProviderDiagnostic(node) : "" };
}
