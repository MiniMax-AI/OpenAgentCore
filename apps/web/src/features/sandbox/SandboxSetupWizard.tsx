import { AgentCoreError, type InitializeSandboxDeployment, type UpdateSandboxDeployment, type SandboxE2BReadyBuild, type SandboxE2BTemplate, type SandboxProvider, type SandboxResources, type SandboxRuntimeRelease, type SandboxSpecification } from "@oac/agents-client";
import { useQuery } from "@tanstack/react-query";
import { AnimatePresence } from "motion/react";
import * as m from "motion/react-m";
import { ArrowLeft, ArrowRight, Box, Cloud, Cpu, ExternalLink, Server, SlidersHorizontal, type LucideIcon } from "lucide-react";
import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { coreFieldError } from "../../lib/core-error";
import { formatBytes } from "../../lib/format";
import { installationQuery } from "../../lib/installation";
import type { MessageKey } from "../../lib/locale-strings";
import { sandboxConfigurationRejection } from "../../lib/sandbox-labels";
import { defaultSandboxResources, distributionRuntime, savedSpecification, validSandboxResources } from "./deployment-specification";
import { e2bKeyReady, e2bUpdateSelection } from "./sandbox-update";
import { isRuntimeRelease, isRuntimeReleaseField, RUNTIME_RELEASE_FIELDS } from "./runtime-release";
import { sandboxAdmin } from "./sandbox-queries";
import "./sandbox-wizard.css";

type Where = "nodes" | "direct";
type Step = "where" | "backend" | "e2b" | "size" | "review" | "advanced";
type Preset = "small" | "standard" | "large";
type Size = Preset | "current" | "custom";
type E2BService = "sandbase" | "official" | "custom";
const E2B_PRESETS = {
  sandbase: { apiURL: "https://sandbox.sandbase.ai", domain: "sandbox.sandbase.ai" },
  official: { apiURL: "https://api.e2b.app", domain: "e2b.app" },
} as const;
const E2B_KEY_CONSOLES = {
  sandbase: "https://www.sandbase.ai/console/keys",
  official: "https://e2b.dev/dashboard?tab=api-keys",
} as const;
function e2bService(apiURL?: string): E2BService {
  if (!apiURL) return "sandbase";
  if (apiURL === E2B_PRESETS.official.apiURL) return "official";
  return apiURL === E2B_PRESETS.sandbase.apiURL ? "sandbase" : "custom";
}

const MIB = 2 ** 20;
const EASE = [0.16, 1, 0.3, 1] as const;
// Core accepts a template ID of up to 128 characters and a canonical, non-nil build UUID.
const TEMPLATE = /^[a-zA-Z0-9_-]{1,128}:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;
function validTemplate(value: string): boolean {
  const build = TEMPLATE.exec(value)?.[1];
  return build !== undefined && /[^0-]/.test(build);
}

export function validEndpoint(apiURL: string, domain: string): boolean {
  const publicName = (host: string) => host.length <= 253 && host.includes(".") && !/^[0-9.]+$/.test(host) &&
    !host.endsWith(".local") && !host.endsWith(".localhost") &&
    host.split(".").every((label) => label.length <= 63 && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label));
  if (!apiURL && !domain) return true;
  if (!apiURL || !domain || apiURL.length > 512 || !publicName(domain)) return false;
  try {
    const url = new URL(apiURL);
    return url.protocol === "https:" && url.origin === apiURL && !url.username && !url.password &&
      !url.port && publicName(url.hostname) && (url.hostname === domain || url.hostname.endsWith(`.${domain}`));
  } catch {
    return false;
  }
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
 * defaults and this console's Runtime. E2B updates can retain the saved key.
 * Docker isolates less than microsandbox, so choosing it takes a confirmation,
 * once per wizard session; a saved Docker deployment has already made it.
 * Core's address is config.json's `public_url`: the review only shows it, and
 * a configuration Core rejects for it is explained here, where it was saved.
 */
type WizardProps = {
  coreUrl: string;
  expectedGeneration: number;
  current?: { provider: SandboxProvider; specification?: SandboxSpecification; e2bTemplate?: string; e2bAPIURL?: string; e2bDomain?: string };
  disabled: boolean;
} & ({ editing: true; onSubmit: (input: UpdateSandboxDeployment) => Promise<void> } |
  { editing?: false; onSubmit: (input: InitializeSandboxDeployment) => Promise<void> });

export function SandboxSetupWizard({ coreUrl, expectedGeneration, current, disabled, editing, onSubmit }: WizardProps) {
  const { t, i18n } = useTranslation("sandbox");
  const { t: tCommon } = useTranslation("common");
  const id = useId();
  const [step, setStep] = useState<Step>(editing ? current?.provider === "e2b" ? "e2b" : "size" : "where");
  const [where, setWhere] = useState<Where | null>(current ? (current.provider === "e2b" ? "direct" : "nodes") : null);
  const [provider, setProvider] = useState<SandboxProvider | null>(current?.provider ?? null);
  const saved = provider && current ? savedSpecification(provider, current.provider, current.specification) : null;
  const [resources, setResources] = useState<SandboxResources>(current?.specification?.resources ?? defaultSandboxResources("docker"));
  const [size, setSize] = useState<Size>(current?.specification ? presetOf(current.provider, current.specification.resources) ?? "current" : "standard");
  const [apiKey, setApiKey] = useState("");
  const [replacementRequested, setReplacementRequested] = useState(false);
  const [template, setTemplate] = useState(current?.e2bTemplate ?? "");
  const [service, setService] = useState<E2BService>(e2bService(current?.e2bAPIURL));
  const [apiURL, setAPIURL] = useState(current?.e2bAPIURL ?? E2B_PRESETS[e2bService(current?.e2bAPIURL) as keyof typeof E2B_PRESETS]?.apiURL ?? "");
  const [domain, setDomain] = useState(current?.e2bDomain ?? E2B_PRESETS[e2bService(current?.e2bAPIURL) as keyof typeof E2B_PRESETS]?.domain ?? "");
  const keyConsoleURL = service !== "custom" && apiURL === E2B_PRESETS[service].apiURL && domain === E2B_PRESETS[service].domain
    ? E2B_KEY_CONSOLES[service] : null;
  const [templates, setTemplates] = useState<SandboxE2BTemplate[]>([]);
  const [builds, setBuilds] = useState<SandboxE2BReadyBuild[]>([]);
  const [selectedTemplate, setSelectedTemplate] = useState(current?.e2bTemplate?.split(":")[0] ?? "");
  const [discovery, setDiscovery] = useState<"idle" | "loading" | "ready" | "error">("idle");
  const [buildDiscovery, setBuildDiscovery] = useState<"idle" | "loading" | "ready" | "error">("idle");
  const [discoveryRetry, setDiscoveryRetry] = useState(0);
  const [runtime, setRuntime] = useState<Partial<SandboxRuntimeRelease>>({});
  const [busy, setBusy] = useState(false);
  const [dockerConfirmed, setDockerConfirmed] = useState(current?.provider === "docker");
  const [confirmingDocker, setConfirmingDocker] = useState(false);
  const keepMicrosandbox = useRef<HTMLButtonElement>(null);
  // Core's reason for rejecting the saved configuration, such as E2B with a loopback public_url.
  const [rejection, setRejection] = useState<string | null>(null);
  const [fieldRejection, setFieldRejection] = useState<unknown>(null);
  const fieldError = (param: string) => coreFieldError(fieldRejection, param, tCommon);
  const [resetRequired, setResetRequired] = useState(false);
  const [addressRejected, setAddressRejected] = useState(false);
  const installation = useQuery(installationQuery);
  const address = coreUrl || installation.data?.public_url || null;
  const { navigate } = useConsoleNavigation();

  useEffect(() => {
    setTemplates([]); setBuilds([]); setDiscovery("idle"); setBuildDiscovery("idle");
    if (provider !== "e2b" || !apiKey.trim() || !validEndpoint(apiURL.trim(), domain.trim())) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      setDiscovery("loading");
      void sandboxAdmin.listE2BTemplates({ api_key: apiKey.trim(), api_url: apiURL.trim(), domain: domain.trim() }, { signal: controller.signal })
        .then((items) => { if (!controller.signal.aborted) { setTemplates(items); setDiscovery("ready"); } })
        .catch(() => { if (!controller.signal.aborted) setDiscovery("error"); });
    }, 500);
    return () => { window.clearTimeout(timer); controller.abort(); };
  }, [provider, apiKey, apiURL, domain, discoveryRetry]);

  useEffect(() => {
    setBuilds([]); setBuildDiscovery("idle");
    if (discovery !== "ready" || !selectedTemplate || !templates.some((item) => item.id === selectedTemplate)) return;
    const controller = new AbortController();
    setBuildDiscovery("loading");
    void sandboxAdmin.listE2BReadyBuilds(selectedTemplate, { api_key: apiKey.trim(), api_url: apiURL.trim(), domain: domain.trim() }, { signal: controller.signal })
      .then((items) => { if (!controller.signal.aborted) { setBuilds(items); setBuildDiscovery("ready"); } })
      .catch(() => { if (!controller.signal.aborted) setBuildDiscovery("error"); });
    return () => controller.abort();
  }, [discovery, selectedTemplate, templates, apiKey, apiURL, domain, discoveryRetry]);

  function changeConnection(key: string, url: string, dataDomain: string) {
    setApiKey(key); setAPIURL(url); setDomain(dataDomain);
    setTemplate(""); setSelectedTemplate(""); setTemplates([]); setBuilds([]);
  }

  // A release the administrator entered comes first, then the saved one of the same backend,
  // then the one this console distributes (the release its node installer verifies).
  const matched = useQuery({ queryKey: ["sandbox-runtime-release"], queryFn: ({ signal }) => distributionRuntime(signal).catch(() => null), staleTime: Infinity, retry: false });
  const release: Partial<SandboxRuntimeRelease> = Object.keys(runtime).length ? runtime : saved?.runtime ?? matched.data ?? {};

  const needsRuntime = provider === "docker" || provider === "microsandbox";
  const runtimeReady = !needsRuntime || isRuntimeRelease(release);
  // Initial setup requires a key; an update may retain the committed key.
  const keyReady = e2bKeyReady(Boolean(editing), replacementRequested, apiKey);
  const connectionChanged = Boolean(editing && (apiURL.trim() !== (current?.e2bAPIURL || E2B_PRESETS.official.apiURL) || domain.trim() !== (current?.e2bDomain || E2B_PRESETS.official.domain)));
  const e2bReady = provider !== "e2b" || (keyReady && validTemplate(template.trim()) && validEndpoint(apiURL.trim(), domain.trim()) && (!editing || !connectionChanged || apiKey.trim().length > 0));
  // Core sizes E2B sandboxes from the template build, so E2B sends no resources.
  const sized = provider !== null && provider !== "e2b";
  const sizeReady = provider !== null && (!sized || validSandboxResources(provider, resources));
  const ready = provider !== null && runtimeReady && e2bReady && sizeReady && !disabled && !busy;

  const order: Step[] = editing ? where === "direct" ? ["e2b", "review"] : ["size", "review"] : where === "direct" ? ["where", "e2b", "review"] : ["where", "backend", "size", "review"];
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
    setBusy(true); setRejection(null); setFieldRejection(null);
    try {
      const selection = {
        provider, expected_generation: expectedGeneration,
        ...(sized ? { resources } : {}),
        ...(needsRuntime ? { runtime: release as SandboxRuntimeRelease } : {}),
      };
      if (editing) await onSubmit({ ...selection, ...(provider === "e2b" ? { ...e2bUpdateSelection(template, apiKey), configuration: { template: template.trim(), api_url: apiURL.trim(), domain: domain.trim() } } : {}) });
      else await onSubmit({ ...selection, ...(provider === "e2b" ? { credential: { api_key: apiKey.trim() }, configuration: { template: template.trim(), api_url: apiURL.trim(), domain: domain.trim() } } : {}) });
    } catch (error) {
      // A configuration Core rejected is explained here; the page reports every other failure.
      const reason = sandboxConfigurationRejection(error, i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en");
      if (reason === null) throw error;
      setRejection(reason);
      setFieldRejection(error);
      if (error instanceof AgentCoreError && error.param) {
        if (["credential", "configuration", "e2b.api_url", "e2b.domain"].includes(error.param) && error.code !== "sandbox_credential_ownership") setStep("e2b");
        else if (error.param === "runtime" || error.param.startsWith("resources.")) setStep("advanced");
      }
      setResetRequired(error instanceof AgentCoreError && ["sandbox_credential_ownership", "sandbox_reset_required"].includes(error.code ?? ""));
      setAddressRejected(error instanceof AgentCoreError && error.code === "sandbox_configuration_error");
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
          <Field id={`${id}-service`} label={t("E2B provider")}>
            <select id={`${id}-service`} value={service} onChange={(event) => {
              const next = event.target.value as E2BService;
              setService(next);
              const preset = next === "custom" ? { apiURL: "", domain: "" } : E2B_PRESETS[next];
              changeConnection("", preset.apiURL, preset.domain);
            }}>
              <option value="sandbase">SandBase Sandbox</option>
              <option value="official">E2B</option>
              <option value="custom">{t("Other E2B-compatible provider")}</option>
            </select>
          </Field>
          <Field id={`${id}-key`} label={t("E2B API key")} error={fieldError("credential")} help={t(editing ? "Leave blank to keep the saved key. Any key you enter is verified as a replacement, even if unchanged." : "The key is write-only: Core encrypts it and never shows it again.")} afterHelp={keyConsoleURL ? (
            <a className="wizard-key-console" href={keyConsoleURL} target="_blank" rel="noopener noreferrer">
              {t("Console → API Keys")} <ExternalLink size={11} aria-hidden="true" />
            </a>
          ) : null}>
            <input id={`${id}-key`} type="password" autoComplete="off" spellCheck={false} value={apiKey} onChange={(event) => { if (editing) { setApiKey(event.target.value); setTemplates([]); setBuilds([]); } else changeConnection(event.target.value, apiURL, domain); setReplacementRequested(Boolean(event.target.value.trim())); setFieldRejection(null); }} />
          </Field>
          {discovery === "loading" ? <p role="status">{t("Loading templates…")}</p> : null}
          {discovery === "error" ? <p role="alert">{t("Could not load templates. Check the key and provider connection.")} <button type="button" className="wizard-link" onClick={() => setDiscoveryRetry((value) => value + 1)}>{t("Try again")}</button></p> : null}
          {discovery === "ready" && templates.length === 0 ? <p role="status">{t("No templates are visible to this key.")}</p> : null}
          {editing && !apiKey.trim() ? <Field id={`${id}-saved-template`} label={t("Template build")} error={fieldError("configuration")}><input id={`${id}-saved-template`} value={template} onChange={(event) => setTemplate(event.target.value)} /></Field> : <>
          <Field id={`${id}-template`} label={t("Template")}>
            <select id={`${id}-template`} value={selectedTemplate} disabled={discovery !== "ready"} onChange={(event) => { setSelectedTemplate(event.target.value); setTemplate(""); }}>
              <option value="">{t("Select a template")}</option>
              {editing && selectedTemplate && !templates.some((item) => item.id === selectedTemplate) ? <option value={selectedTemplate}>{t("Current")} · {selectedTemplate}</option> : null}
              {templates.map((item) => <option key={item.id} value={item.id}>{item.names[0] ? `${item.names[0]} · ` : ""}{item.id}</option>)}
            </select>
          </Field>
          {buildDiscovery === "loading" ? <p role="status">{t("Loading ready builds…")}</p> : null}
          {buildDiscovery === "error" ? <p role="alert">{t("Could not load builds.")} <button type="button" className="wizard-link" onClick={() => setDiscoveryRetry((value) => value + 1)}>{t("Try again")}</button></p> : null}
          {buildDiscovery === "ready" && builds.length === 0 ? <p role="status">{t("This template has no ready builds.")}</p> : null}
          <Field id={`${id}-build`} label={t("Template build")} help={t("Core validates the exact ready build again when you save.")}>
            <select id={`${id}-build`} value={template} disabled={buildDiscovery !== "ready"} onChange={(event) => setTemplate(event.target.value)}>
              <option value="">{t("Select a ready build")}</option>
              {editing && template && !builds.some((item) => `${selectedTemplate}:${item.id}` === template) ? <option value={template}>{t("Current")} · {template}</option> : null}
              {builds.map((item) => <option key={item.id} value={`${selectedTemplate}:${item.id}`}>{item.id} · {item.cpus} CPU / {item.memory_mib} MiB</option>)}
            </select>
          </Field>
          </>}
          <Field id={`${id}-api-url`} label={t("Sandbox API URL")} help={t("The selected provider supplies a default. You can edit it for a compatible endpoint.")}>
            <input id={`${id}-api-url`} type="url" value={apiURL} onChange={(event) => changeConnection(apiKey, event.target.value, domain)} placeholder="https://sandbox.example.com" autoComplete="off" spellCheck={false} />
          </Field>
          <Field id={`${id}-domain`} label={t("Sandbox data-plane domain")} error={(apiURL || domain) && !validEndpoint(apiURL.trim(), domain.trim()) ? t("Enter both a public HTTPS API origin and a domain.") : null}>
            <input id={`${id}-domain`} value={domain} onChange={(event) => changeConnection(apiKey, apiURL, event.target.value)} placeholder="sandbox.example.com" autoComplete="off" spellCheck={false} />
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
      <Question title={t("How big is each sandbox?")} help={t("These limits apply to the selected configuration generation. Existing sandboxes keep their limits. Concurrency is set per node.")}>
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
          {provider === "e2b" ? <div><dt>{t("Sandbox API URL")}</dt><dd><code>{apiURL || "https://api.e2b.app"}</code></dd></div> : null}
          {provider === "e2b" ? <div><dt>{t("Sandbox data-plane domain")}</dt><dd><code>{domain || "e2b.app"}</code></dd></div> : null}
          {provider === "e2b" && editing ? <div><dt>{t("E2B credential")}</dt><dd>{t(replacementRequested ? "Replace saved key" : "Keep saved key")}</dd></div> : null}
          {needsRuntime ? (
            <div>
              <dt>{t("Runtime")}<HelpTip>{t("The target Runtime release. Existing sandboxes keep their owned release while nodes prepare the target.")}</HelpTip></dt>
              <dd>{runtimeReady ? <code>{release.source_commit!.slice(0, 12)}</code> : <span className="wizard-missing">{t("Runtime release needed")}<HelpTip>{t("This console serves no Runtime manifest. Enter the release under advanced settings.")}</HelpTip></span>}</dd>
            </div>
          ) : null}
          <div>
            <dt>{t("Core address")}<HelpTip>{t("The address nodes and sandboxes use to reach Core.")}</HelpTip></dt>
            <dd>
              {address ? <code>{address}</code> : "—"}
              <span className="wizard-review-sub">{t("Managed in System")}</span>
              {installation.data?.local_only ? <span className="wizard-review-caution">{t("Configure HTTPS in System before connecting remote nodes or E2B sandboxes.")}</span> : null}
            </dd>
          </div>
        </dl>
        {rejection ? (
          <div className="wizard-rejection" role="alert">
            <p>{rejection}</p>
            {resetRequired ? <p>{t("Cancel editing to use Reset deployment. This change requires an explicit reset; the saved configuration is unchanged.")}</p> : null}
            {addressRejected ? <button className="text-action" type="button" onClick={() => navigate("system", { id: "domain" })}>{t("Managed in System")}</button> : null}
          </div>
        ) : null}
        {/* Initial setup needs the cleared key re-entered; updates may keep the saved key. */}
        {provider === "e2b" && !keyReady ? (
          <p className="wizard-key-again">
            {t("Enter the E2B key again to save.")}
            <button className="wizard-link" type="button" onClick={() => setStep("e2b")}>{t("Enter the key")}</button>
            {editing ? <button className="wizard-link" type="button" onClick={() => setReplacementRequested(false)}>{t("Keep saved key")}</button> : null}
          </p>
        ) : null}
        <button className="wizard-link" type="button" onClick={() => setStep("advanced")}>
          <SlidersHorizontal size={14} aria-hidden="true" />{t("Advanced settings")}
        </button>
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
            <legend>{t("Each sandbox")}<HelpTip>{t("1–255 CPUs, 512–1048576 MiB of memory. microsandbox disks are at least 1024 MiB.")}</HelpTip></legend>
            <div className="wizard-grid">
              <NumberField error={fieldError("resources.cpus")} id={`${id}-cpus`} label={t("CPUs")} value={resources.cpus} onChange={(cpus) => { setSize("custom"); setResources({ ...resources, cpus }); setFieldRejection(null); }} />
              <NumberField error={fieldError("resources.memory_mib")} id={`${id}-memory`} label={t("Memory (MiB)")} value={resources.memory_mib} onChange={(memory_mib) => { setSize("custom"); setResources({ ...resources, memory_mib }); setFieldRejection(null); }} />
              {provider === "microsandbox" ? <>
                <NumberField error={fieldError("resources.root_disk_mib")} id={`${id}-root`} label={t("Root disk (MiB)")} value={resources.root_disk_mib ?? 0} onChange={(root_disk_mib) => { setResources({ ...resources, root_disk_mib }); setFieldRejection(null); }} />
                <NumberField error={fieldError("resources.environment_disk_mib")} id={`${id}-data`} label={t("Data disk at /environment (MiB)")} value={resources.environment_disk_mib ?? 0} onChange={(environment_disk_mib) => { setResources({ ...resources, environment_disk_mib }); setFieldRejection(null); }} />
              </> : null}
            </div>
          </fieldset> : null}
          {needsRuntime ? (
            <fieldset className="wizard-group">
              <legend>{t("Runtime release")}<HelpTip>{t("Filled in from this console's distribution when it serves one. Otherwise copy these from the distribution manifest that matches your nodes; image configuration IDs and manifest digests are different values.")}</HelpTip></legend>
              {fieldError("runtime") ? <p id={`${id}-runtime-error`} className="field-error" role="alert">{fieldError("runtime")}</p> : null}
              {RUNTIME_RELEASE_FIELDS.map((field) => {
                const value = release[field] ?? "";
                return (
                  <Field key={field} id={`${id}-${field}`} label={t(releaseLabels[field])} error={value && !isRuntimeReleaseField(field, value) ? t("Check this value") : null}>
                    <input id={`${id}-${field}`} value={value} spellCheck={false} autoComplete="off" aria-invalid={Boolean(fieldError("runtime"))} aria-describedby={fieldError("runtime") ? `${id}-runtime-error` : undefined} onChange={(event) => { setRuntime({ ...release, [field]: event.target.value.trim() }); setFieldRejection(null); }} />
                  </Field>
                );
              })}
            </fieldset>
          ) : null}
          {provider === "e2b" ? (
            <Field id={`${id}-template-advanced`} label={t("Template build")} error={fieldError("configuration") ?? (template && !validTemplate(template.trim()) ? t("Enter a template ID and build UUID separated by a colon.") : null)}>
              <input id={`${id}-template-advanced`} value={template} onChange={(event) => { setTemplate(event.target.value); setFieldRejection(null); }} autoComplete="off" spellCheck={false} />
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

function Nav({ onBack, onNext, nextDisabled, t }: { onBack: () => void; onNext?: () => void; nextDisabled?: boolean; t: (key: MessageKey) => string }) {
  return (
    <div className="wizard-nav">
      <button className="button ghost" type="button" onClick={onBack}><ArrowLeft size={14} aria-hidden="true" />{t("Back")}</button>
      {onNext ? <button className="button primary" type="button" disabled={nextDisabled} onClick={onNext}>{t("Next")}<ArrowRight size={14} aria-hidden="true" /></button> : null}
    </div>
  );
}
