import { ArrowLeft, ArrowRight, Bot, Check, Code2, Copy, Server } from "lucide-react";
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import type { AgentCore, SavedAgent } from "@agents-core-web/agents-client";
import { useTranslation } from "react-i18next";
import type { CoreConnection } from "../../lib/connection";
import { SandboxManagerView } from "../sandbox/SandboxManagerView";
import { FirstRequest } from "./FirstRequest";
import { AppearanceMenu } from "../../components/AppearanceMenu";
import { ConsoleAccountMenu } from "./ConsoleAccess";
import { ApiKeyPanel } from "../api-keys/ApiKeyPanel";
import "./FirstRunHome.css";

export type FirstRunStep = 0 | 1 | 2;
export interface FirstRunHomeProps {
  connection: CoreConnection;
  core: AgentCore;
  username?: string;
  active?: boolean;
  initialStep?: FirstRunStep;
  onStepChange?: (step: FirstRunStep) => void;
  onOpenAgent: (id: string) => void;
  onDone: () => void;
  onRefresh?: () => void;
}

export function FirstRunHome({ active = true, ...props }: FirstRunHomeProps) {
  return active ? <FirstRunBody key={`${props.connection.baseUrl}:${props.username ?? ""}`} {...props} /> : null;
}

function FirstRunBody({ connection, core, username, initialStep = 0, onStepChange, onOpenAgent, onDone, onRefresh }: FirstRunHomeProps) {
  const { t, i18n } = useTranslation("firstRun");
  const locale = i18n.resolvedLanguage || "en";
  const [step, setStep] = useState<FirstRunStep>(initialStep);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const [created, setCreated] = useState(false);
  const [keyReady, setKeyReady] = useState(false);
  const container = useRef<HTMLDivElement>(null);
  const stepHeading = useRef<HTMLHeadingElement>(null);
  const focusAfterStep = useRef<HTMLButtonElement | null>(null);
  const userChangedStep = useRef(false);
  useEffect(() => { setStep(initialStep); }, [initialStep]);
  useEffect(() => {
    if (!userChangedStep.current) return;
    userChangedStep.current = false;
    const trigger = focusAfterStep.current;
    focusAfterStep.current = null;
    container.current?.scrollTo({ top: 0, behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : "smooth" });
    if (trigger && document.visibilityState === "visible"
      && (document.activeElement === trigger || document.activeElement === document.body)) {
      stepHeading.current?.focus({ preventScroll: true });
    }
  }, [step]);
  const lifetime = useRef(0);
  useEffect(() => () => { lifetime.current++; }, []);
  const origin = window.location.origin;
  const account = username || t("Signed-in administrator");
  const scope = `${origin}:${connection.baseUrl}:${username ?? ""}`;
  const go = (next: FirstRunStep, trigger: HTMLButtonElement) => {
    if (next === step) return;
    userChangedStep.current = true;
    focusAfterStep.current = trigger.closest(".first-run-pipeline") ? null : trigger;
    setStep(next); onStepChange?.(next);
  };
  async function copyAccess() {
    const generation = lifetime.current;
    try {
      await navigator.clipboard.writeText(`${t("Console address")}: ${origin}\n${t("Administrator")}: ${account}`);
      if (generation === lifetime.current) { setCopied(true); setCopyFailed(false); }
    } catch { if (generation === lifetime.current) { setCopyFailed(true); setCopied(false); } }
  }
  function agentCreated(_agent: SavedAgent) { setCreated(true); onRefresh?.(); }
  const heading = (title: string, description: string, aside?: ReactNode) => (
    <div className="first-run-step-heading">
      <h2 ref={stepHeading} tabIndex={-1}>{title}</h2>
      <p>{description}</p>
      {aside}
    </div>
  );
  return <main className="first-run-home" lang={locale} aria-label={t("First steps")}>
    <header className="first-run-toolbar">
      <div className="first-run-toolbar-preferences"><ConsoleAccountMenu /><AppearanceMenu /></div>
      <button className="first-run-skip" type="button" onClick={onDone}>{t("Skip introduction")}<ArrowRight size={14} /></button>
    </header>
    <FirstRunPipeline step={step} keyReady={keyReady} created={created} onSelect={go} />
    <div ref={container} className={`first-run-stage first-run-step-${step}`}>
      <div className="first-run-stage-content" key={step}>
        {step === 0 ? <>
          {heading(t("Keep your sign-in details."), t("Use this address and administrator account to return to your console."))}
          <div className="first-run-access">
            <section className="first-run-panel" aria-labelledby="first-run-access-title">
              <h3 id="first-run-access-title">{t("This is your Agent cloud.")}</h3>
              <dl className="first-run-access-details"><div><dt>{t("Console address")}</dt><dd>{origin}</dd></div><div><dt>{t("Administrator")}</dt><dd>{account}</dd></div></dl>
              <button className="button outline first-run-copy-access" type="button" onClick={() => void copyAccess()}>{copied ? <Check size={14} /> : <Copy size={14} />}{copied ? t("Copied") : t("Copy sign-in details")}</button>
              {copyFailed ? <p role="alert">{t("Select the details and copy them manually.")}</p> : null}
            </section>
            <ApiKeyPanel onReady={setKeyReady} />
          </div>
        </> : step === 1 ? <>
          {heading(t("Connect your own machine."), t("Add this machine as a host. In hosted mode, Core creates and manages sandboxes on these hosts to provide resources for Agents."))}
          <div className="first-run-machine"><SandboxManagerView coreBaseUrl={connection.baseUrl} presentation="home" /></div>
        </> : <>
          {heading(t("Make your first API request."), t("Create an Agent and see it here."))}
          <FirstRequest core={core} connection={connection} scope={scope} onCreated={agentCreated} onOpenAgent={onOpenAgent} />
        </>}
      </div>
    </div>
    <footer className="first-run-footer">
      {step > 0 ? <button className="first-run-text-button" type="button" onClick={(event) => go((step - 1) as FirstRunStep, event.currentTarget)}><ArrowLeft size={14} />{t("Back")}</button> : <span />}
      <div className="first-run-footer-actions">
        {step === 0 ? <>
          {!keyReady ? <span className="first-run-key-reminder">{t("Save a key before continuing.")}</span> : null}
          <button className="button primary" type="button" disabled={!keyReady} onClick={(event) => go(1, event.currentTarget)}>{t("I've saved it. Continue")}<ArrowRight size={15} /></button>
        </> : step === 1 ? <>
          <button className="button primary" type="button" onClick={(event) => go(2, event.currentTarget)}>{t("Continue to the API")}<ArrowRight size={15} /></button>
        </> : created ? <button className="button primary" type="button" onClick={onDone}>{t("Finish introduction")}<ArrowRight size={14} /></button> : null}
      </div>
    </footer>
  </main>;
}

/**
 * The introduction's one continuous animation: a request's path from the
 * caller's code through Core and the machines to an Agent. Each step lights
 * the next segment; the path doubles as the step navigation.
 */
function FirstRunPipeline({ step, keyReady, created, onSelect }: {
  step: FirstRunStep;
  keyReady: boolean;
  created: boolean;
  onSelect: (step: FirstRunStep, trigger: HTMLButtonElement) => void;
}) {
  const { t } = useTranslation("firstRun");
  const stations = [
    { id: "code", label: t("Your code"), icon: <Code2 size={17} strokeWidth={1.6} /> },
    { id: "core", label: "Parsar Core", icon: <><img className="brand-mark-light" src="/parsar-mark-light.png" width="18" height="18" alt="" /><img className="brand-mark-dark" src="/parsar-mark-dark.png" width="18" height="18" alt="" /></> },
    { id: "machines", label: t("Machines and sandboxes"), icon: <Server size={17} strokeWidth={1.6} /> },
    { id: "agent", label: "Agent", icon: <Bot size={18} strokeWidth={1.6} /> },
  ] as const;
  const segments = [t("Your access"), t("Your machines"), t("Your first Agent")] as const;
  // A station is reached once the request has travelled to it.
  const reached = (index: number) => index <= step || (index === 3 && created);
  return (
    <nav className="first-run-pipeline" aria-label={t("First steps")}>
      <ol>
        {stations.map((station, index) => (
          <li key={station.id} className="first-run-pipeline-item">
            <div className={`first-run-station${reached(index) ? " reached" : ""}${index === 3 && created ? " arrived" : ""}`} style={{ "--order": index * 2 } as CSSProperties}>
              <span className="first-run-station-mark" aria-hidden="true">{station.icon}</span>
              <span className="first-run-station-label">{station.label}</span>
            </div>
            {index < segments.length ? (
              <div
                className={`first-run-segment${index < step || (index === 2 && created) ? " done" : index === step ? " active" : ""}`}
                style={{ "--order": index * 2 + 1 } as CSSProperties}
              >
                <span className="first-run-segment-line" aria-hidden="true"><span className="first-run-segment-fill" /><span className="first-run-packet" /></span>
                <button
                  type="button"
                  disabled={step === 0 && index > 0 && !keyReady}
                  aria-current={step === index ? "step" : undefined}
                  onClick={(event) => onSelect(index as FirstRunStep, event.currentTarget)}
                >
                  {index < step || (index === 2 && created) ? <Check size={13} aria-hidden="true" /> : null}
                  {segments[index]}
                  {index === 1 ? <small>{t("Optional")}</small> : null}
                </button>
              </div>
            ) : null}
          </li>
        ))}
      </ol>
    </nav>
  );
}
