import { useState } from "react";
import { ResourceEditor } from "./Resources";
import { ExecutionFields } from "./ExecutionFields";
import { useSkills } from "./Skills";
import { CapabilityEditorFor, type EditorTarget } from "./Library";
import { SecretEditor, useAllVaultCredentials } from "./Credentials";
import { useQuery } from "@tanstack/react-query";
import { api, readHistory } from "./lib/api";
import { tr } from "./lib/i18n";
import { useCapabilities, type Capability } from "./Capabilities";
import {
  useModels,
  useMCPs,
  useRuntimes,
  useFeishuBots,
  useSecrets,
  useSkillCredentials,
  type AgentProfile,
} from "./lib/product";
import { botVariables } from "../shared/bot-bindings.mjs";
import { ResourceField } from "./components/ResourceField";
import {
  maxFiles,
  maxPlugins,
  maxSkills,
} from "../shared/environment-limits.mjs";
import { Select, SelectOption } from "./components/ui/select";
import { Textarea } from "./components/ui/textarea";
import { ErrorNotice, Field, Section, Toggle } from "./components/shared";
import { randomUuid } from "@oac/agents-client";
import { Plus } from "lucide-react";
import { Button } from "./components/ui/button";
export type Configuration = Pick<
  AgentProfile,
  | "harness"
  | "model_id"
  | "reasoning_effort"
  | "reasoning_summary"
  | "web_search"
  | "verbosity"
  | "service_tier"
  | "instructions"
  | "skill_ids"
  | "skill_versions"
  | "mcp_ids"
  | "runtime_id"
  | "enabled"
  | "ask_user"
  | "capability_ids"
  | "capability_versions"
  | "credentials"
  | "mcp_credentials"
>;
export const emptyConfiguration: Configuration = {
  harness: "codex",
  model_id: "",
  reasoning_effort: "",
  reasoning_summary: "",
  web_search: "disabled",
  verbosity: "",
  service_tier: "",
  instructions: "",
  skill_ids: [],
  mcp_ids: [],
};
export function ConfigurationFields({
  agentId,
  value,
  change,
  errors = {},
}: {
  agentId: string;
  value: Configuration;
  change: (value: Configuration) => void;
  errors?: { model_id?: string; runtime_id?: string };
}) {
  const capabilities = useCapabilities(),
    runtimes = useRuntimes(),
    models = useModels(),
    skills = useSkills(),
    mcps = useMCPs();
  const [editor, setEditor] = useState<{
    kind: string;
    id: string;
    creating?: boolean;
  } | null>(null);
  const add = (kind: string) =>
    setEditor({ kind, id: randomUuid(), creating: true });
  const inspect = (kind: string) => (id: string) => setEditor({ kind, id });
  const close = () => setEditor(null);
  const capabilityField = (kind: Capability["kind"], label: string) => (
    <ResourceField
      label={label}
      multiple
      limit={
        kind === "plugin"
          ? maxPlugins
          : kind === "knowledge"
            ? Math.max(
                0,
                maxFiles -
                  (runtimes.data?.find((row) => row.id === value.runtime_id)
                    ?.files?.length || 0),
              )
            : undefined
      }
      loading={capabilities.isPending}
      options={(capabilities.data || [])
        .filter((row) => row.kind === kind)
        .map((row) => ({ ...row, disabled: row.deprecated }))}
      selected={(value.capability_ids || []).filter((id) =>
        capabilities.data?.some((row) => row.id === id && row.kind === kind),
      )}
      change={(ids) =>
        change({
          ...value,
          capability_ids: [
            ...(value.capability_ids || []).filter(
              (id) =>
                !capabilities.data?.some(
                  (row) => row.id === id && row.kind === kind,
                ),
            ),
            ...ids,
          ],
        })
      }
      create={() => add(kind)}
      createLabel={tr("新建{{name}}", { name: label })}
      inspect={inspect(kind)}
      detail={(id) => {
        const row = capabilities.data?.find((row) => row.id === id);
        return (
          <div className="w-28">
            <Select
              aria-label={tr("{{value0}} 版本", { value0: row?.name || id })}
              value={value.capability_versions?.[id] || ""}
              onValueChange={(version) =>
                change({
                  ...value,
                  capability_versions: {
                    ...value.capability_versions,
                    [id]: version,
                  },
                })
              }
            >
              <SelectOption value="">{tr("默认版本")}</SelectOption>
              {row?.versions.map((version) => (
                <SelectOption key={version.version} value={version.version}>
                  v{version.version}
                </SelectOption>
              ))}
            </Select>
          </div>
        );
      }}
    />
  );
  return (
    <div className="space-y-6">
      <Section title={tr("执行与环境")}>
        <ExecutionFields
          value={value}
          change={change}
          error={errors.model_id}
        />
        <ResourceField
          label={tr("环境模板")}
          required
          error={errors.runtime_id}
          loading={runtimes.isPending}
          options={runtimes.data || []}
          selected={value.runtime_id ? [value.runtime_id] : []}
          change={(ids) => change({ ...value, runtime_id: ids[0] || "" })}
          empty={tr("选择环境模板")}
          create={() => add("runtimes")}
          createLabel={tr("添加环境模板")}
          inspect={inspect("runtimes")}
        />
      </Section>
      <Section title={tr("指令")}>
        <Field label={tr("主指令")} id="config-instructions">
          <Textarea
            id="config-instructions"
            rows={10}
            className="min-h-52 leading-relaxed"
            placeholder={tr("描述它负责什么、如何工作，以及输出什么结果。")}
            value={value.instructions}
            onChange={(event) =>
              change({ ...value, instructions: event.target.value })
            }
          />
        </Field>
        {capabilityField("prompt", tr("指令片段"))}
      </Section>
      <Section title={tr("能力")}>
        <ResourceField
          label="Skills"
          multiple
          limit={maxSkills}
          loading={skills.isPending}
          options={skills.data || []}
          selected={value.skill_ids}
          change={(ids) => change({ ...value, skill_ids: ids })}
          create={() => add("skills")}
          createLabel={tr("添加Skills")}
          inspect={inspect("skills")}
          detail={(id) => (
            <SkillVersionSelect
              id={id}
              name={skills.data?.find((row) => row.id === id)?.name || id}
              value={value.skill_versions?.[id] || ""}
              change={(version) =>
                change({
                  ...value,
                  skill_versions: { ...value.skill_versions, [id]: version },
                })
              }
            />
          )}
        />
        <ResourceField
          label="MCP"
          multiple
          loading={mcps.isPending}
          options={(mcps.data || []).map((row) => ({
            ...row,
            description: row.url,
          }))}
          selected={value.mcp_ids}
          change={(ids) => change({ ...value, mcp_ids: ids })}
          create={() => add("mcps")}
          createLabel={tr("添加MCP")}
          inspect={inspect("mcps")}
        />
        {capabilityField("plugin", "Plugins")}
        {capabilityField("knowledge", tr("知识"))}
        {(value.capability_ids || [])
          .filter(
            (id) =>
              capabilities.data &&
              !capabilities.data.some((row) => row.id === id),
          )
          .map((id) => (
            <ResourceField
              key={id}
              label={tr("不可用资源")}
              options={[]}
              selected={[id]}
              change={() =>
                change({
                  ...value,
                  capability_ids: value.capability_ids?.filter(
                    (key) => key !== id,
                  ),
                })
              }
            />
          ))}
      </Section>
      <CredentialFields agentId={agentId} value={value} change={change} />
      <Section title={tr("行为")}>
        <div className="space-y-3">
          <Toggle
            label={tr("启用 Agent")}
            checked={value.enabled !== false}
            onChange={(enabled) => change({ ...value, enabled })}
          />
          <Toggle
            label={tr("允许 Agent 向用户提问")}
            checked={!!value.ask_user}
            onChange={(ask_user) => change({ ...value, ask_user })}
          />
        </div>
      </Section>
      {editor?.kind === "runtimes" && (
        <ResourceEditor
          kind="runtimes"
          title={tr("环境模板")}
          value={
            editor.creating
              ? { id: editor.id, revision: 0, name: "", environment: "openai_hosted" }
              : runtimes.data!.find((row) => row.id === editor.id)!
          }
          close={close}
          onSaved={(id) => change({ ...value, runtime_id: id })}
        />
      )}
      {editor && editor.kind !== "runtimes" && (
        <CapabilityEditorFor
          target={editor as EditorTarget}
          close={close}
          onSaved={(id) =>
            change(
              editor.kind === "skills"
                ? { ...value, skill_ids: [...new Set([...value.skill_ids, id])] }
                : editor.kind === "mcps"
                  ? { ...value, mcp_ids: [...new Set([...value.mcp_ids, id])] }
                  : { ...value, capability_ids: [...new Set([...(value.capability_ids || []), id])] },
            )
          }
        />
      )}
      <ErrorNotice
        error={
          models.error ||
          skills.error ||
          mcps.error ||
          capabilities.error ||
          runtimes.error
        }
      />
    </div>
  );
}
function SkillVersionSelect({
  id,
  name,
  value,
  change,
}: {
  id: string;
  name: string;
  value: string;
  change: (version: string) => void;
}) {
  const versions = useQuery({
    queryKey: ["skill-versions", id],
    queryFn: ({ signal }) =>
      readHistory((options) => api.listSkillVersions(id, options), signal),
  });
  return (
    <div className="w-28">
      <Select
        aria-label={`${name} ${tr("版本")}`}
        value={value}
        onValueChange={change}
      >
        <SelectOption value="">{tr("默认版本")}</SelectOption>
        {versions.data?.map((row) => (
          <SelectOption key={row.id} value={row.version}>
            v{row.version}
          </SelectOption>
        ))}
      </Select>
      <ErrorNotice error={versions.error} />
    </div>
  );
}

// Where each credential the bound capabilities need comes from: a secret for
// each variable, and a Vault credential for each MCP that needs one.
function CredentialFields({
  agentId,
  value,
  change,
}: {
  agentId: string;
  value: Configuration;
  change: (value: Configuration) => void;
}) {
  const capabilities = useCapabilities(),
    skillCredentials = useSkillCredentials(),
    mcps = useMCPs(),
    runtimes = useRuntimes(),
    bots = useFeishuBots(),
    secrets = useSecrets(),
    vaultCredentials = useAllVaultCredentials();
  const [creating, setCreating] = useState<string | null>(null);
  const needed = [
    ...new Set([
      ...value.skill_ids.flatMap((id) => skillCredentials.data?.find((row) => row.id === id)?.credentials || []),
      ...(capabilities.data || [])
        .filter((row) => value.capability_ids?.includes(row.id))
        .flatMap((row) => row.credentials || []),
      ...(runtimes.data?.find((row) => row.id === value.runtime_id)?.credentials || []),
    ]),
  ];
  // The bot's identity comes from the bot bound to this Agent; the rest from chosen secrets.
  const needsBot = needed.some((name) => name in botVariables);
  const names = needed.filter((name) => !(name in botVariables));
  const bot = bots.data?.find((row) => row.agent_id === agentId);
  const authMcps = (mcps.data || []).filter((row) => value.mcp_ids.includes(row.id) && row.requires_credential);
  if (!needed.length && !authMcps.length) return null;
  const choose = (name: string, secretId: string) =>
    change({ ...value, credentials: { ...value.credentials, [name]: secretId } });
  return (
    <Section title={tr("凭据")}>
      {needsBot && (
        <Field label={tr("飞书机器人身份")} id="credential-bot">
          {bot ? (
            <p id="credential-bot" className="text-sm text-fg">
              {tr("由绑定的飞书机器人「{{value0}}」提供", { value0: bot.name })}
            </p>
          ) : (
            <p id="credential-bot" className="text-sm text-danger">
              {tr("请先在飞书接入中给这个 Agent 绑定机器人")}
            </p>
          )}
        </Field>
      )}
      {names.map((name) => (
        <Field key={name} label={name} id={`credential-${name}`}>
          <div className="flex gap-2">
            <Select
              id={`credential-${name}`}
              aria-label={tr("{{value0}} 使用的密钥", { value0: name })}
              value={value.credentials?.[name] || ""}
              onValueChange={(secretId) => choose(name, secretId)}
            >
              <SelectOption value="">{tr("选择密钥")}</SelectOption>
              {secrets.data?.map((secret) => (
                <SelectOption key={secret.id} value={secret.id}>
                  {secret.name}
                </SelectOption>
              ))}
            </Select>
            <Button
              variant="outline"
              aria-label={tr("新建 {{value0}} 的密钥", { value0: name })}
              onClick={() => setCreating(name)}
            >
              <Plus />
              {tr("新建")}
            </Button>
          </div>
        </Field>
      ))}
      {authMcps.map((mcp) => {
        const choice = value.mcp_credentials?.[mcp.id];
        const options = (vaultCredentials.data || []).filter(({ credential }) => credential.auth.mcp_server_url === mcp.url);
        return (
          <Field key={mcp.id} label={mcp.name} id={`mcp-credential-${mcp.id}`}>
            <Select
              id={`mcp-credential-${mcp.id}`}
              aria-label={tr("{{value0}} 使用的凭据", { value0: mcp.name })}
              value={choice ? `${choice.vault_id}/${choice.credential_id}` : ""}
              onValueChange={(selected) => {
                const [vault_id, credential_id] = selected.split("/");
                change({
                  ...value,
                  mcp_credentials: { ...value.mcp_credentials, [mcp.id]: { vault_id: vault_id!, credential_id: credential_id! } },
                });
              }}
            >
              <SelectOption value="">{options.length ? tr("选择凭据") : tr("先在凭据中为这个地址添加 MCP 凭据")}</SelectOption>
              {options.map(({ vault, credential }) => (
                <SelectOption key={credential.id} value={`${vault.id}/${credential.id}`}>
                  {`${vault.name || vault.id} / ${credential.name}`}
                </SelectOption>
              ))}
            </Select>
          </Field>
        );
      })}
      <ErrorNotice error={secrets.error || vaultCredentials.error || skillCredentials.error} />
      {creating && (
        <SecretEditor
          value={{ id: randomUuid(), name: creating, revision: 0 }}
          close={() => setCreating(null)}
          onSaved={(id) => choose(creating, id)}
        />
      )}
    </Section>
  );
}
