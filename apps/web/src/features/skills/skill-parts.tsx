import { Check, Copy } from "lucide-react";
import { useEffect, useState, type MouseEvent } from "react";
import { useTranslation } from "react-i18next";

import type { Skill } from "@oac/agents-client";

import { StatusDot } from "../../components/console-ui";
import { shortId } from "../../lib/format";

/** A resource ID with a copy button; compact IDs keep the full value in the tooltip. */
export function CopyableId({ id, compact = false }: { id: string; compact?: boolean }) {
  const { t } = useTranslation("skills");
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
  const label = copied ? t("actions.copied") : t("actions.copyId");
  return (
    <span className="skill-id">
      <code title={id}>{compact ? shortId(id) : id}</code>
      <button type="button" className="icon-button ghost skill-copy" aria-label={label} title={label} onClick={copy}>
        {copied ? <Check size={13} strokeWidth={1.7} aria-hidden="true" /> : <Copy size={13} strokeWidth={1.7} aria-hidden="true" />}
      </button>
    </span>
  );
}

/** The latest version, marked when it is newer than the default. */
export function LatestVersion({ skill }: { skill: Skill }) {
  const { t } = useTranslation("skills");
  const newer = skill.latest_version !== skill.default_version;
  return (
    <span className="skill-latest">
      <span>{t("version", { version: skill.latest_version })}</span>
      {newer ? (
        <span title={t("list.newerThanDefaultHelp")}>
          <StatusDot tone="warning" label={t("list.newerThanDefault")} />
        </span>
      ) : null}
    </span>
  );
}
