import { useState } from "react";
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
import { ErrorNotice, Field, Section, Toggle } from "./components/shared";
import { randomUuid } from "@oac/agents-client";
export type Configuration = Pick<
  AgentProfile,
  | "model_id"
  | "harness"
  | "instructions"
  | "skill_ids"
  | "skill_versions"
  | "mcp_ids"
  | "runtime_id"
  | "enabled"
  | "ask_user"
  | "web_search"
  | "reasoning_effort"
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
export function ConfigurationFields({
  value,
  change,
  errors = {},
}: {
  value: Configuration;
  change: (value: Configuration) => void;
  errors?: { model_id?: string; runtime_id?: string };
}) {
  const capabilities = useCapabilities(),
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
    setEditor({ kind, id: randomUuid(), creating: true });
  const inspect = (kind: string) => (id: string) => setEditor({ kind, id });
  const close = () => setEditor(null);
  const capabilityField = (kind: Capability["kind"], label: string) => (
    <ResourceField
      label={label}
      multiple
      limit={
        Math.max(0, 8 - (value.capability_ids?.length || 0)) +
        (value.capability_ids || []).filter((id) =>
          capabilities.data?.some((row) => row.id === id && row.kind === kind),
        ).length
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
      <Section title={tr("模型与环境")}>
        <ResourceField
          label={tr("模型")}
          required
          error={errors.model_id}
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
              change({
                ...value,
                harness: harness as Configuration["harness"],
                web_search: harness === "mcode" ? false : value.web_search,
              })
            }
          >
            <SelectOption value="claude_sdk">Claude Code</SelectOption>
            <SelectOption value="codex">Codex</SelectOption>
            <SelectOption value="mcode">MiniMax Code</SelectOption>
          </Select>
        </Field>
        <Field label={tr("推理强度")} id="config-reasoning">
          <Select
            id="config-reasoning"
            value={value.reasoning_effort || ""}
            onValueChange={(reasoning_effort) =>
              change({
                ...value,
                reasoning_effort:
                  reasoning_effort as Configuration["reasoning_effort"],
              })
            }
          >
            <SelectOption value="">{tr("默认")}</SelectOption>
            <SelectOption value="low">{tr("低")}</SelectOption>
            <SelectOption value="medium">{tr("中")}</SelectOption>
            <SelectOption value="high">{tr("高")}</SelectOption>
          </Select>
        </Field>
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
      <Section title={tr("工具与知识")}>
        <Toggle
          label={tr("网页搜索")}
          checked={!!value.web_search}
          disabled={value.harness === "mcode"}
          hint={
            value.harness === "mcode"
              ? tr("MiniMax Code 暂不支持网页搜索")
              : undefined
          }
          onChange={(web_search) => change({ ...value, web_search })}
        />
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
