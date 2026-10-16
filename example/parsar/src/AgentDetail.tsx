import { useSkills } from "./Skills";
import { useMCPs } from "./lib/product";
import { useCapabilities } from "./Capabilities";
import { AgentMCP } from "./AgentMCP";
import { tr } from "./lib/i18n";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowLeft,
  Plus,
  Settings2,
  MessageSquare,
  ChevronRight,
} from "lucide-react";
import {
  product,
  useSessions,
  useModels,
  useRuntimes,
  harnessNames,
  type AgentProfile,
} from "./lib/product";
import { dateTime } from "./lib/api";
import { AgentEditor, readAgentDraft } from "./Agents";
import { NewSession } from "./NewSession";
import { Button } from "./components/ui/button";
import { EmptyState } from "./components/ui/empty-state";
import { Property, PropertyList } from "./components/ui/property-list";
import { Audit } from "./Records";
import { ErrorNotice, Help, PageHeader } from "./components/shared";
export function AgentDetail({ id }: { id: string }) {
  const [tab, setTab] = useState("config");
  const skills = useSkills(),
    mcps = useMCPs(),
    capabilities = useCapabilities();
  const models = useModels(),
    runtimes = useRuntimes();
  const [editing, setEditing] = useState(() => !!readAgentDraft(id));
  const [creating, setCreating] = useState(false);
  const query = useQuery({
    queryKey: ["agent", id],
    queryFn: () => product<AgentProfile>(`agents/${id}`),
  });
  const sessions = useSessions();
  const rows = sessions.data?.filter((row) => row.agent_id === id) || [];
  return (
    <>
      <PageHeader title={query.data?.name || "Agent"}>
        <Button asChild variant="ghost" size="icon">
          <a href="#/agents" aria-label={tr("返回 Agents")}>
            <ArrowLeft />
          </a>
        </Button>
        <Help>
          {tr("每个会话独立保存工作区和历史。修改 Agent 配置只影响新会话。")}
        </Help>
        <Button
          variant="outline"
          disabled={!query.data}
          onClick={() => setEditing(true)}
        >
          <Settings2 />
          {tr("配置")}
        </Button>
        <Button disabled={!query.data} onClick={() => setCreating(true)}>
          <Plus />
          {tr("开始会话")}
        </Button>
      </PageHeader>
      <div
        className="flex h-10 shrink-0 items-center gap-1 border-b border-line px-4"
        role="tablist"
        aria-label={tr("Agent 详情标签")}
      >
        {[
          ["activity", tr("动态")],
          ["config", tr("配置")],
          ["audit", tr("审计")],
          ["exposure", tr("对外提供")],
        ].map(([key, label]) => (
          <Button
            key={key}
            role="tab"
            aria-selected={tab === key}
            variant="ghost"
            onClick={() => setTab(key!)}
          >
            {label}
          </Button>
        ))}
        <a
          className="ml-auto text-xs text-fg-muted"
          href={`#/agents/${id}?view=full`}
        >
          {tr("展开")}
        </a>
      </div>
      <ErrorNotice error={query.error || sessions.error} />
      {tab === "exposure" ? (
        <AgentMCP id={id} />
      ) : tab === "audit" ? (
        <Audit resourceId={id} />
      ) : tab === "config" && query.data ? (
        <div className="overflow-auto p-4">
          <PropertyList>
            <Property label={tr("状态")}>
              {query.data.enabled === false ? tr("已停用") : tr("已启用")}
            </Property>
            <Property label={tr("引擎")}>
              {harnessNames[query.data.harness]}
            </Property>
            <Property label={tr("模型")}>
              {models.data?.find((row) => row.id === query.data?.model_id)
                ?.name || "—"}
            </Property>
            <Property label={tr("默认环境模板")}>
              {runtimes.data?.find((row) => row.id === query.data?.runtime_id)
                ?.name || tr("开始会话时选择")}
            </Property>
            <Property label="Skills">
              {query.data.skill_ids
                .map(
                  (id) =>
                    `${skills.data?.find((row) => row.id === id)?.name || id} · ${query.data?.skill_versions?.[id] || tr("默认版本")}`,
                )
                .join(", ") || "—"}
            </Property>
            <Property label="MCP">
              {query.data.mcp_ids
                .map(
                  (id) => mcps.data?.find((row) => row.id === id)?.name || id,
                )
                .join(", ") || "—"}
            </Property>
            <Property label={tr("能力")}>
              {query.data.capability_ids
                ?.map(
                  (id) =>
                    `${capabilities.data?.find((row) => row.id === id)?.name || id} · ${query.data?.capability_versions?.[id] || tr("默认版本")}`,
                )
                .join(", ") || "—"}
            </Property>
            <Property label={tr("业务提问")}>
              {query.data.ask_user ? tr("已启用") : tr("已停用")}
            </Property>
          </PropertyList>
          <h2 className="mb-2 mt-6 text-sm font-medium">{tr("主指令")}</h2>
          <p className="whitespace-pre-wrap text-sm leading-relaxed">
            {query.data.instructions || tr("尚未设置指令")}
          </p>
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto">
          <h2 className="px-6 h-10 flex items-center border-b border-line text-sm font-medium">
            {tr("会话")}
          </h2>
          <div className="divide-y divide-line-muted">
            {rows.map((row) => (
              <a
                key={row.id}
                href={`#/sessions/${row.id}`}
                className="flex min-h-9 items-center gap-3 px-6 hover:bg-surface-subtle"
              >
                <MessageSquare className="h-3.5 w-3.5 shrink-0 text-fg-muted" />
                <span className="min-w-0 flex-1 truncate text-sm">
                  {row.name}
                </span>
                <span className="text-xs text-fg-muted">
                  {row.core_session_id
                    ? dateTime(row.created_at)
                    : tr(row.connection_wait ? "等待连接" : "待恢复")}
                </span>
                <ChevronRight className="h-4 w-4 shrink-0" />
              </a>
            ))}
          </div>
          {!sessions.isPending && !rows.length && (
            <EmptyState title={tr("开始第一个会话")} />
          )}
        </div>
      )}
      {editing && query.data && (
        <AgentEditor value={query.data} close={() => setEditing(false)} />
      )}
      {creating && query.data && (
        <NewSession agent={query.data} close={() => setCreating(false)} />
      )}
    </>
  );
}
