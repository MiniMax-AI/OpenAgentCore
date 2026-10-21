import { useState } from "react";
import { ResourceEditor } from "./Resources";
import { ProfileEditor, emptyProfile, profileSummary } from "./Profiles";
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
  useMCPs,
  useProfiles,
  useRuntimes,
  type AgentProfile,
} from "./lib/product";
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
export type Configuration = Pick<
  AgentProfile,
  | "profile_id"
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
  profile_id: "",
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
  errors?: { profile_id?: string; runtime_id?: string };
}) {
  const capabilities = useCapabilities(),
    runtimes = useRuntimes(),
    models = useModels(),
    profiles = useProfiles(),
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
        <ResourceField
          label={tr("执行配置")}
          required
          error={errors.profile_id}
          loading={profiles.isPending || models.isPending}
          options={(profiles.data || []).map((row) => ({
            id: row.id,
            name: row.name,
            description: profileSummary(row, models.data),
          }))}
          selected={value.profile_id ? [value.profile_id] : []}
          change={(ids) => change({ ...value, profile_id: ids[0] || "" })}
          empty={tr("选择执行配置")}
          create={() => add("profiles")}
          createLabel={tr("添加执行配置")}
          inspect={inspect("profiles")}
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
      {editor?.kind === "profiles" && (
        <ProfileEditor
          value={
            editor.creating
              ? { ...emptyProfile(), id: editor.id }
              : profiles.data!.find((row) => row.id === editor.id)!
          }
          close={close}
          onSaved={(profile_id) => change({ ...value, profile_id })}
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
          profiles.error ||
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
