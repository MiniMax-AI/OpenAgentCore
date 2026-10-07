import type { PageOptions } from "./types";

export interface AdminClientOptions {
  /** Prefix that request paths are appended to; defaults to `/core/v1`. */
  baseUrl?: string;
  /** Core key for trusted server callers; console browsers use their same-origin session instead. */
  adminToken?: string | (() => string | undefined);
  /** Fetch implementation; defaults to the global fetch. */
  fetch?: typeof fetch;
}

export interface AdminProject {
  id: string;
  name: string;
  created_at: string;
  archived_at: string | null;
  active_key_count: number;
}
export interface CreateAdminProjectInput { name: string }
export interface RenameAdminProjectInput { name: string }
export interface AdminAPIKey {
  id: string;
  project_id: string;
  name: string;
  prefix: string;
  created_at: string;
  revoked_at: string | null;
}
export interface AdminIssuedAPIKey extends AdminAPIKey { key: string }
export interface IssueAdminAPIKeyInput { name: string }
export interface AdminPage<T> { data: T[]; has_more: boolean }
export interface AdminDeleted<O extends string = string> { id: string; object: O; deleted: true }
export interface ArchiveAdminSessionInput { expected_generation: number }
/** Current resource disposition; released does not imply that the active Turn has finalized. */
export interface AdminSessionArchive {
  session_id: string;
  environment_id: string;
  state: "active" | "cleanup_pending" | "released";
}
export interface Skill {
  id: string;
  object: "skill";
  created_at: number;
  name: string;
  description: string;
  default_version: string;
  latest_version: string;
}
export interface SkillVersion {
  id: string;
  object: "skill.version";
  created_at: number;
  skill_id: string;
  version: string;
  name: string;
  description: string;
}
export interface SessionArtifact {
  id: string;
  object: "agent.session.artifact";
  created_at: number;
  environment_id: string;
  path: string;
  session_id: string;
  size_bytes: number;
  turn_id: string;
}
export interface AdminContent { blob: Blob; contentType: string | null; contentDisposition: string | null }
export type AdminResourceType = "agent" | "skill" | "environment_template" | "file" | "vault" | "credential" | "session" | "environment" | "skill_version" | "artifact";
export interface AdminKeyProvenance {
  id: string;
  name: string;
  prefix: string;
  kind: "issued";
  revoked_at: string | null;
}
/** `api_key` is null when Core has no creation record. */
export interface AdminResourceOwner {
  resource_id: string;
  api_key: AdminKeyProvenance | null;
}
export interface AdminWriteOperation {
  id: string;
  created_at: string;
  api_key: AdminKeyProvenance | null;
  action: string;
  resource_type: AdminResourceType;
  resource_id: string;
  parent_id: string;
  request_id: string;
  trace_id: string;
}
export interface AdminWriteOperationOptions extends Omit<PageOptions, "order"> {
  key_id?: string;
  resource_type?: AdminResourceType;
  resource_id?: string;
  created_after?: string;
  created_before?: string;
}
export interface AdminWriteOperationPage extends AdminPage<AdminWriteOperation> { next_cursor: string }

export interface AdminSummaryOptions extends PageOptions {
  project_id?: string;
  group_by?: "project" | "agent" | "key";
  created_after?: string;
  created_before?: string;
}
export interface AdminSummaryEntry {
  project_id: string;
  /** Session creation provenance for key grouping; null includes unknown creators. */
  key_id: string | null;
  agent_id: string | null;
  assets: { agents: number; skills: number; environment_templates: number; files: number; vaults: number; credentials: number } | null;
  sessions: { total: number; idle: number; in_progress: number; requires_action: number; failed: number };
  usage: import("./types").TokenUsage;
  coverage: { measured_sessions: number; total_sessions: number; ratio: number | null };
  last_active_at: number | null;
}
export interface AdminSummary extends AdminPage<AdminSummaryEntry> { next_cursor: string }
/** Current disk usage and capacity; E2B reports them, Docker and microsandbox return null. */
export interface RuntimeDiskObservation { usage_bytes: number | null; limit_bytes: number | null }
/** The administrator list adds disk to the project observation shape. */
export interface AdminRuntimeObservation {
  project_id: string;
  observation: import("./types").RuntimeObservation & { disk: RuntimeDiskObservation | null };
}

export interface AdminAuditOptions extends Omit<AdminWriteOperationOptions, "resource_type" | "key_id"> {
  project_id?: string;
  action?: string;
  resource_type?: string;
}
export interface AdminAuditEntry {
  id: string;
  created_at: string;
  admin_credential_id: string;
  actor_label: string;
  action: string;
  /** Null for deployment-wide writes, such as deployment default model providers. */
  project_id: string | null;
  resource_type: string;
  resource_id: string;
  request_id: string;
  trace_id: string;
}
export interface AdminAuditPage extends AdminPage<AdminAuditEntry> { next_cursor: string }

/** Executor credential metadata for one self_hosted environment; the credential itself is never listed. */
export interface ExecutorCredential { key_id: string; created_at: string; revoked_at: string | null }
/** Core authority plus a matching live gateway peer; timestamps alone are not readiness. */
export interface ExecutorConnection {
  status: "never_enrolled" | "connected" | "disconnected";
  bound_key_id: string | null;
  enrolled_at: string | null;
  last_seen_at: string | null;
}
export interface ExecutorCredentialList { data: ExecutorCredential[]; connection: ExecutorConnection }

/** `key_id` is chosen by the caller, so an uncertain issuance can be reissued with the same ID and `rotate: true`. */
export interface IssueExecutorCredentialInput { key_id: string; rotate?: boolean }
/** Returned once, on issuance or rotation. */
export interface IssuedExecutorCredential { key_id: string; environment_id: string; executor_token: string }

/** What is bound to the installation public URL; a change of that URL affects all of it. */
export interface CoreAddressBindings {
  /** Enrolled nodes that are not removed. */
  nodes: number;
  /** Nodes enrolled with another address; they receive no new sandboxes until re-added. At most `nodes`. */
  nodes_on_other_address: number;
  /** Retained and pending hosted sandboxes, which were started with the address current at the time. */
  hosted_sandboxes: number;
  /** Unrevoked self-hosted executor credentials, whose executors were installed with an advertised remote_url. */
  self_hosted_executors: number;
}
/** One process setting from the installation's config.json, as last applied. */
export interface CoreInstallationSetting {
  /** Dotted config.json key, such as `ports.core`. Unique within the snapshot. */
  key: string;
  /** Applied JSON value; always null for a sensitive setting. */
  value: unknown;
  default: unknown;
  /** Present exactly for a sensitive setting: whether it has a value. */
  configured?: boolean;
  /** False for settings fixed at installation. */
  changeable: boolean;
  sensitive: boolean;
  /** Services that restart when the setting changes. */
  restarts: ("core" | "web" | "database")[];
}
/** The process settings Core loaded. */
export interface CoreInstallationConfiguration {
  settings: CoreInstallationSetting[];
}
/** `GET /core/v1/installation`: available before any sandbox deployment exists. */
export interface CoreInstallation {
  object: "core.installation";
  installation_id: string | null;
  /** The origin applications, nodes, sandboxes and self-hosted executors use; null when Core runs without one. */
  public_url: string | null;
  /** `public_url` followed by `/v1`; the base URL for application API keys. */
  api_base_url: string | null;
  /** True when `public_url` is a loopback origin that only the Core host reaches. */
  local_only: boolean;
  /** Full source commit Core was built from; null for development builds. */
  source_commit: string | null;
  /** The process settings Core loaded. */
  configuration: CoreInstallationConfiguration;
  address_bindings: CoreAddressBindings;
}
