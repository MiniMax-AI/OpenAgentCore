import { useTranslation } from "react-i18next";

import { HelpTip } from "../../components/console-ui";

import type { KeyActor, OwnerRecord } from "./ownership";

/** The label for the key that performed an action; never a secret, only name and prefix. */
export function actorLabel(actor: KeyActor, t: (key: "actor.console" | "actor.static") => string): string {
  if (actor.type === "console") return t("actor.console");
  if (actor.type === "static") return actor.name ?? (actor.prefix ? `${actor.prefix}…` : t("actor.static"));
  return actor.name ?? actor.prefix ?? "";
}

export function ActorName({ actor }: { actor: KeyActor }) {
  const { t } = useTranslation("ownership");
  const label = actorLabel(actor, t);
  const title = [actor.prefix ? `${actor.prefix}…` : null, actor.revoked_at ? t("actor.revokedTitle") : null].filter(Boolean).join(" · ");
  return (
    <span className={actor.revoked_at ? "owner-name owner-revoked" : "owner-name"} title={title || undefined}>
      {label}
      {actor.revoked_at ? <span className="owner-flag">{t("actor.revoked")}</span> : null}
    </span>
  );
}

/** "API key" column cell: the owning key, or a dash when Core has no record. */
export function OwnerCell({ record }: { record: OwnerRecord | null | undefined }) {
  const { t } = useTranslation("ownership");
  if (record === undefined) return <span className="owner-missing" aria-label={t("owner.loading")}>—</span>;
  if (!record?.owner) return <span className="owner-missing" title={t("owner.noneTitle")}>—</span>;
  return <ActorName actor={record.owner} />;
}

/** Column heading for the owning API key, with its explanation behind the help tip. */
export function OwnerHeading() {
  const { t } = useTranslation("ownership");
  return <span className="column-help">{t("column")}<HelpTip>{t("columnHelp")}</HelpTip></span>;
}
