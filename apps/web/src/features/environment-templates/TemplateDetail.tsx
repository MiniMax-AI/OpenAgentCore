import { ArrowLeft, Copy, Trash2 } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import type {
  EnvironmentTemplateFile,
  EnvironmentTemplateResource,
  EnvironmentTemplateSection,
  EnvironmentTemplateSkill,
} from "@agents-core-web/agents-client";

import { PageBody, PageHeader, RefreshButton, Section, StatusDot } from "../../components/console-ui";
import { formatBytes, formatDateTime, MISSING } from "../../lib/format";
import { CopyableId, CopyIdButton } from "../../components/list-ui";
import { templateName } from "./template-name";

export interface TemplateLinks {
  /** Opens the Files page for a referenced File ID. */
  onOpenFile?: (fileId: string) => void;
  /** Opens the Skills page for a referenced Skill ID. */
  onOpenSkill?: (skillId: string) => void;
}

export interface TemplateDetailPageProps extends TemplateLinks {
  template: EnvironmentTemplateResource;
  blocked: boolean;
  refreshing: boolean;
  notice?: ReactNode;
  onBack: () => void;
  onRefresh: () => void;
  onDelete: () => void;
  /** Copies the Template into another project; absent while unavailable. */
  onCopy?: () => void;
  /** Extra facts shown first, such as the owning project and creator. */
  facts?: ReactNode;
}

const sectionKeys: Record<EnvironmentTemplateSection, string> = {
  network: "detail.network",
  capability_directories: "detail.directories",
  packages: "detail.packages",
  files: "detail.files",
  plugins: "detail.plugins",
  skills: "detail.skills",
};

/** Readable names of unrecognized sections; unexpected fields keep their raw name. */
export function unrecognizedLabels(template: EnvironmentTemplateResource, translate: (key: string) => string): string[] {
  return (template.unrecognized ?? []).map((field) => field in sectionKeys
    ? translate(sectionKeys[field as EnvironmentTemplateSection])
    : field);
}

export function skillVersionLabel(version: string | null, translate: (key: string, options?: Record<string, unknown>) => string): string {
  if (version === null) return translate("detail.versionDefault");
  if (version === "latest") return translate("detail.versionLatest");
  return translate("detail.versionExact", { version });
}

function Count({ value }: { value: number | undefined }) {
  return value === undefined ? null : <span className="heading-count">{value}</span>;
}

function Unrecognized() {
  const { t } = useTranslation("templates");
  return <p className="template-muted"><StatusDot tone="warning" label={t("unrecognizedValue")} /></p>;
}

function None() {
  const { t } = useTranslation("templates");
  return <p className="template-muted">{t("detail.none")}</p>;
}

function Values({ values }: { values: string[] }) {
  return values.length ? (
    <ul className="template-values">
      {values.map((value, index) => <li key={`${index}:${value}`}><code>{value}</code></li>)}
    </ul>
  ) : <None />;
}

function ReferenceLink({ id, label, copyLabel, onOpen }: { id: string; label: string; copyLabel?: string; onOpen?: (id: string) => void }) {
  if (!onOpen) return <CopyableId id={id} label={copyLabel} />;
  return (
    <span className="copyable-id">
      <button type="button" className="table-link template-reference" aria-label={label} title={label} onClick={() => onOpen(id)}>
        <code>{id}</code>
      </button>
      <CopyIdButton id={id} label={copyLabel} />
    </span>
  );
}

function FileRow({ file, locale, onOpenFile }: { file: EnvironmentTemplateFile; locale?: string } & TemplateLinks) {
  const { t } = useTranslation("templates");
  const { t: tFiles } = useTranslation("files");
  return (
    <tr>
      <th scope="row"><code className="template-path">{file.path}</code></th>
      <td>{file.type === "inline" ? t("detail.inline") : t("detail.fileReference")}</td>
      <td>
        {file.type === "inline" ? (
          <span className="template-nowrap" title={`${file.size_bytes.toLocaleString(locale)} B`}>{formatBytes(file.size_bytes)}</span>
        ) : <ReferenceLink id={file.file_id} label={t("detail.openFile", { id: file.file_id })} copyLabel={tFiles("actions.copyId")} onOpen={onOpenFile} />}
      </td>
    </tr>
  );
}

function SkillRow({ skill, onOpenSkill }: { skill: EnvironmentTemplateSkill } & TemplateLinks) {
  const { t } = useTranslation("templates");
  if (skill.type === "inline") {
    return (
      <tr>
        <th scope="row"><strong>{skill.name}</strong></th>
        <td>{t("detail.inline")}</td>
        <td>{MISSING}</td>
        <td className="template-text">{skill.description}</td>
      </tr>
    );
  }
  return (
    <tr>
      <th scope="row"><ReferenceLink id={skill.skill_id} label={t("detail.openSkill", { id: skill.skill_id })} onOpen={onOpenSkill} /></th>
      <td>{t("detail.reference")}</td>
      <td className="template-nowrap">{skillVersionLabel(skill.version, t as never)}</td>
      <td>{MISSING}</td>
    </tr>
  );
}

/** One Template with every safe configuration section Core returns. */
export function TemplateDetailPage({
  template, blocked, refreshing, notice, onBack, onRefresh, onDelete, onCopy, facts, onOpenFile, onOpenSkill,
}: TemplateDetailPageProps) {
  const { t, i18n } = useTranslation("templates");
  const { t: tCommon } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const name = templateName(template);
  const unrecognized = unrecognizedLabels(template, t as never);
  const { network, packages, capability_directories: directories, files, skills, plugins } = template;
  const packageCount = packages ? packages.npm.length + packages.python.length + packages.system.length : undefined;

  return (
    <section className="page-section console-page templates-page" aria-labelledby="template-detail-heading">
      <PageHeader
        headingId="template-detail-heading"
        title={(
          <>
            <button type="button" className="icon-button ghost template-back" aria-label={t("back")} title={t("back")} onClick={onBack}>
              <ArrowLeft size={16} strokeWidth={1.6} aria-hidden="true" />
            </button>
            {name}
          </>
        )}
        actions={(
          <>
            <RefreshButton onClick={onRefresh} refreshing={refreshing} disabled={blocked && !refreshing} />
            {onCopy ? (
              <button className="button outline" type="button" disabled={blocked} onClick={onCopy}>
                <Copy size={14} aria-hidden="true" />{tCommon("copy.action")}
              </button>
            ) : null}
            <button className="button danger" type="button" disabled={blocked} aria-label={t("deleteLabel", { name })} onClick={onDelete}>
              <Trash2 size={14} aria-hidden="true" />{t("delete")}
            </button>
          </>
        )}
      />
      <PageBody>
        {notice}
        {unrecognized.length ? (
          <p className="coverage-note template-unrecognized" role="note">{t("unrecognizedSections", { sections: unrecognized.join(", ") })}</p>
        ) : null}
        <dl className="template-facts" aria-label={t("detail.facts")}>
          <div><dt>{t("detail.id")}</dt><dd><CopyableId id={template.id} /></dd></div>
          {facts}
          <div><dt>{t("detail.created")}</dt><dd>{formatDateTime(template.created_at, locale)}</dd></div>
          <div><dt>{t("detail.updated")}</dt><dd>{formatDateTime(template.updated_at, locale)}</dd></div>
          <div>
            <dt>{t("detail.network")}</dt>
            <dd>{network ? t(`access.${network.access}`) : <StatusDot tone="warning" label={t("unrecognizedValue")} />}</dd>
          </div>
        </dl>

        {network?.access === "restricted" ? (
          <Section headingId="template-domains-heading" title={<>{t("detail.allowedDomains")} <Count value={network.allowed_domains.length} /></>} help={t("accessHelp.restricted")}>
            <Values values={network.allowed_domains} />
          </Section>
        ) : null}

        <Section headingId="template-packages-heading" title={<>{t("detail.packages")} <Count value={packageCount} /></>} help={t("detail.packagesHelp")}>
          {!packages ? <Unrecognized /> : packageCount === 0 ? <None /> : (
            <div className="table-frame">
              <table className="data-table data-table-compact template-table">
                <thead><tr><th scope="col">{t("detail.manager")}</th><th scope="col">{t("detail.packages")}</th></tr></thead>
                <tbody>
                  {(["npm", "python", "system"] as const).map((manager) => (
                    <tr key={manager}>
                      <th scope="row">{t(`detail.${manager}`)}</th>
                      <td><Values values={packages[manager]} /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Section>

        <Section headingId="template-directories-heading" title={<>{t("detail.directories")} <Count value={directories?.length} /></>} help={t("detail.directoriesHelp")}>
          {directories ? <Values values={directories} /> : <Unrecognized />}
        </Section>

        <Section headingId="template-files-heading" title={<>{t("detail.files")} <Count value={files?.length} /></>} help={t("detail.filesHelp")}>
          {!files ? <Unrecognized /> : files.length === 0 ? <None /> : (
            <div className="table-frame">
              <table className="data-table data-table-compact template-table">
                <thead><tr><th scope="col">{t("detail.path")}</th><th scope="col">{t("detail.source")}</th><th scope="col">{t("detail.size")} / {t("detail.fileReference")}</th></tr></thead>
                <tbody>{files.map((file) => <FileRow key={file.path} file={file} locale={locale} onOpenFile={onOpenFile} />)}</tbody>
              </table>
            </div>
          )}
        </Section>

        <Section headingId="template-skills-heading" title={<>{t("detail.skills")} <Count value={skills?.length} /></>} help={t("detail.skillsHelp")}>
          {!skills ? <Unrecognized /> : skills.length === 0 ? <None /> : (
            <div className="table-frame">
              <table className="data-table data-table-compact template-table">
                <thead><tr><th scope="col">{t("detail.skill")}</th><th scope="col">{t("detail.type")}</th><th scope="col">{t("detail.version")}</th><th scope="col">{t("detail.description")}</th></tr></thead>
                <tbody>{skills.map((skill, index) => <SkillRow key={index} skill={skill} onOpenSkill={onOpenSkill} />)}</tbody>
              </table>
            </div>
          )}
        </Section>

        <Section headingId="template-plugins-heading" title={<>{t("detail.plugins")} <Count value={plugins?.length} /></>}>
          {!plugins ? <Unrecognized /> : plugins.length === 0 ? <None /> : (
            <div className="table-frame">
              <table className="data-table data-table-compact template-table">
                <thead><tr><th scope="col">{t("detail.name")}</th><th scope="col">{t("detail.description")}</th></tr></thead>
                <tbody>
                  {plugins.map((plugin) => (
                    <tr key={plugin.name}><th scope="row"><strong>{plugin.name}</strong></th><td className="template-text">{plugin.description}</td></tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Section>

        <Section headingId="template-secrets-heading" title={t("detail.secrets")}>
          <p className="template-muted">{t("detail.secretsText")}</p>
        </Section>
      </PageBody>
    </section>
  );
}
