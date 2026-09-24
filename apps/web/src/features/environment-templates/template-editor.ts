import {
  AgentCoreError,
  type EnvironmentNetworkAccess,
  type EnvironmentTemplateNetworkInput,
  type EnvironmentTemplateResource,
  type UpdateEnvironmentTemplateInput,
} from "@agents-core-web/agents-client";
import i18n from "../../i18n";

const tt = (key: string, options?: Record<string, unknown>) => i18n.t(key as never, { ns: "templates", ...options });

/** The editable fields. `domains` is the raw one-hostname-per-line text of a restricted policy. */
export interface TemplateDraft { name: string; access: EnvironmentNetworkAccess; domains: string }

export function draftFromTemplate(template?: EnvironmentTemplateResource): TemplateDraft {
  return {
    name: template?.name ?? "",
    access: template?.network?.access ?? "enabled",
    domains: template?.network?.allowed_domains.join("\n") ?? "",
  };
}

/** One hostname per line; blank lines are ignored, spelling, order and duplicates are kept for Core to validate. */
export function parseDomains(text: string): string[] {
  return text.split(/\r?\n/u).map((line) => line.trim()).filter((line) => line.length > 0);
}

export function nameTooLong(draft: TemplateDraft): boolean {
  return [...draft.name.trim()].length > 256;
}

export function domainsMissing(draft: TemplateDraft): boolean {
  return draft.access === "restricted" && parseDomains(draft.domains).length === 0;
}

export function draftNetwork(draft: TemplateDraft): EnvironmentTemplateNetworkInput {
  return draft.access === "restricted"
    ? { access: "restricted", allowed_domains: parseDomains(draft.domains) }
    : { access: draft.access };
}

/**
 * The update body: only a changed name and a changed network, never any other
 * section, so Core keeps files, packages, Skills, Plugins, env and setup as
 * they are. A network Web does not recognize is never written.
 */
export function templatePatch(template: EnvironmentTemplateResource, draft: TemplateDraft): UpdateEnvironmentTemplateInput {
  const name = draft.name.trim() || null;
  if (nameTooLong(draft)) throw new Error(tt("form.nameTooLong"));
  const patch: UpdateEnvironmentTemplateInput = {};
  if (draft.name !== (template.name ?? "")) patch.name = name;
  const current = template.network;
  if (!current) return patch;
  const network = draftNetwork(draft);
  const domains = network.access === "restricted" ? network.allowed_domains : [];
  const changed = network.access !== current.access ||
    domains.length !== current.allowed_domains.length ||
    domains.some((domain, index) => domain !== current.allowed_domains[index]);
  if (changed) {
    if (domainsMissing(draft)) throw new Error(tt("form.domainsRequired"));
    patch.network = network;
  }
  return patch;
}

export function templateName(template: Pick<EnvironmentTemplateResource, "name">): string {
  return template.name?.trim() || tt("unnamed");
}

export function templateFailure(error: unknown): string {
  if (error instanceof AgentCoreError) {
    if (error.status === 401 || error.status === 403) return tt("errors.forbidden");
    if (error.status === 404) return tt("errors.missing");
    // Core's Template rejections are fixed messages that never echo the request.
    if (error.status === 400 || error.status === 422) return tt("errors.invalid", { reason: error.message });
  }
  return tt("errors.uncertain");
}

export type TemplateWriteResult =
  | { kind: "ignored" }
  | { kind: "saved" | "saved-refresh-failed" }
  | { kind: "failed"; message: string };

/** One write at a time; disposal fences both late UI updates and catalog refreshes. */
export function createTemplateWriteScope() {
  let active = true;
  let pending = false;
  let controller: AbortController | null = null;
  return {
    dispose() { active = false; controller?.abort(); },
    async run(write: (signal: AbortSignal) => Promise<unknown>, refresh: () => Promise<void>): Promise<TemplateWriteResult> {
      if (!active || pending) return { kind: "ignored" };
      pending = true;
      controller = new AbortController();
      try {
        await write(controller.signal);
        if (!active) return { kind: "ignored" };
        try {
          await refresh();
          return active ? { kind: "saved" } : { kind: "ignored" };
        } catch {
          return active ? { kind: "saved-refresh-failed" } : { kind: "ignored" };
        }
      } catch (error) {
        return active ? { kind: "failed", message: templateFailure(error) } : { kind: "ignored" };
      } finally { pending = false; controller = null; }
    },
  };
}
