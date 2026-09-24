import { AgentCoreError } from "@agents-core-web/agents-client";
import type {
  AgentCore,
  EnvironmentTemplateResource,
  OpenAIHostedNetworkAccess,
} from "@agents-core-web/agents-client";

import { listAllCollectionPages } from "../../../lib/collection-pagination";

/**
 * Reusable managed Environment configuration published by the connected Core.
 *
 * A Template is durable Core-owned configuration, never a running Workspace and
 * never a provider selector: whether a managed Runtime is a local container or a
 * remote sandbox stays operator-owned behind the same `openai_hosted`
 * discriminator. Web therefore presents configuration, not a provider choice.
 */
export type EnvironmentTemplateCatalog =
  /**
   * Core answered the pinned five-operation resource. A Template with
   * configuration Web does not recognize stays listed and selectable; Core
   * remains the authority on whether a Session can be created from it.
   */
  | { state: "ready"; templates: EnvironmentTemplateResource[] }
  /** The connected Core build does not expose the Template resource. */
  | { state: "unsupported" }
  /** The resource exists but the read failed; selection must stay blocked. */
  | { state: "failed"; message: string };

const unsupportedStatuses = new Set([400, 404, 405, 501]);

/**
 * Cores older than the Template batch route this collection path into
 * Environment retrieval, so a rejected request is an absent capability rather
 * than proof of an available one. Anything else stays an explicit failure.
 */
export function environmentTemplatesUnsupported(error: unknown): boolean {
  return error instanceof AgentCoreError && unsupportedStatuses.has(error.status);
}

export async function loadEnvironmentTemplateCatalog(
  core: AgentCore,
  signal?: AbortSignal,
): Promise<EnvironmentTemplateCatalog> {
  try {
    const templates = await listAllCollectionPages(
      (options) => core.listEnvironmentTemplates(options),
      signal,
    );
    return { state: "ready", templates };
  } catch (error) {
    if (signal?.aborted) throw error;
    if (environmentTemplatesUnsupported(error)) return { state: "unsupported" };
    return {
      state: "failed",
      message: error instanceof AgentCoreError && error.message.trim()
        ? error.message
        : "Agent Core could not list Environment Templates.",
    };
  }
}

export function environmentTemplateLabel(template: EnvironmentTemplateResource): string {
  const name = template.name?.trim();
  return `${name || "Unnamed Template"} · network ${template.network?.access ?? "unrecognized"}`;
}

/**
 * Core inherits an omitted Session network from the Template and rejects a
 * Session that tries to widen a disabled or restricted Template to enabled.
 * An unrecognized Template policy is left to Core instead of being guessed.
 */
export function hostedNetworkNarrowingBlocker(
  template: EnvironmentTemplateResource | null,
  requested: OpenAIHostedNetworkAccess | null,
): string | null {
  if (!template || requested !== "enabled") return null;
  if (template.network?.access === "disabled") {
    return "This Template disables network access. A Session can keep or narrow that policy, never widen it.";
  }
  if (template.network?.access === "restricted") {
    return "This Template restricts network access to listed domains. A Session can keep or narrow that policy, never widen it.";
  }
  return null;
}
