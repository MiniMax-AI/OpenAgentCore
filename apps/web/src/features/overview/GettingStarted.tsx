import { useQuery } from "@tanstack/react-query";
import { Check, Compass, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useReducer, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip, StatusDot } from "../../components/console-ui";
import { useConsoleIntent, useConsoleNavigation } from "../../lib/console-navigation";
import { projectsQuery } from "../../lib/queries";
import { type FleetState } from "../fleet/use-sandbox-fleet";
import { useConsoleTour } from "../onboarding/ConsoleTour";
import { harnessesQuery } from "../system/harness-queries";
import {
  checklistStorageKey,
  checklistView,
  gettingStartedSteps,
  isCelebrating,
  readChecklistMemory,
  rememberInstallation,
  setCelebrating,
  writeChecklistMemory,
  type ChecklistMemory,
  type StepState,
} from "./getting-started";

/**
 * Getting started: sandboxes, a default model, a project key and the first
 * Session, each with its state and one action, in any order. It shows until every step is done
 * or it is hidden; Show Getting started in the sidebar opens it again. The
 * optional console tour opens from it.
 */
export function GettingStarted({ fleet, sessions, localOnly, onRetryInstallation }: { fleet: FleetState; sessions: number | "failed" | null; localOnly: boolean | "failed" | undefined; onRetryInstallation: () => void }) {
  const { t } = useTranslation("overview");
  const { navigate } = useConsoleNavigation();
  const openTour = useConsoleTour();
  const projects = useQuery(projectsQuery);
  const harnesses = useQuery(harnessesQuery);
  const steps = gettingStartedSteps({
    fleet, sessions, localOnly,
    projects: projects.data ?? (projects.isError ? "failed" : undefined),
    harnesses: harnesses.data?.data ?? (harnesses.isError ? "failed" : undefined),
  });
  const states = [steps.sandboxes.state, steps.model, steps.key.state, steps.session.state];
  const allDone = states.every((state) => state === "done");

  // Remembered per installation; a choice made here overrides what was read.
  const installationId = fleet.status === "ready" ? fleet.snapshot.deployment.installation_id : "";
  useEffect(() => { if (installationId) rememberInstallation(installationId); }, [installationId]);
  const storageKey = checklistStorageKey(fleet);
  const stored = useMemo(() => (storageKey === null ? null : readChecklistMemory(storageKey)), [storageKey]);
  const [chosen, setChosen] = useState<{ key: string; memory: ChecklistMemory } | null>(null);
  const memory = chosen && chosen.key === storageKey ? chosen.memory : stored;
  const remember = useCallback((value: Exclude<ChecklistMemory, null>) => {
    if (storageKey === null) return;
    writeChecklistMemory(storageKey, value);
    setChosen({ key: storageKey, memory: value });
  }, [storageKey]);
  // "You're set" stays until it is dismissed, while the checklist is already closed; it outlasts the tour.
  const [, rerender] = useReducer((count: number) => count + 1, 0);
  const celebrating = storageKey !== null && isCelebrating(storageKey);
  const celebrate = useCallback((on: boolean) => {
    if (storageKey === null) return;
    setCelebrating(storageKey, on);
    rerender();
  }, [storageKey]);
  const [focusPending, setFocusPending] = useState(false);
  const view = storageKey === null ? "hidden" : localOnly === "failed" && memory !== "closed" ? "full" : checklistView(states, memory);

  // Shown with a step to do, the checklist opens; "You're set" closes it once
  // shown, so a later regression never brings it back; a deployment first seen
  // already set up is closed without either.
  const next = view === "complete" ? "closed" : memory === null && storageKey !== null ? (view === "full" ? "open" : allDone ? "closed" : null) : null;
  useEffect(() => {
    if (!next) return;
    if (view === "complete") celebrate(true);
    remember(next);
  }, [celebrate, next, remember, view]);

  // Show Getting started, from the sidebar.
  useConsoleIntent("getting-started", storageKey === null ? "wait" : "ready", () => {
    celebrate(false);
    remember("open");
    setFocusPending(true);
  });
  const showing = (celebrating && allDone) || view !== "hidden";
  useEffect(() => {
    if (!focusPending || !showing) return;
    setFocusPending(false);
    document.getElementById("getting-started-heading")?.focus();
  }, [focusPending, showing]);

  const tourButton = (
    <button className="button ghost" type="button" data-tour-opener="" onClick={(event) => openTour(event.currentTarget)}>
      <Compass size={14} aria-hidden="true" />{t("gettingStarted.tour")}
    </button>
  );

  if ((celebrating && allDone) || view === "complete") {
    return (
      <section className="overview-card getting-started getting-started-line" aria-labelledby="getting-started-heading">
        <div className="console-section-title">
          <span className="getting-started-mark done" aria-hidden="true"><Check size={13} strokeWidth={2.2} /></span>
          <h2 id="getting-started-heading" tabIndex={-1}>{t("gettingStarted.complete.title")}</h2>
          <span className="overview-card-meta">{t("gettingStarted.complete.body")}</span>
        </div>
        <div className="getting-started-actions">
          {tourButton}
          <button className="button outline" type="button" onClick={() => { celebrate(false); remember("closed"); }}>{t("gettingStarted.complete.dismiss")}</button>
        </div>
      </section>
    );
  }
  if (view === "hidden") return null;

  const done = states.filter((state) => state === "done").length;
  const sandbox = steps.sandboxes;
  const sandboxAction = localOnly === "failed"
    ? { label: t("actions.retry", { ns: "common" }), run: onRetryInstallation }
    : sandbox.action === "setup"
    ? { label: t("gettingStarted.sandboxes.setup"), run: () => navigate("nodes") }
    : sandbox.action === "add-node"
      ? { label: t("gettingStarted.sandboxes.addNode"), run: () => navigate("nodes", {}, "add-node") }
      : { label: t(sandbox.cloud ? "gettingStarted.sandboxes.backend" : "gettingStarted.sandboxes.nodes"), run: () => navigate("nodes") };
  const keyProject = steps.key.project;
  const keyAction = keyProject
    ? { label: t("gettingStarted.key.issue"), run: () => navigate("projects", { id: keyProject.id }, "issue-key") }
    : { label: t("gettingStarted.key.create"), run: () => navigate("projects", {}, "create-project") };
  const callProject = steps.session.project;
  const sessionAction = callProject
    ? { label: t("gettingStarted.session.howToCall"), run: () => navigate("projects", { id: callProject.id }, "how-to-call") }
    : { label: t("gettingStarted.session.open"), run: () => navigate("projects") };

  return (
    <section className="overview-card getting-started" aria-labelledby="getting-started-heading">
      <header className="overview-card-header">
        <div className="console-section-title">
          <h2 id="getting-started-heading" tabIndex={-1}>{t("gettingStarted.title")}</h2>
          <span className="overview-card-meta">{t("gettingStarted.progress", { done, total: states.length })}</span>
          <HelpTip>{t("gettingStarted.help")}</HelpTip>
        </div>
        <div className="getting-started-actions">
          {tourButton}
          <button className="icon-button ghost" type="button" aria-label={t("gettingStarted.dismiss")} title={t("gettingStarted.dismiss")} onClick={() => remember("closed")}>
            <X size={15} aria-hidden="true" />
          </button>
        </div>
      </header>
      <ol className="getting-started-steps">
        <Step index={1} state={sandbox.state} title={t("gettingStarted.sandboxes.title")} body={localOnly === "failed" ? t("gettingStarted.sandboxes.addressFailed") : localOnly ? t("gettingStarted.sandboxes.localOnly") : t(sandbox.cloud ? "gettingStarted.sandboxes.bodyCloud" : "gettingStarted.sandboxes.body")} action={sandboxAction} />
        <Step index={2} state={steps.model} title={t("gettingStarted.model.title")} body={t("gettingStarted.model.body")} action={{ label: t("gettingStarted.model.open"), run: () => navigate("system", {}, "default-model") }} />
        <Step index={3} state={steps.key.state} title={t("gettingStarted.key.title")} body={t("gettingStarted.key.body")} action={keyAction} />
        <Step index={4} state={steps.session.state} title={t("gettingStarted.session.title")} body={t("gettingStarted.session.body")} action={sessionAction} />
      </ol>
    </section>
  );
}

function Step({ index, state, title, body, action }: {
  index: number;
  state: StepState;
  title: string;
  body: ReactNode;
  action: { label: string; run: () => void };
}) {
  const { t } = useTranslation("overview");
  const done = state === "done";
  return (
    <li className={done ? "getting-started-step done" : "getting-started-step"}>
      <span className={done ? "getting-started-mark done" : "getting-started-mark"} aria-hidden="true">
        {done ? <Check size={13} strokeWidth={2.2} /> : index}
      </span>
      <div className="getting-started-text">
        <h3>{title}</h3>
        <p>{body}</p>
      </div>
      <StatusDot tone={done ? "ok" : state === null ? "pending" : "neutral"} label={t(`gettingStarted.state.${state ?? "checking"}`)} />
      <div className="getting-started-action">
        {done ? null : <button className="button outline" type="button" onClick={action.run}>{action.label}</button>}
      </div>
    </li>
  );
}
