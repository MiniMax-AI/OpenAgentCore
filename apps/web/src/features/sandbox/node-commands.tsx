import { Check, Copy, Terminal } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { useCopy } from "../api-keys/IssuedKey";
import { hostRequirements, type HostPrerequisite } from "./node-enrollment";

/** A multi-line command to paste into a terminal, with its copy button. Key it by the command so its copy state starts over. */
export function CommandBlock({ value, label, copyName, extra, autoFocus = false, onCopy }: {
  value: string;
  /** Names the command's field for assistive technology. */
  label: string;
  /** Names the copy button when a dialog has more than one; it starts with the visible "Copy command". */
  copyName?: string;
  /** Shown in the heading before the copy button, such as the command's expiry. */
  extra?: ReactNode;
  autoFocus?: boolean;
  /** Called when the administrator copies this command. */
  onCopy?: () => void;
}) {
  const { t } = useTranslation("sandbox");
  const { state, copy } = useCopy(value);
  return <>
    <div className="sandbox-command">
      <div className="sandbox-command-heading">
        <span><Terminal size={15} />{t("Terminal")}</span>
        {extra}
        <button type="button" className="button outline" autoFocus={autoFocus} aria-label={state === "copied" ? t("Copied") : copyName} onClick={() => { onCopy?.(); void copy(); }}>
          {state === "copied" ? <Check size={14} /> : <Copy size={14} />}{state === "copied" ? t("Copied") : t("Copy command")}
        </button>
      </div>
      {/* Named by aria-label: a wrapping label would fold the command into its own name. */}
      <div className="field"><textarea aria-label={label} readOnly rows={5} value={value} onClick={(event) => event.currentTarget.select()} spellCheck={false} /></div>
    </div>
    {state === "failed" ? <p role="alert">{t("Select the command above and copy it manually.")}</p> : null}
  </>;
}

/** A short command to run on the host, copied with one click. */
export function CopyCommand({ value }: { value: string }) {
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

/** The public URL and sandbox size the requirement labels name. */
export type RequirementValues = { core: string; size: string };

function PrerequisiteList({ items, values }: { items: HostPrerequisite[]; values: RequirementValues }) {
  const { t } = useTranslation("sandbox");
  return <ul>
    {items.map((item) => (
      <li key={item.label}>
        <span>{t(item.label, values)}</span>
      </li>
    ))}
  </ul>;
}

/** What the host needs for the default command, which installs the node as a system service. */
export function HostRequirements({ provider, sized, values, open, onToggle }: {
  provider: "docker" | "microsandbox";
  sized: boolean;
  values: RequirementValues;
  open: boolean;
  onToggle: (open: boolean) => void;
}) {
  const { t } = useTranslation("sandbox");
  return <details className="sandbox-host-requirements" open={open} onToggle={(event) => onToggle(event.currentTarget.open)}>
    <summary>{t("Host requirements")}</summary>
    <PrerequisiteList items={hostRequirements(provider, sized)} values={values} />
    <div className="sandbox-host-requirements-note">
      <p>{t("The command creates the oac-node service user and a system service. It installs no software; if something is missing it stops and says what to install.")}</p>
      {provider === "docker" ? <p>{t("oac-node joins the docker group, which is equivalent to root on this host.")}</p> : null}
    </div>
  </details>;
}
