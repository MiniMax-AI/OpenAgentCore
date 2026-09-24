import { Puzzle, Upload } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { AgentCoreError, type AgentCore, type Skill } from "@agents-core-web/agents-client";

import { EmptyState, PageBody, PageHeader, RefreshButton } from "../../components/console-ui";
import { ListToolbar, listSummary, NameCell, SearchField } from "../../components/list-ui";
import { OwnerCell, OwnerHeading } from "../ownership/OwnerCell";
import { useOwners } from "../ownership/use-owners";
import { useToast } from "../../components/Toast";
import { formatDateTime, MISSING } from "../../lib/format";
import { LatestVersion } from "./skill-parts";
import { classifySkillsError, coreErrorMessage, filterSkills, isAbortError, readSkillsPage } from "./skill-operations";
import { SkillDetail } from "./SkillDetail";
import { SkillUploadDialog, type SkillUploadResult } from "./SkillUploadDialog";
import "./skills.css";

export { probeSkillsSupport, type SkillsSupport } from "./skill-operations";

export type SkillsListStatus = "loading" | "ready" | "storage-unavailable" | "unsupported" | "failed";

export interface SkillsListState {
  status: SkillsListStatus;
  skills: Skill[];
  /** Cursor of the next page, or null once Core reported the end. */
  nextAfter: string | null;
  /** A first-load failure, or a refresh failure while earlier rows stay visible. */
  error: string | null;
  /** The HTTP status that marked Skills unsupported. */
  unsupportedStatus: number | null;
  refreshing: boolean;
  loadingMore: boolean;
  moreError: string | null;
}

export const initialSkillsListState: SkillsListState = {
  status: "loading",
  skills: [],
  nextAfter: null,
  error: null,
  unsupportedStatus: null,
  refreshing: false,
  loadingMore: false,
  moreError: null,
};

const SKILL_EXAMPLE = `---
name: report
description: Create the weekly status report.
---

Collect the open items, group them by owner and write the report.`;

export interface SkillsListPageProps {
  state: SkillsListState;
  query: string;
  onQueryChange: (query: string) => void;
  onRefresh: () => void;
  onLoadMore: () => void;
  onOpen: (skill: Skill) => void;
  onUpload: () => void;
}

/** The Skill list: header, local filter, table and cursor pagination. */
export function SkillsListPage({ state, query, onQueryChange, onRefresh, onLoadMore, onOpen, onUpload }: SkillsListPageProps) {
  const { t, i18n } = useTranslation("skills");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const visible = useMemo(() => filterSkills(state.skills, query), [query, state.skills]);
  const skillIds = useMemo(() => state.skills.map((skill) => skill.id), [state.skills]);
  const owners = useOwners("skill", skillIds, state.status === "ready");
  const uploadButton = (
    <button className="button primary" type="button" disabled={state.status !== "ready"} onClick={onUpload}>
      <Upload size={14} aria-hidden="true" />{t("actions.upload")}
    </button>
  );
  const retry = <button className="button outline" type="button" onClick={onRefresh}>{t("actions.retry")}</button>;

  let body;
  if (state.status === "loading") {
    body = <p className="page-status" role="status">{t("list.loading")}</p>;
  } else if (state.status === "storage-unavailable") {
    body = <EmptyState icon={Puzzle} title={t("storage.title")} description={t("storage.description")} action={retry} />;
  } else if (state.status === "unsupported") {
    body = <EmptyState icon={Puzzle} title={t("unsupported.title")} description={t("unsupported.description", { status: state.unsupportedStatus ?? MISSING })} />;
  } else if (state.status === "failed") {
    body = <EmptyState title={t("list.loadFailed")} description={state.error ?? undefined} action={retry} />;
  } else if (!state.skills.length) {
    body = (
      <>
        {state.error ? <p className="coverage-note coverage-note-error" role="alert">{t("list.refreshFailed", { reason: state.error })}</p> : null}
        <EmptyState
          icon={Puzzle}
          title={t("empty.title")}
          description={t("empty.description")}
          action={(
            <div className="skills-empty-action">
              <figure className="skill-example">
                <figcaption>{t("empty.exampleLabel")}</figcaption>
                <pre>{SKILL_EXAMPLE}</pre>
              </figure>
              {uploadButton}
            </div>
          )}
        />
      </>
    );
  } else {
    body = (
      <>
        {state.error ? <p className="coverage-note coverage-note-error" role="alert">{t("list.refreshFailed", { reason: state.error })}</p> : null}
        <ListToolbar
          label={t("list.filterLabel")}
          summary={listSummary(tCommon, visible.length, state.skills.length, { hasMore: Boolean(state.nextAfter), locale })}
        >
          <SearchField value={query} onChange={onQueryChange} placeholder={t("list.filterPlaceholder")} label={t("list.filterLabel")} />
        </ListToolbar>
        {visible.length ? (
          <div className="table-frame">
            <table className="data-table skills-table" aria-label={t("list.label")}>
              <thead>
                <tr>
                  <th scope="col">{t("list.name")}</th>
                  <th scope="col">{t("list.description")}</th>
                  <th scope="col">{t("list.defaultVersion")}</th>
                  <th scope="col">{t("list.latestVersion")}</th>
                  <th scope="col">{t("list.created")}</th>
                  {owners.available ? <th scope="col"><OwnerHeading /></th> : null}
                </tr>
              </thead>
              <tbody>
                {visible.map((skill) => (
                  <tr key={skill.id} className="clickable-row" onClick={() => onOpen(skill)}>
                    <th scope="row">
                      <NameCell name={skill.name} id={skill.id} onOpen={() => onOpen(skill)} openLabel={t("actions.open", { name: skill.name })} />
                    </th>
                    <td><span className="skill-description" title={skill.description}>{skill.description || MISSING}</span></td>
                    <td className="skill-nowrap">{t("version", { version: skill.default_version })}</td>
                    <td className="skill-nowrap"><LatestVersion skill={skill} /></td>
                    <td className="skill-nowrap">{formatDateTime(skill.created_at, locale)}</td>
                    {owners.available ? <td><OwnerCell record={owners.ownerOf(skill.id)} /></td> : null}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <EmptyState
            title={t("list.noMatchTitle")}
            description={t("list.noMatchDescription")}
            action={<button className="button outline" type="button" onClick={() => onQueryChange("")}>{t("actions.clearFilter")}</button>}
          />
        )}
        {state.moreError ? <p className="coverage-note coverage-note-error" role="alert">{t("list.moreFailed", { reason: state.moreError })}</p> : null}
        {state.nextAfter ? (
          <footer className="table-footer">
            <button className="button outline" type="button" disabled={state.loadingMore} onClick={onLoadMore}>
              {state.loadingMore ? t("actions.loadingMore") : t("actions.loadMore")}
            </button>
          </footer>
        ) : null}
      </>
    );
  }

  return (
    <section className="page-section console-page skills-page" aria-labelledby="skills-heading">
      <PageHeader
        headingId="skills-heading"
        title={t("title")}
        help={<>{t("help")} {t("limits")}</>}
        actions={(
          <>
            <RefreshButton onClick={onRefresh} refreshing={state.status === "loading" || state.refreshing} label={t("actions.refresh")} />
            {uploadButton}
          </>
        )}
      />
      <PageBody>{body}</PageBody>
    </section>
  );
}

export interface SkillsViewProps {
  /** The browser's Agents API client; Skill requests never carry a key of their own. */
  core: AgentCore;
  /** Called when Core answers the Skill list with 404 or 405, so the entry can be hidden. */
  onUnsupported?: () => void;
  /** Opens this Skill's detail directly, for example from an Environment Template. */
  initialSkillId?: string;
}

/** Skills: every Skill of the project, with upload, versions, default pointer, download and deletion. */
export function SkillsView({ core, onUnsupported, initialSkillId }: SkillsViewProps) {
  const { t } = useTranslation("skills");
  const toast = useToast();
  const [state, setState] = useState<SkillsListState>(initialSkillsListState);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<{ id: string; skill: Skill | null } | null>(initialSkillId ? { id: initialSkillId, skill: null } : null);
  const [upload, setUpload] = useState({ open: false, key: 0 });
  const request = useRef(0);
  const abortRef = useRef<AbortController | null>(null);
  const moreAbortRef = useRef<AbortController | null>(null);
  const unsupportedRef = useRef(onUnsupported);
  unsupportedRef.current = onUnsupported;

  const reload = useCallback(async () => {
    abortRef.current?.abort();
    moreAbortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const id = request.current + 1;
    request.current = id;
    setState((current) => ({
      ...current,
      status: current.status === "ready" ? "ready" : "loading",
      refreshing: true,
      loadingMore: false,
      moreError: null,
    }));
    try {
      const page = await readSkillsPage(core, [], undefined, controller.signal);
      if (id !== request.current) return;
      setState({ ...initialSkillsListState, status: "ready", skills: page.values, nextAfter: page.nextAfter });
    } catch (error) {
      if (id !== request.current || isAbortError(error)) return;
      const kind = classifySkillsError(error);
      if (kind === "unsupported") {
        setState({ ...initialSkillsListState, status: "unsupported", unsupportedStatus: error instanceof AgentCoreError ? error.status : null });
        unsupportedRef.current?.();
      } else if (kind === "storage-unavailable") {
        setState({ ...initialSkillsListState, status: "storage-unavailable" });
      } else {
        const message = coreErrorMessage(error);
        setState((current) => current.status === "ready"
          ? { ...current, refreshing: false, error: message }
          : { ...initialSkillsListState, status: "failed", error: message });
      }
    } finally {
      if (abortRef.current === controller) abortRef.current = null;
    }
  }, [core]);

  useEffect(() => {
    void reload();
    return () => {
      request.current += 1;
      abortRef.current?.abort();
      moreAbortRef.current?.abort();
    };
  }, [reload]);

  const loadMore = async () => {
    const after = state.nextAfter;
    if (!after || state.loadingMore) return;
    const loaded = state.skills;
    const id = request.current;
    const controller = new AbortController();
    moreAbortRef.current = controller;
    setState((current) => ({ ...current, loadingMore: true, moreError: null }));
    try {
      const page = await readSkillsPage(core, loaded, after, controller.signal);
      if (id !== request.current) return;
      const appended = page.values.slice(loaded.length);
      setState((current) => ({
        ...current,
        skills: [...current.skills, ...appended.filter((skill) => !current.skills.some((existing) => existing.id === skill.id))],
        nextAfter: page.nextAfter,
        loadingMore: false,
      }));
    } catch (error) {
      if (id !== request.current || isAbortError(error)) return;
      setState((current) => ({ ...current, loadingMore: false, moreError: coreErrorMessage(error) }));
    } finally {
      if (moreAbortRef.current === controller) moreAbortRef.current = null;
    }
  };

  const replaceSkill = useCallback((skill: Skill) => {
    setState((current) => current.skills.some((existing) => existing.id === skill.id)
      ? { ...current, skills: current.skills.map((existing) => existing.id === skill.id ? skill : existing) }
      : current);
  }, []);

  const uploaded = (result: SkillUploadResult) => {
    if (result.kind !== "skill") return;
    setUpload((current) => ({ ...current, open: false }));
    toast.show(t("upload.created", { name: result.skill.name }), { tone: "success" });
    setSelected({ id: result.skill.id, skill: result.skill });
    void reload();
  };

  if (selected) {
    return (
      <SkillDetail
        key={selected.id}
        core={core}
        skillId={selected.id}
        initialSkill={selected.skill}
        onBack={() => setSelected(null)}
        onChanged={replaceSkill}
        onDeleted={(skillId) => {
          setSelected(null);
          setState((current) => ({ ...current, skills: current.skills.filter((skill) => skill.id !== skillId) }));
          void reload();
        }}
      />
    );
  }

  return (
    <>
      <SkillsListPage
        state={state}
        query={query}
        onQueryChange={setQuery}
        onRefresh={() => void reload()}
        onLoadMore={() => void loadMore()}
        onOpen={(skill) => setSelected({ id: skill.id, skill })}
        onUpload={() => setUpload((current) => ({ open: true, key: current.key + 1 }))}
      />
      <SkillUploadDialog
        key={upload.key}
        open={upload.open}
        core={core}
        target={{ kind: "skill" }}
        onClose={() => setUpload((current) => ({ ...current, open: false }))}
        onUploaded={uploaded}
        onInterrupted={() => void reload()}
      />
    </>
  );
}
