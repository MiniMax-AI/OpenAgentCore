import { Layers3, Server } from "lucide-react";
import { useLocale } from "../lib/LocaleProvider";
export type SystemView = "system" | "sandbox";
export function SystemNavigation({ active, onSelect }: { active: SystemView | null; onSelect: (view: SystemView) => void }) {
  const { t, locale, setLocale } = useLocale();
  return <nav className="main-nav" aria-label={t("System navigation")} lang={locale}>
    <p className="nav-label">{t("System")}</p>
    {([{ id: "system", label: t("System"), icon: Layers3 }, { id: "sandbox", label: t("Hosted Sandbox Manager"), icon: Server }] as const).map(({ id, label, icon: Icon }) => (
      <button key={id} type="button" className={active === id ? "active" : ""} onClick={() => onSelect(id)} aria-label={label} aria-current={active === id ? "page" : undefined}>
        <Icon size={15} strokeWidth={1.5} aria-hidden="true" /><span>{label}</span>
      </button>
    ))}
    <label className="field"><span>Language / 语言</span><select aria-label="Language / 语言" value={locale} onChange={(event) => setLocale(event.target.value === "zh" ? "zh" : "en")}><option value="en">English</option><option value="zh">中文</option></select></label>
  </nav>;
}
