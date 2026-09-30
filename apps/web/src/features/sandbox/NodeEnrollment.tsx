import { createPortal } from "react-dom";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import type { SandboxAdminClient, SandboxDeployment, SandboxEnrollment, SandboxNode } from "@agents-core-web/agents-client";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { HelpTip, StatusDot, type Tone } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { coreFieldError } from "../../lib/core-error";
import { formatBytes } from "../../lib/format";
import { useConsoleNavigation } from "../../lib/console-navigation";
import { installationQuery } from "../../lib/installation";
import { sandboxDiagnosticMessage } from "../../lib/sandbox-diagnostic";
import { sandboxRequestError } from "../../lib/sandbox-labels";
import { checklistOpenFor, modelStep, nextStepAfterNode } from "../overview/getting-started";
import { harnessesQuery } from "../system/harness-queries";
import { nodeSourceUrl } from "./core-origin";
import { nodeFilesAvailable, type SandboxConsoleConfig } from "./console-config";
import { nodeInstallCommand, nodeLogCommand } from "./enrollment-command";
import { CommandBlock, CopyCommand, HostRequirements } from "./node-commands";
import { enrolledNode, enrollmentProgress, formatCountdown, progressSteps, type StepState } from "./node-enrollment";
import { sandboxConsoleConfigQuery } from "./sandbox-queries";

/** The host requirements open by default until this browser has shown them once. */
const REQUIREMENTS_SEEN = "agents-core-web.node-requirements-seen";
function requirementsSeen(): boolean {
  try { return window.localStorage.getItem(REQUIREMENTS_SEEN) === "1"; } catch { return false; }
}
function rememberRequirementsSeen() {
  try { window.localStorage.setItem(REQUIREMENTS_SEEN, "1"); } catch { /* Storage can be unavailable; the list then opens each time. */ }
}

/** The limits a new flow starts from. */
const DEFAULT_ACTIVE = "2";
const DEFAULT_RETAINED = "8";

/**
 * Add node: the administrator sets the node's sandbox limits, then Core issues a
 * one-time enrollment command that approves them
 * (`POST /core/v1/sandbox/enrollment-tokens`). Only microsandbox suspends
 * sandboxes, so only it asks for a retained limit; Docker retains exactly the
 * sandboxes it runs at once. The command downloads the installer from the
 * installation's public URL, never the browser's address, and runs it with
 * sudo (or directly as root), which installs the node as a system service.
 * The log hint names that system service. No command is issued until the installation
 * is read: one whose public URL other machines can't use (loopback, as
 * `local_only` says, or not HTTPS), an unreadable one, or a console that
 * reports no node files for the deployment's provider (`node_artifacts`) says
 * so instead. Each opening, and each return to the window while open, reads
 * the installation and the console again, so a fix on the Core host shows
 * without a reload.
 *
 * The page keeps this dialog mounted, so a command survives closing it: it is
 * shown again until it expires or its node connects. An expired command is
 * replaced only when the administrator asks. After the command, the dialog
 * follows the node that reports the command's `enrollment_id` through the node
 * list: read on each opening, every few seconds while open, and once more at
 * expiry, since a node registered by the command outranks its expiry.
 */
export function NodeEnrollment({ client, consoleConfig, deployment, nodes, open, fresh, onClose, onRefresh }: {
  client: SandboxAdminClient;
  consoleConfig: SandboxConsoleConfig;
  deployment: SandboxDeployment;
  nodes: SandboxNode[];
  open: boolean;
  fresh: boolean;
  onClose: () => void;
  /** Returns the confirmed node list, or null when the read failed. */
  onRefresh: () => Promise<SandboxNode[] | null>;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const { t: tCommon } = useTranslation("common");
  const id = useId();
  const [active, setActive] = useState(DEFAULT_ACTIVE);
  const [retained, setRetained] = useState(DEFAULT_RETAINED);
  const [busy, setBusy] = useState(false);
  const [enrollment, setEnrollment] = useState<SandboxEnrollment | null>(null);
  const [appeared, setAppeared] = useState<{ id: string; at: number } | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [now, setNow] = useState(Date.now);
  const [requirementsOpen, setRequirementsOpen] = useState(() => !requirementsSeen());
  // The command last copied, so the log hint names that one's service.
  const queryClient = useQueryClient();
  const installation = useQuery(installationQuery);
  // Keep the completed read's start time paired with its data: query notifications can render later.
  const [checked, setChecked] = useState<{ startedAt: number; nodes: SandboxNode[] } | null>(null);
  const reading = useRef(false);
  const generation = useRef(0);
  const request = useRef<AbortController | null>(null);
  // Nodes download from, and reach Core at, the public URL; the browser's address may be a tunnel or loopback.
  // The deployment's core_url is the same address, but the installation is read again on each opening, so a fix shows at once.
  const publicUrl = installation.data ? nodeSourceUrl(installation.data) : null;
  const available = consoleConfig.node_installer;
  const provider = deployment.provider === "docker" || deployment.provider === "microsandbox" ? deployment.provider : null;
  const backend = provider === "microsandbox" ? "microsandbox" : "Docker";
  // Nodes and their sandboxes reach Core at its public URL, so a loopback one serves no other machine;
  // and without the provider's node files the installer would fail on the host. Either way no command
  // is issued, nor before the installation is read: a failed read (an older Core, say) proves nothing.
  const blocker: { text: string; failed?: boolean } | null = deployment.reset
    ? { text: t("Node enrollment is paused while reset is in progress.") }
    : !fresh
      ? { text: t("Sandbox state is unconfirmed. Refresh before issuing a node command.") }
    : installation.data === undefined
    ? installation.isError ? { text: t("The installation couldn't be read, so no command can be issued."), failed: true } : { text: t("Checking this installation's public URL…") }
    : !publicUrl
      ? { text: t("Configure a domain and HTTPS in System before adding nodes.") }
      : !nodeFilesAvailable(consoleConfig, deployment.provider)
        ? { text: t("This console has no node files for {{provider}}. Install Core from the offline bundle, or add the release artifacts and rerun ./install.sh.", { provider: backend }) }
        : null;
  // Core takes whole numbers from 1 to a million, with the retained limit at least the active one.
  const suspends = deployment.provider === "microsandbox";
  const whole = (value: string) => (/^\d+$/.test(value.trim()) ? Number(value.trim()) : null);
  const inRange = (limit: number | null): limit is number => limit !== null && limit >= 1 && limit <= 1_000_000;
  const activeLimit = whole(active);
  const retainedLimit = suspends ? whole(retained) : activeLimit;
  const activeProblem = inRange(activeLimit) ? null : t("Enter a whole number from 1 to 1,000,000.");
  const retainedProblem = !suspends ? null
    : !inRange(retainedLimit) ? t("Enter a whole number from 1 to 1,000,000.")
    : inRange(activeLimit) && retainedLimit < activeLimit ? t("Enter at least the number of sandboxes at once.") : null;
  const activeError = coreFieldError(error, "max_active", tCommon) ?? activeProblem;
  const retainedError = coreFieldError(error, "max_retained", tCommon) ?? retainedProblem;
  const limitsReady = !activeProblem && !retainedProblem;
  const node = enrollment ? enrolledNode(nodes, enrollment) : null;
  const progress = enrollmentProgress(node, node && appeared?.id === node.id ? appeared.at : undefined, now);
  // Registration uses the token, so from then on its expiry no longer matters; rerunning
  // the command on that host resumes with the node's retained identity.
  const registered = progress.stage !== "waiting";
  const ready = progress.stage === "ready" && fresh;
  // While Getting started is open, a ready node points to what comes next: the default model, or the checklist.
  const { navigate } = useConsoleNavigation();
  const onboarding = ready && checklistOpenFor(deployment.installation_id);
  const harnesses = useQuery({ ...harnessesQuery, enabled: onboarding });
  const next = nextStepAfterNode(onboarding, modelStep(harnesses.data?.data ?? (harnesses.isError ? "failed" : undefined)));
  const expiresAt = enrollment ? Date.parse(enrollment.expires_at) : 0;
  const lapsed = Boolean(enrollment && expiresAt <= now);
  // Expired only once a read begun after the expiry found no node for the command.
  const expired = lapsed && fresh && checked !== null && checked.startedAt >= expiresAt && checked.nodes === nodes;
  const command = enrollment && provider && available && publicUrl && (registered || !expired) && !ready
    ? nodeInstallCommand({ token: enrollment.token, coreUrl: publicUrl, sourceUrl: publicUrl, provider, installationId: deployment.installation_id, scriptDigest: consoleConfig.node_installer_sha256 }) : "";
  const nodeId = node?.id ?? null;
  const polling = open && enrollment !== null && !ready && (registered || !expired);
  const check = useCallback(async () => {
    if (reading.current) return;
    reading.current = true;
    const started = Date.now();
    try {
      const confirmedNodes = await onRefresh();
      if (confirmedNodes) setChecked({ startedAt: started, nodes: confirmedNodes });
    } finally { reading.current = false; }
  }, [onRefresh]);
  useEffect(() => () => { generation.current++; request.current?.abort(); }, []);
  useEffect(() => { if (open) rememberRequirementsSeen(); }, [open]);
  // Rerunning ./install.sh or oac apply on the Core host changes what the console and the installation report.
  useEffect(() => {
    if (!open) return;
    const reread = () => {
      void queryClient.invalidateQueries({ queryKey: sandboxConsoleConfigQuery.queryKey });
      void queryClient.invalidateQueries({ queryKey: installationQuery.queryKey });
    };
    reread();
    window.addEventListener("focus", reread);
    return () => window.removeEventListener("focus", reread);
  }, [open, queryClient]);
  // The command's node may have registered while the dialog was closed.
  useEffect(() => { if (open && enrollment) void check(); }, [open]); // eslint-disable-line react-hooks/exhaustive-deps
  // At expiry, one more read decides between the command's node and "Command expired".
  useEffect(() => {
    if (open && lapsed && !registered && !expired) void check();
  }, [open, lapsed, registered, expired, check]);
  // The installer's wait for readiness counts from when the node appears.
  useEffect(() => {
    if (nodeId) setAppeared((current) => (current?.id === nodeId ? current : { id: nodeId, at: Date.now() }));
  }, [nodeId]);
  useEffect(() => {
    if (!open || !enrollment || ready) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [open, enrollment, ready]);
  useEffect(() => {
    if (!polling) return;
    const interval = window.setInterval(() => void check(), 3000);
    return () => window.clearInterval(interval);
  }, [polling, check]);
  function close() {
    generation.current++;
    request.current?.abort(); request.current = null;
    setError(null); setBusy(false);
    // A connected node ends the command; otherwise it waits here for the next opening.
    if (ready) { setEnrollment(null); setAppeared(null); }
    // The limits, and which command was copied, stay only with an unfinished flow: a command still waiting for its node.
    if (!enrollment || ready) { setActive(DEFAULT_ACTIVE); setRetained(DEFAULT_RETAINED); }
    onClose();
  }
  /**
   * Back to the limits, whose first field takes focus again. The command already
   * shown is abandoned (it lapses when it expires), as is a node that registered
   * but is not ready; Generate issues a fresh command.
   */
  function changeLimits() {
    generation.current++;
    setEnrollment(null); setAppeared(null); setError(null);
  }
  async function generate() {
    if (request.current || !fresh || !available || blocker || !limitsReady || activeLimit === null || retainedLimit === null) return;
    const capacity = { max_active: activeLimit, max_retained: retainedLimit };
    generation.current++;
    const controller = new AbortController(); request.current = controller;
    setBusy(true); setError(null);
    try {
      // A node the previous command enrolled since the last read is that command's success:
      // follow it instead of issuing another command.
      if (enrollment) {
        const current = await client.listNodes({ signal: controller.signal });
        if (controller.signal.aborted) return;
        if (enrolledNode(current.data, enrollment)) {
          await onRefresh();
          return;
        }
      }
      const result = await client.createEnrollment({ signal: controller.signal }, capacity);
      if (!controller.signal.aborted) {
        setEnrollment({ token: result.token, expires_at: result.expires_at, enrollment_id: result.enrollment_id }); setAppeared(null); setNow(Date.now());
        // Seen with the limits; the command comes first now.
        setRequirementsOpen(false);
      }
    } catch (error) {
      // The failure stays in the dialog, on the limits, so they can be corrected and sent again.
      if (!controller.signal.aborted) { setEnrollment(null); setError(error); }
    }
    finally { if (!controller.signal.aborted) { request.current = null; setBusy(false); } }
  }
  const size = deployment.specification?.resources;
  const values = {
    core: publicUrl ?? "",
    size: size ? t("{{cpus}} CPU · {{memory}}", { cpus: size.cpus, memory: formatBytes(size.memory_mib * 2 ** 20) }) : "",
  };
  const requirements = provider ? <>
    <HostRequirements provider={provider} sized={Boolean(size)} values={values} open={requirementsOpen} onToggle={setRequirementsOpen} />
  </> : null;
  const limitsForm = `${id}-limits`;
  const footer = !available || (!enrollment && blocker) ? undefined
    : !enrollment ? <>
      <button className="button outline" type="button" onClick={close}>{t("Cancel")}</button>
      <button className="button primary" type="submit" form={limitsForm} disabled={busy || !limitsReady}>{busy ? t("Preparing your command…") : t("Generate command")}</button>
    </>
    : ready ? <button className="button primary" type="button" autoFocus onClick={close}>{t("Done")}</button>
    : registered ? <button className="button outline" type="button" onClick={changeLimits}>{t("Add another node")}</button>
    : <>
      <button className="button outline" type="button" disabled={busy} onClick={changeLimits}>{t("Change limits")}</button>
      {expired && !blocker ? <button className="button primary" type="button" autoFocus disabled={busy} onClick={() => void generate()}>{busy ? t("Preparing your command…") : t("Generate new command")}</button> : null}
    </>;
  // Tense tells a step's state: done in the past, the current one waiting, later ones as plain nouns.
  // Readiness from an unconfirmed read still counts as waiting.
  const labels: Record<StepState, string>[] = [
    { done: t("Registered · {{name}}", { name: node?.name ?? "" }), current: t("Waiting for registration"), future: t("Waiting for registration") },
    { done: t("Connected"), current: t("Waiting to connect"), future: t("Connect") },
    { done: t("{{backend}} ready", { backend }), current: t("Waiting for {{backend}}", { backend }), future: t("{{backend}} check", { backend }) },
  ];
  const tones: Record<StepState, Tone> = { done: "ok", current: progress.problem ? "warning" : "pending", future: "neutral" };
  const steps = progressSteps(progress.stage === "ready" ? "connected" : progress.stage).map((state, index) => ({ state, label: labels[index]![state], tone: tones[state] }));
  const problem: { label: string; advice: string; help?: string } | null = progress.problem === "not_connected"
    ? { label: t("Not connected yet"), advice: t("The node registered but its service hasn't reached Core."),
      help: t("The node service keeps retrying while Core is unavailable, and stops once the node is removed.") }
    : sandboxDiagnosticMessage(progress.problem, locale);
  return createPortal(<Modal open={open} title={t("Add node")} onClose={close} footer={footer}>
    <div className="sandbox-add-node form-stack">
      {!available ? <p role="status">{t("This console serves no node installer. For a console deployed by hand, point OAC_WEB_NODE_PAYLOAD_DIR at the distribution's node payload and restart it.")}</p>
      : !enrollment && blocker ? blocker.failed
        ? <p role="alert">{blocker.text} <button className="text-action" type="button" disabled={installation.isFetching} onClick={() => void installation.refetch()}>{t("Try again")}</button></p>
        : <p role="status">{blocker.text}</p>
      : !enrollment ? (
        <form id={limitsForm} className="form-stack" onSubmit={(event) => { event.preventDefault(); void generate(); }}>
          <p>{t("Set the sandbox limits for the host you want to add.")}</p>
          <div className="field">
            <span className="field-label-row"><label htmlFor={`${id}-active`}>{t("Sandboxes at once")}</label><HelpTip>{t("The most sandboxes Core places on this node at the same time.")}</HelpTip></span>
            <input id={`${id}-active`} inputMode="numeric" autoComplete="off" autoFocus={open} value={active} onChange={(event) => { setActive(event.target.value); setError(null); }} aria-invalid={Boolean(activeError)} aria-errormessage={activeError ? `${id}-active-error` : undefined} />
            {activeError ? <span id={`${id}-active-error`} className="field-error">{activeError}</span> : null}
          </div>
          {suspends ? (
            <div className="field">
              <span className="field-label-row"><label htmlFor={`${id}-retained`}>{t("Retained sandboxes")}</label><HelpTip>{t("Sandboxes kept on this node for resuming, the running ones included. At least the number at once.")}</HelpTip></span>
              <input id={`${id}-retained`} inputMode="numeric" autoComplete="off" value={retained} onChange={(event) => { setRetained(event.target.value); setError(null); }} aria-invalid={Boolean(retainedError)} aria-errormessage={retainedError ? `${id}-retained-error` : undefined} />
              {retainedError ? <span id={`${id}-retained-error`} className="field-error">{retainedError}</span> : null}
            </div>
          ) : null}
          {error !== null ? <p role="alert" className="sandbox-error">{sandboxRequestError(error, locale)}</p> : null}
          {requirements}
        </form>
      ) : <>
        {/* Once used, the command only recovers its own node: running it on another host fails. */}
        {command ? <p>{registered && node ? t("Rerun only on {{name}} if asked", { name: node.name }) : t("Run on the host you want to add.")}</p> : null}
        {command ? <CommandBlock key={command} value={command} label={t("One-time enrollment command")} autoFocus
          extra={!registered ? <span className="sandbox-command-expiry" role="timer" title={new Date(enrollment.expires_at).toLocaleString(locale)}>{t("Expires in {{time}}", { time: formatCountdown(Date.parse(enrollment.expires_at) - now) })}</span> : null} /> : null}
        {/* The installer keeps partial downloads and exits 130 on Ctrl-C; the token lasts until the countdown ends. */}
        {command ? <p className="sandbox-command-note">{t("If the command is interrupted or the download stalls, run the same command again: the download resumes.")}</p> : null}
        {/* One live region for the whole flow; only its contents change, so each change is announced. */}
        <div role="status" aria-label={t("Registration progress")}>
          {ready && node ? <div className="sandbox-enrollment-status connected"><span className="sandbox-status-dot" />{t("{{name}} · Connected", { name: node.name })}</div>
            : expired && !registered ? <div className="sandbox-enrollment-status"><span className="sandbox-status-dot" />{t("Command expired")} · {t("Generate a new command to continue.")}</div>
            : <ol className="sandbox-enrollment-progress">
              {steps.map((step, index) => <li key={index} className={step.state}><StatusDot tone={step.tone} label={step.label} /></li>)}
            </ol>}
        </div>
        {next ? <p className="sandbox-next-step">
          <span>{t(next === "default-model" ? "Next: set a default model provider." : "Next: finish Getting started.")}</span>
          <button className="text-action" type="button" onClick={() => { close(); if (next === "default-model") navigate("system", {}, "default-model"); else navigate("overview"); }}>
            {t(next === "default-model" ? "Open System" : "Open Overview")}
          </button>
        </p> : null}
        {problem && !ready ? <div className="sandbox-enrollment-problem" role="alert">
          <p><strong>{problem.label}</strong> {problem.advice}{problem.help ? <HelpTip>{problem.help}</HelpTip> : null}</p>
          <div className="sandbox-log-hint">
            <span>{t("Check the log on the host:")}</span><CopyCommand value={nodeLogCommand(deployment.installation_id)} />
          </div>
        </div> : null}
        {deployment.reset ? <p role="status">{t("Node enrollment is paused while reset is in progress.")}</p> : !fresh ? <p>{t("Connection status unavailable. Refresh to check your node.")}</p> : null}
        {!ready ? requirements : null}
      </>}
    </div>
  </Modal>, document.body);
}
