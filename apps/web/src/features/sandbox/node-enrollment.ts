import type { SandboxEnrollment, SandboxNode } from "@agents-core-web/agents-client";

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
 * What a host needs for the default command, which runs the installer as root
 * and the node as the `parsar-node` system service (sudo mode), as the command
 * and deploy/install/node_install.py check it:
 * - the command runs curl, sha256sum and python3 (enrollment-command.ts), and
 *   `sudo` unless the shell is root; `host_checks` needs Python 3.9+, Linux amd64
 *   and systemd as the init system;
 * - Docker: `provider_group` needs Docker Engine running with a group-accessible
 *   socket, enforcing CPU and memory limits (the node is ready only then:
 *   services/agents-api/internal/sandbox/config/probe.go); it installs nothing;
 * - microsandbox: `provider_group` needs /dev/kvm, and `prepare_runtime` the
 *   libraries its binaries link (the ldd check);
 * - `host_capacity`: the host's CPUs and memory hold one sandbox of the
 *   deployment's size (else the node reports capacity_insufficient); the Runtime
 *   image needs about 2 GB of disk;
 * - network: node files and artifacts only from the console (`fetch` and
 *   `metadata`, which never use a release URL), Core's /api/v1 (node_spec.py, the
 *   `register` call), and sandboxes reach Core as well (`provider_config`).
 * `sized` says whether the deployment's sandbox size is known for the capacity item.
 */
export function hostRequirements(provider: "docker" | "microsandbox", sized: boolean): HostPrerequisite[] {
  return [
    { label: "Linux amd64 with systemd; Python 3.9+, curl and sha256sum; root or sudo" },
    provider === "docker"
      ? { label: "Docker Engine installed and running, enforcing CPU and memory limits (cgroup v2)" }
      : { label: "/dev/kvm (hardware or nested virtualization) and the libraries microsandbox links (glibc)" },
    { label: sized ? "CPUs and memory for at least one sandbox: {{size}}; about 2 GB of disk for the Runtime image" : "CPUs and memory for at least one sandbox; about 2 GB of disk for the Runtime image" },
    { label: "Reaches {{console}} and {{core}}; sandboxes reach {{core}}" },
  ];
}

/**
 * What the node's own user needs besides the host requirements when the command
 * runs without sudo, so the node is that user's systemd service (node_install.py
 * `preflight` and `install`):
 * - a non-root user, with Docker's socket or read/write /dev/kvm through its group;
 * - lingering. It starts the user's systemd manager, whose services, the node's
 *   included, keep the groups it started with; so the group change comes first,
 *   or that manager is restarted after it (USER_MANAGER_RESTART). A shell open
 *   before the change lacks the group too, so the user signs in again;
 * - the user's own systemd session (`systemctl --user`), which sudo -u and su
 *   don't provide;
 * - microsandbox: a home short enough for ~/.parsar/m/<12 hex> to fit in 48 bytes
 *   (`micro_home`): at most 48 - len("/.parsar/m/") - 12 = 25 bytes.
 */
export function userModePrerequisites(provider: "docker" | "microsandbox"): HostPrerequisite[] {
  return [
    { label: "A non-root user to run the node (NODE_USER below)" },
    provider === "docker"
      ? { label: "Docker at /var/run/docker.sock for that user, enforcing CPU and memory limits", command: "sudo usermod -aG docker NODE_USER" }
      : { label: "Read and write access to /dev/kvm for that user", command: "sudo usermod -aG kvm NODE_USER" },
    { label: "systemd lingering for that user, enabled after the group change", command: "sudo loginctl enable-linger NODE_USER" },
    ...(provider === "microsandbox" ? [{ label: "A home directory of 25 bytes or less, such as /home/parsar" } satisfies HostPrerequisite] : []),
    { label: "Run the command signed in as that user: over SSH, or with", command: "sudo machinectl shell NODE_USER@" },
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

/**
 * The node this command registered: Core copies the command's `enrollment_id`
 * onto the node it enrolls. A node enrolled before Core recorded it reports null
 * and never matches, nor does anything for a command without an ID.
 */
export function enrolledNode(nodes: readonly SandboxNode[], command: Pick<SandboxEnrollment, "enrollment_id">): SandboxNode | null {
  if (!command.enrollment_id) return null;
  return nodes.find((node) => node.enrollment_id === command.enrollment_id) ?? null;
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
