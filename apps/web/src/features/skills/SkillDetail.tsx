import { ArrowLeft, Download, Trash2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { AgentCoreError, type Skill, type SkillVersion } from "@oac/agents-client";

import type { ProjectClient } from "../../lib/projects";
import { collections } from "../../lib/queries";
import { skillQuery, skillVersionsQuery } from "../resources/detail-queries";

import { EmptyState, PageBody, PageHeader, RefreshButton, Section, StatusDot } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { DetailSkeleton, TableSkeleton } from "../../components/Skeleton";
import { useFailureToast, useToast } from "../../components/Toast";
import { formatDateTime, MISSING } from "../../lib/format";
import { CopyableId, LatestVersion } from "./skill-parts";
import {
  coreErrorMessage,
  downloadSkillArchive,
  isDefaultVersionConflict,
  readSkillVersionsPage,
  versionDeleteState,
  type VersionDeleteState,
} from "./skill-operations";
import "./skills.css";

export type SkillLoadStatus = "loading" | "ready" | "missing" | "failed";

export interface SkillVersionsState {
  status: "loading" | "ready" | "failed";
  items: SkillVersion[];
  nextAfter: string | null;
  error: string | null;
  loadingMore: boolean;
  moreError: string | null;
}

type Dialog =
  | { kind: "upload"; key: number }
  | { kind: "set-default"; version: SkillVersion }
  | { kind: "delete-version"; version: SkillVersion; only: boolean }
  | { kind: "delete-skill" }
  | null;

function isNotFound(error: unknown): boolean {
  return error instanceof AgentCoreError && error.status === 404;
}

export interface SkillDetailPageProps {
  skill: Skill | null;
  status: SkillLoadStatus;
  error: string | null;
  versions: SkillVersionsState;
  refreshing: boolean;
  /** The version ID, or "default", whose download is running. */
  downloading: string | null;
  onBack: () => void;
  onRefresh: () => void;
  onDownload: (version?: SkillVersion) => void;
  onDeleteSkill: () => void;
  onDeleteVersion: (version: SkillVersion, state: VersionDeleteState) => void;
  onLoadMoreVersions: () => void;
}

/** One Skill: its facts, header actions and the version table. */
export function SkillDetailPage({
  skill, status, error, versions, refreshing, downloading,
  onBack, onRefresh, onDownload, onDeleteSkill, onDeleteVersion, onLoadMoreVersions,
}: SkillDetailPageProps) {
  const { t, i18n } = useTranslation("skills");
  const locale = i18n.resolvedLanguage;
  const available = skill !== null && status !== "missing";
  const busy = downloading !== null;
  useFailureToast(skill !== null && status === "failed" && Boolean(error), t("detail.refreshFailed", { reason: error ?? "" }), "skill-refresh");
  return (
    <section className="page-section console-page skills-page" aria-labelledby="skill-detail-heading">
      <PageHeader
        headingId="skill-detail-heading"
        title={(
          <>
            <button type="button" className="icon-button ghost skill-back" aria-label={t("actions.back")} title={t("actions.back")} onClick={onBack}>
              <ArrowLeft size={16} strokeWidth={1.6} aria-hidden="true" />
            </button>
            {skill?.name ?? t("title")}
          </>
        )}
        actions={(
          <>
            <RefreshButton onClick={onRefresh} refreshing={refreshing} label={t("actions.refreshSkill")} />
            <button className="button outline" type="button" disabled={!available || busy} title={t("actions.downloadDefault")} onClick={() => onDownload()}>
              <Download size={14} aria-hidden="true" />{t("actions.download")}
            </button>
            <button className="button danger" type="button" disabled={!available} onClick={onDeleteSkill}>
              <Trash2 size={14} aria-hidden="true" />{t("actions.deleteSkill")}
            </button>
          </>
        )}
      />
      <PageBody>
        {status === "missing" ? (
          <EmptyState
            title={t("detail.missingTitle")}
            hint={t("detail.missingDescription")}
            action={<button className="button outline" type="button" onClick={onBack}>{t("actions.back")}</button>}
          />
        ) : !skill ? (
          status === "failed" ? (
            <EmptyState
              title={t("detail.loadFailed")}
              description={error ?? undefined}
              action={<button className="button outline" type="button" onClick={onRefresh}>{t("actions.retry")}</button>}
            />
          ) : <DetailSkeleton label={t("detail.loading")} />
        ) : (
          <>
            <dl className="skill-facts" aria-label={t("detail.facts")}>
              <div className="skill-fact-wide"><dt>{t("detail.description")}</dt><dd className="skill-fact-text">{skill.description || MISSING}</dd></div>
              <div><dt>{t("detail.id")}</dt><dd><CopyableId id={skill.id} /></dd></div>
              <div><dt>{t("detail.created")}</dt><dd>{formatDateTime(skill.created_at, locale)}</dd></div>
              <div><dt>{t("detail.defaultVersion")}</dt><dd>{t("version", { version: skill.default_version })}</dd></div>
              <div><dt>{t("detail.latestVersion")}</dt><dd><LatestVersion skill={skill} /></dd></div>
            </dl>
            <Section headingId="skill-versions-heading" title={t("detail.versions")} help={t("detail.versionsHelp")}>
              <SkillVersionsTable
                skill={skill}
                versions={versions}
                downloading={downloading}
                onRetry={onRefresh}
                onDownload={onDownload}
                onDeleteVersion={onDeleteVersion}
                onLoadMore={onLoadMoreVersions}
              />
            </Section>
          </>
        )}
      </PageBody>
    </section>
  );
}

function SkillVersionsTable({
  skill, versions, downloading, onRetry, onDownload, onDeleteVersion, onLoadMore,
}: {
  skill: Skill;
  versions: SkillVersionsState;
  downloading: string | null;
  onRetry: () => void;
  onDownload: (version: SkillVersion) => void;
  onDeleteVersion: (version: SkillVersion, state: VersionDeleteState) => void;
  onLoadMore: () => void;
}) {
  const { t, i18n } = useTranslation("skills");
  const locale = i18n.resolvedLanguage;
  useFailureToast(versions.items.length > 0 && versions.status === "failed" && Boolean(versions.error), t("detail.refreshFailed", { reason: versions.error ?? "" }), "skill-versions-refresh");
  useFailureToast(versions.moreError, t("list.moreFailed", { reason: versions.moreError ?? "" }), "skill-versions-more");
  if (!versions.items.length) {
    if (versions.status === "loading") return <TableSkeleton label={t("detail.versionsLoading")} rows={3} columns={6} />;
    if (versions.status === "failed") {
      return (
        <EmptyState
          title={t("detail.versionsFailed")}
          description={versions.error ?? undefined}
          action={<button className="button outline" type="button" onClick={onRetry}>{t("actions.retry")}</button>}
        />
      );
    }
    return <p className="page-status">{t("detail.versionsEmpty")}</p>;
  }
  const loaded = { count: versions.items.length, complete: versions.status === "ready" && versions.nextAfter === null };
  return (
    <>
      <div className="table-frame">
        <table className="data-table skills-table">
          <thead>
            <tr>
              <th scope="col">{t("detail.version")}</th>
              <th scope="col">{t("detail.name")}</th>
              <th scope="col">{t("detail.description")}</th>
              <th scope="col">{t("detail.created")}</th>
              <th scope="col">{t("detail.marks")}</th>
              <th scope="col"><span className="visually-hidden">{t("detail.actions")}</span></th>
            </tr>
          </thead>
          <tbody>
            {versions.items.map((version) => {
              const isDefault = version.version === skill.default_version;
              const isLatest = version.version === skill.latest_version;
              const deleteState = versionDeleteState(version, skill, loaded);
              const label = t("version", { version: version.version });
              return (
                <tr key={version.id}>
                  <th scope="row">{label}</th>
                  <td><span className="skill-cell-text">{version.name}</span></td>
                  <td><span className="skill-description" title={version.description}>{version.description || MISSING}</span></td>
                  <td className="skill-nowrap">{formatDateTime(version.created_at, locale)}</td>
                  <td>
                    <span className="skill-marks">
                      {isDefault ? <StatusDot tone="ok" label={t("detail.default")} /> : null}
                      {isLatest ? <StatusDot tone="neutral" label={t("detail.latest")} /> : null}
                    </span>
                  </td>
                  <td className="row-actions skill-row-actions">
                    <button
                      className="text-action"
                      type="button"
                      disabled={downloading !== null}
                      aria-label={t("actions.downloadVersion", { version: version.version })}
                      onClick={() => onDownload(version)}
                    >
                      {t("actions.download")}
                    </button>
                    {deleteState === "blocked-default" ? (
                      <span className="skill-disabled-action" title={t("detail.deleteBlocked")}>
                        <button className="text-action" type="button" disabled aria-label={`${t("actions.deleteVersion", { version: version.version })}. ${t("detail.deleteBlocked")}`}>
                          {t("actions.delete")}
                        </button>
                      </span>
                    ) : (
                      <button
                        className="text-action skill-danger-action"
                        type="button"
                        aria-label={t("actions.deleteVersion", { version: version.version })}
                        onClick={() => onDeleteVersion(version, deleteState)}
                      >
                        {t("actions.delete")}
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {versions.nextAfter ? (
        <footer className="table-footer skills-footer">
          <span>{t("list.loaded", { count: versions.items.length })}</span>
          <button className="button outline" type="button" disabled={versions.loadingMore} onClick={onLoadMore}>
            {versions.loadingMore ? t("actions.loadingMore") : t("actions.loadMore")}
          </button>
        </footer>
      ) : null}
    </>
  );
}

export interface SkillDetailProps {
  core: Pick<ProjectClient, "retrieveSkill" | "listSkillVersions" | "deleteSkill" | "deleteSkillVersion" | "downloadSkill" | "downloadSkillVersion">;
  /** The Skill's project; with the Skill ID it keys the cached reads. */
  projectId: string;
  skillId: string;
  initialSkill: Skill | null;
  onBack: () => void;
  /** Called with every fresh Skill read so the list row stays current. */
  onChanged: (skill: Skill) => void;
  /** Called once the Skill no longer exists because this page deleted it. */
  onDeleted: (skillId: string) => void;
}

export function SkillDetail({ core, projectId, skillId, initialSkill, onBack, onChanged, onDeleted }: SkillDetailProps) {
  const { t } = useTranslation("skills");
  const { t: tCommon } = useTranslation();
  const toast = useToast();
  const queryClient = useQueryClient();
  // The Skill opens from the cache (or its list row) at once; a refresh keeps it on screen.
  const skillRead = useQuery(skillQuery(projectId, skillId, core, initialSkill));
  const versionsOptions = skillVersionsQuery(projectId, skillId, core);
  const versionsRead = useQuery(versionsOptions);
  const [more, setMore] = useState<{ loading: boolean; error: string | null }>({ loading: false, error: null });
  const [downloading, setDownloading] = useState<string | null>(null);
  const [dialog, setDialog] = useState<Dialog>(null);
  const [lastDialog, setLastDialog] = useState<Dialog>(null);
  const [dialogBusy, setDialogBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [typedName, setTypedName] = useState("");
  const mounted = useRef(true);
  const dialogBusyRef = useRef(false);
  const callbacks = useRef({ onChanged, onDeleted });
  callbacks.current = { onChanged, onDeleted };

  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);

  const skill = skillRead.data ?? null;
  const skillError = skillRead.isError ? skillRead.error : null;
  const status: SkillLoadStatus = isNotFound(skillError) ? "missing" : skillError ? "failed" : skill ? "ready" : "loading";
  const error = skillError ? coreErrorMessage(skillError) : null;

  const freshSkill = skillRead.isPlaceholderData ? undefined : skillRead.data;
  useEffect(() => {
    if (freshSkill) callbacks.current.onChanged(freshSkill);
  }, [freshSkill, skillRead.dataUpdatedAt]);

  const versionsData = versionsRead.data;
  const versions: SkillVersionsState = {
    status: !versionsData && versionsRead.isFetching ? "loading" : versionsRead.isError ? "failed" : versionsData ? "ready" : "loading",
    items: versionsData?.values ?? [],
    nextAfter: versionsData?.nextAfter ?? null,
    error: versionsRead.isError ? coreErrorMessage(versionsRead.error) : null,
    loadingMore: more.loading,
    moreError: more.error,
  };

  const { refetch: refetchSkill } = skillRead;
  const { refetch: refetchVersions } = versionsRead;
  const load = useCallback(() => {
    setMore({ loading: false, error: null });
    void refetchSkill();
    void refetchVersions();
  }, [refetchSkill, refetchVersions]);

  /** The list shows default and latest versions; it reads again after any deletion here. */
  const listChanged = () => void queryClient.invalidateQueries({ queryKey: ["collection", ...collections.skills.key] });
  const deleted = () => {
    listChanged();
    callbacks.current.onDeleted(skillId);
    queryClient.removeQueries({ queryKey: skillQuery(projectId, skillId, core).queryKey });
  };

  const loadMoreVersions = async () => {
    const start = versionsRead.data;
    const after = start?.nextAfter;
    if (!start || !after || more.loading) return;
    setMore({ loading: true, error: null });
    try {
      const page = await readSkillVersionsPage(core, skillId, start.values, after);
      if (!mounted.current) return;
      // A refresh that finished meanwhile replaced the first page; this page followed the old one.
      if (queryClient.getQueryData(versionsOptions.queryKey) === start) queryClient.setQueryData(versionsOptions.queryKey, page);
      setMore({ loading: false, error: null });
    } catch (reason) {
      if (!mounted.current) return;
      setMore({ loading: false, error: coreErrorMessage(reason) });
    }
  };

  const download = async (version?: SkillVersion) => {
    if (!skill || downloading) return;
    setDownloading(version?.id ?? "default");
    try {
      await downloadSkillArchive(core, skill, version);
    } catch (reason) {
      if (mounted.current) toast.show(t("detail.downloadFailed", { reason: coreErrorMessage(reason) }), { tone: "error" });
    } finally {
      if (mounted.current) setDownloading(null);
    }
  };

  const openDialog = (next: Dialog) => {
    setDialogError(null);
    setTypedName("");
    setDialog(next);
    setLastDialog(next);
  };
  const closeDialog = () => {
    if (!dialogBusy) setDialog(null);
  };

  const runDialogAction = async (action: () => Promise<void>) => {
    if (dialogBusyRef.current) return;
    dialogBusyRef.current = true;
    setDialogBusy(true);
    setDialogError(null);
    try {
      await action();
    } catch (reason) {
      if (!mounted.current) return;
      if (isNotFound(reason)) {
        // Core does not say whether the Skill or the version is gone; a reload tells.
        setDialog(null);
        listChanged();
        load();
      } else if (isDefaultVersionConflict(reason)) {
        // Another client changed the versions; show the rule and reload.
        setDialog(null);
        toast.show(t("deleteVersion.defaultConflict"), { tone: "error" });
        listChanged();
        load();
      } else {
        setDialogError(t("detail.actionFailed", { reason: coreErrorMessage(reason) }));
      }
    } finally {
      dialogBusyRef.current = false;
      if (mounted.current) setDialogBusy(false);
    }
  };

  const confirmDeleteVersion = (version: SkillVersion, only: boolean) => runDialogAction(async () => {
    await core.deleteSkillVersion(skillId, version.version);
    if (!mounted.current) return;
    setDialog(null);
    if (only) {
      toast.show(t("deleteSkill.done", { name: skill?.name ?? skillId }), { tone: "success" });
      deleted();
      return;
    }
    toast.show(t("deleteVersion.done", { version: version.version }), { tone: "success" });
    // latest_version may fall back to a surviving version.
    listChanged();
    load();
  });

  const confirmDeleteSkill = () => runDialogAction(async () => {
    await core.deleteSkill(skillId);
    if (!mounted.current) return;
    setDialog(null);
    toast.show(t("deleteSkill.done", { name: skill?.name ?? skillId }), { tone: "success" });
    deleted();
  });

  // A closing dialog keeps its content while the exit animation runs.
  const shown = dialog ?? lastDialog;

  const cancelButton = (
    <button className="button outline" type="button" disabled={dialogBusy} onClick={closeDialog}>{tCommon("actions.cancel")}</button>
  );
  const dialogErrorNote = dialogError ? <p className="coverage-note coverage-note-error" role="alert">{dialogError}</p> : null;

  return (
    <>
      <SkillDetailPage
        skill={skill}
        status={status}
        error={error}
        versions={versions}
        refreshing={skillRead.isFetching || versionsRead.isFetching}
        downloading={downloading}
        onBack={onBack}
        onRefresh={load}
        onDownload={(version) => void download(version)}
        onDeleteSkill={() => openDialog({ kind: "delete-skill" })}
        onDeleteVersion={(version, state) => {
          if (state !== "blocked-default") openDialog({ kind: "delete-version", version, only: state === "only-version" });
        }}
        onLoadMoreVersions={() => void loadMoreVersions()}
      />
      <Modal
        open={dialog?.kind === "delete-version"}
        title={shown?.kind === "delete-version"
          ? shown.only ? t("deleteVersion.onlyTitle") : t("deleteVersion.title", { version: shown.version.version })
          : ""}
        onClose={closeDialog}
        footer={shown?.kind === "delete-version" ? (
          <>
            {cancelButton}
            <button className="button danger" type="button" disabled={dialogBusy} onClick={() => void confirmDeleteVersion(shown.version, shown.only)}>
              {dialogBusy ? t("deleteVersion.deleting") : shown.only ? t("deleteVersion.confirmSkill") : t("deleteVersion.confirm")}
            </button>
          </>
        ) : null}
      >
        {shown?.kind === "delete-version" ? (
          <div className="skill-confirm">
            {shown.only ? (
              <>
                <p className="coverage-note coverage-note-error skill-only-warning"><strong>{t("deleteVersion.onlyWarning")}</strong></p>
                <p>{t("deleteVersion.onlyBody")}</p>
              </>
            ) : <p>{t("deleteVersion.body")}</p>}
            {dialogErrorNote}
          </div>
        ) : null}
      </Modal>
      <Modal
        open={dialog?.kind === "delete-skill"}
        title={skill ? t("deleteSkill.title", { name: skill.name }) : ""}
        onClose={closeDialog}
        footer={shown?.kind === "delete-skill" && skill ? (
          <>
            {cancelButton}
            <button className="button danger" type="button" disabled={dialogBusy || typedName !== skill.name} onClick={() => void confirmDeleteSkill()}>
              {dialogBusy ? t("deleteSkill.deleting") : t("deleteSkill.confirm")}
            </button>
          </>
        ) : null}
      >
        {shown?.kind === "delete-skill" && skill ? (
          <div className="skill-confirm">
            <ul className="skill-confirm-list">
              <li>{t("deleteSkill.allVersions")}</li>
              <li>{t("deleteSkill.existingSessions")}</li>
              <li><strong>{t("deleteSkill.templates")}</strong></li>
            </ul>
            <label className="field">
              <span>{t("deleteSkill.typeName", { name: skill.name })}</span>
              <input value={typedName} onChange={(event) => setTypedName(event.target.value)} disabled={dialogBusy} autoComplete="off" spellCheck={false} />
            </label>
            {dialogErrorNote}
          </div>
        ) : null}
      </Modal>
    </>
  );
}
