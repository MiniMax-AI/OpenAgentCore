import type { AdminProject } from "@oac/agents-client";
import { useTranslation } from "react-i18next";


import { StatusDot } from "../../components/console-ui";

export function ProjectStatus({ project }: { project: AdminProject }) {
  const { t } = useTranslation("keys");
  return project.archived_at !== null
    ? <StatusDot tone="neutral" label={t("status.archived")} />
    : <StatusDot tone="ok" label={t("status.active")} />;
}
