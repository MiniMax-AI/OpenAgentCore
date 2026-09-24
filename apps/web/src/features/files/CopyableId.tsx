import { Check, Copy } from "lucide-react";
import { useEffect, useState, type MouseEvent } from "react";
import { useTranslation } from "react-i18next";

import { shortId } from "../../lib/format";

/** A one-click copy button for an ID. */
export function CopyIdButton({ id, label }: { id: string; label?: string }) {
  const { t } = useTranslation("files");
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const copy = (event: MouseEvent) => {
    event.stopPropagation();
    const clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
    if (!clipboard) return;
    void clipboard.writeText(id).then(() => setCopied(true), () => undefined);
  };
  const name = copied ? t("actions.copied") : label ?? t("actions.copyId");
  return (
    <button type="button" className="icon-button ghost copyable-id-button" aria-label={name} title={name} onClick={copy}>
      {copied ? <Check size={13} strokeWidth={1.7} aria-hidden="true" /> : <Copy size={13} strokeWidth={1.7} aria-hidden="true" />}
    </button>
  );
}

/** A resource ID with its copy button; compact IDs keep the full value in the tooltip. */
export function CopyableId({ id, compact = false, label }: { id: string; compact?: boolean; label?: string }) {
  return (
    <span className="copyable-id">
      <code title={id}>{compact ? shortId(id) : id}</code>
      <CopyIdButton id={id} label={label} />
    </span>
  );
}
