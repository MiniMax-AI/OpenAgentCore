import { useConnections } from "./Connections";
import { useState, type ReactNode } from "react";
import { ModelEditor } from "./ModelEditor";
import { ResourceEditor } from "./Resources";
import { SkillCreate, SkillDetail, useSkills } from "./Skills";
import { useQuery } from "@tanstack/react-query";
import { api, readHistory } from "./lib/api";
import { tr } from "./lib/i18n";
import {
  CapabilityEditor,
  useCapabilities,
  type Capability,
} from "./Capabilities";
import {
  useModels,
  useProviders,
  useMCPs,
  useRuntimes,
  type AgentProfile,
} from "./lib/product";
import { ResourceField } from "./components/ResourceField";
import { Select, SelectOption } from "./components/ui/select";
import { Textarea } from "./components/ui/textarea";
import { ErrorNotice, Field } from "./components/shared";
export type Configuration = Pick<
  AgentProfile,
  | "connection_requirement"
  | "model_id"
  | "harness"
  | "instructions"
  | "skill_ids"
  | "skill_versions"
  | "mcp_ids"
  | "runtime_id"
  | "enabled"
  | "ask_user"
  | "capability_ids"
  | "capability_versions"
>;
export const emptyConfiguration: Configuration = {
  model_id: "",
  harness: "claude_sdk",
  instructions: "",
  skill_ids: [],
  mcp_ids: [],
};
function Section({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <section className="space-y-4 rounded-lg border border-line bg-surface p-4">
      <div className="border-b border-line pb-3">
        <h3 className="text-sm font-semibold">{title}</h3>
        <p className="mt-1 text-xs leading-relaxed text-fg-muted">
          {description}
        </p>
      </div>
      {children}
    </section>
  );
}
export function ConfigurationFields({
  value,
  change,
}: {
  value: Configuration;
  change: (value: Configuration) => void;
}) {
  const connections = useConnections(),
    capabilities = useCapabilities(),
    runtimes = useRuntimes(),
    models = useModels(),
    providers = useProviders(),
    skills = useSkills(),
    mcps = useMCPs();
  const [editor, setEditor] = useState<{
    kind: string;
    id: string;
    creating?: boolean;
  } | null>(null);
  const add = (kind: string) =>
    setEditor({ kind, id: crypto.randomUUID(), creating: true });
  const inspect = (kind: string) => (id: string) => setEditor({ kind, id });
  const close = () => setEditor(null);
  const capabilityField = (
    kind: Capability["kind"],
    label: string,
    description?: string,
  ) => (
    <ResourceField
      label={label}
      multiple
      limit={
        Math.max(0, 8 - (value.capability_ids?.length || 0)) +
        (value.capability_ids || []).filter((id) =>
          capabilities.data?.some((row) => row.id === id && row.kind === kind),
        ).length
      }
      description={description}
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
    <div className="grid items-start gap-4 lg:grid-cols-2">
      <Section
        title={tr("系统提示词")}
        description={tr("定义 Agent 的职责、工作方式和交付要求。")}
      >
        <Field label={tr("主指令")} id="config-instructions">
          <Textarea
            id="config-instructions"
            rows={8}
            className="min-h-40 leading-relaxed"
            placeholder={tr("描述它负责什么、如何工作，以及输出什么结果。")}
            value={value.instructions}
            onChange={(event) =>
              change({ ...value, instructions: event.target.value })
            }
          />
        </Field>
        {capabilityField(
          "prompt",
          tr("指令片段"),
          tr("可选。按选择顺序追加到主指令后，合并为一份系统提示词。"),
        )}
      </Section>
      <Section
        title={tr("模型与环境")}
        description={tr("选择执行任务的模型、引擎和默认工作环境。")}
      >
        <ResourceField
          label={tr("模型")}
          loading={models.isPending || providers.isPending}
          options={(models.data || []).map((row) => ({
            id: row.id,
            name: `${providers.data?.find((provider) => provider.id === row.provider_id)?.name || "Provider"} / ${row.name}`,
            description: row.model,
            disabled: row.enabled === false,
          }))}
          selected={value.model_id ? [value.model_id] : []}
          change={(ids) => change({ ...value, model_id: ids[0] || "" })}
          empty={tr("选择模型")}
          create={() => add("models")}
          createLabel={tr("添加模型")}
          inspect={inspect("models")}
        />
        <Field label={tr("执行引擎")} id="config-harness">
          <Select
            id="config-harness"
            value={value.harness}
            onValueChange={(harness) =>
              change({ ...value, harness: harness as Configuration["harness"] })
            }
          >
            <SelectOption value="claude_sdk">Claude Code</SelectOption>
            <SelectOption value="codex">Codex</SelectOption>
            <SelectOption value="mcode">MiniMax Code</SelectOption>
          </Select>
        </Field>
        <ResourceField
          label={tr("默认环境模板")}
          loading={runtimes.isPending}
          options={runtimes.data || []}
          selected={value.runtime_id ? [value.runtime_id] : []}
          change={(ids) => change({ ...value, runtime_id: ids[0] || "" })}
          empty={tr("开始会话时选择")}
          create={() => add("runtimes")}
          createLabel={tr("添加环境模板")}
          inspect={inspect("runtimes")}
        />
      </Section>
      <Section
        title={tr("工具与知识")}
        description={tr("从资源库选择能力；只展示已绑定的资源。")}
      >
        <ResourceField
          label="Skills"
          multiple
          limit={8}
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
          limit={8}
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
        <p className="text-xs text-fg-muted">
          {tr(
            "指令片段、Plugins 和知识合计最多 8 个；各选择器显示当前可用额度。",
          )}
        </p>
        <p className="text-xs text-fg-muted">
          {tr("编辑共享资源会影响使用它的其他 Agent。")}
        </p>
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
      <Section
        title={tr("账号与行为")}
        description={tr("按需绑定任务发起者的账号，并设置 Agent 的工作方式。")}
      >
        <ResourceField
          label={tr("所需用户连接")}
          manage={{ href: "#/connections", label: tr("管理连接") }}
          options={(connections.data || []).map((entry) => ({
            id: entry.bot_id,
            name: `${tr(entry.label || "账号连接")} · ${entry.name}`,
          }))}
          loading={connections.isPending}
          selected={
            value.connection_requirement
              ? [value.connection_requirement.bot_id]
              : []
          }
          empty={tr("不需要个人连接")}
          change={(ids) => {
            const entry = connections.data?.find(
              (row) => row.bot_id === ids[0],
            );
            change({
              ...value,
              connection_requirement: entry
                ? {
                    service: entry.service,
                    bot_id: entry.bot_id,
                    scopes:
                      value.connection_requirement?.bot_id === entry.bot_id
                        ? value.connection_requirement.scopes
                        : entry.default_scopes || "",
                  }
                : undefined,
            });
          }}
        />
        {value.connection_requirement && (
          <div className="space-y-2">
            <Field label={tr("授权范围")} id="agent-connection-scopes">
              <Textarea
                id="agent-connection-scopes"
                rows={2}
                value={value.connection_requirement.scopes}
                onChange={(event) =>
                  change({
                    ...value,
                    connection_requirement: {
                      ...value.connection_requirement!,
                      scopes: event.target.value,
                    },
                  })
                }
              />
            </Field>
            <p className="text-xs leading-relaxed text-fg-muted">
              {tr(
                "运行时使用任务发起者的账号；未授权时等待连接，授权完成后自动继续。",
              )}
            </p>
          </div>
        )}
        <div className="divide-y divide-line">
          <label className="flex items-center justify-between gap-4 py-3 text-sm">
            {tr("启用 Agent")}
            <input
              type="checkbox"
              className="accent-accent"
              checked={value.enabled !== false}
              onChange={(event) =>
                change({ ...value, enabled: event.target.checked })
              }
            />
          </label>
          <label className="flex items-center justify-between gap-4 py-3 text-sm">
            {tr("允许 Agent 提问业务信息（不用于执行审批）")}
            <input
              type="checkbox"
              className="accent-accent"
              checked={!!value.ask_user}
              onChange={(event) =>
                change({ ...value, ask_user: event.target.checked })
              }
            />
          </label>
        </div>
      </Section>
      {editor?.kind === "models" && (
        <ModelEditor
          value={
            editor.creating
              ? {
                  id: editor.id,
                  revision: 0,
                  name: "",
                  model: "",
                  provider_id: "",
                }
              : models.data!.find((row) => row.id === editor.id)!
          }
          close={close}
          onSaved={(model_id) => change({ ...value, model_id })}
        />
      )}
      {editor?.kind === "skills" &&
        (editor.creating ? (
          <SkillCreate
            close={close}
            onSaved={(id) =>
              change({ ...value, skill_ids: [...value.skill_ids, id] })
            }
          />
        ) : (
          <SkillDetail
            skill={skills.data!.find((row) => row.id === editor.id)!}
            close={close}
          />
        ))}
      {(editor?.kind === "mcps" || editor?.kind === "runtimes") && (
        <ResourceEditor
          kind={editor.kind}
          title={editor.kind === "mcps" ? "MCP" : tr("环境模板")}
          value={
            editor.creating
              ? editor.kind === "mcps"
                ? { id: editor.id, revision: 0, name: "", label: "", url: "" }
                : {
                    id: editor.id,
                    revision: 0,
                    name: "",
                    environment: "openai_hosted",
                  }
              : editor.kind === "mcps"
                ? mcps.data!.find((row) => row.id === editor.id)!
                : runtimes.data!.find((row) => row.id === editor.id)!
          }
          close={close}
          onSaved={(id) =>
            change(
              editor.kind === "mcps"
                ? { ...value, mcp_ids: [...new Set([...value.mcp_ids, id])] }
                : { ...value, runtime_id: id },
            )
          }
        />
      )}
      {editor && ["prompt", "knowledge", "plugin"].includes(editor.kind) && (
        <CapabilityEditor
          value={
            editor.creating
              ? {
                  id: editor.id,
                  name: "",
                  description: "",
                  kind: editor.kind as Capability["kind"],
                  revision: 0,
                  deprecated: false,
                  default_version: "",
                  versions: [],
                }
              : capabilities.data!.find((row) => row.id === editor.id)!
          }
          close={close}
          onSaved={(id) =>
            change({
              ...value,
              capability_ids: [
                ...new Set([...(value.capability_ids || []), id]),
              ],
            })
          }
        />
      )}
      <ErrorNotice
        error={
          connections.error ||
          providers.error ||
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
