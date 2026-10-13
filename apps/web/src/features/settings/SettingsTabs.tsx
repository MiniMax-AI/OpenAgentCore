import { useTranslation } from "react-i18next";
import { useConsoleNavigation } from "../../lib/console-navigation";
import "./settings.css";

export type SettingsSection = "sandbox" | "storage" | "models" | "service";
const sections: SettingsSection[] = ["sandbox", "storage", "models", "service"];
export function SettingsTabs({ active }: { active: SettingsSection }) {
  const { t } = useTranslation("navigation");
  const { navigate } = useConsoleNavigation();
  return <nav className="settings-tabs" aria-label={t("views.settings")}>
    {sections.map((section) => <button key={section} className="settings-tab" type="button" aria-current={active === section ? "page" : undefined} onClick={() => navigate("settings", { id: section })}>{t(`settings.${section}`)}</button>)}
  </nav>;
}
