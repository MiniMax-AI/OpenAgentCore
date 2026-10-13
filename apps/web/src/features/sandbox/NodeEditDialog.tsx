import { useId, useState } from "react";
import type { SandboxAdminClient, SandboxNode, SandboxResources } from "@oac/agents-client";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { nodeCapacity, NodeCapacityFields } from "./NodeCapacityFields";
import { Modal } from "../../components/Modal";
import { coreFieldError } from "../../lib/core-error";
import { formatBytes } from "../../lib/format";
import { sandboxRequestError } from "../../lib/sandbox-labels";
import { nodeDetailQuery } from "../fleet/fleet-queries";
import { sandboxesThatFit } from "./deployment-specification";

/**
 * A node's name and sandbox limits (`PATCH /core/v1/sandbox/nodes/{id}`). Core
 * takes all three together. Only a deployment whose Provider suspends sandboxes
 * shows the retained limit; otherwise the saved one is kept, raised to at least
 * the active limit because Core requires it. Under the limit, the host's CPUs
 * and memory from the node's last heartbeat, each sandbox's size and how many
 * of those the host holds.
 */
export function NodeEditDialog({ client, node, size, suspends, onClose, onSaved }: {
  client: SandboxAdminClient;
  node: SandboxNode | null;
  /** Each sandbox's CPUs and memory, from the deployment. */
  size: SandboxResources | null;
  /** Whether the deployment's Provider suspends sandboxes, which its suspension policy declares. */
  suspends: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh-CN" : "en";
  const { t: tCommon } = useTranslation("common");
  const id = useId();
  const [name, setName] = useState(node?.name ?? "");
  const [active, setActive] = useState(String(node?.max_active ?? 2));
  const [retained, setRetained] = useState(String(node?.max_retained ?? 8));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const limits = nodeCapacity(active, suspends ? retained : String(Math.max(node?.max_retained ?? 0, Number(active) || 0)));
  const activeLimit = limits.active;
  const retainedLimit = limits.retained;
  const nameProblem = !name.trim() || new TextEncoder().encode(name.trim()).length > 128 ? t("Enter a shorter name.") : null;
  const activeProblem = limits.activeError ? t(limits.activeError) : null;
  const retainedProblem = suspends && limits.retainedError ? t(limits.retainedError) : null;
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
  ].join(locale === "zh-CN" ? "" : " ") : null;

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
        <NodeCapacityFields id={id} active={active} retained={retained} suspends={suspends} activeError={activeError} retainedError={retainedError} help={hostFacts ? <> {hostFacts}</> : null} onActive={(value) => { setActive(value); setError(null); }} onRetained={(value) => { setRetained(value); setError(null); }} />
        {error !== null ? <p className="confirm-dialog-error" role="alert">{sandboxRequestError(error, locale)}</p> : null}
      </form>
    </Modal>
  );
}
