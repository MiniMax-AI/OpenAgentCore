import { AgentCoreError, deploymentContract, sandboxProviderSetup, type InitializeSandboxDeployment, type UpdateSandboxDeployment, type SandboxConfigurationOption, type SandboxDiscoveryInput, type SandboxProvider, type SandboxResources, type SandboxSetupField, type SandboxSpecification } from "@oac/agents-client";
import { useQuery } from "@tanstack/react-query";
import { AnimatePresence } from "motion/react";
import * as m from "motion/react-m";
import { ArrowLeft, ArrowRight, Cloud, ExternalLink, Server, SlidersHorizontal, type LucideIcon } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { coreFieldError } from "../../lib/core-error";
import { formatBytes } from "../../lib/format";
import { installationQuery } from "../../lib/installation";
import type { ParseKeys } from "i18next";
import { sandboxConfigurationRejection, sandboxProviderLabel } from "../../lib/sandbox-labels";
import { defaultSandboxResources, savedSpecification, selectedWorkspace, validSandboxResources } from "./deployment-specification";
import { workspaceStorageQuery } from "../settings/workspace-storage";
import { credentialReady, submittedCredential } from "./sandbox-update";
import { sandboxAdmin } from "./sandbox-queries";
import "./sandbox-wizard.css";

type Where = "nodes" | "direct";
type Step = "where" | "backend" | "connection" | "size" | "review" | "advanced";
type Preset = "small" | "standard" | "large";
type Size = Preset | "current" | "custom";
type Values = Record<string, string>;
const kinds = Object.keys(deploymentContract.providers) as SandboxProvider[];
const providersOf = (mode: Where) => kinds.filter((kind) => deploymentContract.providers[kind].mode === mode);

/** The service whose values the configuration holds, or -1 for a custom one. */
function serviceOf(provider: SandboxProvider, configuration: Values): number {
  return sandboxProviderSetup(provider).presets.findIndex((preset) => Object.entries(preset.values).every(([name, value]) => configuration[name] === value));
}

/** The values of the connection fields a service sets, empty for a custom one. */
function serviceValues(provider: SandboxProvider, index: number): Values {
  const setup = sandboxProviderSetup(provider);
  const values: Readonly<Record<string, string>> | undefined = setup.presets[index]?.values;
  return Object.fromEntries(setup.fields.filter((field) => !field.secret && !field.discovered).map((field) => [field.name, values?.[field.name] ?? ""]));
}

const MIB = 2 ** 20;
const bounds = Object.fromEntries(deploymentContract.resources.map(({ name, min, max }) => [name, `${min}–${max}`]));
const resourceBounds = { cpus: bounds.cpus, memory: bounds.memory_mib, disk: deploymentContract.minimum_disk };
const EASE = [0.16, 1, 0.3, 1] as const;
/**
 * Per-sandbox presets around the Provider's declared default size: half and
 * double of it, disks included where the default declares them. A Provider
 * whose configuration selects the size declares none and has no presets.
 */
function presets(provider: SandboxProvider): Record<Preset, SandboxResources> | null {
  const standard = defaultSandboxResources(provider);
  if (!standard) return null;
  const scale = (factor: number): SandboxResources => ({
    cpus: Math.max(1, standard.cpus * factor),
    memory_mib: standard.memory_mib * factor,
    ...(standard.root_disk_mib ? { root_disk_mib: standard.root_disk_mib * factor } : {}),
  });
  return { small: scale(0.5), standard, large: scale(2) };
}

function presetOf(provider: SandboxProvider, resources: SandboxResources): Preset | null {
  const same = (a: SandboxResources, b: SandboxResources) => a.cpus === b.cpus && a.memory_mib === b.memory_mib
    && (a.root_disk_mib ?? 0) === (b.root_disk_mib ?? 0) && (a.environment_disk_mib ?? 0) === (b.environment_disk_mib ?? 0);
  const all = presets(provider);
  return all ? (Object.keys(all) as Preset[]).find((key) => same(all[key], resources)) ?? null : null;
}

/**
 * Hosted sandbox setup as pages, one decision each: where sandboxes run,
 * which backend (own machines, the recommended one preselected) or the
 * Provider's connection, how big each sandbox is (only for a Provider that
 * declares a default size), then a review. A Provider's setup declaration
 * decides its connection fields, services, cautions and discovered options; the
 * Provider's resource declarations decide the size and disk inputs. Advanced
 * settings hold the complete size form.
 * `current` pre-selects the saved choices when a deployment changes. Keeping
 * the backend keeps its saved size; another backend starts from its defaults.
 * An update can retain the saved credential.
 * A Provider that declares cautions takes a confirmation, once per wizard
 * session; a saved deployment has already made it.
 * Core's address is config.json's `public_url`: the review only shows it, and
 * a configuration Core rejects for it is explained here, where it was saved.
 */
type WizardProps = {
  coreUrl: string;
  expectedGeneration: number;
  current?: { provider: SandboxProvider; specification?: SandboxSpecification; configuration?: Values };
  disabled: boolean;
} & ({ editing: true; onSubmit: (input: UpdateSandboxDeployment) => Promise<void> } |
  { editing?: false; onSubmit: (input: InitializeSandboxDeployment) => Promise<void> });

export function SandboxSetupWizard({ coreUrl, expectedGeneration, current, disabled, editing, onSubmit }: WizardProps) {
  const { t, i18n } = useTranslation("sandbox");
  const { t: tCommon } = useTranslation("common");
  const id = useId();
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const currentMode: Where | null = current ? deploymentContract.providers[current.provider].mode : null;
  const [step, setStep] = useState<Step>(editing ? currentMode === "direct" ? "connection" : "size" : "where");
  const [where, setWhere] = useState<Where | null>(currentMode);
  const [provider, setProvider] = useState<SandboxProvider | null>(current?.provider ?? null);
  const policy = provider ? deploymentContract.providers[provider] : null;
  const setup = provider ? sandboxProviderSetup(provider) : null;
  const saved = provider && current ? savedSpecification(provider, current.provider, current.specification) : null;
  // The selected storage sizes a first initialization; a saved specification takes precedence.
  const storage = useQuery(workspaceStorageQuery).data;
  const workspace = saved?.workspace ?? (provider ? selectedWorkspace(provider, storage) : undefined);
  // Choosing a Provider that declares a default size replaces this placeholder.
  const [resources, setResources] = useState<SandboxResources>(current?.specification?.resources ?? { cpus: 0, memory_mib: 0 });
  const [size, setSize] = useState<Size>(current?.specification ? presetOf(current.provider, current.specification.resources) ?? "current" : "standard");
  // Every declared field's value, secret ones included; a secret is cleared once a save was attempted.
  const [values, setValues] = useState<Values>(current?.configuration ?? {});
  const [replacementRequested, setReplacementRequested] = useState(false);
  const [checking, setChecking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmed, setConfirmed] = useState<SandboxProvider | null>(current?.provider ?? null);
  const [confirming, setConfirming] = useState<SandboxProvider | null>(null);
  const goBack = useRef<HTMLButtonElement>(null);
  // Core's reason for rejecting the saved configuration, such as a loopback public_url.
  const [rejection, setRejection] = useState<string | null>(null);
  const [fieldRejection, setFieldRejection] = useState<unknown>(null);
  const fieldError = (param: string) => coreFieldError(fieldRejection, param, tCommon);
  const [resetRequired, setResetRequired] = useState(false);
  const [addressRejected, setAddressRejected] = useState(false);
  const installation = useQuery(installationQuery);
  const address = coreUrl || installation.data?.public_url || null;
  const { navigate } = useConsoleNavigation();
  const text = (value: { readonly en: string; readonly "zh-CN": string }) => value[locale];

  const fields = setup?.fields ?? [];
  const secretFields = fields.filter((field) => field.secret);
  const valueOf = (field: SandboxSetupField) => (values[field.name] ?? "").trim();
  // A connection change invalidates the discovered values; a secret edited while updating keeps the saved ones.
  function change(field: SandboxSetupField, value: string) {
    const resets = field.secret && editing ? [] : fields.filter((other) => other.discovered);
    setValues((previous) => ({ ...previous, ...Object.fromEntries(resets.map((other) => [other.name, ""])), [field.name]: value }));
    setFieldRejection(null);
  }
  const service = provider ? serviceOf(provider, values) : -1;
  // Discovery waits for the credential and every required value it depends on.
  const dependencies = fields.filter((field) => !field.discovered && !field.optional);
  const request: Omit<SandboxDiscoveryInput, "query"> | null = dependencies.length > 0 && dependencies.every((field) => valueOf(field)) ? {
    configuration: Object.fromEntries(fields.filter((field) => !field.secret && !field.discovered).map((field) => [field.name, valueOf(field)] as const).filter(([, value]) => value)),
    credential: Object.fromEntries(secretFields.map((field) => [field.name, valueOf(field)])),
  } : null;
  // Initial setup requires the credential; an update may retain the committed one.
  const keyReady = credentialReady(Boolean(editing), replacementRequested, secretFields.every((field) => valueOf(field)));
  const connectionReady = fields.every((field) => field.secret || field.optional || valueOf(field));
  // A Provider without a declared default size takes it from its configuration, so the selection sends no resources.
  const sized = Boolean(policy?.default_resources);
  const sizeReady = provider !== null && (!sized || validSandboxResources(provider, resources, workspace));
  const ready = provider !== null && keyReady && connectionReady && sizeReady && !disabled && !busy;

  const connects = where === "direct";
  const order: Step[] = editing ? connects ? ["connection", "review"] : ["size", "review"] : connects ? ["where", "connection", "review"] : ["where", "backend", "size", "review"];
  const index = Math.max(0, order.indexOf(step === "advanced" ? "review" : step));
  const back = () => setStep(step === "advanced" ? "review" : order[Math.max(0, index - 1)]!);

  // The saved backend keeps its saved size and connection; another starts from its declared defaults.
  function choose(next: SandboxProvider) {
    if (next !== provider) {
      setRejection(null);
      const kept = current ? savedSpecification(next, current.provider, current.specification) : null;
      const proposed = kept?.resources ?? defaultSandboxResources(next);
      if (proposed) setResources(proposed);
      setSize(kept ? presetOf(next, kept.resources) ?? "current" : "standard");
      setValues(kept ? current?.configuration ?? {} : serviceValues(next, 0));
    }
    setProvider(next);
  }

  function proceed(next: SandboxProvider) {
    setConfirming(null);
    choose(next);
    setStep(deploymentContract.providers[next].mode === "direct" ? "connection" : "size");
  }
  function select(next: SandboxProvider) {
    if (sandboxProviderSetup(next).cautions.length > 0 && confirmed !== next) setConfirming(next);
    else proceed(next);
  }

  async function save(event?: FormEvent) {
    event?.preventDefault();
    if (!ready || !provider) return;
    setBusy(true); setRejection(null); setFieldRejection(null);
    try {
      const selection = {
        provider, expected_generation: expectedGeneration,
        ...(sized ? { resources } : {}),
        ...(fields.length ? { configuration: Object.fromEntries(fields.filter((field) => !field.secret).map((field) => [field.name, valueOf(field)])) } : {}),
        ...submittedCredential(secretFields.map((field) => [field.name, valueOf(field)])),
      };
      await onSubmit(selection);
    } catch (error) {
      // A configuration Core rejected is explained here; the page reports every other failure.
      const reason = sandboxConfigurationRejection(error, locale);
      if (reason === null) throw error;
      setRejection(reason);
      setFieldRejection(error);
      if (error instanceof AgentCoreError && error.param) {
        if (["credential", "configuration"].includes(error.param) && error.code !== "sandbox_credential_ownership") setStep("connection");
        else if (error.param.startsWith("resources.")) setStep("advanced");
      }
      setResetRequired(error instanceof AgentCoreError && ["sandbox_credential_ownership", "sandbox_reset_required"].includes(error.code ?? ""));
      setAddressRejected(error instanceof AgentCoreError && error.code === "sandbox_configuration_error");
    } finally {
      setValues((previous) => ({ ...previous, ...Object.fromEntries(secretFields.map((field) => [field.name, ""])) }));
      setBusy(false);
    }
  }

  const sizeLabel = (value: SandboxResources) => t("{{cpus}} CPU · {{memory}}", { cpus: value.cpus, memory: formatBytes(value.memory_mib * MIB) });
  const diskLabel = (value: SandboxResources) => t("Root disk {{root}} · external workspace", { root: formatBytes((value.root_disk_mib ?? 0) * MIB) });

  let page: ReactNode;
  if (step === "where") {
    const hosted = providersOf("direct");
    const help = [...hosted.map((kind) => `${sandboxProviderSetup(kind).label}: ${text(sandboxProviderSetup(kind).summary)}`), t("Own machines run sandboxes on hosts you add.")].join(" ");
    page = (
      <Question title={t("Where should sandboxes run?")} help={help}>
        <div className="wizard-choices">
          {hosted.map((kind) => <Choice key={kind} icon={Cloud} title={sandboxProviderSetup(kind).label} selected={provider === kind} onClick={() => { setWhere("direct"); select(kind); }} />)}
          {providersOf("nodes").length > 0 ? <Choice icon={Server} title={t("Own machines")} selected={where === "nodes"} onClick={() => {
            setWhere("nodes");
            setValues({});
            if (policy?.mode !== "nodes") choose(providersOf("nodes").find((kind) => sandboxProviderSetup(kind).recommended) ?? providersOf("nodes")[0]!);
            setStep("backend");
          }} /> : null}
        </div>
      </Question>
    );
  } else if (step === "backend") {
    const backends = providersOf("nodes");
    page = (
      <Question title={t("Which sandbox backend?")} help={backends.map((kind) => `${sandboxProviderSetup(kind).label}: ${text(sandboxProviderSetup(kind).summary)}`).join(" ")}>
        <div className="wizard-choices">
          {backends.map((kind) => <Choice key={kind} title={sandboxProviderSetup(kind).label} badge={sandboxProviderSetup(kind).recommended ? t("Recommended") : undefined} selected={provider === kind} onClick={() => select(kind)} />)}
        </div>
        <Nav onBack={back} t={t} />
      </Question>
    );
  } else if (step === "connection" && setup) {
    const preset = setup.presets[service];
    page = (
      <Question title={t("Connect {{provider}}", { provider: setup.label })}>
        <div className="wizard-fields">
          {setup.presets.length > 0 ? (
            <Field id={`${id}-service`} label={t("Service")}>
              <select id={`${id}-service`} value={service} onChange={(event) => setValues(serviceValues(provider!, Number(event.target.value)))}>
                {setup.presets.map((entry, position) => <option key={entry.label} value={position}>{entry.label}</option>)}
                <option value={-1}>{t("Other compatible service")}</option>
              </select>
            </Field>
          ) : null}
          {fields.map((field) => {
            const fieldId = `${id}-${field.name}`;
            const help = field.help.en ? text(field.help) : undefined;
            if (field.secret) {
              return (
                <Field key={field.name} id={fieldId} label={text(field.label)} error={fieldError("credential")} help={t(editing ? "Leave blank to keep the saved key. Any key you enter is verified as a replacement, even if unchanged." : "The key is write-only: Core encrypts it and never shows it again.")} afterHelp={preset?.credential_url ? (
                  <a className="wizard-key-console" href={preset.credential_url} target="_blank" rel="noopener noreferrer">
                    {t("Console → API keys")} <ExternalLink size={11} aria-hidden="true" />
                  </a>
                ) : null}>
                  <input id={fieldId} type="password" autoComplete="off" spellCheck={false} value={values[field.name] ?? ""} onChange={(event) => { change(field, event.target.value); setReplacementRequested(Boolean(event.target.value.trim())); }} />
                </Field>
              );
            }
            if (field.discovered) {
              return <DiscoveredField key={`${field.name}:${JSON.stringify(request)}`} id={fieldId} provider={provider!} field={field} label={text(field.label)} help={help} request={request} value={values[field.name] ?? ""} editing={Boolean(editing)} error={fieldError("configuration")} onChange={(value) => change(field, value)} onChecking={setChecking} />;
            }
            return (
              <Field key={field.name} id={fieldId} label={text(field.label)} help={help}>
                <input id={fieldId} value={values[field.name] ?? ""} onChange={(event) => change(field, event.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
            );
          })}
        </div>
        <Nav onBack={back} onNext={() => setStep("review")} nextDisabled={!(keyReady && connectionReady) || checking} t={t} />
      </Question>
    );
  } else if (step === "size") {
    const options = provider ? presets(provider) : null;
    // A saved size outside the presets stays on offer as the current one.
    const kept = saved && provider && presetOf(provider, saved.resources) === null ? saved.resources : null;
    const disks = (value: SandboxResources) => (policy?.disk ? diskLabel(value) : undefined);
    page = (
      <Question title={t("How big is each sandbox?")} help={t("These limits apply to the selected configuration generation. Existing sandboxes keep their limits. Concurrency is set per node.")}>
        <div className={kept ? "wizard-choices wizard-choices-4" : "wizard-choices wizard-choices-3"}>
          {kept ? <Choice title={t("Current")} value={sizeLabel(kept)} detail={disks(kept)} selected={size === "current"} onClick={() => { setSize("current"); setResources(kept); setStep("review"); }} /> : null}
          {options && (Object.keys(options) as Preset[]).map((key) => (
            <Choice
              key={key}
              title={t(key === "small" ? "Small" : key === "standard" ? "Standard" : "Large")}
              value={sizeLabel(options[key])}
              detail={disks(options[key])}
              selected={size === key}
              onClick={() => { setSize(key); setResources(options[key]); setStep("review"); }}
            />
          ))}
        </div>
        {policy && "workspace" in policy && !workspace ? <p>{t("This backend runs every sandbox on workspace storage. Select it under Settings, Workspace storage first.")}</p> : null}
        <button className="wizard-link" type="button" onClick={() => { setSize("custom"); setStep("advanced"); }}>
          <SlidersHorizontal size={14} aria-hidden="true" />{t("Custom size in advanced settings")}
        </button>
        <Nav onBack={back} t={t} />
      </Question>
    );
  } else if (step === "review") {
    page = (
      <Question title={t("Review and save")}>
        <dl className="wizard-review">
          <div><dt>{t("Sandboxes run on")}</dt><dd>{where === "direct" ? setup?.label : `${t("Own machines")} · ${sandboxProviderLabel(provider ?? "", locale)}`}</dd></div>
          <div><dt>{t("Each sandbox")}</dt><dd>{sized ? sizeLabel(resources) : t("From the selected configuration")}{policy?.disk ? <span className="wizard-review-sub">{diskLabel(resources)}</span> : null}</dd></div>
          {fields.filter((field) => !field.secret).map((field) => <div key={field.name}><dt>{text(field.label)}</dt><dd>{valueOf(field) ? <code>{valueOf(field)}</code> : t("Provider default")}</dd></div>)}
          {secretFields.length > 0 && editing ? <div><dt>{t("Credential")}</dt><dd>{t(replacementRequested ? "Replace saved key" : "Keep saved key")}</dd></div> : null}
          <div>
            <dt>{t("Core address")}<HelpTip>{t("The address nodes and sandboxes use to reach Core.")}</HelpTip></dt>
            <dd>
              {address ? <code>{address}</code> : "—"}
              <span className="wizard-review-sub">{t("Managed in Settings")}</span>
              {installation.data?.local_only ? <span className="wizard-review-caution">{setup?.public_origin === "required" ? t("Set a public address before connecting; {{provider}} sandboxes need an HTTPS one.", { provider: setup.label }) : t("Set a public address before connecting remote nodes.")}</span> : null}
            </dd>
          </div>
        </dl>
        {rejection ? (
          <div className="wizard-rejection" role="alert">
            <p>{rejection}</p>
            {resetRequired ? <p>{t("Cancel editing to use Reset deployment. This change requires an explicit reset; the saved configuration is unchanged.")}</p> : null}
            {addressRejected ? <button className="text-action" type="button" onClick={() => navigate("settings", { id: "service" })}>{t("Managed in Settings")}</button> : null}
          </div>
        ) : null}
        {/* Initial setup needs the cleared key re-entered; updates may keep the saved key. */}
        {!keyReady ? (
          <p className="wizard-key-again">
            {t("Enter the key again to save.")}
            <button className="wizard-link" type="button" onClick={() => setStep("connection")}>{t("Enter the key")}</button>
            {editing ? <button className="wizard-link" type="button" onClick={() => setReplacementRequested(false)}>{t("Keep saved key")}</button> : null}
          </p>
        ) : null}
        {sized ? (
          <button className="wizard-link" type="button" onClick={() => setStep("advanced")}>
            <SlidersHorizontal size={14} aria-hidden="true" />{t("Advanced settings")}
          </button>
        ) : null}
        <div className="wizard-nav">
          <button className="button ghost" type="button" onClick={back}><ArrowLeft size={14} aria-hidden="true" />{t("Back")}</button>
          <button className="button primary" type="button" disabled={!ready} onClick={() => void save()}>
            {busy ? t("Saving…") : t("Save configuration")}<ArrowRight size={14} aria-hidden="true" />
          </button>
        </div>
      </Question>
    );
  } else {
    page = (
      <Question title={t("Advanced settings")}>
        <form className="wizard-fields" onSubmit={(event) => { event.preventDefault(); setStep("review"); }}>
          {sized ? <fieldset className="wizard-group">
            <legend>{t("Each sandbox")}<HelpTip>{t("{{cpus}} CPUs, {{memory}} MiB of memory. Disks are at least {{disk}} MiB.", resourceBounds)}</HelpTip></legend>
            <div className="wizard-grid">
              <NumberField error={fieldError("resources.cpus")} id={`${id}-cpus`} label={t("CPUs")} value={resources.cpus} onChange={(cpus) => { setSize("custom"); setResources({ ...resources, cpus }); setFieldRejection(null); }} />
              <NumberField error={fieldError("resources.memory_mib")} id={`${id}-memory`} label={t("Memory (MiB)")} value={resources.memory_mib} onChange={(memory_mib) => { setSize("custom"); setResources({ ...resources, memory_mib }); setFieldRejection(null); }} />
              {policy?.disk ? <>
                <NumberField error={fieldError("resources.root_disk_mib")} id={`${id}-root`} label={t("Root disk (MiB)")} value={resources.root_disk_mib ?? 0} onChange={(root_disk_mib) => { setResources({ ...resources, root_disk_mib }); setFieldRejection(null); }} />
                {workspace?.capacity_quota ? <NumberField error={fieldError("resources.environment_disk_mib")} id={`${id}-data`} label={t("Capacity quota at /environment (MiB)")} value={resources.environment_disk_mib ?? 0} onChange={(environment_disk_mib) => { setResources({ ...resources, environment_disk_mib }); setFieldRejection(null); }} /> : workspace ? <p>{t("Workspace storage is configured by the operator; no capacity quota is enforced.")}</p> : null}
              </> : null}
            </div>
          </fieldset> : null}
          <div className="wizard-nav">
            <span />
            <button className="button primary" type="submit" disabled={!sizeReady}>{t("Done")}<ArrowRight size={14} aria-hidden="true" /></button>
          </div>
        </form>
      </Question>
    );
  }

  return (
    <section className="sandbox-wizard" aria-label={t(editing ? "Change the sandbox configuration" : "Set up hosted sandboxes")}>
      {step !== "advanced" ? (
        <ol className="wizard-steps" aria-hidden="true">
          {order.map((entry, position) => <li key={entry} className={position === index ? "current" : position < index ? "done" : undefined} />)}
        </ol>
      ) : null}
      <AnimatePresence mode="wait" initial={false}>
        <m.div
          key={step}
          initial={{ opacity: 0, x: 28, filter: "blur(6px)" }}
          animate={{ opacity: 1, x: 0, filter: "blur(0px)" }}
          exit={{ opacity: 0, x: -28, filter: "blur(6px)" }}
          transition={{ duration: 0.34, ease: EASE }}
        >
          {page}
        </m.div>
      </AnimatePresence>
      {/* Portaled: the sliding page's transform would otherwise contain the fixed backdrop. */}
      {createPortal(
        <Modal
          open={confirming !== null}
          title={t("Use {{provider}}?", { provider: confirming ? sandboxProviderSetup(confirming).label : "" })}
          initialFocus={goBack}
          onClose={() => setConfirming(null)}
          footer={<>
            <button type="button" className="button outline" onClick={() => { if (confirming) { setConfirmed(confirming); proceed(confirming); } }}>{t("Use {{provider}}", { provider: confirming ? sandboxProviderSetup(confirming).label : "" })}</button>
            <button ref={goBack} type="button" className="button primary" onClick={() => setConfirming(null)}>{t("Back")}</button>
          </>}
        >
          <ul className="wizard-cautions">
            {confirming ? sandboxProviderSetup(confirming).cautions.map((caution) => <li key={caution.en}>{text(caution)}</li>) : null}
          </ul>
        </Modal>,
        document.body,
      )}
    </section>
  );
}

function Question({ title, help, children }: { title: string; help?: string; children: ReactNode }) {
  return (
    <div className="wizard-page">
      <h2 className="wizard-title">{title}{help ? <HelpTip>{help}</HelpTip> : null}</h2>
      {children}
    </div>
  );
}

/** A large option that selects and moves on in one click; a badge, such as Recommended, sits beside its title. */
function Choice({ icon: Icon, title, badge, value, detail, selected, onClick }: { icon?: LucideIcon; title: string; badge?: string; value?: string; detail?: string; selected: boolean; onClick: () => void }) {
  return (
    <button type="button" className={selected ? "wizard-choice selected" : "wizard-choice"} aria-pressed={selected} onClick={onClick}>
      {Icon ? <span className="wizard-choice-icon"><Icon size={20} strokeWidth={1.5} aria-hidden="true" /></span> : null}
      <span className="wizard-choice-heading"><span className="wizard-choice-title">{title}</span>{badge ? <span className="pill">{badge}</span> : null}</span>
      {value ? <span className="wizard-choice-value">{value}</span> : null}
      {detail ? <span className="wizard-choice-value">{detail}</span> : null}
    </button>
  );
}

function Field({ id, label, help, afterHelp, error, children }: { id: string; label: string; help?: string; afterHelp?: ReactNode; error?: string | null; children: ReactNode }) {
  return (
    <div className="field wizard-field">
      <span className="field-label-row"><label htmlFor={id}>{label}</label>{help ? <HelpTip>{help}</HelpTip> : null}{afterHelp}</span>
      {children}
      {error ? <span id={`${id}-error`} className="field-error" role="alert">{error}</span> : null}
    </div>
  );
}

function NumberField({ id, label, value, onChange, error }: { error?: string | null; id: string; label: string; value: number; onChange: (value: number) => void }) {
  return (
    <Field id={id} label={label} error={error}>
      <input id={id} aria-invalid={Boolean(error)} aria-describedby={error ? `${id}-error` : undefined} type="number" inputMode="numeric" min={1} step={1} value={Number.isFinite(value) ? value : ""} onChange={(event) => onChange(Number.parseInt(event.target.value, 10))} />
    </Field>
  );
}

function Nav({ onBack, onNext, nextDisabled, t }: { onBack: () => void; onNext?: () => void; nextDisabled?: boolean; t: (key: ParseKeys<"sandbox">) => string }) {
  return (
    <div className="wizard-nav">
      <button className="button ghost" type="button" onClick={onBack}><ArrowLeft size={14} aria-hidden="true" />{t("Back")}</button>
      {onNext ? <button className="button primary" type="button" disabled={nextDisabled} onClick={onNext}>{t("Next")}<ArrowRight size={14} aria-hidden="true" /></button> : null}
    </div>
  );
}

type Level = "loading" | "error" | SandboxConfigurationOption[];

/**
 * A field whose value the Provider offers for the entered credential: one select
 * per level, each loaded after the option chosen above it, until an option is
 * complete. The page remounts it when the connection changes. While updating
 * without a new credential it keeps the saved value in an input.
 */
function DiscoveredField({ id, provider, field, label, help, request, value, editing, error, onChange, onChecking }: {
  id: string; provider: SandboxProvider; field: SandboxSetupField; label: string; help?: string; request: Omit<SandboxDiscoveryInput, "query"> | null;
  value: string; editing: boolean; error: string | null; onChange: (value: string) => void; onChecking: (checking: boolean) => void;
}) {
  const { t } = useTranslation("sandbox");
  const [picks, setPicks] = useState<SandboxConfigurationOption[]>([]);
  const [levels, setLevels] = useState<Level[]>([]);
  const [retry, setRetry] = useState(0);
  const open = picks.length === 0 || !picks[picks.length - 1]!.complete;
  const last = levels[picks.length];
  // The step cannot be left while the credential is still being checked, whether settling or loading.
  const checking = Boolean(request) && open && (last === undefined || last === "loading");
  useEffect(() => { onChecking(checking); return () => onChecking(false); }, [checking]);
  useEffect(() => {
    if (!request || !open) return;
    const depth = picks.length;
    const controller = new AbortController();
    // Typing the credential settles for half a second before the first read.
    const timer = window.setTimeout(() => {
      setLevels((previous) => [...previous.slice(0, depth), "loading"]);
      void sandboxAdmin.discoverConfiguration(provider, { ...request, query: { field: field.name, path: picks.map((pick) => pick.value) } }, { signal: controller.signal })
        .then((options) => { if (!controller.signal.aborted) setLevels((previous) => [...previous.slice(0, depth), options]); })
        .catch(() => { if (!controller.signal.aborted) setLevels((previous) => [...previous.slice(0, depth), "error"]); });
    }, depth === 0 ? 500 : 0);
    return () => { window.clearTimeout(timer); controller.abort(); };
    // The page remounts this field when the request changes.
  }, [picks, retry]);

  if (!request && editing) {
    return <Field id={id} label={label} help={help} error={error}><input id={id} value={value} onChange={(event) => onChange(event.target.value)} autoComplete="off" spellCheck={false} /></Field>;
  }
  const choose = (depth: number, option: SandboxConfigurationOption | undefined) => {
    setLevels((previous) => previous.slice(0, depth + 1));
    setPicks((previous) => option ? [...previous.slice(0, depth), option] : previous.slice(0, depth));
    onChange(option?.complete ? option.value : "");
  };
  const selects = Array.from({ length: Math.max(1, Math.min(picks.length + 1, levels.length)) }, (_, depth) => {
    const level = levels[depth];
    const options = Array.isArray(level) ? level : [];
    const pick = picks[depth];
    return (
      <select key={depth} id={depth === 0 ? id : `${id}-${depth}`} aria-label={label} value={pick?.value ?? ""} disabled={options.length === 0} onChange={(event) => choose(depth, options.find((option) => option.value === event.target.value))}>
        <option value="">{depth === 0 && editing && value ? `${t("Current")} · ${value}` : t("Select {{field}}", { field: label })}</option>
        {options.map((option) => <option key={option.value} value={option.value}>{option.label}{option.resources ? ` · ${t("{{cpus}} CPU · {{memory}}", { cpus: option.resources.cpus, memory: formatBytes(option.resources.memory_mib * MIB) })}` : ""}</option>)}
      </select>
    );
  });
  return (
    <Field id={id} label={label} help={help} error={error}>
      {selects}
      {last === "loading" ? <p role="status">{t("Loading options…")}</p> : null}
      {last === "error" ? <p role="alert">{t("Could not load options. Check the credential and provider connection.")} <button type="button" className="wizard-link" onClick={() => setRetry((count) => count + 1)}>{t("Try again")}</button></p> : null}
      {Array.isArray(last) && last.length === 0 ? <p role="status">{t("No options are available for this credential.")}</p> : null}
    </Field>
  );
}
