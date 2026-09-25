import { useQuery } from "@tanstack/react-query";
import { Check, Compass, X } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip, StatusDot } from "../../components/console-ui";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { projectsQuery } from "../../lib/queries";
import { type FleetState } from "../fleet/use-sandbox-fleet";
import { useConsoleTour } from "../onboarding/ConsoleTour";
import {
  checklistView,
  gettingStartedSteps,
  readChecklistMemory,
  writeChecklistMemory,
  type ChecklistMemory,
  type StepState,
} from "./getting-started";

/**
 * Getting started: sandboxes, a project key and the first Session, each with
 * its state and one action, in any order. It shows until every step is done
 * or it is dismissed; the optional console tour opens from its header.
 */
export function GettingStarted({ fleet, sessions }: { fleet: FleetState; sessions: number | "failed" | null }) {
  const { t } = useTranslation("overview");
  const { navigate } = useConsoleNavigation();
  const openTour = useConsoleTour();
  const projects = useQuery(projectsQuery);
  const steps = gettingStartedSteps({ fleet, projects: projects.data ?? (projects.isError ? "failed" : undefined), sessions });
  const states = [steps.sandboxes.state, steps.key.state, steps.session];
  const [memory, setMemory] = useState<ChecklistMemory>(readChecklistMemory);
  const view = checklistView(states, memory);
  const allDone = states.every((state) => state === "done");

  const remember = (value: Exclude<ChecklistMemory, null>) => {
    writeChecklistMemory(value);
    setMemory(value);
  };
  // Seen with a step to do, the checklist ends with "You're set"; a deployment
  // first seen already set up never shows it.
  const first = memory !== null ? null : view === "full" ? "open" : allDone ? "closed" : null;
  useEffect(() => {
    if (!first) return;
    writeChecklistMemory(first);
    setMemory(first);
  }, [first]);

  if (view === "hidden") return null;
  const done = states.filter((state) => state === "done").length;
  const progress = t("gettingStarted.progress", { done, total: states.length });

  if (view === "compact") {
    return (
      <section className="overview-card getting-started getting-started-compact" aria-labelledby="getting-started-heading">
        <div className="console-section-title">
          <h2 id="getting-started-heading">{t("gettingStarted.title")}</h2>
          <span className="overview-card-meta">{progress}</span>
        </div>
        <button className="button outline" type="button" onClick={() => remember("open")}>{t("gettingStarted.show")}</button>
      </section>
    );
  }

  if (view === "complete") {
    return (
      <section className="overview-card getting-started getting-started-compact" aria-labelledby="getting-started-heading">
        <div className="console-section-title">
          <span className="getting-started-mark done" aria-hidden="true"><Check size={13} strokeWidth={2.2} /></span>
          <h2 id="getting-started-heading">{t("gettingStarted.complete.title")}</h2>
          <span className="overview-card-meta">{t("gettingStarted.complete.body")}</span>
        </div>
        <button className="button outline" type="button" onClick={() => remember("closed")}>{t("gettingStarted.complete.dismiss")}</button>
      </section>
    );
  }

  const sandbox = steps.sandboxes;
  const sandboxAction = sandbox.action === "setup"
    ? { label: t("gettingStarted.sandboxes.setup"), run: () => navigate("nodes") }
    : sandbox.action === "add-node"
      ? { label: t("gettingStarted.sandboxes.addNode"), run: () => navigate("nodes", {}, "add-node") }
      : { label: t(sandbox.cloud ? "gettingStarted.sandboxes.backend" : "gettingStarted.sandboxes.nodes"), run: () => navigate("nodes") };
  const keyProject = steps.key.project;
  const keyAction = keyProject
    ? { label: t("gettingStarted.key.issue"), run: () => navigate("projects", { id: keyProject.id }, "issue-key") }
    : { label: t("gettingStarted.key.create"), run: () => navigate("projects", {}, "create-project") };

  return (
    <section className="overview-card getting-started" aria-labelledby="getting-started-heading">
      <header className="overview-card-header">
        <div className="console-section-title">
          <h2 id="getting-started-heading">{t("gettingStarted.title")}</h2>
          <span className="overview-card-meta">{progress}</span>
          <HelpTip>{t("gettingStarted.help")}</HelpTip>
        </div>
        <div className="getting-started-actions">
          <button className="button ghost" type="button" onClick={(event) => openTour(event.currentTarget)}>
            <Compass size={14} aria-hidden="true" />{t("gettingStarted.tour")}
          </button>
          <button className="icon-button ghost" type="button" aria-label={t("gettingStarted.dismiss")} title={t("gettingStarted.dismiss")} onClick={() => remember("dismissed")}>
            <X size={15} aria-hidden="true" />
          </button>
        </div>
      </header>
      <ol className="getting-started-steps">
        <Step index={1} state={sandbox.state} title={t("gettingStarted.sandboxes.title")} body={t(sandbox.cloud ? "gettingStarted.sandboxes.bodyCloud" : "gettingStarted.sandboxes.body")} action={sandboxAction} />
        <Step index={2} state={steps.key.state} title={t("gettingStarted.key.title")} body={t("gettingStarted.key.body")} action={keyAction} />
        <Step index={3} state={steps.session} title={t("gettingStarted.session.title")} body={t("gettingStarted.session.body")} action={{ label: t("gettingStarted.session.open"), run: () => navigate("projects") }} />
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
