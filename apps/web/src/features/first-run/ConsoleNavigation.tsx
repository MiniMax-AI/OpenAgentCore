import { KeyRound, Sparkles } from "lucide-react";
import { useLocale } from "../../lib/LocaleProvider";
export function ConsoleNavigation({ showIntroduction, introductionAvailable, keysAvailable, activeView, onIntroduction, onKeys }: {
  showIntroduction: boolean; introductionAvailable: boolean; keysAvailable: boolean;
  activeView: string; onIntroduction: () => void; onKeys: () => void;
}) {
  const { t } = useLocale();
  if (!introductionAvailable && !keysAvailable) return null;
  return <nav className="main-nav" aria-label={t("Getting started")}>
    {introductionAvailable ? <button type="button" className={showIntroduction ? "active" : ""} aria-current={showIntroduction ? "page" : undefined} onClick={onIntroduction}>
      <Sparkles size={15} strokeWidth={1.5} aria-hidden="true" /><span>{t("Getting started")}</span>
    </button> : null}
    {keysAvailable ? <button type="button" className={activeView === "api-keys" ? "active" : ""} aria-current={activeView === "api-keys" ? "page" : undefined} onClick={onKeys}>
      <KeyRound size={15} strokeWidth={1.5} aria-hidden="true" /><span>{t("API keys")}</span>
    </button> : null}
  </nav>;
}
