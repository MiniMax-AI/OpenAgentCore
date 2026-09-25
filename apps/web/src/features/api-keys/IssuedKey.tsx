import { Check, Copy } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

type CopyState = "idle" | "copied" | "failed";

export function useCopy(value: string) {
  const [state, setState] = useState<CopyState>("idle");
  useEffect(() => {
    if (state !== "copied") return;
    const timer = window.setTimeout(() => setState("idle"), 1800);
    return () => window.clearTimeout(timer);
  }, [state]);
  const copy = async () => {
    try {
      if (!navigator.clipboard) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(value);
      setState("copied");
    } catch {
      setState("failed");
    }
  };
  return { state, copy };
}

/**
 * The one-time plaintext of a new key: a read-only field and a copy button.
 * The value lives only in React state; it is never written to browser storage
 * and never offered to password managers.
 */
export function PlaintextKey({ value, label, caption }: { value: string; label: string; caption?: string }) {
  const { t } = useTranslation("keys");
  const input = useRef<HTMLInputElement>(null);
  const { state, copy } = useCopy(value);
  useEffect(() => {
    if (state === "failed") input.current?.select();
  }, [state]);
  return (
    <div className="plaintext-key">
      {caption ? <span className="plaintext-key-caption">{caption}</span> : null}
      <div className="plaintext-key-row">
        <input
          ref={input}
          aria-label={label}
          value={value}
          readOnly
          spellCheck={false}
          autoComplete="off"
          data-1p-ignore=""
          data-lpignore="true"
          onFocus={(event) => event.target.select()}
        />
        <button type="button" className="button outline" onClick={() => void copy()}>
          {state === "copied" ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
          {state === "copied" ? t("issued.copied") : t("issued.copy")}
        </button>
      </div>
      {state === "failed" ? <p className="plaintext-key-error" role="alert">{t("issued.copyFailed")}</p> : null}
    </div>
  );
}

/** A short command with a copy button; the console never runs it. */
export function CommandBlock({ value, label }: { value: string; label: string }) {
  const { t } = useTranslation("keys");
  const { state, copy } = useCopy(value);
  return (
    <div className="command-block">
      <pre aria-label={label} tabIndex={0}><code>{value}</code></pre>
      <button type="button" className="icon-button ghost command-block-copy" aria-label={state === "copied" ? t("issued.copied") : t("issued.copyCommand")} title={state === "copied" ? t("issued.copied") : t("issued.copyCommand")} onClick={() => void copy()}>
        {state === "copied" ? <Check size={14} strokeWidth={1.7} aria-hidden="true" /> : <Copy size={14} strokeWidth={1.7} aria-hidden="true" />}
      </button>
      {state === "failed" ? <p className="plaintext-key-error" role="alert">{t("issued.copyCommandFailed")}</p> : null}
    </div>
  );
}
