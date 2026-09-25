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
  /** A root command that prepares the host; `<user>` stands for the node's user. */
  command?: string;
}

/**
 * What a node host needs, exactly as the enrollment command, the installer and
 * the node's readiness probe check it (line numbers as of this revision):
 * - the command runs curl, sha256sum and python3 (enrollment-command.ts);
 *   deploy/install/node_install.py:67-69 needs Python 3.9+, Linux amd64 and a non-root user;
 * - node_install.py:70-72 needs a systemd user session with lingering enabled;
 * - Docker: node_install.py:73-74 runs `docker info` through /var/run/docker.sock,
 *   and the node is ready only when Docker enforces CPU and memory limits
 *   (services/agents-api/internal/sandbox/config/probe.go:37-38), which the
 *   installer waits for (node_install.py:363-364);
 * - microsandbox: read/write /dev/kvm (node_install.py:75-76), the host libraries
 *   its binaries link (the ldd check, node_install.py:249-253), and a home short
 *   enough for ~/.parsar/m/<12 hex> to fit in 48 bytes (node_install.py:205-209),
 *   so at most 48 - len("/.parsar/m/") - 12 = 25 bytes;
 * - network: node files from the console (node_install.py:94-106), artifacts from
 *   the manifest's artifact_base_url, the console by default (node_install.py:139-140,
 *   distribution.py:150-154), Core's /api/v1 (node_spec.py:89, node_install.py:385),
 *   and sandboxes reach Core as well (node_install.py:213, 220-233).
 */
export function hostPrerequisites(provider: "docker" | "microsandbox"): HostPrerequisite[] {
  const backend: HostPrerequisite[] = provider === "docker"
    ? [{ label: "Docker at /var/run/docker.sock for that user, enforcing CPU and memory limits", command: "sudo usermod -aG docker <user>" }]
    : [
      { label: "Read and write access to /dev/kvm for that user", command: "sudo usermod -aG kvm <user>" },
      { label: "The shared libraries microsandbox needs, on a glibc system" },
      { label: "A home directory of 25 bytes or less, such as /home/parsar" },
    ];
  return [
    { label: "Linux amd64 with Python 3.9+, curl and sha256sum" },
    { label: "A non-root user with systemd lingering enabled", command: "sudo loginctl enable-linger <user>" },
    ...backend,
    { label: "Can reach {{console}}, {{core}} and the release downloads; sandboxes must reach {{core}}" },
  ];
}

/** Time left as m:ss (h:mm:ss from an hour), rounded up so it reads 0:00 only once expired. */
export function formatCountdown(milliseconds: number): string {
  const total = Math.max(0, Math.ceil(milliseconds / 1000));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = String(total % 60).padStart(2, "0");
  return hours ? `${hours}:${String(minutes).padStart(2, "0")}:${seconds}` : `${minutes}:${seconds}`;
}

/** The newest node that was not registered when the command was issued. */
export function enrolledNode(nodes: readonly SandboxNode[], known: ReadonlySet<string>): SandboxNode | null {
  let newest: SandboxNode | null = null;
  for (const node of nodes) {
    if (known.has(node.id)) continue;
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
