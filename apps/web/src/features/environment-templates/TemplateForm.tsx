import { useId, useState, type FormEvent } from "react";
import type { EnvironmentTemplate } from "@agents-core-web/agents-client";
import { templatePatch, type TemplateDraft } from "./template-editor";

export function TemplateForm({ template, busy, onSave, onCancel }: {
  template?: EnvironmentTemplate;
  busy: boolean;
  onSave: (draft: TemplateDraft) => void;
  onCancel: () => void;
}) {
  const id = useId();
  const [draft, setDraft] = useState<TemplateDraft>({ name: template?.name ?? "", access: template?.network.access ?? "enabled" });
  const tooLong = [...draft.name.trim()].length > 256;
  const networkEditable = !template || template.network.allowed_domains.length === 0;
  const unchanged = template && !tooLong && Object.keys(templatePatch(template, draft)).length === 0;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!busy && !tooLong && !unchanged) onSave(draft);
  };
  return (
    <form className="template-form" onSubmit={submit}>
      <label htmlFor={`${id}-name`}>Name <span aria-hidden="true">Optional</span></label>
      <input id={`${id}-name`} value={draft.name} disabled={busy} aria-invalid={tooLong || undefined} aria-describedby={tooLong ? `${id}-error` : undefined}
        onChange={(event) => setDraft({ ...draft, name: event.target.value })} autoComplete="off" placeholder="e.g. Restricted network" />
      {tooLong ? <p id={`${id}-error`} role="alert">A name accepts at most 256 characters.</p> : null}
      <label htmlFor={`${id}-network`}>Network access</label>
      <select id={`${id}-network`} value={draft.access} disabled={busy || !networkEditable}
        onChange={(event) => setDraft({ ...draft, access: event.target.value as TemplateDraft["access"] })}>
        <option value="enabled">Enabled</option><option value="disabled">Disabled</option>
      </select>
      {!networkEditable ? <p>This Web cannot edit this network policy without replacing its allowed domains.</p> : null}
      <p>Applies when creating a new Session with this Template. Existing Sessions keep their configuration.</p>
      <div className="template-form-actions">
        <button className="button outline" type="button" disabled={busy} onClick={onCancel}>Cancel</button>
        <button className="button primary" type="submit" disabled={busy || tooLong || Boolean(unchanged)}>{busy ? "Saving…" : template ? "Save changes" : "Create Template"}</button>
      </div>
    </form>
  );
}
