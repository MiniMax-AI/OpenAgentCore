import { useId } from "react";
import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";
import { Modal } from "../../components/Modal";
import { isUsableName, keyNameProblem, normalizeName, type FlowError, type KeyFlow, type NameProblem } from "./key-flows";
import { PlaintextKey } from "./IssuedKey";
import type { KeyFlowControls } from "./use-key-flow";

export function FlowErrorMessage({ error, name }: { error: FlowError | null; name?: string }) {
  const { t } = useTranslation("keys");
  if (!error) return null;
  const text = error.kind === "uncertain" ? t("errors.uncertain") : t("errors.rejected", { message: error.message });
  return <p className="key-flow-error" role="alert">{text}</p>;
}

/** A labelled name input with its rules behind a help tip and its problem underneath. */
export function NameField({ label, help, value, onChange, problem, problemText, placeholder, disabled = false, autoFocus = false, name }: {
  label: string;
  help: string;
  value: string;
  onChange: (value: string) => void;
  problem: NameProblem | null;
  problemText: string;
  placeholder?: string;
  disabled?: boolean;
  autoFocus?: boolean;
  name: string;
}) {
  const id = useId();
  return (
    // The label names the input explicitly: it also holds the help tip's button,
    // which would otherwise become the control it labels.
    <label className="field key-name-field" htmlFor={`${id}-input`}>
      <span className="field-label-row">{label}<HelpTip id={`${id}-help`}>{help}</HelpTip></span>
      <input
        id={`${id}-input`}
        name={name}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        autoComplete="off"
        spellCheck={false}
        autoFocus={autoFocus}
        disabled={disabled}
        aria-invalid={problem ? true : undefined}
        aria-describedby={`${id}-help${problem ? ` ${id}-problem` : ""}`}
      />
      {problem ? <span id={`${id}-problem`} className="field-error" role="alert">{problemText}</span> : null}
    </label>
  );
}

/** The key-name field of an issue flow. `taken` are the project's active key names. */
export function KeyNameField({ flow, controls, taken, autoFocus = false }: {
  flow: Extract<KeyFlow, { step: "issue" }>;
  controls: KeyFlowControls;
  taken: readonly string[];
  autoFocus?: boolean;
}) {
  const { t } = useTranslation("keys");
  const problem = keyNameProblem(flow.name, taken);
  return (
    <NameField
      name="key-name"
      label={t("issueDialog.name")}
      help={t("issueDialog.nameHelp")}
      value={flow.name}
      onChange={(name) => controls.dispatch({ type: "setName", name })}
      problem={problem}
      problemText={problem ? t(`issueDialog.problems.${problem}`) : ""}
      placeholder={t("issueDialog.placeholder")}
      disabled={flow.busy}
      autoFocus={autoFocus}
    />
  );
}

export function canIssue(flow: Extract<KeyFlow, { step: "issue" }>, taken: readonly string[]): boolean {
  return !flow.busy && isUsableName(flow.name, keyNameProblem(flow.name, taken));
}

/**
 * The issue-key dialog and the one-time plaintext. Closing the plaintext
 * dialog keeps the key on the page (`PendingKeyNotice`) until the operator
 * confirms it was saved.
 */
export function KeyFlowDialogs({ controls, taken }: { controls: KeyFlowControls; taken: readonly string[] }) {
  const { t } = useTranslation("keys");
  const { t: tCommon } = useTranslation("common");
  const { flow, dispatch } = controls;
  const cancel = () => dispatch({ type: "cancel" });

  if (flow.step === "issue" && flow.project) {
    const ready = canIssue(flow, taken);
    return (
      <Modal
        open
        title={t("issueDialog.title", { project: flow.project.name })}
        onClose={cancel}
        footer={(
          <>
            <button className="button outline" type="button" onClick={cancel} disabled={flow.busy}>{tCommon("actions.cancel")}</button>
            <button className="button primary" type="submit" form="key-issue-form" disabled={!ready}>
              {flow.busy ? t("issueDialog.submitting") : t("issueDialog.submit")}
            </button>
          </>
        )}
      >
        <form id="key-issue-form" className="key-dialog-form" onSubmit={(event) => { event.preventDefault(); if (ready) void controls.submit(); }}>
          <KeyNameField flow={flow} controls={controls} taken={taken} />
          <FlowErrorMessage error={flow.error} name={normalizeName(flow.name)} />
        </form>
      </Modal>
    );
  }

  if (flow.step === "issued") {
    return (
      <Modal
        open={flow.open}
        title={t("issued.title")}
        onClose={() => dispatch({ type: "hideIssued" })}
        footer={<button className="button primary" type="button" onClick={() => dispatch({ type: "saved" })}>{t("issued.saved")}</button>}
      >
        <div className="key-dialog-body">
          <p className="plaintext-key-notice">{t("issued.onceNotice")}</p>
          <PlaintextKey value={flow.issued.key} label={t("issued.keyLabel", { name: flow.issued.name })} caption={flow.issued.name} />
        </div>
      </Modal>
    );
  }

  return null;
}

/** A plaintext key whose dialog was closed stays here until it is confirmed saved. */
export function PendingKeyNotice({ controls }: { controls: KeyFlowControls }) {
  const { t } = useTranslation("keys");
  const { flow, dispatch } = controls;
  if (flow.step !== "issued" || flow.open) return null;
  return (
    <section className="pending-key" aria-label={t("issued.keyLabel", { name: flow.issued.name })}>
      <p className="plaintext-key-notice">{t("issued.pending", { name: flow.issued.name, project: flow.project.name })}</p>
      <PlaintextKey value={flow.issued.key} label={t("issued.keyLabel", { name: flow.issued.name })} />
      <div className="pending-key-actions">
        <button className="button primary" type="button" onClick={() => dispatch({ type: "saved" })}>{t("issued.saved")}</button>
      </div>
    </section>
  );
}
