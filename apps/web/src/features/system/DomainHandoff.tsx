import { useTranslation } from "react-i18next";
import { domainTarget } from "./domain-api";

/** A navigation invitation, never a browser-side claim that HTTPS is reachable. */
export function DomainHandoff({ url }: { url: string | null }) {
  const { t } = useTranslation("system");
  const target = domainTarget(url);
  if (!target) return null;
  return <aside className="form-stack" role="status">
    <p>{t("domain.reconnect")}</p>
    <a className="button outline" href={target} rel="noreferrer">{t("domain.open", { url: target })}</a>
  </aside>;
}
