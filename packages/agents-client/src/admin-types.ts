import type {
  AddressBindings, AdminauditOperation, AdminauditPage, AdminRuntimeObservationDetail, AdminSessionArchiveRequest, AdminSummaryResponse,
  AdminSummaryRow, EnvironmentExecutorCredentialRequest, Installation, InstallationConfiguration, InstallationSetting, IssuedAPIKey,
  IssuedExecutorCredential as IssuedExecutorCredentialResource, ManagedArchive, Project, ProjectAPIKeyRequest, ProjectRequest, ProjectsAPIKey,
  ResourceOwner, WriteauditAPIKey, WriteauditOperation, ResourceType, WriteauditPage,
} from "./generated/core-api";
import type { PageOptions, RuntimeObservation } from "./types";

export type { ExecutorConnection, ExecutorCredential, ExecutorCredentialList, RuntimeDiskObservation } from "./generated/core-api";
export { resourceTypeValues as adminResourceTypes } from "./generated/core-api";

// Generated types keep their schema names in ./generated/core-api; these are the client's names for them.
export type AdminProject = Project;
export type CreateAdminProjectInput = ProjectRequest;
export type RenameAdminProjectInput = ProjectRequest;
export type AdminAPIKey = ProjectsAPIKey;
export type AdminIssuedAPIKey = IssuedAPIKey;
export type IssueAdminAPIKeyInput = ProjectAPIKeyRequest;
export type ArchiveAdminSessionInput = AdminSessionArchiveRequest;
/** Current resource disposition; released does not imply that the active Turn has finalized. */
export type AdminSessionArchive = ManagedArchive;
export type AdminResourceType = ResourceType;
export type AdminKeyProvenance = WriteauditAPIKey;
/** `api_key` is null when Core has no creation record. */
export type AdminResourceOwner = ResourceOwner;
export type AdminWriteOperation = WriteauditOperation;
export type AdminWriteOperationPage = WriteauditPage;
export type AdminSummaryEntry = AdminSummaryRow;
export type AdminSummary = AdminSummaryResponse;
export type AdminAuditEntry = AdminauditOperation;
export type AdminAuditPage = AdminauditPage;
/** `key_id` is chosen by the caller, so an uncertain issuance can be reissued with the same ID and `rotate: true`. */
export type IssueExecutorCredentialInput = EnvironmentExecutorCredentialRequest;
// This route always restricts the credential to its Environment, so environment_id is always present.
export type IssuedExecutorCredential = Required<IssuedExecutorCredentialResource>;
export type CoreAddressBindings = AddressBindings;
export type CoreInstallationSetting = InstallationSetting;
export type CoreInstallationConfiguration = InstallationConfiguration;
export type CoreInstallation = Installation;

// The schema's flat observation cannot state each mode's null rules; the client's RuntimeObservation union does.
export interface AdminRuntimeObservation {
  project_id: string;
  observation: RuntimeObservation & Pick<AdminRuntimeObservationDetail, "disk">;
}

// Call options and receipts the client composes; they are not schema definitions.
export interface AdminClientOptions {
  /** Prefix that request paths are appended to; defaults to `/core/v1`. */
  baseUrl?: string;
  /** Core key for trusted server callers; console browsers use their same-origin session instead. */
  adminToken?: string | (() => string | undefined);
  /** Fetch implementation; defaults to the global fetch. */
  fetch?: typeof fetch;
}
/** The project and key pages; the schema repeats this envelope for each. */
export interface AdminPage<T> { data: T[]; has_more: boolean }
/** Deletion receipts of the `/v1` objects administrators delete; `deleted` is always true. */
export interface AdminDeleted<O extends string = string> { id: string; object: O; deleted: true }
/** A downloaded body; it is not JSON. */
export interface AdminContent { blob: Blob; contentType: string | null; contentDisposition: string | null }
export interface AdminWriteOperationOptions extends Omit<PageOptions, "order"> {
  key_id?: string;
  resource_type?: AdminResourceType;
  resource_id?: string;
  created_after?: string;
  created_before?: string;
}
export interface AdminSummaryOptions extends PageOptions {
  project_id?: string;
  group_by?: "project" | "agent" | "key";
  created_after?: string;
  created_before?: string;
}
export interface AdminAuditOptions extends Omit<AdminWriteOperationOptions, "resource_type" | "key_id"> {
  project_id?: string;
  action?: string;
  resource_type?: string;
}
