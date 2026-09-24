import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import type { EnvironmentNetworkAccess, EnvironmentTemplateResource } from "@agents-core-web/agents-client";
import { HelpTip } from "../../components/console-ui";
import { domainsMissing, draftFromTemplate, nameTooLong, templatePatch, type TemplateDraft } from "./template-editor";

const accessModes: EnvironmentNetworkAccess[] = ["enabled", "disabled", "restricted"];

/** Name and network only. Editing never sends any other Template section. */
export function TemplateForm({ template, busy, onSave, onCancel }: {
  template?: EnvironmentTemplateResource;
  busy: boolean;
  onSave: (draft: TemplateDraft) => void;
  onCancel: () => void;
}) {
  const { t } = useTranslation("templates");
  const { t: tCommon } = useTranslation("common");
  const id = useId();
  const [draft, setDraft] = useState<TemplateDraft>(() => draftFromTemplate(template));
  const tooLong = nameTooLong(draft);
  const networkEditable = !template || template.network !== undefined;
  const missingDomains = networkEditable && domainsMissing(draft);
  const invalid = tooLong || missingDomains;
  const unchanged = template && !invalid && Object.keys(templatePatch(template, draft)).length === 0;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!busy && !invalid && !unchanged) onSave(draft);
  };
  return (
    <form className="template-form" onSubmit={submit}>
      <label htmlFor={`${id}-name`}>{t("form.name")} <span aria-hidden="true">{t("form.optional")}</span></label>
      <input id={`${id}-name`} value={draft.name} disabled={busy} aria-invalid={tooLong || undefined} aria-describedby={tooLong ? `${id}-name-error` : undefined}
        onChange={(event) => setDraft({ ...draft, name: event.target.value })} autoComplete="off" placeholder={t("form.namePlaceholder")} />
      {tooLong ? <p id={`${id}-name-error`} className="template-form-error" role="alert">{t("form.nameTooLong")}</p> : null}
      <div className="template-form-label">
        <label htmlFor={`${id}-network`}>{t("form.networkAccess")}</label>
        <HelpTip>{t("form.networkHelp")} {t(`accessHelp.${draft.access}`)}</HelpTip>
      </div>
      <select id={`${id}-network`} value={draft.access} disabled={busy || !networkEditable}
        onChange={(event) => setDraft({ ...draft, access: event.target.value as EnvironmentNetworkAccess })}>
        {accessModes.map((mode) => <option key={mode} value={mode}>{t(`access.${mode}`)}</option>)}
      </select>
      {!networkEditable ? <p className="template-form-note">{t("form.policyLocked")}</p> : null}
      {networkEditable && draft.access === "restricted" ? (
        <>
          <div className="template-form-label">
            <label htmlFor={`${id}-domains`}>{t("form.domains")}</label>
            <HelpTip>{t("form.domainsHelp")}</HelpTip>
          </div>
          <textarea id={`${id}-domains`} value={draft.domains} disabled={busy} rows={4} spellCheck={false} autoComplete="off"
            placeholder={t("form.domainsPlaceholder")} aria-invalid={missingDomains || undefined}
            aria-describedby={missingDomains ? `${id}-domains-error` : undefined}
            onChange={(event) => setDraft({ ...draft, domains: event.target.value })} />
          {missingDomains ? <p id={`${id}-domains-error`} className="template-form-error">{t("form.domainsRequired")}</p> : null}
        </>
      ) : null}
      {template ? <p className="template-form-note">{t("form.keepsOther")}</p> : null}
      <div className="template-form-actions">
        <button className="button outline" type="button" disabled={busy} onClick={onCancel}>{tCommon("actions.cancel")}</button>
        <button className="button primary" type="submit" disabled={busy || invalid || Boolean(unchanged)}>{busy ? t("form.saving") : template ? t("form.saveChanges") : t("form.create")}</button>
      </div>
    </form>
  );
}
