import { ArrowLeft, ArrowRight, Check, Copy, KeyRound, Network, Terminal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
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
  const chapters = ["Your access", "Your machines", "Your first Agent"] as const;
  const icons = [KeyRound, Network, Terminal] as const;
  const go = (next: FirstRunStep, trigger: HTMLButtonElement) => {
    if (next === step) return;
    userChangedStep.current = true;
    focusAfterStep.current = trigger.closest(".first-run-chapters") ? null : trigger;
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
  return <main className="first-run-home" lang={locale} aria-label={t("First steps")}>
    <header className="first-run-toolbar">
      <div className="first-run-toolbar-preferences"><ConsoleAccountMenu /><AppearanceMenu /></div>
      <button className="first-run-skip" type="button" onClick={onDone}>{t("Skip introduction")}<ArrowRight size={14} /></button>
    </header>
    <nav className="first-run-chapters" aria-label={t("First steps")}>
      {chapters.map((chapter, index) => {
        const Icon = icons[index as FirstRunStep];
        return <button type="button" key={chapter} disabled={step === 0 && index > 0 && !keyReady} onClick={(event) => go(index as FirstRunStep, event.currentTarget)} aria-current={step === index ? "step" : undefined} className={step === index ? "active" : ""}><span className="first-run-chapter-number">0{index + 1}</span><Icon size={15} strokeWidth={1.5} /><span>{t(chapter)}</span>{index === 1 ? <small>{t("Optional")}</small> : null}</button>;
      })}
    </nav>
    <div ref={container} className={`first-run-stage first-run-step-${step}`}>
      <div className="first-run-stage-content" key={step}>
        {step === 0 ? <div className="first-run-access">
          <div className="first-run-story"><span className="first-run-section-number">01 / 03</span><h2 ref={stepHeading} tabIndex={-1}>{t("Keep your sign-in details.")}</h2><p>{t("Use this address and administrator account to return to your console.")}</p><button className="button primary" type="button" disabled={!keyReady} onClick={(event) => go(1, event.currentTarget)}>{t("I've saved it. Continue")}<ArrowRight size={15} /></button>{!keyReady ? <small className="first-run-key-reminder">{t("Save a key before continuing.")}</small> : null}</div>
          <div className="first-run-access-scene"><div className="first-run-access-brand"><div className="first-run-core-symbol" aria-hidden="true"><Network size={24} strokeWidth={1.2} /></div><h3>{t("This is your Agent cloud.")}</h3></div><dl className="first-run-access-details"><div><dt>{t("Console address")}</dt><dd>{origin}</dd></div><div><dt>{t("Administrator")}</dt><dd>{account}</dd></div></dl><button className="button outline first-run-copy-access" type="button" onClick={() => void copyAccess()}>{copied ? <Check size={14} /> : <Copy size={14} />}{copied ? t("Copied") : t("Copy sign-in details")}</button>{copyFailed ? <p role="alert">{t("Select the details and copy them manually.")}</p> : null}<ApiKeyPanel onReady={setKeyReady} /></div>
        </div> : step === 1 ? <>
          <div className="first-run-step-heading"><div><span className="first-run-section-number">02 / 03</span><h2 ref={stepHeading} tabIndex={-1}>{t("Connect your own machine.")}</h2><p>{t("Add this machine as a host. In hosted mode, Core creates and manages sandboxes on these hosts to provide resources for Agents.")}</p></div><span className="first-run-optional">{t("Optional")}</span></div>
          <div className="first-run-machine"><SandboxManagerView coreBaseUrl={connection.baseUrl} presentation="home" /></div>
          <div className="first-run-machine-footer"><span>{t("You can add more machines whenever you need them.")}</span><button className="button primary" type="button" onClick={(event) => go(2, event.currentTarget)}>{t("Continue to the API")}<ArrowRight size={15} /></button></div>
        </> : <>
          <div className="first-run-step-heading"><div><span className="first-run-section-number">03 / 03</span><h2 ref={stepHeading} tabIndex={-1}>{t("Make your first API request.")}</h2><p>{t("Create an Agent and see it here.")}</p></div></div>
          <FirstRequest core={core} connection={connection} scope={scope} onCreated={agentCreated} onOpenAgent={onOpenAgent} />
        </>}
      </div>
    </div>
    <footer className="first-run-footer">{step > 0 ? <button className="first-run-text-button" type="button" onClick={(event) => go((step - 1) as FirstRunStep, event.currentTarget)}><ArrowLeft size={14} />{t("Back")}</button> : <span />}{step === 1 ? <button className="first-run-text-button" type="button" onClick={(event) => go(2, event.currentTarget)}>{t("Skip for now")}<ArrowRight size={14} /></button> : step === 2 ? <button className="first-run-text-button" type="button" onClick={onDone}>{t(created ? "Finish introduction" : "Skip introduction")}<ArrowRight size={14} /></button> : null}</footer>
  </main>;
}
