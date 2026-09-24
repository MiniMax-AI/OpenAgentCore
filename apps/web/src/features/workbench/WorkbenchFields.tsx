import { Lock, Plus, X } from "lucide-react";
import { useEffect, useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

import type {
  AgentSession,
  CoreHarnessKind,
  EnvironmentTemplateResource,
  SandboxDirectoryNode,
  SavedAgent,
} from "@agents-core-web/agents-client";

import { HelpTip } from "../../components/console-ui";
import { shortId } from "../../lib/format";
import type { AgentToolDraft } from "../agents/agent-form";
import { harnessLabel } from "../agents/AgentForm";
import { useSandboxClient } from "../sandbox/SandboxContext";
import type { SessionEnvironmentType } from "../sessions/create/session-environment";
import { deriveSessionVaultPlan, type VaultCatalog } from "../vaults/vault-catalog";
import {
  LOOKUP_KINDS,
  lookupParent,
  WORKBENCH_METADATA,
  type LookupKind,
  type MetadataRow,
  type WorkbenchForm,
} from "./workbench-requests";

/** Metadata pairs Core accepts on one object, including the workbench tag. */
const METADATA_LIMIT = 16;

type Patch<T> = (patch: Partial<T>) => void;

/** A titled group of rows inside the form frame. */
function FormSection({ title, help, actions, children }: { title: string; help?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  const id = useId();
  return (
    <section className="wb-section" aria-labelledby={id}>
      <header className="wb-section-head">
        <h3 id={id}>{title}</h3>
        {help ? <HelpTip>{help}</HelpTip> : null}
        {actions ? <div className="wb-section-actions">{actions}</div> : null}
      </header>
      <div className="wb-rows">{children}</div>
    </section>
  );
}

/** One labelled control. Labels share one column so every control starts on the same edge. */
function Row({ label, help, htmlFor, children }: { label: string; help?: ReactNode; htmlFor?: string; children: ReactNode }) {
  return (
    <div className="wb-row">
      <div className="wb-label">
        {htmlFor ? <label htmlFor={htmlFor}>{label}</label> : <span>{label}</span>}
        {help ? <HelpTip>{help}</HelpTip> : null}
      </div>
      <div className="wb-control">{children}</div>
    </div>
  );
}

/** Fixed request values the form shows but does not let the operator change. */
function StaticRow({ label, children }: { label: string; children: ReactNode }) {
  return <Row label={label}><span className="wb-readonly">{children}</span></Row>;
}

function AddAction({ label, name, onClick, disabled }: { label: string; name: string; onClick: () => void; disabled?: boolean }) {
  return (
    <button className="wb-add" type="button" aria-label={label} onClick={onClick} disabled={disabled}>
      <Plus size={13} strokeWidth={1.8} aria-hidden="true" />{name}
    </button>
  );
}

/** Key/value rows; the key column lines up with the label column. */
function MetadataFields({ rows, onChange, fixedCount }: { rows: MetadataRow[]; onChange: (rows: MetadataRow[]) => void; fixedCount: number }) {
  const { t } = useTranslation("workbench");
  const full = rows.length + fixedCount >= METADATA_LIMIT;
  return (
    <FormSection
      title={t("sections.metadata")}
      help={t("sectionHelp.metadata")}
      actions={<AddAction label={t("metadata.addLabel")} name={t("metadata.add")} disabled={full} onClick={() => onChange([...rows, { key: "", value: "" }])} />}
    >
      {Object.entries(WORKBENCH_METADATA).map(([key, value]) => (
        <div className="wb-kv" key={key}>
          <input className="wb-mono" value={key} readOnly aria-label={t("metadata.key")} />
          <div className="wb-affix">
            <input value={value} readOnly aria-label={t("metadata.value")} />
            <span className="wb-affix-icon" title={t("metadata.fixed")}><Lock size={13} strokeWidth={1.7} aria-label={t("metadata.fixed")} /></span>
          </div>
        </div>
      ))}
      {rows.map((row, index) => (
        <div className="wb-kv" key={index}>
          <input
            className="wb-mono"
            value={row.key}
            placeholder={t("metadata.key")}
            aria-label={t("metadata.keyLabel", { number: index + 1 })}
            maxLength={64}
            spellCheck={false}
            onChange={(event) => onChange(rows.map((candidate, position) => position === index ? { ...candidate, key: event.target.value } : candidate))}
          />
          <div className="wb-affix">
            <input
              value={row.value}
              placeholder={t("metadata.value")}
              aria-label={t("metadata.valueLabel", { number: index + 1 })}
              maxLength={512}
              onChange={(event) => onChange(rows.map((candidate, position) => position === index ? { ...candidate, value: event.target.value } : candidate))}
            />
            <button
              className="wb-affix-icon wb-remove"
              type="button"
              aria-label={t("metadata.remove", { number: index + 1 })}
              title={t("metadata.remove", { number: index + 1 })}
              onClick={() => onChange(rows.filter((_, position) => position !== index))}
            >
              <X size={14} strokeWidth={1.7} aria-hidden="true" />
            </button>
          </div>
        </div>
      ))}
    </FormSection>
  );
}

function ToolFields({ tools, onChange, vaultCatalog }: { tools: AgentToolDraft[]; onChange: (tools: AgentToolDraft[]) => void; vaultCatalog: VaultCatalog | null }) {
  const { t } = useTranslation("workbench");
  const id = useId();
  const update = (index: number, tool: AgentToolDraft) => onChange(tools.map((candidate, position) => position === index ? tool : candidate));
  const credentialVaults = vaultCatalog?.vaults.filter((vault) => vaultCatalog.credentials.some((credential) => credential.vault_id === vault.id)) ?? [];
  return (
    <FormSection
      title={t("sections.tools")}
      help={t("sectionHelp.tools")}
      actions={<>
        <AddAction label={t("tools.addFunction")} name={t("tools.function")} onClick={() => onChange([...tools, { kind: "function", name: "", description: "", parameters: "{\n  \"type\": \"object\",\n  \"properties\": {}\n}" }])} />
        <AddAction label={t("tools.addMcp")} name={t("tools.mcp")} onClick={() => onChange([...tools, { kind: "mcp", serverLabel: "", serverUrl: "", allowedToolsMode: "all", allowedTools: "", allowedToolsValue: null, required: false, credentialId: null }])} />
      </>}
    >
      {tools.length === 0 ? <p className="wb-empty">{t("tools.none")}</p> : null}
      {tools.map((tool, index) => {
        if (tool.kind === "read-only") return null;
        const name = tool.kind === "function" ? t("tools.function") : t("tools.mcp");
        const field = (suffix: string) => `${id}-${index}-${suffix}`;
        return (
          <div className="wb-tool" key={index}>
            <div className="wb-tool-head">
              <span>{name} <span className="wb-tool-index">{index + 1}</span></span>
              <button
                className="icon-button wb-remove"
                type="button"
                aria-label={t("tools.remove", { name, number: index + 1 })}
                title={t("tools.remove", { name, number: index + 1 })}
                onClick={() => onChange(tools.filter((_, position) => position !== index))}
              >
                <X size={14} strokeWidth={1.7} aria-hidden="true" />
              </button>
            </div>
            {tool.kind === "function" ? <>
              <Row label={t("fields.functionName")} htmlFor={field("name")}>
                <input id={field("name")} className="wb-mono" value={tool.name} placeholder="lookup_customer" spellCheck={false} onChange={(event) => update(index, { ...tool, name: event.target.value })} />
              </Row>
              <Row label={t("fields.description")} htmlFor={field("description")}>
                <input id={field("description")} value={tool.description} onChange={(event) => update(index, { ...tool, description: event.target.value })} />
              </Row>
              <Row label={t("fields.parameters")} htmlFor={field("parameters")}>
                <textarea id={field("parameters")} className="wb-mono" rows={5} value={tool.parameters} spellCheck={false} onChange={(event) => update(index, { ...tool, parameters: event.target.value })} />
              </Row>
            </> : <>
              <Row label={t("fields.serverLabel")} htmlFor={field("label")}>
                <input id={field("label")} className="wb-mono" value={tool.serverLabel} placeholder="docs" spellCheck={false} onChange={(event) => update(index, { ...tool, serverLabel: event.target.value })} />
              </Row>
              {vaultCatalog && credentialVaults.length ? (
                <Row label={t("fields.credential")} htmlFor={field("credential")}>
                  <select
                    id={field("credential")}
                    value={tool.credentialId ?? ""}
                    onChange={(event) => {
                      const credentialId = event.target.value || null;
                      const credential = credentialId ? vaultCatalog.credentials.find((candidate) => candidate.id === credentialId) : null;
                      update(index, { ...tool, credentialId, serverUrl: credential?.auth.mcp_server_url ?? tool.serverUrl });
                    }}
                  >
                    <option value="">{t("values.anonymous")}</option>
                    {credentialVaults.map((vault) => (
                      <optgroup key={vault.id} label={vault.name ?? t("values.unnamedVault")}>
                        {vaultCatalog.credentials.filter((credential) => credential.vault_id === vault.id).map((credential) => (
                          <option key={credential.id} value={credential.id}>{credential.name}</option>
                        ))}
                      </optgroup>
                    ))}
                  </select>
                </Row>
              ) : null}
              <Row label={t("fields.serverUrl")} htmlFor={field("url")}>
                <input id={field("url")} className="wb-mono" value={tool.serverUrl} placeholder="https://mcp.example/tools" inputMode="url" spellCheck={false} readOnly={Boolean(tool.credentialId)} onChange={(event) => update(index, { ...tool, serverUrl: event.target.value })} />
              </Row>
              <Row label={t("fields.allowedTools")} htmlFor={field("allowed")}>
                <select id={field("allowed")} value={tool.allowedToolsMode} onChange={(event) => update(index, event.target.value === "all" ? { ...tool, allowedToolsMode: "all", allowedToolsValue: null } : { ...tool, allowedToolsMode: "list" })}>
                  <option value="all">{t("values.allTools")}</option>
                  <option value="list">{t("values.listedTools")}</option>
                </select>
                {tool.allowedToolsMode === "list" ? (
                  <textarea className="wb-mono" rows={3} value={tool.allowedTools} placeholder={t("values.allowedToolsPlaceholder")} aria-label={t("fields.allowedTools")} spellCheck={false} onChange={(event) => update(index, { ...tool, allowedTools: event.target.value })} />
                ) : null}
              </Row>
              <Row label={t("fields.required")}>
                <label className="wb-check">
                  <input type="checkbox" checked={tool.required === true} onChange={(event) => update(index, { ...tool, required: event.target.checked })} />
                  <span>{t("values.requiredServer")}</span>
                </label>
              </Row>
            </>}
          </div>
        );
      })}
    </FormSection>
  );
}

export function AgentFields({
  value,
  onChange,
  harnesses,
  defaultHarness,
  models,
  vaultCatalog,
}: {
  value: WorkbenchForm["agent"];
  onChange: Patch<WorkbenchForm["agent"]>;
  harnesses: readonly CoreHarnessKind[];
  defaultHarness?: CoreHarnessKind;
  models: readonly string[];
  vaultCatalog: VaultCatalog | null;
}) {
  const { t } = useTranslation("workbench");
  const id = useId();
  return <>
    <FormSection title={t("sections.basics")}>
      <Row label={t("fields.name")} htmlFor={`${id}-name`}>
        <input id={`${id}-name`} value={value.name} maxLength={128} onChange={(event) => onChange({ name: event.target.value })} />
      </Row>
      <Row label={t("fields.model")} help={t("fieldHelp.model")} htmlFor={`${id}-model`}>
        <input id={`${id}-model`} className="wb-mono" list={`${id}-models`} value={value.model} spellCheck={false} autoComplete="off" onChange={(event) => onChange({ model: event.target.value })} />
        <datalist id={`${id}-models`}>{models.map((model) => <option key={model} value={model} />)}</datalist>
      </Row>
      <Row label={t("fields.harness")} help={t("fieldHelp.harness")} htmlFor={`${id}-harness`}>
        <select id={`${id}-harness`} value={value.harness} disabled={harnesses.length === 0} onChange={(event) => onChange({ harness: event.target.value as CoreHarnessKind | "" })}>
          <option value="">{defaultHarness ? t("values.coreDefaultNamed", { name: harnessLabel(defaultHarness) }) : t("values.coreDefault")}</option>
          {harnesses.map((harness) => <option key={harness} value={harness}>{harnessLabel(harness)}</option>)}
        </select>
      </Row>
      <Row label={t("fields.instructions")} htmlFor={`${id}-instructions`}>
        <textarea id={`${id}-instructions`} rows={4} value={value.instructions} onChange={(event) => onChange({ instructions: event.target.value })} />
      </Row>
    </FormSection>
    <ToolFields tools={value.tools} vaultCatalog={vaultCatalog} onChange={(tools) => onChange({ tools })} />
    <FormSection title={t("sections.generation")} help={t("sectionHelp.generation")}>
      <StaticRow label={t("fields.textFormat")}><code>text</code></StaticRow>
      <StaticRow label={t("fields.verbosity")}><code>medium</code></StaticRow>
      <StaticRow label={t("fields.serviceTier")}><code>auto</code></StaticRow>
      <StaticRow label={t("fields.reasoning")}>{t("values.omitted")}</StaticRow>
    </FormSection>
    <MetadataFields rows={value.metadata} fixedCount={1} onChange={(metadata) => onChange({ metadata })} />
  </>;
}

const ENVIRONMENTS: readonly SessionEnvironmentType[] = ["none", "self_hosted", "openai_hosted"];

function useSandboxNodes(active: boolean): SandboxDirectoryNode[] {
  const client = useSandboxClient();
  const [nodes, setNodes] = useState<SandboxDirectoryNode[]>([]);
  useEffect(() => {
    if (!active || !client) return;
    const controller = new AbortController();
    void client.listSandboxNodes({ signal: controller.signal })
      .then((result) => { if (!controller.signal.aborted) setNodes(result.data); })
      // The directory is optional: automatic placement stays available without it.
      .catch(() => { if (!controller.signal.aborted) setNodes([]); });
    return () => controller.abort();
  }, [active, client]);
  return nodes;
}

export function SessionFields({
  value,
  onChange,
  agents,
  vaultCatalog,
  templates,
  environments,
}: {
  value: WorkbenchForm["session"];
  onChange: Patch<WorkbenchForm["session"]>;
  agents: readonly SavedAgent[];
  vaultCatalog: VaultCatalog | null;
  /** null when this console cannot read Environment templates. */
  templates: readonly EnvironmentTemplateResource[] | null;
  environments: { self_hosted: boolean; openai_hosted: boolean };
}) {
  const { t } = useTranslation("workbench");
  const id = useId();
  const managed = value.environment === "openai_hosted";
  const nodes = useSandboxNodes(managed);
  const agent = agents.find((candidate) => candidate.id === value.agentId);
  const plan = agent ? deriveSessionVaultPlan(agent, vaultCatalog, value.vaultIds) : null;
  const required = new Set(plan?.requiredVaultIds ?? []);
  const toggleVault = (vaultId: string, checked: boolean) => onChange({
    vaultIds: checked ? [...new Set([...value.vaultIds, vaultId])].sort() : value.vaultIds.filter((candidate) => candidate !== vaultId),
  });
  return <>
    <FormSection title={t("sections.basics")}>
      <Row label={t("fields.agent")} htmlFor={`${id}-agent`}>
        <select id={`${id}-agent`} value={value.agentId} onChange={(event) => onChange({ agentId: event.target.value })}>
          <option value="">{agents.length ? t("values.chooseAgent") : t("values.noAgents")}</option>
          {agents.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.name ? `${candidate.name} · ${candidate.model}` : candidate.id}</option>)}
        </select>
      </Row>
      <Row label={t("fields.title")} help={t("fieldHelp.title")} htmlFor={`${id}-title`}>
        <input id={`${id}-title`} value={value.title} maxLength={512} placeholder={agent?.name ?? t("values.untitledSession")} onChange={(event) => onChange({ title: event.target.value })} />
      </Row>
      <Row label={t("fields.input")} help={t("fieldHelp.input")} htmlFor={`${id}-input`}>
        <textarea id={`${id}-input`} rows={3} value={value.input} onChange={(event) => onChange({ input: event.target.value })} />
      </Row>
    </FormSection>
    <FormSection title={t("sections.environment")} help={t("sectionHelp.environment")}>
      <Row label={t("fields.environment")} htmlFor={`${id}-environment`}>
        <select id={`${id}-environment`} value={value.environment} onChange={(event) => onChange({ environment: event.target.value as SessionEnvironmentType })}>
          {ENVIRONMENTS.filter((type) => type === "none" || environments[type] || type === value.environment).map((type) => (
            <option key={type} value={type}>{t(`values.environments.${type}`)}</option>
          ))}
        </select>
      </Row>
      {value.environment === "self_hosted" ? (
        <Row label={t("fields.workspace")} htmlFor={`${id}-workspace`}>
          <input id={`${id}-workspace`} className="wb-mono" value={value.workspaceDirectory} placeholder="/workspace" spellCheck={false} onChange={(event) => onChange({ workspaceDirectory: event.target.value })} />
        </Row>
      ) : null}
      {managed ? <>
        <Row label={t("fields.template")} htmlFor={`${id}-template`}>
          <select id={`${id}-template`} value={value.templateId} disabled={templates === null} onChange={(event) => onChange({ templateId: event.target.value })}>
            <option value="">{templates === null ? t("values.templatesUnavailable") : t("values.noTemplate")}</option>
            {(templates ?? []).map((template) => (
              <option key={template.id} value={template.id}>{template.name?.trim() || t("values.unnamedTemplate")}</option>
            ))}
          </select>
        </Row>
        <Row label={t("fields.network")} htmlFor={`${id}-network`}>
          <select id={`${id}-network`} value={value.network} onChange={(event) => onChange({ network: event.target.value as WorkbenchForm["session"]["network"] })}>
            {(["default", "enabled", "disabled"] as const).map((network) => <option key={network} value={network}>{t(`values.networks.${network}`)}</option>)}
          </select>
        </Row>
        <Row label={t("fields.node")} help={t("fieldHelp.node")} htmlFor={`${id}-node`}>
          <select id={`${id}-node`} value={value.sandboxNodeId} onChange={(event) => onChange({ sandboxNodeId: event.target.value })}>
            <option value="">{t("values.automaticPlacement")}</option>
            {value.sandboxNodeId && !nodes.some((node) => node.id === value.sandboxNodeId) ? <option value={value.sandboxNodeId}>{value.sandboxNodeId}</option> : null}
            {nodes.map((node) => (
              <option key={node.id} value={node.id} disabled={!node.available}>{node.available ? node.name : t("values.nodeUnavailable", { name: node.name })}</option>
            ))}
          </select>
        </Row>
      </> : null}
    </FormSection>
    <FormSection title={t("sections.vaults")} help={t("sectionHelp.vaults")}>
      <Row label={t("fields.vaults")}>
        {!vaultCatalog ? <span className="wb-static wb-muted">{t("values.vaultsUnavailable")}</span>
          : vaultCatalog.vaults.length === 0 ? <span className="wb-static wb-muted">{t("values.noVaults")}</span>
            : (
              <div className="wb-checks">
                {vaultCatalog.vaults.map((vault) => {
                  const forced = required.has(vault.id);
                  return (
                    <label className="wb-check" key={vault.id} title={forced ? t("values.requiredVault") : undefined}>
                      <input type="checkbox" checked={forced || value.vaultIds.includes(vault.id)} disabled={forced} onChange={(event) => toggleVault(vault.id, event.target.checked)} />
                      <span>{vault.name ?? t("values.unnamedVault")}</span>
                      <code>{vault.id}</code>
                    </label>
                  );
                })}
              </div>
            )}
      </Row>
    </FormSection>
    <MetadataFields rows={value.metadata} fixedCount={value.title.trim() ? 2 : 1} onChange={(metadata) => onChange({ metadata })} />
  </>;
}

/** Session IDs never contain a colon, so this cannot collide with a listed Session. */
const CUSTOM_SESSION = ":custom";

export function MessageFields({ value, onChange, sessions }: { value: WorkbenchForm["message"]; onChange: Patch<WorkbenchForm["message"]>; sessions: readonly AgentSession[] }) {
  const { t } = useTranslation("workbench");
  const id = useId();
  const listed = sessions.some((session) => session.id === value.sessionId);
  // Typing an ID is a mode of its own, so a partial ID never selects a listed Session.
  const [custom, setCustom] = useState(false);
  const choice = custom ? CUSTOM_SESSION : value.sessionId;
  return (
    <FormSection title={t("sections.message")} help={t("sectionHelp.message")}>
      <Row label={t("fields.session")} help={t("fieldHelp.session")} htmlFor={`${id}-session`}>
        <select
          id={`${id}-session`}
          value={choice}
          onChange={(event) => {
            const next = event.target.value;
            setCustom(next === CUSTOM_SESSION);
            onChange({ sessionId: next === CUSTOM_SESSION ? "" : next });
          }}
        >
          <option value="">{sessions.length ? t("values.chooseSession") : t("values.noSessions")}</option>
          {value.sessionId && !listed && !custom ? <option value={value.sessionId}>{value.sessionId}</option> : null}
          {sessions.map((session) => (
            <option key={session.id} value={session.id}>
              {`${session.metadata.title || session.agent?.name || t("values.untitledSession")} · ${shortId(session.id)}`}
            </option>
          ))}
          <option value={CUSTOM_SESSION}>{t("values.otherSession")}</option>
        </select>
      </Row>
      {custom ? (
        <Row label={t("fields.sessionId")} htmlFor={`${id}-session-id`}>
          <input id={`${id}-session-id`} className="wb-mono" value={value.sessionId} spellCheck={false} autoComplete="off" autoFocus onChange={(event) => onChange({ sessionId: event.target.value })} />
        </Row>
      ) : null}
      <Row label={t("fields.text")} htmlFor={`${id}-text`}>
        <textarea id={`${id}-text`} rows={6} value={value.text} onChange={(event) => onChange({ text: event.target.value })} />
      </Row>
    </FormSection>
  );
}

export function LookupFields({ value, onChange }: { value: WorkbenchForm["lookup"]; onChange: Patch<WorkbenchForm["lookup"]> }) {
  const { t } = useTranslation("workbench");
  const id = useId();
  const parent = lookupParent(value.kind);
  return (
    <FormSection title={t("sections.resource")} help={t("sectionHelp.resource")}>
      <Row label={t("fields.resource")} htmlFor={`${id}-kind`}>
        <select id={`${id}-kind`} value={value.kind} onChange={(event) => onChange({ kind: event.target.value as LookupKind })}>
          {LOOKUP_KINDS.map((kind) => <option key={kind} value={kind}>{t(`objects.${kind}`)}</option>)}
        </select>
      </Row>
      {parent ? (
        <Row label={t(parent === "session" ? "fields.sessionId" : "fields.vaultId")} htmlFor={`${id}-parent`}>
          <input id={`${id}-parent`} className="wb-mono" value={value.parentId} spellCheck={false} autoComplete="off" onChange={(event) => onChange({ parentId: event.target.value })} />
        </Row>
      ) : null}
      <Row label={t("fields.id")} htmlFor={`${id}-id`}>
        <input id={`${id}-id`} className="wb-mono" value={value.id} spellCheck={false} autoComplete="off" onChange={(event) => onChange({ id: event.target.value })} />
      </Row>
    </FormSection>
  );
}
