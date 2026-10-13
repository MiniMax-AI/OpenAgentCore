import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { HelpTip } from "../../components/console-ui";

export function nodeCapacity(active: string, retained: string) {
  const whole = (value: string) => /^\d+$/.test(value.trim()) ? Number(value.trim()) : null;
  const valid = (value: number | null): value is number => value !== null && value >= 1 && value <= 1_000_000;
  const maxActive = whole(active), maxRetained = whole(retained);
  return { active: maxActive, retained: maxRetained,
    activeError: valid(maxActive) ? null : "Enter a whole number from 1 to 1,000,000." as const,
    retainedError: !valid(maxRetained) ? "Enter a whole number from 1 to 1,000,000." as const : valid(maxActive) && maxRetained < maxActive ? "Enter at least the number of sandboxes at once." as const : null };
}

export function NodeCapacityFields({ id, active, retained, suspends, activeError, retainedError, onActive, onRetained, autoFocus, help }: {
  id: string; active: string; retained: string; suspends: boolean; activeError: string | null; retainedError: string | null;
  onActive: (value: string) => void; onRetained: (value: string) => void; autoFocus?: boolean; help?: ReactNode;
}) {
  const { t } = useTranslation("sandbox");
  return <>
    <div className="field">
      <span className="field-label-row"><label htmlFor={`${id}-active`}>{t("Sandboxes at once")}</label><HelpTip>{t("The most sandboxes Core places on this node at the same time.")}{help}</HelpTip></span>
      <input id={`${id}-active`} inputMode="numeric" autoComplete="off" autoFocus={autoFocus} value={active} onChange={(event) => onActive(event.target.value)} aria-invalid={Boolean(activeError)} aria-errormessage={activeError ? `${id}-active-error` : undefined} />
      {activeError ? <span id={`${id}-active-error`} className="field-error">{activeError}</span> : null}
    </div>
    {suspends ? <div className="field">
      <span className="field-label-row"><label htmlFor={`${id}-retained`}>{t("Retained sandboxes")}</label><HelpTip>{t("Sandboxes kept on this node for resuming, the running ones included. At least the number at once.")}</HelpTip></span>
      <input id={`${id}-retained`} inputMode="numeric" autoComplete="off" value={retained} onChange={(event) => onRetained(event.target.value)} aria-invalid={Boolean(retainedError)} aria-errormessage={retainedError ? `${id}-retained-error` : undefined} />
      {retainedError ? <span id={`${id}-retained-error`} className="field-error">{retainedError}</span> : null}
    </div> : null}
  </>;
}
