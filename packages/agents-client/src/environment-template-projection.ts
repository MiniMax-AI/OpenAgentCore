import type {
  AgentEnvironmentPackages,
  CreateEnvironmentTemplateInput,
  EnvironmentNetworkPolicy,
  EnvironmentTemplate,
  EnvironmentTemplateConfiguration,
  EnvironmentTemplateFile,
  EnvironmentTemplatePlugin,
  EnvironmentTemplateResource,
  EnvironmentTemplateSection,
  EnvironmentTemplateSkill,
  UpdateEnvironmentTemplateInput,
} from "./types";
import { canonicalUuid, exactFields, hasOwn, isNonnegativeInteger, isRecord, onlyFields } from "./response-projection";

type Invalid = (message?: string) => never;

const identityFields = ["id", "object", "name", "created_at", "updated_at"] as const;
const templateSections: readonly EnvironmentTemplateSection[] = [
  "network", "capability_directories", "packages", "files", "plugins", "skills",
];
const knownTemplateFields = new Set<string>([...identityFields, ...templateSections]);
const networkFields = new Set(["access", "allowed_domains"]);
const packagesFields = new Set(["npm", "python", "system"]);
const inlineFileFields = new Set(["type", "path", "size_bytes"]);
const referencedFileFields = new Set(["type", "path", "file_id"]);
const skillReferenceFields = new Set(["type", "skill_id", "version"]);
const inlineCapabilityFields = new Set(["type", "name", "description"]);
const canonicalUuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
// Skill IDs are `skill_` plus Core's resource identifier.
const skillIdPattern = /^skill_[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
const skillVersionPattern = /^[1-9][0-9]{0,18}$/;
const controlCharacterPattern = /[\u0000-\u001f\u007f]/;
// Core accepts 1–100 exact hostnames for restricted access.
const maxAllowedDomains = 100;

/** Core preserves a Template name verbatim within a 1–256 Unicode character bound. */
export function isEnvironmentTemplateName(value: unknown): value is string {
  if (typeof value !== "string") return false;
  const characters = [...value].length;
  return characters >= 1 && characters <= 256;
}

/** True when every configuration section of a Template was recognized. */
export function isRecognizedEnvironmentTemplate(template: EnvironmentTemplateResource): template is EnvironmentTemplate {
  return template.unrecognized === undefined;
}

function isText(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && !controlCharacterPattern.test(value);
}

function isWorkspacePath(value: unknown): value is string {
  return isText(value) && value.startsWith("/workspace/") && !value.split("/").includes("..");
}

function isWorkspaceDirectory(value: unknown): value is string {
  return isText(value) && (value === "/workspace" || value.startsWith("/workspace/")) && !value.split("/").includes("..");
}

function stringList(value: unknown, valid: (entry: unknown) => entry is string = (entry): entry is string => typeof entry === "string"): string[] | null {
  return Array.isArray(value) && value.every((entry) => valid(entry)) ? [...value] as string[] : null;
}

function projectNetwork(value: unknown): EnvironmentNetworkPolicy | null {
  if (!isRecord(value) || !exactFields(value, networkFields)) return null;
  const domains = stringList(value.allowed_domains, isText);
  if (domains === null) return null;
  if (value.access === "enabled" || value.access === "disabled") {
    return domains.length === 0 ? { access: value.access, allowed_domains: [] } : null;
  }
  if (value.access === "restricted") {
    return domains.length >= 1 && domains.length <= maxAllowedDomains ? { access: "restricted", allowed_domains: domains } : null;
  }
  return null;
}

function projectPackages(value: unknown): AgentEnvironmentPackages | null {
  if (!isRecord(value) || !exactFields(value, packagesFields)) return null;
  const npm = stringList(value.npm, isText);
  const python = stringList(value.python, isText);
  const system = stringList(value.system, isText);
  return npm && python && system ? { npm, python, system } : null;
}

function projectEntries<T>(value: unknown, project: (entry: Record<string, unknown>) => T | null): T[] | null {
  if (!Array.isArray(value)) return null;
  const entries: T[] = [];
  for (const entry of value) {
    const projected = isRecord(entry) ? project(entry) : null;
    if (projected === null) return null;
    entries.push(projected);
  }
  return entries;
}

function projectFile(entry: Record<string, unknown>): EnvironmentTemplateFile | null {
  if (entry.type === "inline" && exactFields(entry, inlineFileFields)) {
    return isWorkspacePath(entry.path) && isNonnegativeInteger(entry.size_bytes)
      ? { type: "inline", path: entry.path, size_bytes: entry.size_bytes }
      : null;
  }
  if (entry.type === "file_id" && exactFields(entry, referencedFileFields)) {
    return isWorkspacePath(entry.path) && isText(entry.file_id)
      ? { type: "file_id", path: entry.path, file_id: entry.file_id }
      : null;
  }
  return null;
}

function projectSkill(entry: Record<string, unknown>): EnvironmentTemplateSkill | null {
  if (entry.type === "skill_reference" && exactFields(entry, skillReferenceFields)) {
    const version = entry.version;
    return typeof entry.skill_id === "string" && skillIdPattern.test(entry.skill_id) &&
      (version === null || version === "latest" || (typeof version === "string" && skillVersionPattern.test(version)))
      ? { type: "skill_reference", skill_id: entry.skill_id, version }
      : null;
  }
  if (entry.type === "inline" && exactFields(entry, inlineCapabilityFields)) {
    return typeof entry.name === "string" && entry.name.length > 0 && typeof entry.description === "string" && entry.description.length > 0
      ? { type: "inline", name: entry.name, description: entry.description }
      : null;
  }
  return null;
}

function projectPlugin(entry: Record<string, unknown>): EnvironmentTemplatePlugin | null {
  return entry.type === "inline" && exactFields(entry, inlineCapabilityFields) &&
    typeof entry.name === "string" && entry.name.length > 0 &&
    typeof entry.description === "string" && entry.description.length > 0
    ? { type: "inline", name: entry.name, description: entry.description }
    : null;
}

const sectionProjections: { [K in EnvironmentTemplateSection]: (value: unknown) => EnvironmentTemplateConfiguration[K] | null } = {
  network: projectNetwork,
  capability_directories: (value) => stringList(value, isWorkspaceDirectory),
  packages: projectPackages,
  files: (value) => projectEntries(value, projectFile),
  plugins: (value) => projectEntries(value, projectPlugin),
  skills: (value) => projectEntries(value, projectSkill),
};

/**
 * Projects one Template. Identity fields are strict: a Template without a
 * valid identity is an invalid response. Configuration is strict per section:
 * an unrecognized section (or an unexpected field) marks only this Template,
 * and its value is never projected, so a confidential field cannot leak.
 */
export function projectEnvironmentTemplate(
  value: unknown,
  invalid: Invalid,
  expectedId?: string,
): EnvironmentTemplateResource {
  if (!isRecord(value) || !identityFields.every((field) => hasOwn(value, field))) return invalid();
  if (
    typeof value.id !== "string" || !canonicalUuidPattern.test(value.id) ||
    (expectedId !== undefined && (canonicalUuid(value.id) === null || canonicalUuid(value.id) !== canonicalUuid(expectedId))) ||
    value.object !== "agent.environment.template" ||
    !(value.name === null || isEnvironmentTemplateName(value.name)) ||
    !isNonnegativeInteger(value.created_at) ||
    !isNonnegativeInteger(value.updated_at) || value.updated_at < value.created_at
  ) {
    return invalid();
  }
  const configuration: Partial<EnvironmentTemplateConfiguration> = {};
  const unrecognized: string[] = [];
  for (const section of templateSections) {
    const projected = hasOwn(value, section) ? sectionProjections[section](value[section]) : null;
    if (projected === null) unrecognized.push(section);
    else (configuration as Record<string, unknown>)[section] = projected;
  }
  for (const field of Object.keys(value)) {
    if (!knownTemplateFields.has(field)) unrecognized.push(field);
  }
  const identity = {
    id: value.id,
    object: "agent.environment.template" as const,
    name: value.name as string | null,
    created_at: value.created_at,
    updated_at: value.updated_at,
  };
  if (unrecognized.length === 0) return { ...identity, ...configuration as EnvironmentTemplateConfiguration };
  return { ...identity, ...configuration, unrecognized };
}

/** Validates a create or update body: only name and network are ever sent. */
export function environmentTemplateRequestBody(
  input: CreateEnvironmentTemplateInput | UpdateEnvironmentTemplateInput,
): string {
  if (!isRecord(input) || !onlyFields(input, new Set(["name", "network"]))) {
    throw new TypeError("Environment Template requests accept only name and network.");
  }
  if (hasOwn(input, "name") && !(input.name === null || isEnvironmentTemplateName(input.name))) {
    throw new TypeError("An Environment Template name must be null or 1 through 256 characters.");
  }
  if (hasOwn(input, "network") && input.network !== null) {
    const network: unknown = input.network;
    const basic = isRecord(network) && exactFields(network, new Set(["access"])) &&
      (network.access === "enabled" || network.access === "disabled");
    const restricted = isRecord(network) && exactFields(network, networkFields) && network.access === "restricted" &&
      Array.isArray(network.allowed_domains) && network.allowed_domains.every((domain) => typeof domain === "string");
    if (!basic && !restricted) {
      throw new TypeError("An Environment Template network accepts enabled or disabled access, or restricted access with a domain list.");
    }
  }
  return JSON.stringify(input);
}

/** The network a write stores: an omitted or null network is the enabled default. */
export function expectedEnvironmentTemplateNetwork(
  network: CreateEnvironmentTemplateInput["network"],
): EnvironmentNetworkPolicy {
  if (network == null) return { access: "enabled", allowed_domains: [] };
  return network.access === "restricted"
    ? { access: "restricted", allowed_domains: [...network.allowed_domains] }
    : { access: network.access, allowed_domains: [] };
}

export function sameEnvironmentNetwork(left: EnvironmentNetworkPolicy | undefined, right: EnvironmentNetworkPolicy): boolean {
  return left !== undefined && left.access === right.access &&
    left.allowed_domains.length === right.allowed_domains.length &&
    left.allowed_domains.every((domain, index) => domain === right.allowed_domains[index]);
}
