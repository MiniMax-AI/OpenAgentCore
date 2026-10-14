import type { CoreInstallationConfiguration, CoreInstallationSetting } from "@oac/agents-client";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { ValuePill } from "../../components/atoms/ValuePill";
import { Section } from "../../components/console-ui";
import { docsUrl } from "../../lib/docs";
import { harnessNames } from "../../lib/harness-labels";

const serviceNames: Record<CoreInstallationSetting["restarts"][number], string> = { core: "Core", web: "Web" };

/** The settings whose values are Harness IDs, shown by their names. */
const harnessSettings = new Set(["core.harnesses", "core.default_harness"]);

function word(value: unknown, harnesses: boolean): string {
  if (typeof value === "string") return harnesses && Object.hasOwn(harnessNames, value) ? harnessNames[value as keyof typeof harnessNames] : value;
  return typeof value === "number" ? String(value) : JSON.stringify(value);
}

function isDefault(setting: CoreInstallationSetting): boolean {
  return !setting.sensitive && JSON.stringify(setting.value ?? null) === JSON.stringify(setting.default ?? null);
}

/**
 * Settings › Service and deployment: the process settings Core loaded, read-only, by their
 * names in the configuration guide, which the section links to for the variables to edit.
 * A sensitive setting shows whether it is set, never its value.
 */
export function StartupSettings({ configuration }: { configuration: CoreInstallationConfiguration }) {
  const { t, i18n } = useTranslation("system");
  const muted = (text: string) => <span className="system-muted">{text}</span>;
  const value = (setting: CoreInstallationSetting): ReactNode => {
    if (setting.sensitive) return t(setting.configured ? "startup.configured" : "startup.notSet");
    const current = setting.value;
    if (current === null || current === undefined) return muted(t("startup.notSet"));
    if (typeof current === "boolean") return t(current ? "values.on" : "values.off");
    const harnesses = harnessSettings.has(setting.key);
    if (Array.isArray(current)) return current.length ? current.map((entry) => word(entry, harnesses)).join(", ") : muted(t("startup.none"));
    return word(current, harnesses);
  };
  return (
    <Section
      headingId="system-startup-heading"
      title={t("startup.title")}
      help={t("startup.help")}
      actions={<a className="text-action" href={docsUrl("docs/configuration#process-settings", i18n.resolvedLanguage)} target="_blank" rel="noreferrer">{t("startup.reference")}</a>}
    >
      <div className="settings-table">
        <table className="data-table" aria-label={t("startup.title")}>
          <thead>
            <tr>
              <th scope="col">{t("startup.columns.key")}</th>
              <th scope="col">{t("startup.columns.value")}</th>
              <th scope="col">{t("startup.columns.restarts")}</th>
            </tr>
          </thead>
          <tbody>
            {configuration.settings.filter((setting) => setting.key !== "public_url").map((setting) => (
              <tr key={setting.key}>
                <th scope="row">{t(`startup.labels.${setting.key}`, { defaultValue: setting.key })}</th>
                <td>
                  <span className="system-setting-value">
                    {value(setting)}
                    {isDefault(setting) ? <ValuePill>{t("startup.default")}</ValuePill> : null}
                  </span>
                </td>
                <td>{setting.restarts.length ? setting.restarts.map((service) => serviceNames[service]).join(", ") : muted(t("startup.none"))}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Section>
  );
}
