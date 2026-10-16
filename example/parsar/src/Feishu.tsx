import { sessionRestriction } from "../shared/session-profile.mjs";
import { QRCodeSVG } from "qrcode.react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { product, useAgents, useRuntimes, useProjects } from "./lib/product";
import { PageHeader, Field, ErrorNotice } from "./components/shared";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Select, SelectOption } from "./components/ui/select";
import { EmptyState } from "./components/ui/empty-state";
import { ResourceField } from "./components/ResourceField";
import { dateTime } from "./lib/api";
interface FeishuConfig {
  brand?: "feishu" | "lark";
  history_context?: boolean;
  id: string;
  name: string;
  health?: {
    state: string;
  };
  revision: number;
  app_id: string;
  app_secret?: string;
  has_app_secret: boolean;
  enabled: boolean;
  routes: {
    chat_id: string;
    project_id: string;
    agent_id: string;
    runtime_id: string;
    allow_agent_selection?: boolean;
  }[];
}
interface Delivery {
  id: string;
  direction: string;
  chat_id: string;
  state: string;
  error?: string;
  created_at: number;
  attempts: number;
  task_id?: string;
}
const states: Record<string, string> = {
  stopped: tr("已停用"),
  connecting: tr("连接中"),
  connected: tr("已连接"),
  reconnecting: tr("重连中"),
  error: tr("连接失败，请检查机器人配置"),
  core_unavailable: tr("Core 暂不可用"),
  queued: tr("等待处理"),
  submitting: tr("正在确认"),
  delivered: tr("已送达"),
  failed: tr("需要处理"),
  rejected: tr("已拒绝"),
};
export function Feishu() {
  const cache = useQueryClient();
  const retry = useMutation({
    mutationFn: (row: Delivery) =>
      product(`feishu/deliveries/${row.direction}/${row.id}`, "PUT", {}),
    onSettled: () =>
      void cache.invalidateQueries({ queryKey: ["feishu-deliveries"] }),
  });
  const [editing, setEditing] = useState<FeishuConfig | null>(null);
  const config = useQuery({
    queryKey: ["feishu"],
    queryFn: () => product<FeishuConfig[]>("feishu/bots"),
    refetchInterval: 5000,
  });
  const deliveries = useQuery({
    queryKey: ["feishu-deliveries"],
    queryFn: () => product<Delivery[]>("feishu/deliveries"),
    refetchInterval: 5000,
  });
  const selected = editing
    ? config.data?.find((bot) => bot.id === editing.id) || editing
    : config.data?.length === 1
      ? config.data[0]
      : null;
  return (
    <>
      <PageHeader title={tr("飞书接入")}>
        <FeishuRegistration complete={setEditing} />
        <Button
          variant="outline"
          onClick={() =>
            setEditing({
              id: crypto.randomUUID(),
              name: "",
              revision: 0,
              app_id: "",
              enabled: false,
              has_app_secret: false,
              routes: [],
            })
          }
        >
          <Plus />
          {tr("添加机器人")}
        </Button>
      </PageHeader>
      <ErrorNotice error={config.error || deliveries.error || retry.error} />
      <div className="min-h-0 flex-1 overflow-auto">
        <div className="space-y-6 px-6 py-4">
          <div className="divide-y divide-line border-b border-line">
            {config.data?.map((bot) => (
              <div
                key={bot.id}
                className="flex min-h-9 items-center gap-4 text-sm"
              >
                <span className="min-w-0 flex-1 truncate font-medium">
                  {bot.name}
                </span>
                <span className="font-mono text-xs text-fg-muted">
                  {bot.app_id}
                </span>
                <span className="text-xs">
                  {states[bot.health?.state || "stopped"] || bot.health?.state}
                </span>
                <Button variant="ghost" onClick={() => setEditing(bot)}>
                  {tr("配置机器人")}
                </Button>
              </div>
            ))}
          </div>
          {!config.data?.length && !editing && (
            <EmptyState
              title={tr("连接飞书机器人")}
              description={tr(
                "扫码连接机器人，选择一个 Agent，就可以在飞书私聊中交代任务。",
              )}
            />
          )}
          {selected && (
            <section className="max-w-3xl border-b border-line pb-6">
              <FeishuSetup key={selected.id} value={selected} />
              {(config.data?.length || 0) !== 1 && (
                <Button
                  className="mt-3"
                  variant="ghost"
                  onClick={() => setEditing(null)}
                >
                  {tr("关闭配置")}
                </Button>
              )}
            </section>
          )}
          <details>
            <summary className="cursor-pointer text-sm text-fg-muted">
              {tr("消息记录与排查")}
            </summary>
            <h2 className="my-4 text-lg font-medium">{tr("最近投递")}</h2>
            <p className="mb-4 text-sm text-fg-muted">
              {tr(
                "连接中断会按相同请求标识重试。超过重试上限或飞书去重窗口的记录会保留为「需要处理」，避免重复发送。",
              )}
            </p>
            {!deliveries.data?.length ? (
              <EmptyState
                size="compact"
                title={tr("尚无消息")}
                description={tr(
                  "启用后，私聊机器人发送第一项任务；已接入的群中可以 @机器人。",
                )}
              />
            ) : (
              <div className="divide-y divide-line rounded-lg border border-line">
                {deliveries.data.map((row) => (
                  <div key={`${row.direction}-${row.id}`} className="p-4">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span>
                        {row.direction === "inbound"
                          ? tr("飞书 \u2192 Agent")
                          : tr("Agent \u2192 飞书")}
                      </span>
                      <span className="text-sm">
                        {states[row.state] || row.state}
                      </span>
                    </div>
                    <p className="mt-1 break-all text-xs text-fg-muted">
                      {row.chat_id} · {dateTime(row.created_at / 1000)}
                      {tr("\u00B7 尝试")} {row.attempts}
                      {tr("次")}
                    </p>
                    {row.error && <p className="mt-2 text-sm">{row.error}</p>}
                    {row.state === "failed" && (
                      <Button
                        className="mt-2 mr-3"
                        variant="outline"
                        disabled={retry.isPending}
                        onClick={() => retry.mutate(row)}
                      >
                        {tr("重试此投递")}
                      </Button>
                    )}
                    {row.task_id && (
                      <a
                        className="mt-2 inline-block text-sm underline"
                        href={`#/tasks/${row.task_id}`}
                      >
                        {tr("查看任务")}
                      </a>
                    )}
                  </div>
                ))}
              </div>
            )}
          </details>
        </div>
      </div>
    </>
  );
}
function FeishuSetup({ value }: { value: FeishuConfig }) {
  const cache = useQueryClient();
  const agents = useAgents(),
    runtimes = useRuntimes();
  const existing = value.routes.find((route) => route.chat_id === "p2p");
  const [agentId, setAgentId] = useState(existing?.agent_id || "");
  const [advanced, setAdvanced] = useState(!value.has_app_secret);
  const agent = agents.data?.find((row) => row.id === agentId);
  const runtime = runtimes.data?.find(
    (row) => row.id === (existing?.runtime_id || agent?.runtime_id),
  );
  const issue =
    !existing && value.routes.length >= 20
      ? tr("接入数量已满，请先在高级设置中移除一项。")
      : !value.has_app_secret || !value.app_id
        ? tr("请先在高级设置中填写应用凭证。")
        : !agent
          ? tr("选择一个 Agent 来回复私聊消息。")
          : agent.enabled === false
            ? tr("请先启用这个 Agent。")
            : !runtime
              ? tr("Agent 未配置有效的默认环境模板。")
              : sessionRestriction(agent, runtime);
  const save = useMutation({
    mutationFn: () =>
      product<FeishuConfig>(`feishu/bots/${value.id}`, "PUT", {
        ...value,
        enabled: true,
        routes: existing
          ? value.routes.map((route) =>
              route.chat_id === "p2p" ? { ...route, agent_id: agentId } : route,
            )
          : [
              ...value.routes,
              {
                chat_id: "p2p",
                agent_id: agentId,
                project_id: "",
                runtime_id: "",
              },
            ],
      }),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["feishu"] });
      void cache.invalidateQueries({ queryKey: ["feishu-status"] });
    },
  });
  const groupCount = value.routes.filter(
    (route) => route.chat_id !== "p2p",
  ).length;
  const resumesGroups = !value.enabled && groupCount > 0;
  const connected =
    value.enabled && !!existing && value.health?.state === "connected";
  return (
    <div className="space-y-6">
      {!advanced && (
        <div className="rounded-xl border border-line bg-bg p-6 space-y-5">
          <div>
            <p className="text-xs text-fg-muted">
              {tr("1 连接机器人 → 2 选择 Agent → 3 开始聊天")}
            </p>
            <h2 className="mt-3 text-xl font-semibold">
              {existing && value.enabled
                ? tr("私聊助手")
                : value.has_app_secret
                  ? tr("机器人已添加，接下来选择 Agent")
                  : tr("连接你的机器人")}
            </h2>
            <p className="mt-2 text-sm text-fg-muted">
              {existing?.allow_agent_selection
                ? tr(
                    "用户可以在飞书切换 Agent；这里设置默认 Agent，已有个人选择会保留。",
                  )
                : tr("私聊收到的消息会交给这个 Agent，环境跟随 Agent 模板。")}
            </p>
          </div>
          <ResourceField
            label={
              existing?.allow_agent_selection
                ? tr("私聊默认 Agent")
                : tr("回复私聊的 Agent")
            }
            options={(agents.data || []).map((row) => ({
              id: row.id,
              name: row.name,
              disabled: row.enabled === false,
            }))}
            loading={agents.isPending}
            manage={{ href: "#/agents", label: tr("管理 Agent") }}
            selected={agentId ? [agentId] : []}
            change={(ids) => setAgentId(ids[0] || "")}
          />
          {runtime && (
            <p className="text-sm text-fg-muted">
              {tr("工作环境")}: {runtime.name}
              {existing?.runtime_id ? ` · ${tr("已在高级设置中指定")}` : ""}
            </p>
          )}
          {issue && <p className="text-sm text-fg-muted">{issue}</p>}
          {resumesGroups && (
            <p className="text-sm text-fg-muted">
              {tr(
                "此机器人还有 {{count}} 个群聊接入。启用后，私聊和这些群聊会一起恢复。",
                { count: groupCount },
              )}
            </p>
          )}
          <ErrorNotice error={save.error || agents.error || runtimes.error} />
          <Button
            disabled={
              !!issue ||
              save.isPending ||
              (!existing && value.routes.length >= 20)
            }
            onClick={() => save.mutate()}
          >
            {save.isPending
              ? tr("保存中…")
              : value.enabled && existing
                ? tr("保存私聊设置")
                : resumesGroups
                  ? tr("启用机器人（私聊和已有群聊）")
                  : tr("启用私聊")}
          </Button>
          {value.enabled && existing && (
            <div
              role="status"
              className="rounded-lg border border-line p-4 text-sm space-y-2"
            >
              <p className="font-medium">
                {connected
                  ? tr("已连接，可以去飞书聊天了")
                  : states[value.health?.state || "connecting"] || tr("连接中")}
              </p>
              {connected && (
                <>
                  <p>
                    {tr("在飞书搜索机器人名称，打开私聊，发送你的第一项任务。")}
                  </p>
                  <p className="text-fg-muted">
                    {tr(
                      "例如：介绍一下你能帮我做什么。需要使用你的账号时，机器人会引导你授权。",
                    )}
                  </p>
                </>
              )}
            </div>
          )}
        </div>
      )}
      <Button variant="ghost" onClick={() => setAdvanced(!advanced)}>
        {advanced
          ? tr("收起高级设置")
          : tr("高级设置：群聊、应用凭证与更多选项")}
      </Button>
      {advanced && (
        <FeishuEditor
          value={value}
          onSaved={(bot) => {
            setAgentId(
              bot.routes.find((route) => route.chat_id === "p2p")?.agent_id ||
                "",
            );
          }}
        />
      )}
    </div>
  );
}
function FeishuEditor({
  value,
  onSaved,
}: {
  value: FeishuConfig;
  onSaved: (bot: FeishuConfig) => void;
}) {
  const cache = useQueryClient(),
    agents = useAgents(),
    runtimes = useRuntimes(),
    projects = useProjects();
  const [form, setForm] = useState({ ...value, app_secret: "" });
  const [saved, setSaved] = useState(false);
  const save = useMutation({
    mutationFn: () =>
      product<FeishuConfig>(`feishu/bots/${form.id}`, "PUT", form),
    onSuccess: (config) => {
      setForm({ ...config, app_secret: "" });
      setSaved(true);
      onSaved(config);
      void cache.invalidateQueries({ queryKey: ["feishu"] });
      void cache.invalidateQueries({ queryKey: ["feishu-status"] });
    },
  });
  const updateRoute = (index: number, field: string, value: string | boolean) =>
    setForm({
      ...form,
      routes: form.routes.map((row, i) =>
        i === index ? { ...row, [field]: value } : row,
      ),
    });
  return (
    <form
      className="space-y-5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <Field label={tr("机器人名称")} id="feishu-name">
        <Input
          id="feishu-name"
          value={form.name}
          required
          maxLength={80}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
      </Field>
      <Field label={tr("地区")} id="feishu-brand">
        <Select
          id="feishu-brand"
          value={form.brand || "feishu"}
          onValueChange={(brand) =>
            setForm({ ...form, brand: brand as "feishu" | "lark" })
          }
        >
          <SelectOption value="feishu">Feishu</SelectOption>
          <SelectOption value="lark">Lark</SelectOption>
        </Select>
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="App ID" id="feishu-app">
          <Input
            id="feishu-app"
            autoComplete="off"
            value={form.app_id}
            onChange={(e) => setForm({ ...form, app_id: e.target.value })}
            placeholder="cli_…"
          />
        </Field>
        <Field label="App Secret" id="feishu-secret">
          <Input
            id="feishu-secret"
            type="password"
            autoComplete="new-password"
            value={form.app_secret}
            onChange={(e) => setForm({ ...form, app_secret: e.target.value })}
            placeholder={
              form.has_app_secret ? tr("已保存，留空保留") : tr("输入应用密钥")
            }
          />
        </Field>
      </div>
      <div className="flex items-center justify-between">
        <h3 className="font-medium">{tr("群聊 / 私聊分配规则")}</h3>
        <Button
          type="button"
          variant="outline"
          disabled={form.routes.length >= 20}
          onClick={() =>
            setForm({
              ...form,
              routes: [
                ...form.routes,
                { chat_id: "", project_id: "", agent_id: "", runtime_id: "" },
              ],
            })
          }
        >
          <Plus />
          {tr("添加群")}
        </Button>
      </div>
      {form.routes.map((route, index) => (
        <div
          key={index}
          className="space-y-4 rounded-lg border border-line p-4"
        >
          <div className="flex gap-3">
            <Select
              aria-label={tr("接入类型 {{value0}}", { value0: index + 1 })}
              value={route.chat_id === "p2p" ? "private" : "group"}
              onValueChange={(type) =>
                updateRoute(index, "chat_id", type === "private" ? "p2p" : "")
              }
            >
              <SelectOption value="group">{tr("群聊")}</SelectOption>
              <SelectOption value="private">{tr("私聊")}</SelectOption>
            </Select>
            {route.chat_id !== "p2p" && (
              <Input
                aria-label={tr("群 ID {{value0}}", { value0: index + 1 })}
                required
                placeholder="oc_…"
                value={route.chat_id}
                onChange={(e) => updateRoute(index, "chat_id", e.target.value)}
              />
            )}
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={tr("移除群 {{value0}}", { value0: index + 1 })}
              onClick={() =>
                setForm({
                  ...form,
                  routes: form.routes.filter((_, i) => i !== index),
                })
              }
            >
              <Trash2 />
            </Button>
          </div>
          <label className="flex items-start gap-3 text-sm">
            <input
              type="checkbox"
              checked={!!route.allow_agent_selection}
              onChange={(e) =>
                updateRoute(index, "allow_agent_selection", e.target.checked)
              }
            />
            <span>
              {tr("允许群成员切换 Agent（共享机器人）")}
              <span className="mt-1 block text-fg-muted">
                {tr(
                  "用 /list 和 /select 选择此安装中的 Agent；关闭时固定使用下方 Agent。选择按群成员保存，仅影响新任务。",
                )}
              </span>
            </span>
          </label>
          <div className="grid gap-3 sm:grid-cols-3">
            <Select
              aria-label={tr("群项目 {{value0}}", { value0: index + 1 })}
              value={route.project_id}
              onValueChange={(value) => updateRoute(index, "project_id", value)}
            >
              <SelectOption value="">{tr("独立任务")}</SelectOption>
              {projects.data?.map((row) => (
                <SelectOption key={row.id} value={row.id}>
                  {row.name}
                </SelectOption>
              ))}
            </Select>
            <Select
              aria-label={tr("群 Agent {{value0}}", { value0: index + 1 })}
              value={route.agent_id}
              onValueChange={(value) => updateRoute(index, "agent_id", value)}
            >
              <SelectOption value="">{tr("选择 Agent")}</SelectOption>
              {agents.data?.map((row) => (
                <SelectOption key={row.id} value={row.id}>
                  {row.name}
                </SelectOption>
              ))}
            </Select>
            <Select
              aria-label={tr("群环境模板 {{value0}}", { value0: index + 1 })}
              value={route.runtime_id}
              onValueChange={(value) => updateRoute(index, "runtime_id", value)}
            >
              <SelectOption value="">{tr("使用 Agent 默认环境")}</SelectOption>
              {runtimes.data
                ?.filter((row) => row.environment === "openai_hosted")
                .map((row) => (
                  <SelectOption key={row.id} value={row.id}>
                    {row.name}
                  </SelectOption>
                ))}
            </Select>
          </div>
          {(() => {
            const agent = agents.data?.find((row) => row.id === route.agent_id);
            const runtime = runtimes.data?.find(
              (row) => row.id === (route.runtime_id || agent?.runtime_id),
            );
            const issue = !agent
              ? tr("请选择 Agent")
              : !runtime
                ? tr("Agent 未配置有效的默认环境模板。")
                : sessionRestriction(agent, runtime);
            return (
              <p className="mt-2 text-xs text-fg-muted">
                {issue ||
                  `${tr("新话题使用")}: ${runtime?.name} · ${route.runtime_id ? tr("为此接入指定") : tr("跟随 Agent 默认环境")}`}
              </p>
            );
          })()}
        </div>
      ))}
      <label className="flex items-center gap-3">
        <input
          type="checkbox"
          checked={form.history_context || false}
          onChange={(e) =>
            setForm({ ...form, history_context: e.target.checked })
          }
        />
        {tr("新话题附带最近群消息（需要历史消息权限）")}
      </label>
      <label className="flex items-center gap-3">
        <input
          type="checkbox"
          checked={form.enabled}
          onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
        />
        {tr("启用飞书机器人")}
      </label>
      <p className="text-sm text-fg-muted">
        {tr(
          "同一话题继续同一个执行会话。配置更改作用于新话题。支持 /cancel、/review、/done、/reopen；结果回传文本摘要，文件可在 Web 会话中下载。",
        )}
      </p>
      <ErrorNotice
        error={save.error || agents.error || runtimes.error || projects.error}
      />
      <div className="flex items-center gap-3">
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? tr("保存中\u2026") : tr("保存飞书配置")}
        </Button>
        {saved && <span role="status">{tr("配置已保存")}</span>}
      </div>
    </form>
  );
}

interface RegistrationState {
  id: string;
  status: "pending" | "success" | "error";
  verification_url: string;
  expires_at: number;
  interval: number;
  error?: string;
}
function FeishuRegistration({
  complete,
}: {
  complete: (bot: FeishuConfig) => void;
}) {
  const [open, setOpen] = useState(false),
    [id, setId] = useState(""),
    cache = useQueryClient();
  const begin = useMutation({
    mutationFn: () =>
      product<RegistrationState>(
        `feishu/provision/${crypto.randomUUID()}`,
        "POST",
        { action: "begin", name: "Feishu" },
      ),
    onSuccess: (value) => {
      cache.setQueryData(["feishu-registration", value.id], value);
      setId(value.id);
    },
  });
  const state = useQuery({
    queryKey: ["feishu-registration", id],
    enabled: open && !!id,
    queryFn: () =>
      product<RegistrationState>(`feishu/provision/${id}`, "POST", {
        action: "poll",
      }),
    refetchInterval: (query) =>
      query.state.data?.status === "pending"
        ? query.state.data.interval * 1000
        : false,
    retry: false,
  });
  const finish = useMutation({
    mutationFn: () => product<FeishuConfig>(`feishu/bots/${id}`),
    onSuccess: (bot) => {
      void cache.invalidateQueries({ queryKey: ["feishu"] });
      complete(bot);
      setOpen(false);
      setId("");
    },
  });
  return (
    <>
      <Button
        onClick={() => {
          setOpen(true);
          if (!id && !begin.isPending) begin.mutate();
        }}
      >
        {tr("扫码创建机器人")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent aria-describedby={undefined}>
          <DialogHeader>
            <DialogTitle>{tr("扫码创建机器人")}</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-fg-muted">
            {tr(
              "使用飞书扫码并确认创建应用。应用密钥仅保存在服务器；完成后选择 Agent，即可启用私聊。",
            )}
          </p>
          <ErrorNotice error={begin.error || state.error || finish.error} />
          {state.data?.status === "pending" ? (
            <div className="flex flex-col items-center gap-3">
              <QRCodeSVG
                value={state.data.verification_url}
                size={220}
                marginSize={4}
                title={tr("飞书授权二维码")}
              />
              <a
                className="text-sm underline"
                href={state.data.verification_url}
                target="_blank"
                rel="noreferrer"
              >
                {tr("在浏览器打开授权页面")}
              </a>
              <p className="text-xs text-fg-muted">{tr("等待扫码确认")}</p>
              {state.error && (
                <Button onClick={() => void state.refetch()}>
                  {tr("重试")}
                </Button>
              )}
            </div>
          ) : state.data?.status === "success" ? (
            <Button disabled={finish.isPending} onClick={() => finish.mutate()}>
              {tr("选择 Agent，继续设置")}
            </Button>
          ) : (
            <>
              <p className="text-sm">
                {state.data?.error ? tr("授权已结束，请重新获取二维码。") : ""}
              </p>
              <Button disabled={begin.isPending} onClick={() => begin.mutate()}>
                {tr("获取二维码")}
              </Button>
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
