import { exactFields, isRecord } from "./response-projection";
import type { OpenAIHostedAgentEnvironment } from "./types";

const hostedSessionEnvironmentFields = new Set([
  "type", "id", "capability_directories", "network", "packages", "files", "plugins", "skills",
]);
const environmentNetworkFields = new Set(["access", "allowed_domains"]);
const environmentPackagesFields = new Set(["npm", "python", "system"]);

function strings(list: unknown): list is string[] {
  return Array.isArray(list) && list.every((entry) => typeof entry === "string");
}

function records(list: unknown): list is Record<string, unknown>[] {
  return Array.isArray(list) && list.every(isRecord);
}

/**
 * Projects a managed (`openai_hosted`) Session Environment, or returns null
 * when the value is not exactly that shape. A Session created from an advanced
 * Environment Template carries its frozen, safe installation metadata
 * (capability directories, restricted networking, packages, files, Plugins and
 * Skills); it is admitted structurally and kept as returned.
 */
export function projectOpenAIHostedSessionEnvironment(value: unknown): OpenAIHostedAgentEnvironment | null {
  if (!isRecord(value) || value.type !== "openai_hosted") return null;
  const network = value.network;
  const packages = value.packages;
  if (
    !exactFields(value, hostedSessionEnvironmentFields) ||
    typeof value.id !== "string" || value.id.trim() === "" ||
    !strings(value.capability_directories) ||
    !isRecord(network) || !exactFields(network, environmentNetworkFields) ||
    !strings(network.allowed_domains) ||
    !(((network.access === "enabled" || network.access === "disabled") && network.allowed_domains.length === 0) ||
      (network.access === "restricted" && network.allowed_domains.length > 0)) ||
    !isRecord(packages) || !exactFields(packages, environmentPackagesFields) ||
    !strings(packages.npm) || !strings(packages.python) || !strings(packages.system) ||
    !records(value.files) || !records(value.plugins) || !records(value.skills)
  ) return null;
  return {
    type: "openai_hosted",
    id: value.id,
    capability_directories: [...value.capability_directories],
    network: { access: network.access, allowed_domains: [...network.allowed_domains] },
    packages: { npm: [...packages.npm], python: [...packages.python], system: [...packages.system] },
    files: value.files.map((entry) => ({ ...entry })),
    plugins: value.plugins.map((entry) => ({ ...entry })),
    skills: value.skills.map((entry) => ({ ...entry })),
  };
}

/**
 * True for exactly the managed Session Environment shapes the client projects,
 * basic or created from an advanced Template. Unknown fields, sections or
 * network modes are rejected rather than guessed.
 */
export function isOpenAIHostedSessionEnvironment(value: unknown): value is OpenAIHostedAgentEnvironment {
  return projectOpenAIHostedSessionEnvironment(value) !== null;
}
