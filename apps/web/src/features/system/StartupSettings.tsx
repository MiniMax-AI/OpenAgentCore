import type { CoreInstallationConfiguration, CoreInstallationSetting } from "@oac/agents-client";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { ValuePill } from "../../components/atoms/ValuePill";
import { Section } from "../../components/console-ui";
import { docsUrl } from "../../lib/docs";

const serviceNames: Record<CoreInstallationSetting["restarts"][number], string> = { core: "Core", web: "Web" };

function isDefault(setting: CoreInstallationSetting): boolean {
  return !setting.sensitive && JSON.stringify(setting.value ?? null) === JSON.stringify(setting.default ?? null);
}

/**
 * Settings › Service and deployment: the process settings Core loaded, read-only, each named by
 * the environment variable that sets it. The section links to the guide for changing them.
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
    if (Array.isArray(current)) return current.length ? current.join(", ") : muted(t("startup.none"));
    return String(current);
  };
  return (
    <Section
      headingId="system-startup-heading"
      title={t("startup.title")}
      actions={<a className="text-action" href={docsUrl("docs/configuration#process-settings", i18n.resolvedLanguage)} target="_blank" rel="noreferrer">{t("startup.change")}</a>}
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
            {configuration.settings.filter((setting) => setting.key !== "OAC_PUBLIC_URL").map((setting) => (
              <tr key={setting.key}>
                {/* Narrow screens wrap a name after an underscore, never inside a word. */}
                <th scope="row"><code className="system-code">{setting.key.split("_").map((part, index, parts) => index < parts.length - 1 ? [`${part}_`, <wbr key={index} />] : part)}</code></th>
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
