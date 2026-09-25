import type { Skill } from "@agents-core-web/agents-client";
import { Puzzle } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { useFailureToast } from "../../components/Toast";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { EmptyState, PageBody, PageHeader, RefreshButton } from "../../components/console-ui";
import { ListToolbar, listSummary, NameCell, RowActions, SearchField } from "../../components/list-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { useDeleteFlow } from "../../lib/delete-flow";
import { formatDateTime, MISSING } from "../../lib/format";
import { CreatorCell, CreatorHeading, forgetCreators, ProjectFilter, ProjectName, projectClient, useCreators, useProjectCollection, useProjects, type Owned } from "../../lib/projects";
import { CopyDialog, type CopySource } from "../copy/CopyDialog";
import { SkillDetail } from "./SkillDetail";
import { LatestVersion } from "./skill-parts";
import { filterSkills } from "./skill-operations";
import "./skills.css";
import { collections } from "../../lib/queries";
import { TableSkeleton } from "../../components/Skeleton";

/**
 * Resources › Skills: every project's Skills. The console views, downloads,
 * deletes and copies them; uploads and default-version changes belong to the
 * project's own keys.
 */
export function SkillsPage() {
  const { params, navigate, back } = useConsoleNavigation();
  const { byId } = useProjects();
  const project = params.project ? byId.get(params.project) : undefined;
  const [copy, setCopy] = useState<CopySource | null>(null);

  if (params.project && params.id) {
    return (
      <>
        <SkillDetail
          key={`${params.project}:${params.id}`}
          core={projectClient(params.project)}
          projectId={params.project}
          skillId={params.id}
          initialSkill={null}
          onBack={() => back("skills", { project: params.project })}
          onChanged={() => undefined}
          onDeleted={() => { forgetCreators(); back("skills", { project: params.project }); }}
          onCopy={project ? (skill) => setCopy({ type: "skill", id: skill.id, name: skill.name, project }) : undefined}
        />
        <CopyDialog source={copy} onClose={() => setCopy(null)} />
      </>
    );
  }
  return <SkillsList />;
}

function SkillsList() {
  const { t, i18n } = useTranslation("skills");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const { params, navigate } = useConsoleNavigation();
  const { byId } = useProjects();
  const [filter, setFilter] = useState(params.project ?? "");
  const [query, setQuery] = useState("");
  const [copy, setCopy] = useState<CopySource | null>(null);
  const collection = useProjectCollection(collections.skills, filter);
  const rows = useMemo(() => {
    const visible = new Set(filterSkills(collection.items.map((row) => row.value), query));
    return collection.items.filter((row) => visible.has(row.value)).sort((a, b) => b.value.created_at - a.value.created_at);
  }, [collection.items, query]);
  const creators = useCreators("skill", useMemo(() => collection.items.map((row) => ({ projectId: row.project.id, id: row.value.id })), [collection.items]));
  const refresh = useCallback(() => { forgetCreators(); collection.refresh(); }, [collection]);
  const remove = useDeleteFlow<Owned<Skill>>(
    useCallback((row: Owned<Skill>) => projectClient(row.project.id).deleteSkill(row.value.id), []),
    refresh,
    { uncertain: tCommon("list.deleteUncertain") },
  );
  const showProject = !filter;

  // Projects that could not be read are reported in a toast; the list shows the rest.
  const failedNames = collection.failures.map((failure) => failure.project.name).join(", ");
  useFailureToast(collection.items.length > 0 && collection.failures.length > 0, tCommon("project.partial", { names: failedNames }), "skills-partial");
  let body;
  if (collection.status === "loading" && !collection.items.length) {
    body = <TableSkeleton label={t("list.loading")} columns={6} />;
  } else if (!collection.items.length && !collection.failures.length) {
    body = <EmptyState icon={Puzzle} title={t("empty.title")} />;
  } else if (!collection.items.length) {
    body = <EmptyState title={tCommon("project.failed", { names: failedNames })} description={collection.failures[0]?.message} action={<button className="button outline" type="button" onClick={collection.refresh}>{tCommon("actions.retry")}</button>} />;
  } else {
    body = (
      <>
        <ListToolbar label={t("list.filterLabel")} summary={listSummary(tCommon, rows.length, collection.items.length, { locale })}>
          <ProjectFilter value={filter} onChange={setFilter} />
          <SearchField value={query} onChange={setQuery} placeholder={t("list.filterPlaceholder")} label={t("list.filterLabel")} />
        </ListToolbar>
        {rows.length ? (
          <div className="table-frame">
            <table className="data-table skills-table" aria-label={t("list.label")}>
              <thead>
                <tr>
                  <th scope="col">{t("list.name")}</th>
                  {showProject ? <th scope="col">{tCommon("project.column")}</th> : null}
                  <th scope="col">{t("list.description")}</th>
                  <th scope="col">{t("list.defaultVersion")}</th>
                  <th scope="col">{t("list.latestVersion")}</th>
                  <th scope="col">{t("list.created")}</th>
                  <th scope="col"><CreatorHeading /></th>
                  <th scope="col"><span className="visually-hidden">{tCommon("list.actions")}</span></th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const skill = row.value;
                  const open = () => navigate("skills", { project: row.project.id, id: skill.id });
                  return (
                    <tr key={`${row.project.id}:${skill.id}`} className="clickable-row" onClick={open}>
                      <th scope="row"><NameCell name={skill.name} id={skill.id} onOpen={open} openLabel={t("actions.open", { name: skill.name })} /></th>
                      {showProject ? <td><ProjectName project={byId.get(row.project.id) ?? row.project} /></td> : null}
                      <td><span className="skill-description" title={skill.description}>{skill.description || MISSING}</span></td>
                      <td className="skill-nowrap">{t("version", { version: skill.default_version })}</td>
                      <td className="skill-nowrap"><LatestVersion skill={skill} /></td>
                      <td className="skill-nowrap">{formatDateTime(skill.created_at, locale)}</td>
                      <td><CreatorCell creator={creators.creatorOf(row.project.id, skill.id)} /></td>
                      <td className="actions-cell" onClick={(event) => event.stopPropagation()}>
                        <RowActions>
                          <button className="text-action" type="button" aria-label={tCommon("copy.actionLabel", { name: skill.name })} onClick={() => setCopy({ type: "skill", id: skill.id, name: skill.name, project: row.project })}>
                            {tCommon("copy.action")}
                          </button>
                          <button className="text-action danger" type="button" aria-label={t("deleteSkill.title", { name: skill.name })} onClick={() => remove.ask(row)}>
                            {t("actions.delete")}
                          </button>
                        </RowActions>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <EmptyState
            title={t("list.noMatchTitle")}
            action={<button className="button outline" type="button" onClick={() => setQuery("")}>{tCommon("actions.clearSearch")}</button>}
          />
        )}
      </>
    );
  }

  return (
    <section className="page-section console-page skills-page" aria-labelledby="skills-heading">
      <PageHeader
        headingId="skills-heading"
        title={t("title")}
        help={t("help")}
        actions={<RefreshButton onClick={refresh} refreshing={collection.status === "loading"} label={t("actions.refresh")} />}
      />
      <PageBody>{body}</PageBody>
      <CopyDialog source={copy} onClose={() => setCopy(null)} />
      <ConfirmDialog
        open={remove.target !== null}
        title={remove.target ? t("deleteSkill.title", { name: remove.target.value.name }) : ""}
        confirmLabel={t("deleteSkill.confirm")}
        busyLabel={t("deleteSkill.deleting")}
        busy={remove.busy}
        error={remove.error}
        onConfirm={() => void remove.confirm()}
        onClose={remove.cancel}
      >
        <p>{t("deleteSkill.allVersions")} {t("deleteSkill.existingSessions")}</p>
        <p>{t("deleteSkill.templates")}</p>
      </ConfirmDialog>
    </section>
  );
}
