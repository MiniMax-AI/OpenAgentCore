import { useId, useState } from "react";
import type { SandboxAdminClient, SandboxNode, SandboxResources } from "@oac/agents-client";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { coreFieldError } from "../../lib/core-error";
import { formatBytes } from "../../lib/format";
import { sandboxRequestError } from "../../lib/sandbox-labels";
import { nodeDetailQuery } from "../fleet/fleet-queries";
import { sandboxesThatFit } from "./deployment-specification";

/**
 * A node's name and sandbox limits (`PATCH /core/v1/sandbox/nodes/{id}`). Core
 * takes all three together. Only microsandbox suspends sandboxes, so only it
 * shows the retained limit; for Docker the saved one is kept, raised to at least
 * the active limit because Core requires it. Under the limit, the host's CPUs
 * and memory from the node's last heartbeat, each sandbox's size and how many
 * of those the host holds.
 */
export function NodeEditDialog({ client, node, size, onClose, onSaved }: {
  client: SandboxAdminClient;
  node: SandboxNode | null;
  /** Each sandbox's CPUs and memory, from the deployment. */
  size: SandboxResources | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const { t: tCommon } = useTranslation("common");
  const id = useId();
  const [name, setName] = useState(node?.name ?? "");
  const [active, setActive] = useState(String(node?.max_active ?? 2));
  const [retained, setRetained] = useState(String(node?.max_retained ?? 8));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const suspends = node?.provider === "microsandbox";
  const whole = (value: string) => (/^\d+$/.test(value.trim()) ? Number(value.trim()) : null);
  const activeLimit = whole(active);
  const retainedLimit = suspends ? whole(retained) : Math.max(node?.max_retained ?? 0, activeLimit ?? 0);
  // Core counts the name in UTF-8 bytes and caps both limits at a million.
  const nameProblem = !name.trim() || new TextEncoder().encode(name.trim()).length > 128 ? t("Enter a shorter name.") : null;
  const activeProblem = activeLimit === null || activeLimit < 1 || activeLimit > 1_000_000 ? t("Enter a whole number from 1 to 1,000,000.") : null;
  const retainedProblem = suspends && (retainedLimit === null || activeLimit === null || retainedLimit < activeLimit || retainedLimit > 1_000_000) ? t("Enter at least the number of sandboxes at once.") : null;
  const nameError = coreFieldError(error, "name", tCommon, "bytes") ?? (name ? nameProblem : null);
  const activeError = coreFieldError(error, "max_active", tCommon) ?? activeProblem;
  const retainedError = coreFieldError(error, "max_retained", tCommon) ?? retainedProblem;
  const ready = node !== null && !nameProblem && !activeProblem && !retainedProblem && !busy;
  // The node detail read adds the host's total memory to its CPU count.
  const detail = useQuery({ ...nodeDetailQuery(node?.id ?? "", "1h"), enabled: node !== null });
  const host = detail.data?.id === node?.id ? detail.data?.host : undefined;
  const hostCpus = host?.effective_cpu_cores ?? node?.cpu_count ?? null;
  const hostMemory = host?.total_memory_bytes ?? null;
  const fit = sandboxesThatFit({ cpus: hostCpus, memoryBytes: hostMemory }, size);
  const measure = (cpus: number, memory: number) => t("{{cpus}} CPU · {{memory}}", { cpus, memory: formatBytes(memory) });
  // Sentences run on with a space in English and without one in Chinese.
  const hostFacts = hostCpus !== null && hostMemory !== null ? [
    t("Host: {{host}}.", { host: measure(hostCpus, hostMemory) }),
    ...(size ? [t("Each sandbox: {{size}}.", { size: measure(size.cpus, size.memory_mib * 2 ** 20) })] : []),
    ...(fit !== null && fit > 0 ? [t("Suggested: at most {{count}} at once.", { count: fit })] : []),
  ].join(locale === "zh" ? "" : " ") : null;

  async function save() {
    if (!ready || !node) return;
    setBusy(true); setError(null);
    try {
      await client.updateNode(node.id, { name: name.trim(), max_active: activeLimit!, max_retained: retainedLimit! });
      onSaved();
    } catch (reason) {
      // Keep the form with Core's reason; nothing changed unless Core confirmed it.
      setError(reason);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={node !== null}
      title={t("Edit node")}
      onClose={() => { if (!busy) onClose(); }}
      footer={<>
        <button type="button" className="button outline" disabled={busy} onClick={onClose}>{t("Cancel")}</button>
        <button type="button" className="button primary" disabled={!ready} onClick={() => void save()}>{busy ? t("Saving…") : t("Save")}</button>
      </>}
    >
      <form className="form-stack" onSubmit={(event) => { event.preventDefault(); void save(); }}>
        <label className="field" htmlFor={`${id}-name`}>
          <span>{t("Name")}</span>
          <input id={`${id}-name`} value={name} onChange={(event) => { setName(event.target.value); setError(null); }} autoComplete="off" aria-invalid={Boolean(nameError)} aria-describedby={nameError ? `${id}-name-error` : undefined} />
          {nameError ? <span id={`${id}-name-error`} className="field-error">{nameError}</span> : null}
        </label>
        <div className="field">
          <span className="field-label-row"><label htmlFor={`${id}-active`}>{t("Sandboxes at once")}</label><HelpTip>{t("The most sandboxes Core places on this node at the same time.")}</HelpTip></span>
          <input id={`${id}-active`} inputMode="numeric" value={active} onChange={(event) => { setActive(event.target.value); setError(null); }} aria-invalid={Boolean(activeError)} aria-errormessage={activeError ? `${id}-active-error` : undefined} aria-describedby={hostFacts ? `${id}-host` : undefined} />
          {activeError ? <span id={`${id}-active-error`} className="field-error">{activeError}</span> : null}
          {hostFacts ? <small id={`${id}-host`}>{hostFacts}</small> : null}
        </div>
        {suspends ? (
          <div className="field">
            <span className="field-label-row"><label htmlFor={`${id}-retained`}>{t("Retained sandboxes")}</label><HelpTip>{t("Sandboxes kept on this node for resuming, the running ones included. At least the number at once.")}</HelpTip></span>
            <input id={`${id}-retained`} inputMode="numeric" value={retained} onChange={(event) => { setRetained(event.target.value); setError(null); }} aria-invalid={Boolean(retainedError)} aria-errormessage={retainedError ? `${id}-retained-error` : undefined} />
            {retainedError ? <span id={`${id}-retained-error`} className="field-error">{retainedError}</span> : null}
          </div>
        ) : null}
        {error !== null ? <p className="confirm-dialog-error" role="alert">{sandboxRequestError(error, locale)}</p> : null}
      </form>
    </Modal>
  );
}
