import { useTranslation } from "react-i18next";


import { StatusDot } from "../../components/console-ui";
import { type Project } from "../../lib/admin-view";

export function ProjectStatus({ project }: { project: Project }) {
  const { t } = useTranslation("keys");
  return project.status === "archived"
    ? <StatusDot tone="neutral" label={t("status.archived")} />
    : <StatusDot tone="ok" label={t("status.active")} />;
}
