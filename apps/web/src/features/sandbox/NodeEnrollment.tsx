import { createPortal } from "react-dom";
import { Check, Copy, Terminal, TriangleAlert } from "lucide-react";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import type { SandboxAdminClient, SandboxDeployment, SandboxNode } from "@agents-core-web/agents-client";
import { useTranslation } from "react-i18next";
import { HelpTip, StatusDot, type Tone } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { formatBytes } from "../../lib/format";
import { sandboxDiagnosticMessage } from "../../lib/sandbox-diagnostic";
import { sandboxRequestError } from "../../lib/sandbox-labels";
import { useCopy } from "../api-keys/IssuedKey";
import { sandboxCoreOrigin, sandboxSetupOrigin } from "./core-origin";
import type { SandboxConsoleConfig } from "./console-config";
import { nodeInstallCommand, nodeLogCommand } from "./enrollment-command";
import { enrolledNode, enrollmentProgress, formatCountdown, hostPrerequisites, progressSteps, USER_MANAGER_RESTART, type EnrollmentTarget, type StepState } from "./node-enrollment";

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

/** A command, with the nodes registered before it and the limits it approved, which identify its node. */
interface Enrollment extends EnrollmentTarget {
  token: string;
  expires_at: string;
}

/**
 * Add node: the administrator sets the node's sandbox limits, then Core issues a
 * one-time enrollment command that approves them
 * (`POST /core/v1/sandbox/enrollment-tokens`). Only microsandbox suspends
 * sandboxes, so only it asks for a retained limit; Docker retains exactly the
 * sandboxes it runs at once.
 *
 * The page keeps this dialog mounted, so a command survives closing it: it is
 * shown again until it expires or its node connects. An expired command is
 * replaced only when the administrator asks. After the command, the dialog
 * follows the new node through the node list: read on each opening, every few
 * seconds while open, and once more at expiry, since a node registered by the
 * command outranks its expiry.
 */
export function NodeEnrollment({ client, consoleConfig, deployment, nodes, open, fresh, onClose, onRefresh }: {
  client: SandboxAdminClient;
  consoleConfig: SandboxConsoleConfig;
  deployment: SandboxDeployment;
  nodes: SandboxNode[];
  open: boolean;
  fresh: boolean;
  onClose: () => void;
  /** Reads the page's node list again; settles when the read has. */
  onRefresh: () => Promise<unknown>;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const id = useId();
  const [active, setActive] = useState(DEFAULT_ACTIVE);
  const [retained, setRetained] = useState(DEFAULT_RETAINED);
  const [busy, setBusy] = useState(false);
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [appeared, setAppeared] = useState<{ id: string; at: number } | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const [now, setNow] = useState(Date.now);
  const [requirementsOpen, setRequirementsOpen] = useState(() => !requirementsSeen());
  // When the latest node-list read this dialog asked for began (Date.now()), once it has finished.
  const [checkedAt, setCheckedAt] = useState(0);
  const reading = useRef(false);
  const generation = useRef(0);
  const request = useRef<AbortController | null>(null);
  const sourceUrl = sandboxCoreOrigin(window.location.origin);
  const coreUrl = sandboxCoreOrigin(deployment.core_url || window.location.origin);
  const available = Boolean(consoleConfig.node_installer && sourceUrl && coreUrl);
  // The command downloads from this console's own address. A loopback one (or any
  // address sandboxSetupOrigin refuses for guests) resolves to the node host itself.
  const localOnly = sourceUrl !== null && sandboxSetupOrigin(sourceUrl) === null;
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
  const limitsReady = !activeProblem && !retainedProblem;
  const node = enrollment ? enrolledNode(nodes, enrollment, suspends) : null;
  const progress = enrollmentProgress(node, node && appeared?.id === node.id ? appeared.at : undefined, now);
  // Registration uses the token, so from then on its expiry no longer matters; rerunning
  // the command on that host resumes with the node's retained identity.
  const registered = progress.stage !== "waiting";
  const ready = progress.stage === "ready" && fresh;
  const expiresAt = enrollment ? Date.parse(enrollment.expires_at) : 0;
  const lapsed = Boolean(enrollment && expiresAt <= now);
  // Expired only once a read begun after the expiry found no node for the command.
  const expired = lapsed && checkedAt >= expiresAt;
  const command = enrollment && available && (registered || !expired) && !ready
    ? nodeInstallCommand(enrollment.token, coreUrl!, sourceUrl!, deployment.provider, deployment.installation_id, consoleConfig.node_installer_sha256) : "";
  const nodeId = node?.id ?? null;
  const polling = open && enrollment !== null && !ready && (registered || !expired);
  const check = useCallback(async () => {
    if (reading.current) return;
    reading.current = true;
    const started = Date.now();
    try { await onRefresh(); } finally { reading.current = false; }
    setCheckedAt((current) => Math.max(current, started));
  }, [onRefresh]);
  useEffect(() => () => { generation.current++; request.current?.abort(); }, []);
  useEffect(() => { if (open) rememberRequirementsSeen(); }, [open]);
  // The command's node may have registered while the dialog was closed.
  useEffect(() => { if (open && enrollment) void check(); }, [open]); // eslint-disable-line react-hooks/exhaustive-deps
  // At expiry, one more read decides between the command's node and "Command expired".
  useEffect(() => {
    if (open && lapsed && !registered && checkedAt < expiresAt) void check();
  }, [open, lapsed, registered, checkedAt, expiresAt, check]);
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
    setError(null); setBusy(false); setCopied(false); setCopyFailed(false);
    // A connected node ends the command; otherwise it waits here for the next opening.
    if (ready) { setEnrollment(null); setAppeared(null); }
    // The limits stay only with an unfinished flow: a command still waiting for its node.
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
    setEnrollment(null); setAppeared(null); setError(null); setCopied(false); setCopyFailed(false);
  }
  async function generate() {
    if (request.current || !available || !limitsReady || activeLimit === null || retainedLimit === null) return;
    const capacity = { max_active: activeLimit, max_retained: retainedLimit };
    generation.current++;
    const controller = new AbortController(); request.current = controller;
    setBusy(true); setError(null); setCopied(false); setCopyFailed(false);
    try {
      // Capture the current node set before issuing a one-time enrollment token.
      const current = await client.listNodes({ signal: controller.signal });
      if (controller.signal.aborted) return;
      // A node the previous command enrolled since the last read is that command's success:
      // follow it instead of folding it into a new command's known nodes.
      if (enrollment && enrolledNode(current.data, enrollment, suspends)) {
        await onRefresh();
        return;
      }
      const known = new Set(current.data.map((entry) => entry.id));
      const result = await client.createEnrollment({ signal: controller.signal }, capacity);
      if (!controller.signal.aborted) {
        setEnrollment({ token: result.token, expires_at: result.expires_at, known, ...capacity }); setAppeared(null); setNow(Date.now());
        // Seen with the limits; the command comes first now.
        setRequirementsOpen(false);
      }
    } catch (error) {
      // The failure stays in the dialog, on the limits, so they can be corrected and sent again.
      if (!controller.signal.aborted) { setEnrollment(null); setError(error); }
    }
    finally { if (!controller.signal.aborted) { request.current = null; setBusy(false); } }
  }
  async function copyCommand() {
    const current = generation.current;
    try { await navigator.clipboard.writeText(command); if (current === generation.current) { setCopied(true); setCopyFailed(false); } }
    catch { if (current === generation.current) { setCopied(false); setCopyFailed(true); } }
  }
  const backend = suspends ? "microsandbox" : "Docker";
  const size = deployment.specification?.resources;
  const values = {
    console: sourceUrl ?? "", core: coreUrl ?? "",
    size: size ? t("{{cpus}} CPU · {{memory}}", { cpus: size.cpus, memory: formatBytes(size.memory_mib * 2 ** 20) }) : "",
  };
  const requirements = deployment.provider === "docker" || deployment.provider === "microsandbox" ? (
    <details className="sandbox-host-requirements" open={requirementsOpen} onToggle={(event) => setRequirementsOpen(event.currentTarget.open)}>
      <summary>{t("Host requirements")}</summary>
      <ul>
        {hostPrerequisites(deployment.provider, Boolean(size)).map((item) => (
          <li key={item.label}>
            <span>{t(item.label, values)}</span>
            {item.command ? <CopyCommand value={item.command} /> : null}
          </li>
        ))}
      </ul>
      <div className="sandbox-host-requirements-note">
        <p>{t("After a group change, sign in again as that user. If its systemd manager was already running, restart it (or reboot):")}</p>
        <CopyCommand value={USER_MANAGER_RESTART} />
      </div>
    </details>
  ) : null;
  const limitsForm = `${id}-limits`;
  const footer = !available ? undefined
    : !enrollment ? <>
      <button className="button outline" type="button" onClick={close}>{t("Cancel")}</button>
      <button className="button primary" type="submit" form={limitsForm} disabled={busy || !limitsReady}>{busy ? t("Preparing your command…") : t("Generate command")}</button>
    </>
    : ready ? <button className="button primary" type="button" autoFocus onClick={close}>{t("Done")}</button>
    : registered ? <button className="button outline" type="button" onClick={changeLimits}>{t("Add another node")}</button>
    : <>
      <button className="button outline" type="button" disabled={busy} onClick={changeLimits}>{t("Change limits")}</button>
      {expired ? <button className="button primary" type="button" autoFocus disabled={busy} onClick={() => void generate()}>{busy ? t("Preparing your command…") : t("Generate new command")}</button> : null}
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
  const problem = progress.problem === "not_connected"
    ? { label: t("Not connected yet"), advice: t("The node registered but its service hasn't reached Core.") }
    : sandboxDiagnosticMessage(progress.problem, locale);
  return createPortal(<Modal open={open} title={t("Add node")} onClose={close} footer={footer}>
    <div className="sandbox-add-node form-stack">
      {!available ? <p role="status">{consoleConfig.node_installer && coreUrl && !sourceUrl
        ? t("Open this console over HTTPS to add a node: the installer downloads only over HTTPS.")
        : t("Node installation is unavailable. Ask the deployment administrator to enable the node installer on this console.")}</p> : !enrollment ? (
        <form id={limitsForm} className="form-stack" onSubmit={(event) => { event.preventDefault(); void generate(); }}>
          <p>{t("Set the sandbox limits for the host you want to add.")}</p>
          {localOnly ? <p className="sandbox-add-node-warning" role="note"><TriangleAlert size={14} aria-hidden="true" /><span>{t("This console is open at {{origin}}, which other machines can't reach. To add another machine, open the console at its HTTPS address, then generate the command.", { origin: sourceUrl })}</span></p> : null}
          <div className="field">
            <span className="field-label-row"><label htmlFor={`${id}-active`}>{t("Sandboxes at once")}</label><HelpTip>{t("The most sandboxes Core places on this node at the same time.")}</HelpTip></span>
            <input id={`${id}-active`} inputMode="numeric" autoComplete="off" autoFocus={open} value={active} onChange={(event) => setActive(event.target.value)} aria-invalid={Boolean(activeProblem)} />
            {activeProblem ? <span className="field-error">{activeProblem}</span> : null}
          </div>
          {suspends ? (
            <div className="field">
              <span className="field-label-row"><label htmlFor={`${id}-retained`}>{t("Retained sandboxes")}</label><HelpTip>{t("Sandboxes kept on this node for resuming, the running ones included. At least the number at once.")}</HelpTip></span>
              <input id={`${id}-retained`} inputMode="numeric" autoComplete="off" value={retained} onChange={(event) => setRetained(event.target.value)} aria-invalid={Boolean(retainedProblem)} />
              {retainedProblem ? <span className="field-error">{retainedProblem}</span> : null}
            </div>
          ) : null}
          {error !== null ? <p role="alert" className="sandbox-error">{sandboxRequestError(error, locale)}</p> : null}
          {requirements}
        </form>
      ) : <>
        {/* Once used, the command only recovers its own node: running it on another host fails. */}
        {command ? <p>{registered && node ? t("Rerun only on {{name}} if asked", { name: node.name }) : t("Run on the host you want to add.")}</p> : null}
        {command ? <div className="sandbox-command">
          <div className="sandbox-command-heading">
            <span><Terminal size={15} />{t("Terminal")}</span>
            {!registered ? <span className="sandbox-command-expiry" role="timer" title={new Date(enrollment.expires_at).toLocaleString(locale)}>{t("Expires in {{time}}", { time: formatCountdown(Date.parse(enrollment.expires_at) - now) })}</span> : null}
            <button type="button" className="button outline" autoFocus aria-label={copied ? t("Copied") : t("Copy node command")} onClick={() => void copyCommand()}>{copied ? <Check size={14} /> : <Copy size={14} />}{copied ? t("Copied") : t("Copy command")}</button>
          </div>
          <label className="field"><span className="sr-only">{t("One-time enrollment command")}</span><textarea readOnly rows={5} value={command} onClick={(event) => event.currentTarget.select()} spellCheck={false} /></label>
        </div> : null}
        {copyFailed && !ready ? <p role="alert">{t("Select the command above and copy it manually.")}</p> : null}
        {/* One live region for the whole flow; only its contents change, so each change is announced. */}
        <div role="status" aria-label={t("Registration progress")}>
          {ready && node ? <div className="sandbox-enrollment-status connected"><span className="sandbox-status-dot" />{t("{{name}} · Connected", { name: node.name })}</div>
            : expired && !registered ? <div className="sandbox-enrollment-status"><span className="sandbox-status-dot" />{t("Command expired")} · {t("Generate a new command to continue.")}</div>
            : <ol className="sandbox-enrollment-progress">
              {steps.map((step, index) => <li key={index} className={step.state}><StatusDot tone={step.tone} label={step.label} /></li>)}
            </ol>}
        </div>
        {problem && !ready ? <div className="sandbox-enrollment-problem" role="alert">
          <p><strong>{problem.label}</strong> {problem.advice}</p>
          <div className="sandbox-log-hint"><span>{t("Check the log on the host:")}</span><CopyCommand value={nodeLogCommand(deployment.installation_id)} /></div>
        </div> : null}
        {!fresh ? <p>{t("Connection status unavailable. Refresh to check your node.")}</p> : null}
        {!ready ? requirements : null}
      </>}
    </div>
  </Modal>, document.body);
}

/** A short command to run on the host, copied with one click. */
function CopyCommand({ value }: { value: string }) {
  const { t } = useTranslation("sandbox");
  const { state, copy } = useCopy(value);
  const label = state === "copied" ? t("Copied") : t("Copy {{command}}", { command: value });
  return <span className="sandbox-copy-command">
    <span className="sandbox-copy-command-row">
      <code>{value}</code>
      <button type="button" className="icon-button ghost" aria-label={label} title={label} onClick={() => void copy()}>
        {state === "copied" ? <Check size={13} strokeWidth={1.7} aria-hidden="true" /> : <Copy size={13} strokeWidth={1.7} aria-hidden="true" />}
      </button>
    </span>
    {state === "failed" ? <span className="field-error" role="alert">{t("Select the command and copy it manually.")}</span> : null}
  </span>;
}
