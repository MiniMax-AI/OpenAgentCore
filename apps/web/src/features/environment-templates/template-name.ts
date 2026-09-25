import type { EnvironmentTemplateResource } from "@agents-core-web/agents-client";

import i18n from "../../i18n";

export function templateName(template: Pick<EnvironmentTemplateResource, "name">): string {
  return template.name?.trim() || i18n.t("unnamed", { ns: "templates" });
}

/** Filters by name or ID. */
export function filterTemplates<T extends Pick<EnvironmentTemplateResource, "id" | "name">>(templates: readonly T[], query: string): T[] {
  const needle = query.trim().toLocaleLowerCase();
  return templates.filter((template) => [templateName(template), template.id].some((value) => value.toLocaleLowerCase().includes(needle)));
}
