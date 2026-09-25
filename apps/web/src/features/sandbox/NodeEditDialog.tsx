import { useId, useState } from "react";
import type { SandboxAdminClient, SandboxNode } from "@agents-core-web/agents-client";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { sandboxRequestError } from "../../lib/sandbox-labels";

/**
 * A node's name and sandbox limits (`PATCH /core/v1/sandbox/nodes/{id}`). Core
 * takes all three together. Only microsandbox suspends sandboxes, so only it
 * shows the retained limit; for Docker the saved one is kept, raised to at least
 * the active limit because Core requires it.
 */
export function NodeEditDialog({ client, node, onClose, onSaved }: {
  client: SandboxAdminClient;
  node: SandboxNode | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t, i18n } = useTranslation("sandbox");
  const locale = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  const id = useId();
  const [name, setName] = useState(node?.name ?? "");
  const [active, setActive] = useState(String(node?.max_active ?? 2));
  const [retained, setRetained] = useState(String(node?.max_retained ?? 8));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const suspends = node?.provider === "microsandbox";
  const whole = (value: string) => (/^\d+$/.test(value.trim()) ? Number(value.trim()) : null);
  const activeLimit = whole(active);
  const retainedLimit = suspends ? whole(retained) : Math.max(node?.max_retained ?? 0, activeLimit ?? 0);
  // Core counts the name in UTF-8 bytes and caps both limits at a million.
  const nameProblem = !name.trim() || new TextEncoder().encode(name.trim()).length > 128 ? t("Enter a shorter name.") : null;
  const activeProblem = activeLimit === null || activeLimit < 1 || activeLimit > 1_000_000 ? t("Enter a whole number from 1 to 1,000,000.") : null;
  const retainedProblem = suspends && (retainedLimit === null || activeLimit === null || retainedLimit < activeLimit || retainedLimit > 1_000_000) ? t("Enter at least the number of sandboxes at once.") : null;
  const ready = node !== null && !nameProblem && !activeProblem && !retainedProblem && !busy;

  async function save() {
    if (!ready || !node) return;
    setBusy(true); setError(null);
    try {
      await client.updateNode(node.id, { name: name.trim(), max_active: activeLimit!, max_retained: retainedLimit! });
      onSaved();
    } catch (reason) {
      // Keep the form with Core's reason; nothing changed unless Core confirmed it.
      setError(sandboxRequestError(reason, locale));
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
          <input id={`${id}-name`} value={name} onChange={(event) => setName(event.target.value)} autoComplete="off" aria-invalid={Boolean(name && nameProblem)} />
          {name && nameProblem ? <span className="field-error">{nameProblem}</span> : null}
        </label>
        <div className="field">
          <span className="field-label-row"><label htmlFor={`${id}-active`}>{t("Sandboxes at once")}</label><HelpTip>{t("The most sandboxes Core places on this node at the same time.")}</HelpTip></span>
          <input id={`${id}-active`} inputMode="numeric" value={active} onChange={(event) => setActive(event.target.value)} aria-invalid={Boolean(activeProblem)} />
          {activeProblem ? <span className="field-error">{activeProblem}</span> : null}
        </div>
        {suspends ? (
          <div className="field">
            <span className="field-label-row"><label htmlFor={`${id}-retained`}>{t("Retained sandboxes")}</label><HelpTip>{t("Sandboxes kept on this node for resuming, the running ones included. At least the number at once.")}</HelpTip></span>
            <input id={`${id}-retained`} inputMode="numeric" value={retained} onChange={(event) => setRetained(event.target.value)} aria-invalid={Boolean(retainedProblem)} />
            {retainedProblem ? <span className="field-error">{retainedProblem}</span> : null}
          </div>
        ) : null}
        {error ? <p className="confirm-dialog-error" role="alert">{error}</p> : null}
      </form>
    </Modal>
  );
}
