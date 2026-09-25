import { KeyRound } from "lucide-react";
import { useTranslation } from "react-i18next";
export function ConsoleNavigation({ keysAvailable, activeView, onKeys }: {
  keysAvailable: boolean; activeView: string; onKeys: () => void;
}) {
  const { t } = useTranslation("firstRun");
  if (!keysAvailable) return null;
  return <nav className="main-nav" aria-label={t("API keys")}>
    <button type="button" className={activeView === "api-keys" ? "active" : ""} aria-current={activeView === "api-keys" ? "page" : undefined} onClick={onKeys}>
      <KeyRound size={15} strokeWidth={1.5} aria-hidden="true" /><span>{t("API keys")}</span>
    </button>
  </nav>;
}
