import { Check, Copy, ExternalLink, Folder, HardDrive, TerminalSquare } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import type {
  AgentCore,
  AgentEnvironment,
  EnvironmentConnectionAction,
  EnvironmentResourceStatus,
  SessionEnvironmentStatus,
} from "@agents-core-web/agents-client";

import { StatusIcon, type StatusKind } from "../../../components/StatusIcon";
import type { LocalDockerGuideProfile } from "../../../lib/docker-guide-config";
import {
  buildLauncherCommand,
  buildLocalDockerCommand,
  isSupportedSelfHostedEnvironmentProjection,
} from "./environment-launcher";
import { EnvironmentFilesPanel, type ListEnvironmentFiles } from "./EnvironmentFilesPanel";
import { EnvironmentFileCreatePanel } from "./EnvironmentFileCreatePanel";
import {
  environmentIdsMatch,
  isSupportedOpenAIHostedEnvironmentProjection,
  isWritableBasicHostedEnvironmentResource,
  type EnvironmentObservation,
} from "./environment-state";

const coreSetupUrl = "https://github.com/MiniMax-AI/parsar-core/blob/main/services/agents-api/README.md#native-executor-transport-prerequisite";
const launcherSetupUrl = "https://github.com/MiniMax-AI/parsar-core/blob/main/packages/codex-executor/README.md#connect-an-executor";
const hostedSetupUrl = "https://github.com/MiniMax-AI/parsar-core/blob/main/services/agents-api/HOSTED-RELEASE.md";

export interface SafeRemoteUrl {
  href: string;
  label: string;
}

export function sanitizeRemoteUrl(value: unknown): SafeRemoteUrl | null {
  if (typeof value !== "string" || !value.trim()) return null;
  try {
    const url = new URL(value);
    if ((url.protocol !== "https:" && url.protocol !== "http:") || !url.hostname) return null;
    url.username = "";
    url.password = "";
    url.search = "";
    url.hash = "";
    const sanitized = url.toString();
    return { href: sanitized, label: sanitized };
  } catch {
    return null;
  }
}

function field(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function directories(value: unknown): string[] | null {
  return Array.isArray(value) && value.every((entry) => typeof entry === "string")
    ? value
    : null;
}

export type EnvironmentDisplayStatus = SessionEnvironmentStatus | EnvironmentResourceStatus | "required" | "unknown" | "unavailable";

function statusKind(status: EnvironmentDisplayStatus): StatusKind {
  if (status === "connected" || status === "ready") return "completed";
  if (status === "failed") return "failed";
  if (status === "pending" || status === "required") return "running";
  return "interrupted";
}

function statusLabel(status: EnvironmentDisplayStatus): string {
  if (status === "required") return "Connection required";
  if (status === "unknown") return "Unknown";
  if (status === "unavailable") return "Unavailable";
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function matchingObservation(
  observation: EnvironmentObservation | null,
  environmentId: string | null,
  environmentType: "self_hosted" | "openai_hosted",
): EnvironmentObservation | null {
  return observation?.environmentType === environmentType && environmentIdsMatch(observation.environmentId, environmentId)
    ? observation
    : null;
}

export interface EnvironmentPresentation {
  visible: boolean;
  status: EnvironmentDisplayStatus;
  statusKind: StatusKind;
  statusLabel: string;
  triggerLabel: string;
  defaultLauncherGuideOpen: boolean;
}

export function resolveEnvironmentPresentation(
  environment: AgentEnvironment,
  observation: EnvironmentObservation | null,
  connectionActions: EnvironmentConnectionAction[],
): EnvironmentPresentation {
  const raw = environment !== null && typeof environment === "object" && !Array.isArray(environment)
    ? environment as unknown as Record<string, unknown>
    : {};
  const type = typeof raw.type === "string" ? raw.type : null;

  if (type === "none") {
    return {
      visible: false,
      status: "unavailable",
      statusKind: "interrupted",
      statusLabel: "Unavailable",
      triggerLabel: "Environment unavailable",
      defaultLauncherGuideOpen: false,
    };
  }

  const environmentId = field(raw.id);
  const supportedType = type === "self_hosted" || type === "openai_hosted" ? type : null;
  const live = supportedType ? matchingObservation(observation, environmentId, supportedType) : null;
  const requiresConnection = type === "self_hosted" && Boolean(environmentId && connectionActions.some(
    (action) => environmentIdsMatch(action.environment_id, environmentId),
  ));
  const status: EnvironmentDisplayStatus = !supportedType
    ? "unavailable"
    : live?.source === "unavailable"
      ? "unavailable"
      : live?.status ?? (requiresConnection ? "required" : "unknown");
  const prefix = type === "openai_hosted" ? "Managed Environment" : "Environment";
  const triggerLabel = status === "connected"
    ? `${prefix} connected`
    : status === "ready"
      ? `${prefix} ready`
      : status === "required" || status === "disconnected"
        ? type === "openai_hosted" ? `${prefix} disconnected` : "Connect environment"
        : status === "pending"
          ? `${prefix} pending`
          : status === "failed"
            ? `${prefix} failed`
            : status === "expired"
              ? `${prefix} expired`
              : status === "unknown"
                ? `${prefix} status unknown`
                : `${prefix} unavailable`;

  return {
    visible: true,
    status,
    statusKind: statusKind(status),
    statusLabel: statusLabel(status),
    triggerLabel,
    defaultLauncherGuideOpen: type === "self_hosted" && (status === "required" || status === "pending" || status === "disconnected" || status === "failed" || status === "expired"),
  };
}

function EnvironmentLauncherGuide({
  environmentId,
  remoteUrl,
  workspaceDirectory,
  capabilityDirectories,
  dockerGuideProfile,
  defaultOpen,
}: {
  environmentId: unknown;
  remoteUrl: unknown;
  workspaceDirectory: unknown;
  capabilityDirectories: unknown;
  dockerGuideProfile: LocalDockerGuideProfile | null;
  defaultOpen: boolean;
}) {
  const { t } = useTranslation("sessions");
  const [copied, setCopied] = useState(false);
  const launcherCommand = buildLauncherCommand(
    environmentId,
    remoteUrl,
    workspaceDirectory,
    capabilityDirectories,
  );
  const dockerCommand = buildLocalDockerCommand(
    environmentId,
    remoteUrl,
    workspaceDirectory,
    capabilityDirectories,
    dockerGuideProfile,
  );
  const [commandType, setCommandType] = useState<"docker" | "native">(
    dockerCommand ? "docker" : "native",
  );
  const [guideOpen, setGuideOpen] = useState(defaultOpen);
  const command = commandType === "docker" && dockerCommand ? dockerCommand : launcherCommand;

  useEffect(() => {
    setCommandType(dockerCommand ? "docker" : "native");
    setCopied(false);
    setGuideOpen(defaultOpen);
  }, [defaultOpen, dockerCommand, launcherCommand]);

  if (!command) {
    return (
      <div className="environment-launcher-unavailable" role="note">
        <strong>{t("environment.connectUnavailable")}</strong>
        <p>{t("environment.connectUnavailableDetail")}</p>
      </div>
    );
  }

  const copyCommand = async () => {
    if (!navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  return (
    <details
      className="environment-launcher-guide"
      open={guideOpen}
      onToggle={(event) => setGuideOpen(event.currentTarget.open)}
    >
      <summary>
        <TerminalSquare size={14} strokeWidth={1.5} aria-hidden="true" />
        <span><strong>{t("environment.connect")}</strong><small>{t("environment.copyCommandHint")}</small></span>
      </summary>
      <div className="environment-launcher-body">
        {dockerCommand ? (
          <div className="environment-launcher-modes" role="group" aria-label={t("environment.commandType")}>
            <button
              type="button"
              aria-pressed={commandType === "docker"}
              onClick={() => { setCommandType("docker"); setCopied(false); }}
            >Docker</button>
            <button
              type="button"
              aria-pressed={commandType === "native"}
              onClick={() => { setCommandType("native"); setCopied(false); }}
            >{t("environment.linuxVm")}</button>
          </div>
        ) : null}
        {commandType === "docker" && dockerCommand ? (
          <p>
            {t("environment.dockerCommandPrefix")} <code>{String(workspaceDirectory)}</code>{t("environment.dockerCommandSuffix")}
          </p>
        ) : (
          <p>
            {t("environment.nativeCommandHint")}
          </p>
        )}
        <pre><code>{command}</code></pre>
        <button className="button outline" type="button" onClick={() => void copyCommand()}>
          {copied ? <Check size={13} aria-hidden="true" /> : <Copy size={13} aria-hidden="true" />}
          {copied ? t("common.copied") : commandType === "docker" && dockerCommand ? t("environment.copyDockerCommand") : t("environment.copyNativeCommand")}
        </button>
        <p className="environment-launcher-security">
          {t("environment.launcherSecurity")}
        </p>
      </div>
    </details>
  );
}

export function EnvironmentConnectionNotice({
  action,
  onOpenSetup,
}: {
  action: EnvironmentConnectionAction;
  onOpenSetup?: () => void;
}) {
  const { t } = useTranslation("sessions");
  return (
    <section className="environment-connection-notice" aria-label={t("environment.connectionRequired")}>
      <div className="environment-connection-notice-heading">
        <TerminalSquare size={14} strokeWidth={1.5} aria-hidden="true" />
        <strong>{t("environment.connectionRequired")}</strong>
      </div>
      <p>
        {t("environment.connectionRequiredPrefix")} <code>{action.environment_id}</code> {t("environment.connectionRequiredSuffix")}
      </p>
      {onOpenSetup ? (
        <button className="button outline environment-connection-notice-action" type="button" onClick={onOpenSetup}>
          {t("environment.openSetup")}
        </button>
      ) : null}
    </section>
  );
}

export function EnvironmentPanel({
  environment,
  observation,
  connectionActions,
  dockerGuideProfile = __AGENTS_CORE_WEB_DOCKER_GUIDE__,
  defaultLauncherGuideOpen = false,
  environmentFilesEnabled = __AGENTS_CORE_WEB_ENVIRONMENT_FILES__,
  onListFiles,
  onCreateFile,
}: {
  environment: AgentEnvironment;
  observation: EnvironmentObservation | null;
  connectionActions: EnvironmentConnectionAction[];
  dockerGuideProfile?: LocalDockerGuideProfile | null;
  defaultLauncherGuideOpen?: boolean;
  environmentFilesEnabled?: boolean;
  onListFiles?: ListEnvironmentFiles;
  onCreateFile?: AgentCore["createEnvironmentFile"];
}) {
  const { t } = useTranslation("sessions");
  const raw = environment !== null && typeof environment === "object" && !Array.isArray(environment)
    ? environment as unknown as Record<string, unknown>
    : {};
  const type = typeof raw.type === "string" ? raw.type : null;
  const presentation = resolveEnvironmentPresentation(environment, observation, connectionActions);

  if (type === "none") {
    return null;
  }

  if (type === "openai_hosted") {
    const supportedHostedProjection = isSupportedOpenAIHostedEnvironmentProjection(environment);
    const environmentId = field(raw.id);
    const network = raw.network !== null && typeof raw.network === "object" && !Array.isArray(raw.network)
      ? raw.network as Record<string, unknown>
      : null;
    const packages = raw.packages !== null && typeof raw.packages === "object" && !Array.isArray(raw.packages)
      ? raw.packages as Record<string, unknown>
      : null;
    const capabilityDirectories = directories(raw.capability_directories);
    const npmPackages = directories(packages?.npm);
    const pythonPackages = directories(packages?.python);
    const systemPackages = directories(packages?.system);
    const installedFiles = Array.isArray(raw.files) ? raw.files : null;
    const installedPlugins = Array.isArray(raw.plugins) ? raw.plugins : null;
    const installedSkills = Array.isArray(raw.skills) ? raw.skills : null;
    const live = matchingObservation(observation, environmentId, "openai_hosted");
    const durableResource = live?.source === "durable"
      ? live.resource
      : live?.source === "live"
        ? live.durableResource
        : undefined;
    const status = presentation.status;
    const exactDurableHosted = status !== "failed" && status !== "expired" &&
      isWritableBasicHostedEnvironmentResource(durableResource, environmentId);
    const networkAccess = network?.access === "enabled"
      ? t("common.enabled")
      : network?.access === "disabled"
        ? t("common.disabled")
        : network?.access === "restricted"
          ? t("common.restricted")
          : t("common.unavailable");
    const allowedDomains = directories(network?.allowed_domains);

    return (
      <section className="environment-panel environment-panel-managed" aria-label={t("environment.statusAria")}>
        <div className="environment-panel-heading">
          <HardDrive size={15} strokeWidth={1.5} aria-hidden="true" />
          <div>
            <strong>{t("environment.managedHosted")}</strong>
            <span>{environmentId ?? t("environment.idUnavailable")}</span>
          </div>
          <div className={`environment-panel-status environment-panel-status-${status}`} role="status" aria-live="polite">
            <StatusIcon status={presentation.statusKind} />
            <span>{t(`environment.status.${status}` as never)}</span>
          </div>
        </div>

        <div className="environment-panel-grid">
          <div className="environment-panel-field">
            <span>{t("environment.id")}</span>
            <code>{environmentId ?? t("common.unavailable")}</code>
          </div>
          <div className="environment-panel-field">
            <span>{t("environment.networkAccess")}</span>
            <strong>{networkAccess}</strong>
          </div>
          <div className="environment-panel-field environment-panel-field-wide">
            <span>{t("environment.workspaceDirectory")}</span>
            <code><Folder size={12} strokeWidth={1.5} aria-hidden="true" />/workspace</code>
          </div>
          <div className="environment-panel-field environment-panel-field-wide">
            <span>{t("environment.allowedDomains")}</span>
            <strong>{allowedDomains ? allowedDomains.length === 0 ? t("environment.noneBasicDomains") : allowedDomains.join(", ") : t("common.unavailable")}</strong>
          </div>
          <div className="environment-panel-field environment-panel-field-wide">
            <span>{t("environment.startupPackages")}</span>
            {npmPackages && pythonPackages && systemPackages ? (
              <strong>{npmPackages.length + pythonPackages.length + systemPackages.length === 0
                ? t("environment.noneBasicPackages")
                : t("environment.packageCounts", { npm: npmPackages.length, python: pythonPackages.length, system: systemPackages.length })}</strong>
            ) : <strong>{t("common.unavailable")}</strong>}
          </div>
          <div className="environment-panel-field environment-panel-field-wide">
            <span>{t("environment.installedMetadata")}</span>
            {capabilityDirectories && installedFiles && installedPlugins && installedSkills ? (
              <strong>{t("environment.installedCounts", { directories: capabilityDirectories.length, files: installedFiles.length, plugins: installedPlugins.length, skills: installedSkills.length })}</strong>
            ) : <strong>{t("common.unavailable")}</strong>}
          </div>
        </div>

        <p className="environment-panel-provenance">
          {live?.source === "live"
            ? t("environment.managedLiveProvenance")
            : live?.source === "durable"
              ? t("environment.managedDurableProvenance")
              : live?.source === "unavailable"
                ? t("environment.managedUnavailableProvenance")
                : t("environment.managedUnknownProvenance")}
        </p>

        {environmentFilesEnabled && supportedHostedProjection && environmentId && onListFiles ? (
          <EnvironmentFilesPanel
            key={`${environmentId}:/workspace`}
            environmentId={environmentId}
            workspaceDirectory="/workspace"
            onListFiles={onListFiles}
          />
        ) : null}

        {environmentFilesEnabled && supportedHostedProjection && exactDurableHosted && environmentId && onCreateFile ? (
          <EnvironmentFileCreatePanel
            key={`create:${environmentId}`}
            environmentId={environmentId}
            workspaceDirectory="/workspace"
            onCreateFile={onCreateFile}
          />
        ) : null}

        {status === "failed" || status === "expired" ? (
          <div className="environment-panel-error" role="alert">
            <strong>{t("environment.managedTerminal", { status: t(`environment.status.${status}` as never) })}</strong>
            <p>{t("environment.managedTerminalDetail")}</p>
          </div>
        ) : null}

        <footer className="environment-panel-footer">
          <p>{t("environment.managedFooter")}</p>
          <nav aria-label={t("environment.managedDocs")}>
            <a href={hostedSetupUrl} target="_blank" rel="noreferrer">{t("environment.operatorSetup")}<ExternalLink size={11} aria-hidden="true" /></a>
          </nav>
        </footer>
      </section>
    );
  }

  if (type !== "self_hosted") {
    return (
      <section className="environment-panel environment-panel-unavailable" aria-label={t("environment.statusAria")}>
        <div className="environment-panel-heading">
          <StatusIcon status="interrupted" />
          <div><strong>{t("environment.unavailable")}</strong><span>{t("environment.unknownType")}</span></div>
        </div>
        <p>{t("environment.unsupportedType")}</p>
      </section>
    );
  }

  const environmentId = field(raw.id);
  const workspaceDirectory = field(raw.workspace_directory);
  const capabilityDirectories = directories(raw.capability_directories);
  const remoteUrl = sanitizeRemoteUrl(raw.remote_url);
  const live = matchingObservation(observation, environmentId, "self_hosted");
  const requiresConnection = Boolean(environmentId && connectionActions.some(
    (action) => environmentIdsMatch(action.environment_id, environmentId),
  ));
  const supportedProjection = isSupportedSelfHostedEnvironmentProjection(
    raw.id,
    raw.remote_url,
    raw.workspace_directory,
    raw.capability_directories,
  );
  const status = presentation.status;

  return (
    <section className="environment-panel" aria-label={t("environment.statusAria")}>
      <div className="environment-panel-heading">
        <HardDrive size={15} strokeWidth={1.5} aria-hidden="true" />
        <div>
          <strong>{t("environment.selfHosted")}</strong>
          <span>{environmentId ?? t("environment.idUnavailable")}</span>
        </div>
        <div className={`environment-panel-status environment-panel-status-${status}`} role="status" aria-live="polite">
          <StatusIcon status={presentation.statusKind} />
          <span>{t(`environment.status.${status}` as never)}</span>
        </div>
      </div>

      <div className="environment-panel-grid">
        <div className="environment-panel-field">
          <span>{t("environment.id")}</span>
          <code>{environmentId ?? t("common.unavailable")}</code>
        </div>
        <div className="environment-panel-field">
          <span>{t("environment.remoteUrl")}</span>
          {remoteUrl ? <code>{remoteUrl.label}</code> : <strong>{t("environment.unsafeUrl")}</strong>}
        </div>
        <div className="environment-panel-field environment-panel-field-wide">
          <span>{t("environment.workspaceDirectory")}</span>
          <code><Folder size={12} strokeWidth={1.5} aria-hidden="true" />{workspaceDirectory ?? t("common.unavailable")}</code>
        </div>
        <div className="environment-panel-field environment-panel-field-wide">
          <span>{t("environment.capabilityDirectories")}</span>
          {capabilityDirectories === null ? <strong>{t("common.unavailable")}</strong> : capabilityDirectories.length ? (
            <ul>{capabilityDirectories.map((directory, index) => <li key={`${index}:${directory}`}><code>{directory}</code></li>)}</ul>
          ) : <strong>{t("environment.noneExposed")}</strong>}
        </div>
      </div>

      <p className="environment-panel-provenance">
        {live?.source === "live"
          ? t("environment.selfLiveProvenance")
          : live?.source === "durable"
            ? live.resource.files.length === 0 && live.resource.plugins.length === 0 && live.resource.skills.length === 0
              ? t("environment.selfDurableEmptyProvenance")
              : t("environment.selfDurableProvenance")
            : live?.source === "unavailable"
              ? t("environment.selfUnavailableProvenance")
              : requiresConnection
                ? t("environment.selfRequiredProvenance")
                : t("environment.selfUnknownProvenance")}
      </p>

      {environmentFilesEnabled && supportedProjection && environmentId && workspaceDirectory && onListFiles ? (
        <EnvironmentFilesPanel
          key={`${environmentId}:${workspaceDirectory}`}
          environmentId={environmentId}
          workspaceDirectory={workspaceDirectory}
          onListFiles={onListFiles}
        />
      ) : null}

      {status !== "connected" && status !== "ready" ? (
        <EnvironmentLauncherGuide
          environmentId={environmentId}
          remoteUrl={raw.remote_url}
          workspaceDirectory={raw.workspace_directory}
          capabilityDirectories={raw.capability_directories}
          dockerGuideProfile={dockerGuideProfile}
          defaultOpen={defaultLauncherGuideOpen}
        />
      ) : null}

      {status === "failed" ? (
        <div className="environment-panel-error" role="alert">
          <strong>{t("environment.failed")}</strong>
          <p>{t("environment.failedDetail")}</p>
        </div>
      ) : null}

      {status === "expired" ? (
        <div className="environment-panel-error environment-panel-expired" role="status">
          <strong>{t("environment.expired")}</strong>
          <p>{t("environment.expiredDetail")}</p>
        </div>
      ) : null}

      <footer className="environment-panel-footer">
        <p>{t("environment.selfFooter")}</p>
        <nav aria-label={t("environment.selfDocs")}>
          <a href={coreSetupUrl} target="_blank" rel="noreferrer">{t("environment.coreSetup")}<ExternalLink size={11} aria-hidden="true" /></a>
          <a href={launcherSetupUrl} target="_blank" rel="noreferrer">{t("environment.launcherSetup")}<ExternalLink size={11} aria-hidden="true" /></a>
        </nav>
      </footer>
    </section>
  );
}
