import { SandboxAdminClient } from "@agents-core-web/agents-client";
import { ArrowRight } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { AppearanceMenu } from "../../components/AppearanceMenu";
import { HelpTip } from "../../components/console-ui";
import { useProjects } from "../../lib/projects";
import { ConsoleAccountMenu } from "../first-run/ConsoleAccess";
import { curlExample, isUsableName, keyNameProblem, normalizeName, projectNameProblem, type KeyFlow } from "./key-flows";
import { FlowErrorMessage, KeyNameField, NameField } from "./KeyFlowDialogs";
import { CommandBlock, PlaintextKey } from "./IssuedKey";
import { useKeyFlow } from "./use-key-flow";
import "./api-keys.css";

/** The Core origin callers use, when the sandbox deployment reports one. */
function useCoreOrigin(): string | null {
  const [origin, setOrigin] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    new SandboxAdminClient({ baseUrl: "/core/v1/sandbox" }).retrieveDeployment({ signal: controller.signal }).then(
      (deployment) => setOrigin(deployment.core_url || null),
      () => undefined,
    );
    return () => controller.abort();
  }, []);
  return origin;
}

function canSubmit(flow: Extract<KeyFlow, { step: "issue" }>): boolean {
  if (flow.busy || !isUsableName(flow.name, keyNameProblem(flow.name))) return false;
  return flow.project !== null || isUsableName(flow.projectName, projectNameProblem(flow.projectName));
}

/**
 * First run: the administrator creates the first project and its first named
 * key, then sees the plaintext once with a request the caller can run. The
 * console never sends that request. The shell leaves this screen only after
 * the operator confirms the key was saved.
 */
export function FirstProjectSetup({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation("keys");
  const { refresh } = useProjects();
  // The project list is refreshed only on "Done": refreshing earlier would
  // take the shell past this screen while the key is still on it.
  const controls = useKeyFlow();
  const { flow, dispatch } = controls;
  const coreOrigin = useCoreOrigin();

  useEffect(() => {
    if (flow.step === "idle") dispatch({ type: "openFirstRun" });
  }, [dispatch, flow.step]);

  const finish = () => {
    dispatch({ type: "saved" });
    refresh();
    onDone();
  };

  let content = null;
  if (flow.step === "issued") {
    content = (
      <>
        <h1 className="first-key-title">{t("firstRun.readyTitle")}</h1>
        <div className="first-key-block">
          <p className="plaintext-key-notice">{t("issued.onceNotice")}</p>
          <PlaintextKey value={flow.issued.key} label={t("issued.keyLabel", { name: flow.issued.name })} caption={t("firstRun.keyCaption", { key: flow.issued.name, project: flow.project.name })} />
        </div>
        <div className="first-key-block">
          <div className="first-key-label">
            <span>{t("firstRun.tryIt")}</span>
            <HelpTip>{t("firstRun.tryItHelp")}</HelpTip>
          </div>
          <CommandBlock value={curlExample(coreOrigin)} label={t("firstRun.command")} />
        </div>
        <div className="first-key-actions">
          <button className="button primary" type="button" onClick={finish}>
            {t("firstRun.continue")}<ArrowRight size={14} aria-hidden="true" />
          </button>
        </div>
      </>
    );
  } else if (flow.step === "issue") {
    const projectProblem = flow.project ? null : projectNameProblem(flow.projectName);
    content = (
      <>
        <h1 className="first-key-title">{t("firstRun.title")}</h1>
        <p className="first-key-lead">{t("firstRun.lead")}</p>
        <form className="first-key-form" onSubmit={(event) => { event.preventDefault(); if (canSubmit(flow)) void controls.submit(); }}>
          <NameField
            name="project-name"
            label={t("firstRun.projectName")}
            help={t("createDialog.nameHelp")}
            value={flow.projectName}
            onChange={(name) => dispatch({ type: "setProjectName", name })}
            problem={projectProblem}
            problemText={projectProblem ? t(`createDialog.problems.${projectProblem}`) : ""}
            // Once created, the project is kept and a retry only issues its key.
            disabled={flow.busy || flow.project !== null}
          />
          <KeyNameField flow={flow} controls={controls} taken={[]} autoFocus />
          {flow.error?.kind === "uncertain"
            // This screen has no list to refresh: check in the console instead of issuing a second key unseen.
            ? <p className="key-flow-error" role="alert">{t("firstRun.uncertain")}</p>
            : <FlowErrorMessage error={flow.error} name={flow.project ? normalizeName(flow.name) : normalizeName(flow.projectName)} />}
          {flow.project && flow.error?.kind === "rejected" ? <p className="first-key-status" role="status">{t("firstRun.projectCreated", { project: flow.project.name })}</p> : null}
          <div className="first-key-actions">
            {flow.error?.kind === "uncertain" ? (
              <button className="button outline" type="button" disabled={flow.busy} onClick={() => { refresh(); onDone(); }}>{t("firstRun.check")}</button>
            ) : null}
            <button className="button primary" type="submit" disabled={!canSubmit(flow)}>
              {flow.busy ? t("firstRun.submitting") : flow.project ? t("issueDialog.submit") : t("firstRun.submit")}
            </button>
          </div>
        </form>
      </>
    );
  }

  return (
    <div className="first-key-screen">
      <header className="first-key-top">
        <span className="brand-lockup">
          <span className="brand-mark-frame">
            <img className="brand-mark brand-mark-light" src="/parsar-mark-light.png" width="18" height="18" alt="" aria-hidden="true" />
            <img className="brand-mark brand-mark-dark" src="/parsar-mark-dark.png" width="18" height="18" alt="" aria-hidden="true" />
          </span>
          <span className="brand-name">Parsar Core</span>
        </span>
        <span className="first-key-top-actions">
          <ConsoleAccountMenu />
          <AppearanceMenu />
        </span>
      </header>
      <main className="first-key-main" id="main-content" tabIndex={-1}>
        <div className="first-key-column">{content}</div>
      </main>
    </div>
  );
}
