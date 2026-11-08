import { tr } from "./lib/i18n";
import { useState } from "react";
import { Blocks, Plug, Plus, Search, Send, Server } from "lucide-react";
import {
  useAgents,
  useFeishuBots,
  useMCPs,
  useModels,
  useRuntimes,
  type AgentProfile,
  type FeishuBot,
  type RuntimeProfile,
} from "./lib/product";
import { useCapabilities } from "./Capabilities";
import { useSkills } from "./Skills";
import { engineSummary } from "./ExecutionFields";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { ErrorNotice, PageHeader } from "./components/shared";
import { EmptyState } from "./components/ui/empty-state";
import { AgentAvatar } from "./components/AgentAvatar";
import { cn } from "./lib/utils";

export function Chip({
  icon: Icon,
  children,
  muted,
}: {
  icon?: typeof Server;
  children: React.ReactNode;
  muted?: boolean;
}) {
  return (
    <span
      className={cn(
        "inline-flex max-w-full items-center gap-1.5 rounded-full px-3 py-1 text-sm",
        muted ? "text-fg-subtle ring-1 ring-line" : "bg-surface-muted text-fg-muted",
      )}
    >
      {Icon && <Icon strokeWidth={1.7} className="h-3.5 w-3.5 shrink-0" />}
      <span className="truncate">{children}</span>
    </span>
  );
}

export function Agents() {
  const agents = useAgents(),
    models = useModels(),
    runtimes = useRuntimes(),
    bots = useFeishuBots(),
    skills = useSkills(),
    mcps = useMCPs(),
    capabilities = useCapabilities();
  const names = (rows: { id: string; name: string }[] = [], ids: string[] = []) =>
    ids.flatMap((id) => rows.find((row) => row.id === id)?.name || []);
  const [search, setSearch] = useState("");
  const rows = (agents.data || []).filter((agent) =>
    `${agent.name} ${agent.description || ""}`
      .toLowerCase()
      .includes(search.toLowerCase().trim()),
  );
  return (
    <>
      <PageHeader title={tr("Agent")}>
        {(agents.data?.length || 0) > 0 && (
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-fg-subtle" />
            <Input
              aria-label={tr("搜索 Agent")}
              placeholder={tr("搜索 Agent")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-56 pl-9"
            />
          </div>
        )}
        <Button asChild>
          <a href="#/agents/new">
            <Plus />
            {tr("新建 Agent")}
          </a>
        </Button>
      </PageHeader>
      <ErrorNotice page error={agents.error || models.error} />
      <div className="mx-auto w-full max-w-6xl px-6 pb-12 sm:px-10">
        {!agents.isPending && !agents.data?.length ? (
          <EmptyState
            title={tr("还没有 Agent")}
            description={tr("Agent 组合指令、模型、工具和环境模板，可以在网页和飞书中对话。")}
            action={
              <Button asChild size="lg">
                <a href="#/agents/new">
                  <Plus />
                  {tr("新建 Agent")}
                </a>
              </Button>
            }
          />
        ) : (
          <div className="grid gap-5 pt-1 sm:grid-cols-2 xl:grid-cols-3">
            {rows.map((agent) => (
              <AgentCard
                key={agent.id}
                agent={agent}
                engine={engineSummary(agent, models.data)}
                runtime={runtimes.data?.find((r) => r.id === agent.runtime_id)}
                bot={bots.data?.find((b) => b.agent_id === agent.id)}
                skills={names(skills.data, agent.skill_ids)}
                capabilities={[
                  ...(agent.web_search && agent.web_search !== "disabled" ? [tr("网页搜索")] : []),
                  ...(agent.ask_user ? [tr("向用户提问")] : []),
                  ...names(mcps.data, agent.mcp_ids),
                  ...names(
                    capabilities.data?.filter((row) => row.kind !== "prompt"),
                    agent.capability_ids,
                  ),
                ]}
              />
            ))}
          </div>
        )}
      </div>
    </>
  );
}

// One line of the card: which bot, template, Skills or capabilities the Agent is assembled from.
function Part({
  icon: Icon,
  label,
  children,
}: {
  icon: typeof Server;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-3">
      <span className="flex min-h-7 w-24 shrink-0 items-center gap-2 text-sm text-fg">
        <Icon strokeWidth={1.7} className="h-4 w-4 shrink-0 text-fg-subtle" />
        {label}
      </span>
      <div className="flex min-h-7 min-w-0 flex-1 flex-wrap items-center gap-1.5">{children}</div>
    </div>
  );
}

const shownSkills = 8;

function AgentCard({
  agent,
  engine,
  runtime,
  bot,
  skills,
  capabilities,
}: {
  agent: AgentProfile;
  engine: string;
  runtime?: RuntimeProfile;
  bot?: FeishuBot;
  skills: string[];
  capabilities: string[];
}) {
  const disabled = agent.enabled === false;
  const packages = [...(runtime?.packages?.npm || []), ...(runtime?.packages?.python || [])];
  return (
    <article className="group relative flex flex-col rounded-2xl border border-line bg-surface p-6 app-shadow-card transition-colors hover:border-line-strong">
      <div className="flex items-start gap-4">
        <AgentAvatar name={agent.name} size="lg" />
        <div className="min-w-0 flex-1 pt-0.5">
          <h2 className="truncate text-base font-semibold tracking-tight">
            <a
              href={`#/agents/${agent.id}`}
              className="after:absolute after:inset-0 after:rounded-2xl focus-visible:outline-none"
            >
              {agent.name}
            </a>
          </h2>
          <p className="mt-0.5 truncate text-sm text-fg-muted">
            {engine}
          </p>
        </div>
        {disabled && <Chip muted>{tr("已停用")}</Chip>}
      </div>
      <p className="mt-5 line-clamp-2 min-h-[3.25em] text-sm leading-relaxed text-fg-muted">
        {agent.description || agent.instructions || tr("暂无描述")}
      </p>
      <div className="mt-5 flex flex-1 flex-col gap-3">
        <Part icon={Send} label={tr("飞书机器人")}>
          {bot ? (
            <span className="flex min-w-0 items-center gap-2 text-sm font-medium">
              <span className="truncate">{bot.name}</span>
              {bot.health?.state === "connected" && (
                <span className="h-2 w-2 shrink-0 rounded-full bg-success" />
              )}
            </span>
          ) : (
            <Chip muted>{tr("未绑定")}</Chip>
          )}
        </Part>
        <Part icon={Server} label={tr("环境模板")}>
          {runtime ? (
            <>
              <span className="mr-1 truncate text-sm font-medium">{runtime.name}</span>
              {packages.map((name) => (
                <Chip key={name}>{name}</Chip>
              ))}
            </>
          ) : (
            <Chip muted>{tr("未选择环境")}</Chip>
          )}
        </Part>
        <Part icon={Blocks} label="Skills">
          {skills.length ? (
            <>
              {skills.slice(0, shownSkills).map((name) => (
                <Chip key={name}>{name}</Chip>
              ))}
              {skills.length > shownSkills && (
                <Chip muted>+{skills.length - shownSkills}</Chip>
              )}
            </>
          ) : (
            <Chip muted>{tr("无")}</Chip>
          )}
        </Part>
        <Part icon={Plug} label={tr("能力")}>
          {capabilities.length ? (
            capabilities.map((name) => <Chip key={name}>{name}</Chip>)
          ) : (
            <Chip muted>{tr("无")}</Chip>
          )}
        </Part>
      </div>
      <div className="relative mt-6 flex gap-2 border-t border-line-muted pt-5">
        <Button asChild size="sm" disabled={disabled}>
          <a
            href={disabled ? undefined : `#/new?agent=${agent.id}`}
            aria-disabled={disabled}
            className={disabled ? "pointer-events-none opacity-50" : undefined}
          >
            {tr("开始对话")}
          </a>
        </Button>
        <Button asChild size="sm" variant="ghost">
          <a href={`#/agents/new?from=${agent.id}`}>{tr("复制")}</a>
        </Button>
      </div>
    </article>
  );
}
