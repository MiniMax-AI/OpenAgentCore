import { AgentCoreError } from "./client";
import { CoreRequester, type CoreClientOptions } from "./core-request";
import { deploymentContract } from "./deployment-contract";
import { hasOwn, isNonnegativeInteger, isOneOf, isRecord, onlyFields, sameResourceId, schemaFields } from "./response-projection";
import {
  attachmentKindValues,
  deploymentResourcesFields, deploymentSpecFields, deploymentSpecRequired, deploymentViewFields, deploymentModeValues, deploymentViewRequired,
  hostHistoryFields, hostHistoryPointFields, nodeAllocationDiagnosticValues, nodeAllocationFields, nodeDetailFields, nodeDetailRequired,
  nodeDiagnosticCodeValues, nodeFields, nodeHostFields, nodeRequired, nodeRolloutFields, nodeRolloutRequired, nodeRolloutStateValues, resetModeValues,
  resetFields, resetOfflineNodeFields, resetRemainingFields, rolloutFields, rolloutNodesFields, rolloutStateValues, runtimeReleaseFields,
  sandboxAllocationListFields, sandboxNodeListFields, sandboxResourcesFields, sandboxResourcesRequired, suspensionFields,
  type DeploymentSpec, type DeploymentView, type HostHistoryPoint, type Node, type NodeAllocation, type NodeDetail, type NodeDiagnosticCode,
  type NodeHost, type NodeRollout, type NodeUpdate, type Reset, type ResetOfflineNode, type ResetRequest, type Rollout, type RuntimeRelease,
  type SandboxDeploymentInput, type SandboxEnrollmentToken, type SandboxResources as SandboxResourcesResource,
} from "./generated/core-api";
import type { ReadOptions } from "./types";

/** Checked against Core's shared node-diagnostics.json fixture. */
export const sandboxNodeDiagnostics = nodeDiagnosticCodeValues;
/** Fixed reason a node's provider is not ready. Core omits the field while the provider is ready, so read it as falsy (undefined) then. The client reads an unknown future value as provider_unavailable. */
export type SandboxNodeDiagnostic = NodeDiagnosticCode;

/** Keep a known readiness cause; never expose unclassified node-supplied text. */
export function normalizeSandboxNodeDiagnostic(value: string): SandboxNodeDiagnostic {
  return isOneOf(nodeDiagnosticCodeValues, value) ? value : "provider_unavailable";
}

/** A registered Provider kind; deploymentContract.providers holds each one's declaration. */
export type SandboxProvider = keyof typeof deploymentContract.providers;
/** Client-generated, never a Core code: a deployment write whose rejection could echo the key and is withheld. */
export const sandboxConfigurationUnconfirmed = "sandbox_configuration_unconfirmed";
const sandboxConfigurationParams = new Set<unknown>(["runtime", ...deploymentContract.resources.map(({ name }) => `resources.${name}`)]);

// Generated types keep their schema names in ./generated/core-api; these are the client's names for them.
/** CPU and MiB limits for each sandbox, not node concurrency. */
export type SandboxResources = SandboxResourcesResource;
export type SandboxRuntimeRelease = RuntimeRelease;
export type SandboxSpecification = DeploymentSpec;
export type SandboxRollout = Rollout;
/** `state` is target preparation, which never invalidates a qualified older serving pin; `ready_generation` is that durable pin and alone does not imply connection readiness. */
export type SandboxNodeRollout = NodeRollout;
export type StartSandboxReset = ResetRequest;
export type SandboxReset = Reset;
/** `provider_ready` is the last provider report, so combine it with `online`; `diagnostic` is absent while the provider is ready. A `core_url` other than the installation public URL means the node receives no new sandboxes and must be re-added. */
export type SandboxNode = Node;
/** A one-time node enrollment command; `enrollment_id` is its public handle, never a credential. */
export type SandboxEnrollment = SandboxEnrollmentToken;
/** One node with its host observation and complete UTC buckets of host history. */
export type SandboxNodeDetail = NodeDetail;
/** A node's name and sandbox limits, with a retained limit of at least the active one. Under Docker, Core sets the retained limit to the active one. */
export type SandboxNodeUpdate = NodeUpdate;
export type SandboxAllocation = NodeAllocation;

// The schema types each Provider's configuration, credential and metadata as free-form objects; the client names E2B's.
/**
 * Core derives the deployment's address from the installation public URL; a `core_url` member is rejected.
 * `expected_generation` is required, zero for first setup. Docker and microsandbox require `resources` and `runtime`;
 * E2B may omit `resources` to adopt its template build's CPU and memory, and always runs that build.
 */
export type InitializeSandboxDeployment = Omit<SandboxDeploymentInput, "provider" | "configuration" | "credential"> & {
  provider: SandboxProvider;
  configuration?: SandboxE2BConfiguration;
  /** Write-only. Omission on update preserves the current credential. */
  credential?: { api_key: string };
};
export type UpdateSandboxDeployment = InitializeSandboxDeployment;
export type SandboxDeployment = Omit<DeploymentView, "provider" | "configuration" | "metadata"> & {
  provider: SandboxProvider | "";
  configuration?: SandboxE2BConfiguration;
  metadata?: { template_build?: SandboxE2BTemplateBuild };
};
export interface SandboxE2BConfiguration { template?: string; api_url?: string; domain?: string }
/** The fixed E2B build as Core read it when the selection was saved; unknown values are null. */
export interface SandboxE2BTemplateBuild {
  status: string | null;
  resources: { cpus: number | null; memory_mib: number | null; root_disk_mib: number | null };
}
export interface SandboxE2BDiscoveryInput { api_key: string; api_url?: string; domain?: string }
export interface SandboxE2BTemplate { id: string; names: string[] }
export interface SandboxE2BReadyBuild { id: string; cpus: number; memory_mib: number }
export type SandboxNodeHistoryRange = "1h" | "6h" | "24h";

function invalidSandboxResponse(): never {
  throw new AgentCoreError("Core returned an invalid sandbox administration response.", 502, "invalid_admin_response");
}

/** The object has every required field, optional ones only where the schema lists them, and nothing else. */
function members(value: unknown, fields: readonly string[], required: readonly string[] = fields): Record<string, unknown> {
  if (!isRecord(value) || !schemaFields(value, fields, required)) return invalidSandboxResponse();
  return value;
}
function valid(condition: boolean): void {
  if (!condition) invalidSandboxResponse();
}
const timestamp = (value: unknown) => typeof value === "string" && Number.isFinite(Date.parse(value));
const measure = (value: unknown) => typeof value === "number" && Number.isFinite(value) && value >= 0;
const nullable = (test: (value: unknown) => boolean) => (value: unknown) => value === null || test(value);
const strings = (value: Record<string, unknown>, fields: readonly string[]) => fields.every((field) => typeof value[field] === "string");

function projectSpecification(value: unknown): SandboxSpecification {
  const specification = members(value, deploymentSpecFields, deploymentSpecRequired);
  const resources = members(specification.resources, sandboxResourcesFields, sandboxResourcesRequired);
  valid(Object.values(resources).every(isNonnegativeInteger));
  const result: SandboxSpecification = { resources: { ...resources } as unknown as SandboxResources };
  if (hasOwn(specification, "workspace")) {
    const workspace = members(specification.workspace, deploymentContract.workspace_fields);
    if (!isOneOf(attachmentKindValues, workspace.attachment) || typeof workspace.user_xattr !== "boolean" || typeof workspace.capacity_quota !== "boolean") return invalidSandboxResponse();
    result.workspace = { attachment: workspace.attachment, user_xattr: workspace.user_xattr, capacity_quota: workspace.capacity_quota };
  }
  if (hasOwn(specification, "runtime")) {
    const runtime = members(specification.runtime, runtimeReleaseFields);
    valid(strings(runtime, runtimeReleaseFields));
    result.runtime = { ...runtime } as unknown as SandboxRuntimeRelease;
  }
  return result;
}
/** The adapter's public projection has no credential member. */
function projectE2B(configuration: unknown, metadata: unknown): Pick<SandboxDeployment, "configuration" | "metadata"> {
  const config = members(configuration, ["template", "api_url", "domain"]);
  valid(strings(config, ["template", "api_url", "domain"]));
  const facts = members(metadata, ["template_build"], []);
  if (!hasOwn(facts, "template_build")) return { configuration: { ...config }, metadata: {} };
  const build = members(facts.template_build, ["status", "resources"]);
  const resources = members(build.resources, ["cpus", "memory_mib", "root_disk_mib"]);
  valid((build.status === null || typeof build.status === "string") && Object.values(resources).every(nullable(isNonnegativeInteger)));
  return { configuration: { ...config }, metadata: { template_build: { status: build.status as string | null, resources: { ...resources } as SandboxE2BTemplateBuild["resources"] } } };
}
/** Counts and blocker identities are one Core snapshot, never reconstructed from node lists. */
function projectReset(value: unknown, held: number): SandboxReset | null {
  if (value === null) return null;
  const reset = members(value, resetFields);
  const remaining = members(reset.remaining, resetRemainingFields);
  valid(isOneOf(resetModeValues, reset.clear) && timestamp(reset.requested_at) &&
    nullable(timestamp)(reset.deadline_at) && nullable(timestamp)(reset.forced_at) &&
    (reset.clear === "auto" ? reset.deadline_at !== null && reset.forced_at === null : reset.forced_at !== null) &&
    [remaining.busy, remaining.idle, remaining.cleanup, remaining.on_offline_nodes].every(isNonnegativeInteger) && Array.isArray(remaining.offline_nodes));
  const nodes = (remaining.offline_nodes as unknown[]).map(value => {
    const node = members(value, resetOfflineNodeFields);
    valid(strings(node, ["node_id", "name"]) && node.node_id !== "" && isNonnegativeInteger(node.resources) && Number(node.resources) > 0);
    return { ...node } as unknown as ResetOfflineNode;
  });
  valid(Number(remaining.busy) + Number(remaining.idle) + Number(remaining.cleanup) === held &&
    Number(remaining.on_offline_nodes) <= held && nodes.reduce((sum, node) => sum + node.resources, 0) === remaining.on_offline_nodes &&
    new Set(nodes.map(node => node.node_id)).size === nodes.length);
  return { ...reset, remaining: { ...remaining, offline_nodes: nodes } } as unknown as SandboxReset;
}
function projectRollout(value: unknown, mode: unknown, held: number): SandboxRollout {
  const rollout = members(value, rolloutFields);
  valid(isOneOf(rolloutStateValues, rollout.state) && isNonnegativeInteger(rollout.previous_generation_sandboxes) && Number(rollout.previous_generation_sandboxes) <= held);
  const nodes = rollout.nodes === null ? null : members(rollout.nodes, rolloutNodesFields);
  valid((nodes !== null) === (mode === "nodes") && (nodes === null || Object.values(nodes).every(isNonnegativeInteger)) && (rollout.state === "preparing") === (nodes !== null && Number(nodes.preparing) > 0));
  return { ...rollout, nodes: nodes && { ...nodes } } as unknown as SandboxRollout;
}
function projectNodeRollout(value: unknown, online: unknown): SandboxNodeRollout {
  const rollout = members(value, nodeRolloutFields, nodeRolloutRequired);
  valid(isOneOf(nodeRolloutStateValues, rollout.state) && nullable(isNonnegativeInteger)(rollout.ready_generation) &&
    (online !== false || rollout.state === "unknown") && (rollout.state !== "ready" || rollout.ready_generation !== null) &&
    (rollout.diagnostic === undefined || (typeof rollout.diagnostic === "string" && rollout.diagnostic !== "" && rollout.state === "failed")));
  return { ...rollout, ...(rollout.diagnostic !== undefined ? { diagnostic: normalizeSandboxNodeDiagnostic(rollout.diagnostic as string) } : {}) } as unknown as SandboxNodeRollout;
}
/** Configured deployments carry a validated public configuration and observation object. */
function projectDeployment(value: unknown): SandboxDeployment {
  const e2b = isRecord(value) && value.provider === "e2b";
  // A selected Provider always has its configuration and metadata; no Provider has neither.
  const selected = isRecord(value) && value.provider !== "";
  const deployment = members(value, deploymentViewFields, selected ? [...deploymentViewRequired, "configuration", "metadata"] : deploymentViewRequired);
  valid(selected || (!hasOwn(deployment, "configuration") && !hasOwn(deployment, "metadata")));
  valid(typeof deployment.credential_configured === "boolean");
  if (!e2b && deployment.provider !== "") { members(deployment.configuration, []); members(deployment.metadata, []); valid(deployment.credential_configured === false); }
  const resources = members(deployment.resources, deploymentResourcesFields);
  const suspension = deployment.suspension === null ? null : members(deployment.suspension, suspensionFields);
  const configured = hasOwn(deployment, "specification");
  valid(strings(deployment, ["installation_id", "core_url"]) && (deployment.provider === "" || (typeof deployment.provider === "string" && hasOwn(deploymentContract.providers, deployment.provider))) && isOneOf(deploymentModeValues, deployment.mode) &&
    [deployment.owner_epoch, deployment.generation, resources.allocations, resources.pending].every(isNonnegativeInteger) &&
    (suspension === null || [suspension.idle_seconds, suspension.retention_seconds].every(isNonnegativeInteger)) &&
    configured === hasOwn(deployment, "specification_digest") && (!configured || (typeof deployment.specification_digest === "string" && deployment.specification_digest !== "")));
  return {
    ...deployment, rollout: projectRollout(deployment.rollout, deployment.mode, Number(resources.allocations) + Number(resources.pending)), reset: projectReset(deployment.reset, Number(resources.allocations) + Number(resources.pending)), resources: { ...resources } as unknown as SandboxDeployment["resources"], suspension: suspension && { ...suspension } as unknown as SandboxDeployment["suspension"],
    ...(configured ? { specification: projectSpecification(deployment.specification) } : {}),
    ...(e2b ? projectE2B(deployment.configuration, deployment.metadata) : {}),
  } as unknown as SandboxDeployment;
}

/** Core omits an empty `diagnostic`, so a present one is a code; an unknown code reads as provider_unavailable. */
function projectNode(node: Record<string, unknown>): SandboxNode {
  const { diagnostic, rollout, ...rest } = node;
  const fields = { ...rest, rollout: projectNodeRollout(rollout, node.online) };
  valid(strings(node, ["id", "name", "provider", "core_url"]) && typeof node.online === "boolean" && typeof node.provider_ready === "boolean" &&
    [node.cpu_count, node.available_memory_bytes, node.available_disk_bytes].every(nullable(isNonnegativeInteger)) &&
    [node.running, node.snapshots, node.max_active, node.max_retained, node.active, node.reserved, node.retained, node.cleanup_pending].every(isNonnegativeInteger) &&
    nullable(timestamp)(node.last_seen_at) && timestamp(node.created_at) && (node.enrollment_id === null || typeof node.enrollment_id === "string") &&
    (diagnostic === undefined || (typeof diagnostic === "string" && diagnostic !== "")));
  if (diagnostic === undefined) return { ...fields } as unknown as SandboxNode;
  return { ...fields, diagnostic: normalizeSandboxNodeDiagnostic(diagnostic as string) } as unknown as SandboxNode;
}
function projectNodeList(value: unknown): { data: SandboxNode[] } {
  const list = members(value, sandboxNodeListFields);
  valid(Array.isArray(list.data));
  return { data: (list.data as unknown[]).map((entry) => projectNode(members(entry, nodeFields, nodeRequired))) };
}
/** A never-observed host is all null; the history lists every bucket of the range. */
function projectNodeDetail(value: unknown, nodeId: string): SandboxNodeDetail {
  const { host, history, ...fields } = members(value, nodeDetailFields, nodeDetailRequired);
  const node = projectNode(fields);
  const observed = members(host, nodeHostFields);
  const buckets = members(history, hostHistoryFields);
  valid(sameResourceId(node.id, nodeId) && [observed.effective_cpu_cores, observed.cpu_utilization].every(nullable(measure)) &&
    [observed.total_memory_bytes, observed.available_memory_bytes, observed.available_disk_bytes].every(nullable(isNonnegativeInteger)) &&
    nullable(timestamp)(observed.observed_at) && isNonnegativeInteger(buckets.resolution_seconds) && buckets.resolution_seconds > 0 && Array.isArray(buckets.points));
  const points = (buckets.points as unknown[]).map((entry) => {
    const point = members(entry, hostHistoryPointFields);
    valid(timestamp(point.start) && nullable(measure)(point.cpu_utilization_max) && [point.memory_used_bytes_max, point.available_disk_bytes_min].every(nullable(isNonnegativeInteger)));
    return { ...point } as unknown as HostHistoryPoint;
  });
  return { ...node, host: { ...observed } as unknown as NodeHost, history: { resolution_seconds: buckets.resolution_seconds as number, points } };
}

const allocationStrings = ["id", "node_id", "tenant_id", "session_id", "environment_id", "state", "compute_phase", "initialization", "diagnostic"];
function projectAllocations(value: unknown, nodeId: string): { data: SandboxAllocation[] } {
  const list = members(value, sandboxAllocationListFields);
  valid(Array.isArray(list.data));
  return { data: (list.data as unknown[]).map((entry) => {
    const allocation = members(entry, nodeAllocationFields);
    valid(isNonnegativeInteger(allocation.deployment_generation) && strings(allocation, allocationStrings) && sameResourceId(allocation.node_id as string, nodeId) && isOneOf(nodeAllocationDiagnosticValues, allocation.diagnostic) &&
      nullable(timestamp)(allocation.compute_phase_changed_at) && timestamp(allocation.created_at));
    return { ...allocation } as unknown as SandboxAllocation;
  }) };
}

/** Deployment administration under `/core/v1/sandbox`, through an authenticated console or an explicit Core key. */
export class SandboxAdminClient {
  readonly #core: CoreRequester;

  constructor(options: CoreClientOptions = {}) {
    this.#core = new CoreRequester(options.baseUrl ?? "/core/v1/sandbox", options.token, options.fetch, invalidSandboxResponse);
  }

  async listE2BTemplates(input: SandboxE2BDiscoveryInput, options?: ReadOptions): Promise<SandboxE2BTemplate[]> {
    const value = await this.#core.json("/providers/e2b/discovery", options, "POST", { configuration: { api_url: input.api_url, domain: input.domain }, credential: { api_key: input.api_key }, query: {} });
    if (!isRecord(value) || !onlyFields(value, new Set(["templates"])) || !Array.isArray(value.templates) || value.templates.length > 200) invalidSandboxResponse();
    return value.templates.map((item) => {
      if (!isRecord(item) || !onlyFields(item, new Set(["id", "names"])) || typeof item.id !== "string" || !Array.isArray(item.names) || !item.names.every((name) => typeof name === "string")) invalidSandboxResponse();
      return { id: item.id, names: item.names };
    });
  }
  async listE2BReadyBuilds(templateId: string, input: SandboxE2BDiscoveryInput, options?: ReadOptions): Promise<SandboxE2BReadyBuild[]> {
    const value = await this.#core.json("/providers/e2b/discovery", options, "POST", { configuration: { api_url: input.api_url, domain: input.domain }, credential: { api_key: input.api_key }, query: { template: templateId } });
    if (!isRecord(value) || !onlyFields(value, new Set(["builds"])) || !Array.isArray(value.builds) || value.builds.length > 200) invalidSandboxResponse();
    return value.builds.map((item) => {
      if (!isRecord(item) || !onlyFields(item, new Set(["id", "cpus", "memory_mib"])) || typeof item.id !== "string" || !isNonnegativeInteger(item.cpus) || !isNonnegativeInteger(item.memory_mib)) invalidSandboxResponse();
      return { id: item.id, cpus: item.cpus, memory_mib: item.memory_mib };
    });
  }

  #json<T>(path: string, options?: ReadOptions, method?: string, body?: unknown): Promise<T> {
    return this.#core.json(path, options, method, body) as Promise<T>;
  }
  async retrieveDeployment(options?: ReadOptions): Promise<SandboxDeployment> {
    return projectDeployment(await this.#core.json("/deployment", options));
  }
  initializeDeployment(input: InitializeSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    return this.#writeDeployment("POST", input, options);
  }
  updateDeployment(input: UpdateSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    return this.#writeDeployment("PUT", input, options);
  }
  async startReset(input: StartSandboxReset, options?: ReadOptions): Promise<SandboxDeployment> {
    return projectDeployment(await this.#core.json("/deployment/reset", options, "POST", input));
  }
  async cancelReset(expectedGeneration: number, options?: ReadOptions): Promise<SandboxDeployment> {
    return projectDeployment(await this.#core.json(`/deployment/reset?expected_generation=${encodeURIComponent(expectedGeneration)}`, options, "DELETE"));
  }
  async #writeDeployment(method: "POST" | "PUT", input: InitializeSandboxDeployment | UpdateSandboxDeployment, options?: ReadOptions): Promise<SandboxDeployment> {
    try {
      // As with an unparsable body, a configuration write with an invalid response is unconfirmed.
      return projectDeployment(await this.#core.json("/deployment", options, method, input));
    } catch (error) {
      if (error instanceof AgentCoreError && [400, 409, 503].includes(error.status)) {
        const messages: Record<string, string> = {
          invalid_sandbox_configuration: "Invalid sandbox provider configuration.",
          sandbox_specification_mismatch: "The node specification differs from the deployment.",
          sandbox_deployment_conflict: "The sandbox deployment cannot change in its current state.",
          sandbox_generation_stale: "The sandbox configuration changed. Refresh before submitting again.",
          sandbox_reset_required: "Reset the sandbox deployment before changing this configuration.",
          sandbox_reset_in_progress: "A sandbox reset is in progress.",
          sandbox_not_configured: "The sandbox deployment is not configured.",
          sandbox_in_use: "Hosted sandbox resources still belong to this deployment.",
          sandbox_credential_ownership: "This E2B key cannot manage the retained deployment. Reset before changing teams.",
          sandbox_credential_invalid: "The E2B API key was rejected.",
          sandbox_configuration_invalid: "Select a ready immutable E2B template build with matching resources.",
          sandbox_verification_unconfirmed: "E2B verification could not be confirmed. Refresh before submitting again.",
        };
        if (error.code && Object.hasOwn(messages, error.code)) {
          // Credential-bearing errors expose fixed local copy and allowlisted
          // numeric facts plus exact status/code/field matches only.
          const fields = error.code === "sandbox_generation_stale" ? ["current_generation"] : error.code === "sandbox_in_use" ? ["allocations", "pending"] : error.code === "invalid_sandbox_configuration" ? ["min", "max"] : [];
          const details = Object.fromEntries(fields.filter(field => isNonnegativeInteger(error.details?.[field])).map(field => [field, Number(error.details![field])]));
          const safeParam = error.status === 400
            ? error.code === "sandbox_credential_invalid" ? "credential" : error.code === "sandbox_configuration_invalid" ? "configuration" : error.code === "invalid_sandbox_configuration" && sandboxConfigurationParams.has(error.param) ? error.param : null
            : error.status === 409 && error.code === "sandbox_credential_ownership" ? "credential" : null;
          const param = error.param === safeParam ? safeParam : null;
          throw new AgentCoreError(messages[error.code]!, error.status, error.code, param, undefined, Object.keys(details).length ? details : undefined);
        }
      }
      // This code has one fixed Core meaning. Never forward its raw message or
      // param: an omitted key cannot be used to detect a reflected stored key.
      if (error instanceof AgentCoreError && error.status === 409 && error.code === "sandbox_configuration_error") {
        throw new AgentCoreError("E2B sandboxes reach Core over the internet. Set an HTTPS public URL that is not loopback.", 409, "sandbox_configuration_error", null);
      }
      // Any other credential-bearing rejection may reflect the key in any error field.
      throw new AgentCoreError("Sandbox configuration could not be confirmed. Refresh before submitting again.", error instanceof AgentCoreError ? error.status : 0, sandboxConfigurationUnconfirmed);
    }
  }
  async listNodes(options?: ReadOptions): Promise<{ data: SandboxNode[] }> {
    return projectNodeList(await this.#core.json("/nodes", options));
  }
  async retrieveNode(nodeId: string, range: SandboxNodeHistoryRange, options?: ReadOptions): Promise<SandboxNodeDetail> {
    return projectNodeDetail(await this.#core.json(`/nodes/${encodeURIComponent(nodeId)}?range=${range}`, options), nodeId);
  }
  async listAllocations(nodeId: string, options?: ReadOptions): Promise<{ data: SandboxAllocation[] }> {
    return projectAllocations(await this.#core.json(`/nodes/${encodeURIComponent(nodeId)}/allocations`, options), nodeId);
  }
  createEnrollment(options?: ReadOptions, capacity: { max_active?: number; max_retained?: number } = {}): Promise<SandboxEnrollment> {
    return this.#json("/enrollment-tokens", options, "POST", capacity);
  }
  updateNode(nodeId: string, input: SandboxNodeUpdate, options?: ReadOptions): Promise<{ id: string; updated: boolean }> {
    return this.#json(`/nodes/${encodeURIComponent(nodeId)}`, options, "PATCH", input);
  }
  removeNode(nodeId: string, options?: ReadOptions): Promise<{ id: string; deleted: boolean }> {
    return this.#json(`/nodes/${encodeURIComponent(nodeId)}`, options, "DELETE");
  }
}
