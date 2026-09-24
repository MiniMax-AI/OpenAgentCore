import { useTranslation } from "react-i18next";

import type { Project } from "@agents-core-web/agents-client";

import { StatusDot } from "../../components/console-ui";

export function ProjectStatus({ project }: { project: Project }) {
  const { t } = useTranslation("keys");
  return project.status === "archived"
    ? <StatusDot tone="neutral" label={t("status.archived")} />
    : <StatusDot tone="ok" label={t("status.active")} />;
}

/** Where a project comes from; config projects are managed by the static key file. */
export function ProjectSource({ project }: { project: Project }) {
  const { t } = useTranslation("keys");
  return <span className={project.source === "config" ? "project-source config" : "project-source"}>{t(`source.${project.source}`)}</span>;
}
