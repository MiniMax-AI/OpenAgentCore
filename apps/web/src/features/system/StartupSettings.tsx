import type { CoreInstallationConfiguration, CoreInstallationSetting } from "@agents-core-web/agents-client";
import { Trans, useTranslation } from "react-i18next";

import { ValuePill } from "../../components/atoms/ValuePill";
import { Section } from "../../components/console-ui";
import { CopyableId } from "../../components/list-ui";
import { formatDateTime } from "../../lib/format";

function display(value: unknown): string | null {
  if (value === null || value === undefined) return null;
  return typeof value === "string" ? value : JSON.stringify(value);
}

function isDefault(setting: CoreInstallationSetting): boolean {
  return !setting.sensitive && JSON.stringify(setting.value ?? null) === JSON.stringify(setting.default ?? null);
}

/**
 * Platform › System: Core's startup settings, read-only. They live in
 * config.json and take effect with the apply command; the console only says
 * where to change them. A sensitive setting shows whether it is set, never
 * its value.
 */
export function StartupSettings({ configuration }: { configuration: CoreInstallationConfiguration | null }) {
  const { t, i18n } = useTranslation("system");
  const locale = i18n.resolvedLanguage;
  return (
    <Section headingId="system-startup-heading" title={t("startup.title")} help={t("startup.help")}>
      {configuration === null ? <p className="system-note">{t("startup.none")}</p> : <>
        <p className="system-where">
          <span>
            <Trans
              t={t}
              i18nKey="startup.where"
              components={{
                path: <CopyableId id={configuration.path} label={t("startup.copyPath")} />,
                command: <CopyableId id={configuration.apply_command} label={t("startup.copyCommand")} />,
              }}
            />
          </span>
          {configuration.applied_at ? <span className="system-applied">{t("startup.appliedAt", { time: formatDateTime(Date.parse(configuration.applied_at) / 1000, locale) })}</span> : null}
        </p>
        <div className="table-frame">
          <table className="data-table system-settings" aria-label={t("startup.title")}>
            <thead>
              <tr>
                <th scope="col">{t("startup.columns.key")}</th>
                <th scope="col">{t("startup.columns.value")}</th>
                <th scope="col">{t("startup.columns.restarts")}</th>
              </tr>
            </thead>
            <tbody>
              {configuration.settings.map((setting) => {
                const value = setting.sensitive ? null : display(setting.value);
                return (
                  <tr key={setting.key}>
                    <th scope="row"><code className="system-code">{setting.key}</code></th>
                    <td>
                      <span className="system-setting-value">
                        {setting.sensitive
                          ? t(setting.configured ? "startup.configured" : "startup.notSet")
                          : value === null ? <span className="system-muted">{t("startup.notSet")}</span> : <code className="system-code">{value}</code>}
                        {isDefault(setting) ? <ValuePill>{t("startup.default")}</ValuePill> : null}
                        {setting.changeable ? null : <ValuePill>{t("startup.fixed")}</ValuePill>}
                      </span>
                    </td>
                    <td>{setting.restarts.length ? setting.restarts.join(", ") : <span className="system-muted">{t("startup.noRestart")}</span>}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </>}
    </Section>
  );
}
