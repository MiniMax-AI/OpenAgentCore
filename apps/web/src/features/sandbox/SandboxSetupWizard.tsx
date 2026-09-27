import type { InitializeSandboxDeployment, SandboxProvider, SandboxResources, SandboxRuntimeRelease, SandboxSpecification } from "@agents-core-web/agents-client";
import { useQuery } from "@tanstack/react-query";
import { AnimatePresence } from "motion/react";
import * as m from "motion/react-m";
import { ArrowLeft, ArrowRight, Box, Cloud, Cpu, Server, SlidersHorizontal, type LucideIcon } from "lucide-react";
import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { CopyableId } from "../../components/list-ui";
import { formatBytes } from "../../lib/format";
import { installationQuery } from "../../lib/installation";
import type { MessageKey } from "../../lib/locale-strings";
import { sandboxConfigurationRejection } from "../../lib/sandbox-labels";
import { defaultSandboxResources, distributionRuntime, savedSpecification, validSandboxResources } from "./deployment-specification";
import { isRuntimeRelease, isRuntimeReleaseField, RUNTIME_RELEASE_FIELDS } from "./runtime-release";
import "./sandbox-wizard.css";

type Where = "nodes" | "direct";
type Step = "where" | "backend" | "e2b" | "size" | "review" | "advanced";
type Preset = "small" | "standard" | "large";
type Size = Preset | "current" | "custom";

const MIB = 2 ** 20;
const EASE = [0.16, 1, 0.3, 1] as const;
// Core accepts a template ID of up to 128 characters and a canonical, non-nil build UUID.
const TEMPLATE = /^[a-zA-Z0-9_-]{1,128}:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;
function validTemplate(value: string): boolean {
  const build = TEMPLATE.exec(value)?.[1];
  return build !== undefined && /[^0-]/.test(build);
}

/**
 * Per-sandbox presets around the deployment default: half and double of it.
 * Disks apply to microsandbox only, the one provider that enforces them.
 */
function presets(provider: SandboxProvider): Record<Preset, SandboxResources> {
  const standard = defaultSandboxResources(provider);
  const scale = (factor: number): SandboxResources => ({
    cpus: Math.max(1, standard.cpus * factor),
    memory_mib: standard.memory_mib * factor,
    ...(standard.root_disk_mib ? { root_disk_mib: standard.root_disk_mib * factor, environment_disk_mib: standard.environment_disk_mib! * factor } : {}),
  });
  return { small: scale(0.5), standard, large: scale(2) };
}

const releaseLabels: Record<keyof SandboxRuntimeRelease, MessageKey> = {
  source_commit: "Source commit",
  image_id: "Image ID",
  image_manifest_digest: "Image manifest digest",
  microsandbox_ref: "microsandbox reference",
  runtime_sha256: "Runtime SHA-256",
  firmware_sha256: "Firmware SHA-256",
};

function presetOf(provider: SandboxProvider, resources: SandboxResources): Preset | null {
  const same = (a: SandboxResources, b: SandboxResources) => a.cpus === b.cpus && a.memory_mib === b.memory_mib
    && (a.root_disk_mib ?? 0) === (b.root_disk_mib ?? 0) && (a.environment_disk_mib ?? 0) === (b.environment_disk_mib ?? 0);
  const all = presets(provider);
  return (Object.keys(all) as Preset[]).find((key) => same(all[key], resources)) ?? null;
}

/**
 * Hosted sandbox setup as pages, one decision each: where sandboxes run,
 * which backend (own machines, microsandbox preselected) or the E2B account,
 * how big each sandbox is (own machines only: E2B sandboxes take the template
 * build's size), then a review. Advanced settings hold the complete form. The Runtime
 * release comes from this console's distribution manifest when it serves one.
 * `current` pre-selects the saved choices when a deployment changes. Keeping
 * the backend keeps its saved size and Runtime; another backend starts from its
 * defaults and this console's Runtime. An E2B key must be entered again.
 * Docker isolates less than microsandbox, so choosing it takes a confirmation,
 * once per wizard session; a saved Docker deployment has already made it.
 * Core's address is config.json's `public_url`: the review only shows it, and
 * a configuration Core rejects for it is explained here, where it was saved.
 */
export function SandboxSetupWizard({ coreUrl, current, disabled, switching = false, onSubmit }: {
  /** The deployment's read-only Core address. */
  coreUrl: string;
  current?: { provider: SandboxProvider; specification?: SandboxSpecification; e2bTemplate?: string };
  disabled: boolean;
  switching?: boolean;
  onSubmit: (input: InitializeSandboxDeployment) => Promise<void>;
}) {
  const { t } = useTranslation("sandbox");
  const id = useId();
  const [step, setStep] = useState<Step>("where");
  const [where, setWhere] = useState<Where | null>(current ? (current.provider === "e2b" ? "direct" : "nodes") : null);
  const [provider, setProvider] = useState<SandboxProvider | null>(current?.provider ?? null);
  const saved = provider && current ? savedSpecification(provider, current.provider, current.specification) : null;
  const [resources, setResources] = useState<SandboxResources>(current?.specification?.resources ?? defaultSandboxResources("docker"));
  const [size, setSize] = useState<Size>(current?.specification ? presetOf(current.provider, current.specification.resources) ?? "current" : "standard");
  const [apiKey, setApiKey] = useState("");
  const [template, setTemplate] = useState(current?.e2bTemplate ?? "");
  const [runtime, setRuntime] = useState<Partial<SandboxRuntimeRelease>>({});
  const [busy, setBusy] = useState(false);
  const [dockerConfirmed, setDockerConfirmed] = useState(current?.provider === "docker");
  const [confirmingDocker, setConfirmingDocker] = useState(false);
  const keepMicrosandbox = useRef<HTMLButtonElement>(null);
  // Core's reason for rejecting the saved configuration, such as E2B with a loopback public_url.
  const [rejection, setRejection] = useState<string | null>(null);
  const installation = useQuery(installationQuery);
  const address = coreUrl || installation.data?.public_url || null;
  const configuration = installation.data?.configuration ?? null;

  // A release the administrator entered comes first, then the saved one of the same backend,
  // then the one this console distributes (the release its node installer verifies).
  const matched = useQuery({ queryKey: ["sandbox-runtime-release"], queryFn: ({ signal }) => distributionRuntime(signal).catch(() => null), staleTime: Infinity, retry: false });
  const release: Partial<SandboxRuntimeRelease> = Object.keys(runtime).length ? runtime : saved?.runtime ?? matched.data ?? {};

  const needsRuntime = provider === "docker" || provider === "microsandbox";
  const runtimeReady = !needsRuntime || isRuntimeRelease(release);
  // Core keeps no key across a change: E2B always needs one.
  const e2bReady = provider !== "e2b" || (apiKey.trim().length > 0 && validTemplate(template.trim()));
  // Core sizes E2B sandboxes from the template build, so E2B sends no resources.
  const sized = provider !== null && provider !== "e2b";
  const sizeReady = provider !== null && (!sized || validSandboxResources(provider, resources));
  const ready = provider !== null && runtimeReady && e2bReady && sizeReady && !disabled && !busy;

  const order: Step[] = where === "direct" ? ["where", "e2b", "review"] : ["where", "backend", "size", "review"];
  const index = Math.max(0, order.indexOf(step === "advanced" ? "review" : step));
  const back = () => setStep(step === "advanced" ? "review" : order[Math.max(0, index - 1)]!);

  // The saved backend keeps its size and Runtime; another starts from its standard size and this console's Runtime.
  function choose(next: SandboxProvider) {
    if (next !== provider) {
      setRejection(null);
      const kept = current ? savedSpecification(next, current.provider, current.specification) : null;
      setResources(kept?.resources ?? presets(next).standard);
      setSize(kept ? presetOf(next, kept.resources) ?? "current" : "standard");
      setRuntime({});
    }
    setProvider(next);
  }

  function selectDocker() {
    setConfirmingDocker(false);
    setDockerConfirmed(true);
    choose("docker");
    setStep("size");
  }

  async function save(event?: FormEvent) {
    event?.preventDefault();
    if (!ready || !provider) return;
    setBusy(true); setRejection(null);
    try {
      await onSubmit({
        provider,
        ...(sized ? { resources } : {}),
        ...(needsRuntime ? { runtime: release as SandboxRuntimeRelease } : {}),
        ...(provider === "e2b" ? { e2b: { api_key: apiKey.trim(), template: template.trim() } } : {}),
      });
    } catch (error) {
      // A configuration Core rejected is explained here; the page reports every other failure.
      const reason = sandboxConfigurationRejection(error);
      if (reason === null) throw error;
      setRejection(reason);
    } finally {
      setApiKey("");
      setBusy(false);
    }
  }

  const sizeLabel = (value: SandboxResources) => t("{{cpus}} CPU · {{memory}}", { cpus: value.cpus, memory: formatBytes(value.memory_mib * MIB) });
  const diskLabel = (value: SandboxResources) => t("Root disk {{root}} · data disk {{data}}", { root: formatBytes((value.root_disk_mib ?? 0) * MIB), data: formatBytes((value.environment_disk_mib ?? 0) * MIB) });

  let page: ReactNode;
  if (step === "where") {
    page = (
      <Question title={t("Where should sandboxes run?")} help={t("E2B runs sandboxes in its cloud: no machines to manage, billed by E2B. Own machines run them on hosts you add, with microsandbox (recommended) or Docker.")}>
        <div className="wizard-choices">
          <Choice icon={Cloud} title={t("E2B cloud")} selected={where === "direct"} onClick={() => { setWhere("direct"); choose("e2b"); setStep("e2b"); }} />
          <Choice icon={Server} title={t("Own machines")} selected={where === "nodes"} onClick={() => { setWhere("nodes"); setApiKey(""); if (provider === null || provider === "e2b") choose("microsandbox"); setStep("backend"); }} />
        </div>
      </Question>
    );
  } else if (step === "backend") {
    page = (
      <Question title={t("Which sandbox backend?")} help={t("microsandbox, the recommended default, runs each sandbox as a lightweight virtual machine: stronger isolation and its own root and data disks with size limits, but the host needs KVM. Docker runs each sandbox as a container on the host's kernel: CPU and memory limits but no disk quota, for trusted workloads or hosts without KVM.")}>
        <div className="wizard-choices">
          <Choice icon={Cpu} title="microsandbox" badge={t("Recommended")} selected={provider === "microsandbox"} onClick={() => { choose("microsandbox"); setStep("size"); }} />
          <Choice icon={Box} title="Docker" selected={provider === "docker"} onClick={() => { if (dockerConfirmed) selectDocker(); else setConfirmingDocker(true); }} />
        </div>
        <Nav onBack={back} t={t} />
      </Question>
    );
  } else if (step === "e2b") {
    page = (
      <Question title={t("Connect E2B")}>
        <div className="wizard-fields">
          <Field id={`${id}-key`} label={t("E2B API key")} help={t("The key is write-only: Core encrypts it and never shows it again.")}>
            <input id={`${id}-key`} type="password" autoComplete="off" spellCheck={false} value={apiKey} onChange={(event) => setApiKey(event.target.value)} />
          </Field>
          <Field id={`${id}-template`} label={t("Template build")} help={t("The exact ready build, as template-id:build-uuid. A template alias alone is not enough. Each sandbox gets the build's CPU and memory.")} error={template && !validTemplate(template.trim()) ? t("Enter a template ID and build UUID separated by a colon.") : null}>
            <input id={`${id}-template`} value={template} onChange={(event) => setTemplate(event.target.value)} placeholder="oac-runtime:0f1e2d3c-4b5a-6978-8a9b-0c1d2e3f4a5b" autoComplete="off" spellCheck={false} aria-invalid={Boolean(template && !validTemplate(template.trim()))} />
          </Field>
        </div>
        <Nav onBack={back} onNext={() => setStep("review")} nextDisabled={!e2bReady} t={t} />
      </Question>
    );
  } else if (step === "size") {
    const options = presets(provider ?? "docker");
    // A saved size outside the presets stays on offer as the current one.
    const kept = saved && provider && presetOf(provider, saved.resources) === null ? saved.resources : null;
    const disks = (value: SandboxResources) => (provider === "microsandbox" ? diskLabel(value) : undefined);
    page = (
      <Question title={t("How big is each sandbox?")} help={t("Every sandbox of this deployment gets these limits. How many run at once on a machine is set per node.")}>
        <div className={kept ? "wizard-choices wizard-choices-4" : "wizard-choices wizard-choices-3"}>
          {kept ? <Choice title={t("Current")} value={sizeLabel(kept)} detail={disks(kept)} selected={size === "current"} onClick={() => { setSize("current"); setResources(kept); setStep("review"); }} /> : null}
          {(Object.keys(options) as Preset[]).map((key) => (
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
          <div><dt>{t("Sandboxes run on")}</dt><dd>{where === "direct" ? t("E2B cloud") : `${t("Own machines")} · ${provider === "docker" ? "Docker" : "microsandbox"}`}</dd></div>
          <div><dt>{t("Each sandbox")}</dt><dd>{sized ? sizeLabel(resources) : t("From the template build")}{provider === "microsandbox" ? <span className="wizard-review-sub">{diskLabel(resources)}</span> : null}</dd></div>
          {provider === "e2b" ? <div><dt>{t("Template build")}</dt><dd><code>{template || "—"}</code></dd></div> : null}
          {needsRuntime ? (
            <div>
              <dt>{t("Runtime")}<HelpTip>{t("The Runtime release every node runs: the saved one while the backend stays the same, otherwise the one this console distributes.")}</HelpTip></dt>
              <dd>{runtimeReady ? <code>{release.source_commit!.slice(0, 12)}</code> : <span className="wizard-missing">{t("Runtime release needed")}<HelpTip>{t("This console serves no Runtime manifest. Enter the release under advanced settings.")}</HelpTip></span>}</dd>
            </div>
          ) : null}
          <div>
            <dt>{t("Core address")}<HelpTip>{t("The address nodes and sandboxes use to reach Core.")}</HelpTip></dt>
            <dd>
              {address ? <code>{address}</code> : "—"}
              <span className="wizard-review-sub">{t("Set by public_url in config.json")}</span>
              {installation.data?.local_only ? <span className="wizard-review-caution">{t("Only the Core machine can reach this address: nodes on other machines and E2B sandboxes can't. Set a public_url in config.json that other machines can reach (not loopback).")}</span> : null}
            </dd>
          </div>
        </dl>
        {rejection ? (
          <div className="wizard-rejection" role="alert">
            <p>{rejection}</p>
            {configuration ? (
              <dl>
                <div><dt>{t("Config file")}</dt><dd><CopyableId id={configuration.path} label={t("Copy path")} /></dd></div>
                <div><dt>{t("Then run")}</dt><dd><CopyableId id={configuration.apply_command} label={t("Copy command")} /></dd></div>
              </dl>
            ) : null}
          </div>
        ) : null}
        {/* Every save attempt clears the E2B key, so a second one needs it entered again. */}
        {provider === "e2b" && !apiKey.trim() ? (
          <p className="wizard-key-again">
            {t("Enter the E2B key again to save.")}
            <button className="wizard-link" type="button" onClick={() => setStep("e2b")}>{t("Enter the key")}</button>
          </p>
        ) : null}
        <button className="wizard-link" type="button" onClick={() => setStep("advanced")}>
          <SlidersHorizontal size={14} aria-hidden="true" />{t("Advanced settings")}
        </button>
        <div className="wizard-nav">
          <button className="button ghost" type="button" onClick={back}><ArrowLeft size={14} aria-hidden="true" />{t("Back")}</button>
          <button className="button primary" type="button" disabled={!ready} onClick={() => void save()}>
            {busy ? t("Saving…") : t(switching ? "Save and stay in maintenance" : "Save configuration")}<ArrowRight size={14} aria-hidden="true" />
          </button>
        </div>
      </Question>
    );
  } else {
    page = (
      <Question title={t("Advanced settings")}>
        <form className="wizard-fields" onSubmit={(event) => { event.preventDefault(); setStep("review"); }}>
          {sized ? <fieldset className="wizard-group">
            <legend>{t("Each sandbox")}<HelpTip>{t("1–255 CPUs, 512–1048576 MiB of memory. microsandbox disks are at least 1024 MiB.")}</HelpTip></legend>
            <div className="wizard-grid">
              <NumberField id={`${id}-cpus`} label={t("CPUs")} value={resources.cpus} onChange={(cpus) => { setSize("custom"); setResources({ ...resources, cpus }); }} />
              <NumberField id={`${id}-memory`} label={t("Memory (MiB)")} value={resources.memory_mib} onChange={(memory_mib) => { setSize("custom"); setResources({ ...resources, memory_mib }); }} />
              {provider === "microsandbox" ? <>
                <NumberField id={`${id}-root`} label={t("Root disk (MiB)")} value={resources.root_disk_mib ?? 0} onChange={(root_disk_mib) => setResources({ ...resources, root_disk_mib })} />
                <NumberField id={`${id}-data`} label={t("Data disk at /environment (MiB)")} value={resources.environment_disk_mib ?? 0} onChange={(environment_disk_mib) => setResources({ ...resources, environment_disk_mib })} />
              </> : null}
            </div>
          </fieldset> : null}
          {needsRuntime ? (
            <fieldset className="wizard-group">
              <legend>{t("Runtime release")}<HelpTip>{t("Filled in from this console's distribution when it serves one. Otherwise copy these from the distribution manifest that matches your nodes; image configuration IDs and manifest digests are different values.")}</HelpTip></legend>
              {RUNTIME_RELEASE_FIELDS.map((field) => {
                const value = release[field] ?? "";
                return (
                  <Field key={field} id={`${id}-${field}`} label={t(releaseLabels[field])} error={value && !isRuntimeReleaseField(field, value) ? t("Check this value") : null}>
                    <input id={`${id}-${field}`} value={value} spellCheck={false} autoComplete="off" onChange={(event) => setRuntime({ ...release, [field]: event.target.value.trim() })} />
                  </Field>
                );
              })}
            </fieldset>
          ) : null}
          {provider === "e2b" ? (
            <Field id={`${id}-template-advanced`} label={t("Template build")} error={template && !validTemplate(template.trim()) ? t("Enter a template ID and build UUID separated by a colon.") : null}>
              <input id={`${id}-template-advanced`} value={template} onChange={(event) => setTemplate(event.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          ) : null}
          <div className="wizard-nav">
            <span />
            <button className="button primary" type="submit" disabled={!sizeReady}>{t("Done")}<ArrowRight size={14} aria-hidden="true" /></button>
          </div>
        </form>
      </Question>
    );
  }

  return (
    <section className="sandbox-wizard" aria-label={t(switching ? "Change the sandbox configuration" : "Set up hosted sandboxes")}>
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
          open={confirmingDocker}
          title={t("Use Docker instead of microsandbox?")}
          initialFocus={keepMicrosandbox}
          onClose={() => setConfirmingDocker(false)}
          footer={<>
            <button type="button" className="button outline" onClick={selectDocker}>{t("Use Docker")}</button>
            <button ref={keepMicrosandbox} type="button" className="button primary" onClick={() => setConfirmingDocker(false)}>{t("Keep microsandbox")}</button>
          </>}
        >
          <ul className="wizard-docker-risks">
            <li><strong>{t("Weaker isolation")}</strong>{t("Containers share the host's kernel, so a container escape reaches the host. microsandbox runs each sandbox in its own microVM.")}</li>
            <li><strong>{t("Root-equivalent access")}</strong>{t("The node's service account joins the docker group, which is equivalent to root on that host.")}</li>
            <li><strong>{t("Limited use")}</strong>{t("Docker suits only trusted workloads, or hosts without KVM.")}</li>
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

function Field({ id, label, help, error, children }: { id: string; label: string; help?: string; error?: string | null; children: ReactNode }) {
  return (
    <div className="field wizard-field">
      <span className="field-label-row"><label htmlFor={id}>{label}</label>{help ? <HelpTip>{help}</HelpTip> : null}</span>
      {children}
      {error ? <span className="field-error" role="alert">{error}</span> : null}
    </div>
  );
}

function NumberField({ id, label, value, onChange }: { id: string; label: string; value: number; onChange: (value: number) => void }) {
  return (
    <Field id={id} label={label}>
      <input id={id} type="number" inputMode="numeric" min={1} step={1} value={Number.isFinite(value) ? value : ""} onChange={(event) => onChange(Number.parseInt(event.target.value, 10))} />
    </Field>
  );
}

function Nav({ onBack, onNext, nextDisabled, t }: { onBack: () => void; onNext?: () => void; nextDisabled?: boolean; t: (key: MessageKey) => string }) {
  return (
    <div className="wizard-nav">
      <button className="button ghost" type="button" onClick={onBack}><ArrowLeft size={14} aria-hidden="true" />{t("Back")}</button>
      {onNext ? <button className="button primary" type="button" disabled={nextDisabled} onClick={onNext}>{t("Next")}<ArrowRight size={14} aria-hidden="true" /></button> : null}
    </div>
  );
}
