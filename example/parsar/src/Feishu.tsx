import { QRCodeSVG } from "qrcode.react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, QrCode, KeyRound, Trash2 } from "lucide-react";
import {
  product,
  useAgents,
  useFeishuBots,
  type FeishuBot,
} from "./lib/product";
import { PageHeader, Field, ErrorNotice, Toggle } from "./components/shared";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Select, SelectOption } from "./components/ui/select";
import { EmptyState } from "./components/ui/empty-state";
import { dateTime } from "./lib/api";
import { cn } from "./lib/utils";

interface Delivery {
  id: string;
  direction: string;
  chat_id: string;
  state: string;
  error?: string;
  created_at: number;
  attempts: number;
  session_id?: string;
}

const states: Record<string, string> = {
  stopped: tr("已停用"),
  connecting: tr("连接中"),
  connected: tr("已连接"),
  reconnecting: tr("重连中"),
  error: tr("连接失败"),
  core_unavailable: tr("Core 暂不可用"),
  queued: tr("等待处理"),
  submitting: tr("正在确认"),
  delivered: tr("已送达"),
  failed: tr("需要处理"),
  rejected: tr("已拒绝"),
};

function Status({ state }: { state: string }) {
  return (
    <span className="inline-flex items-center gap-2 rounded-full bg-surface-muted px-3 py-1 text-sm text-fg-muted">
      <span
        className={cn(
          "h-2 w-2 rounded-full",
          state === "connected"
            ? "bg-success"
            : state === "error" || state === "core_unavailable"
              ? "bg-danger"
              : state === "stopped"
                ? "bg-status-queued"
                : "bg-warning",
        )}
      />
      {states[state] || state}
    </span>
  );
}

export function Feishu() {
  const bots = useFeishuBots();
  const [adding, setAdding] = useState<"qr" | "manual" | null>(null);
  return (
    <>
      <PageHeader title={tr("飞书接入")} narrow>
        {(bots.data?.length || 0) > 0 && (
          <Button onClick={() => setAdding("qr")}>
            <Plus />
            {tr("添加机器人")}
          </Button>
        )}
      </PageHeader>
      <ErrorNotice page error={bots.error} className="max-w-3xl" />
      <div className="mx-auto w-full max-w-3xl space-y-6 px-6 pb-16 sm:px-10">
        {!bots.isPending && !bots.data?.length ? (
          <EmptyState
            title={tr("把 Agent 接入飞书")}
            description={tr("每个机器人对应一个 Agent，并以机器人身份工作。")}
            action={
              <div className="flex flex-wrap justify-center gap-3">
                <Button size="lg" onClick={() => setAdding("qr")}>
                  <QrCode />
                  {tr("扫码创建机器人")}
                </Button>
                <Button size="lg" variant="outline" onClick={() => setAdding("manual")}>
                  <KeyRound />
                  {tr("使用已有应用")}
                </Button>
              </div>
            }
          />
        ) : (
          bots.data?.map((bot) => (
            <BotCard key={`${bot.id}:${bot.revision}`} bot={bot} />
          ))
        )}
        <Deliveries />
      </div>
      {adding && <AddBot initial={adding} close={() => setAdding(null)} />}
    </>
  );
}

function BotCard({ bot }: { bot: FeishuBot }) {
  const cache = useQueryClient();
  const agents = useAgents();
  const initial = {
    ...bot,
    app_secret: "",
    agent_id: bot.agent_id || "",
    direct: !!bot.direct,
    groups: !!bot.groups,
  };
  const [form, setForm] = useState(initial);
  const [credentials, setCredentials] = useState(false);
  const dirty = JSON.stringify(form) !== JSON.stringify(initial);
  const save = useMutation({
    mutationFn: (value: typeof form) =>
      product<FeishuBot>(`feishu/bots/${bot.id}`, "PUT", value),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["feishu"] });
      void cache.invalidateQueries({ queryKey: ["agents"] });
    },
  });
  const remove = useMutation({
    mutationFn: () => product(`feishu/bots/${bot.id}`, "DELETE"),
    onSuccess: () => cache.invalidateQueries({ queryKey: ["feishu"] }),
  });
  const state = bot.enabled ? bot.health?.state || "connecting" : "stopped";
  return (
    <section
      aria-label={bot.name}
      className="rounded-2xl border border-line bg-surface app-shadow-card"
    >
      <div className="flex flex-wrap items-center justify-between gap-4 px-7 pt-6">
        <div className="min-w-0">
          <h2 className="truncate text-lg font-semibold tracking-tight">
            {bot.name || tr("飞书机器人")}
          </h2>
          <p className="mt-0.5 truncate font-mono text-sm text-fg-subtle">
            {bot.app_id || tr("未填写 App ID")}
          </p>
        </div>
        <Status state={state} />
      </div>
      <div className="space-y-6 px-7 py-6">
        <Field label={tr("Agent")} id={`bot-agent-${bot.id}`}>
          <Select
            id={`bot-agent-${bot.id}`}
            value={form.agent_id}
            onValueChange={(agent_id) => setForm({ ...form, agent_id })}
          >
            <SelectOption value="">{tr("选择 Agent")}</SelectOption>
            {agents.data?.map((agent) => (
              <SelectOption
                key={agent.id}
                value={agent.id}
                disabled={agent.enabled === false}
              >
                {agent.name}
              </SelectOption>
            ))}
          </Select>
        </Field>
        <div className="space-y-4 border-t border-line-muted pt-6">
          <Toggle
            label={tr("私聊")}
            checked={form.direct}
            onChange={(direct) => setForm({ ...form, direct })}
          />
          <Toggle
            label={tr("群聊")}
            checked={form.groups}
            onChange={(groups) => setForm({ ...form, groups })}
          />
        </div>
        {credentials && (
          <div className="grid gap-4 border-t border-line-muted pt-6 sm:grid-cols-2">
            <Field label={tr("机器人名称")} id={`bot-name-${bot.id}`}>
              <Input
                id={`bot-name-${bot.id}`}
                value={form.name}
                maxLength={80}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </Field>
            <Field label={tr("地区")} id={`bot-brand-${bot.id}`}>
              <Select
                id={`bot-brand-${bot.id}`}
                value={form.brand || "feishu"}
                onValueChange={(brand) =>
                  setForm({ ...form, brand: brand as "feishu" | "lark" })
                }
              >
                <SelectOption value="feishu">{tr("飞书")}</SelectOption>
                <SelectOption value="lark">Lark</SelectOption>
              </Select>
            </Field>
            <Field label="App ID" id={`bot-app-${bot.id}`}>
              <Input
                id={`bot-app-${bot.id}`}
                autoComplete="off"
                value={form.app_id}
                placeholder="cli_…"
                onChange={(e) => setForm({ ...form, app_id: e.target.value })}
              />
            </Field>
            <Field label="App Secret" id={`bot-secret-${bot.id}`}>
              <Input
                id={`bot-secret-${bot.id}`}
                type="password"
                autoComplete="new-password"
                value={form.app_secret}
                placeholder={
                  form.has_app_secret ? tr("已保存，留空保留") : tr("输入应用密钥")
                }
                onChange={(e) => setForm({ ...form, app_secret: e.target.value })}
              />
            </Field>
          </div>
        )}
        <ErrorNotice error={save.error || remove.error} className="my-0" />
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3 border-t border-line-muted px-7 py-4">
        <div className="flex items-center gap-1">
          <Button
            type="button"
            variant="ghost"
            onClick={() => setCredentials(!credentials)}
          >
            {credentials ? tr("收起应用凭证") : tr("应用凭证")}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={tr("删除机器人")}
            title={bot.enabled ? tr("请先停用机器人") : tr("删除机器人")}
            disabled={bot.enabled || remove.isPending}
            onClick={() => {
              if (confirm(tr("删除机器人 {{value0}}？", { value0: bot.name })))
                remove.mutate();
            }}
          >
            <Trash2 />
          </Button>
        </div>
        <div className="flex items-center gap-2">
          {bot.enabled ? (
            <Button
              type="button"
              variant="outline"
              disabled={save.isPending}
              onClick={() => save.mutate({ ...initial, enabled: false })}
            >
              {tr("停用")}
            </Button>
          ) : null}
          {(dirty || !bot.enabled) && (
            <Button
              type="button"
              disabled={save.isPending}
              onClick={() => save.mutate({ ...form, enabled: true })}
            >
              {save.isPending
                ? tr("保存中\u2026")
                : bot.enabled
                  ? tr("保存")
                  : tr("保存并启用")}
            </Button>
          )}
        </div>
      </div>
    </section>
  );
}

function Deliveries() {
  const cache = useQueryClient();
  const deliveries = useQuery({
    queryKey: ["feishu-deliveries"],
    queryFn: () => product<Delivery[]>("feishu/deliveries"),
    refetchInterval: 5000,
  });
  const retry = useMutation({
    mutationFn: (row: Delivery) =>
      product(`feishu/deliveries/${row.direction}/${row.id}`, "PUT", {}),
    onSettled: () =>
      void cache.invalidateQueries({ queryKey: ["feishu-deliveries"] }),
  });
  if (!deliveries.data?.length) return null;
  const failed = deliveries.data.filter((row) => row.state === "failed").length;
  return (
    <details className="group rounded-2xl border border-line bg-surface app-shadow-card">
      <summary className="flex cursor-pointer list-none items-center justify-between px-7 py-5">
        <span className="text-base font-semibold tracking-tight">
          {tr("消息记录")}
        </span>
        <span className="text-sm text-fg-muted">
          {failed
            ? tr("{{count}} 条需要处理", { count: failed })
            : tr("{{count}} 条", { count: deliveries.data.length })}
        </span>
      </summary>
      <ErrorNotice error={deliveries.error || retry.error} className="mx-7" />
      <div className="divide-y divide-line-muted border-t border-line-muted">
        {deliveries.data.map((row) => (
          <div
            key={`${row.direction}-${row.id}`}
            className="flex flex-wrap items-center gap-x-4 gap-y-1 px-7 py-3.5 text-sm"
          >
            <span className="w-28 shrink-0 text-fg">
              {row.direction === "inbound" ? tr("收到消息") : tr("回复消息")}
            </span>
            <span className="w-24 shrink-0 text-fg-muted">
              {states[row.state] || row.state}
            </span>
            <span className="min-w-0 flex-1 truncate text-fg-subtle">
              {dateTime(row.created_at / 1000)}
              {row.error ? ` · ${row.error}` : ""}
            </span>
            {row.session_id && (
              <a
                className="shrink-0 font-medium underline-offset-4 hover:underline"
                href={`#/sessions/${row.session_id}`}
              >
                {tr("查看对话")}
              </a>
            )}
            {row.state === "failed" && (
              <Button
                size="sm"
                variant="outline"
                disabled={retry.isPending}
                onClick={() => retry.mutate(row)}
              >
                {tr("重试")}
              </Button>
            )}
          </div>
        ))}
      </div>
    </details>
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

function AddBot({
  initial,
  close,
}: {
  initial: "qr" | "manual";
  close: () => void;
}) {
  const [mode, setMode] = useState(initial);
  return (
    <Dialog open onOpenChange={(open) => !open && close()}>
      <DialogContent aria-describedby={undefined} className="max-w-md">
        <DialogHeader>
          <DialogTitle>{tr("添加飞书机器人")}</DialogTitle>
        </DialogHeader>
        <div
          role="tablist"
          className="inline-flex w-full rounded-[10px] bg-surface-muted p-1"
        >
          {(
            [
              ["qr", tr("扫码创建")],
              ["manual", tr("已有应用")],
            ] as const
          ).map(([key, label]) => (
            <button
              key={key}
              role="tab"
              aria-selected={mode === key}
              onClick={() => setMode(key)}
              className={cn(
                "h-8 flex-1 rounded-lg text-sm transition-colors",
                mode === key
                  ? "bg-surface font-medium text-fg app-shadow-control"
                  : "text-fg-muted hover:text-fg",
              )}
            >
              {label}
            </button>
          ))}
        </div>
        {mode === "qr" ? <QrCreate close={close} /> : <ManualCreate close={close} />}
      </DialogContent>
    </Dialog>
  );
}

function QrCreate({ close }: { close: () => void }) {
  const cache = useQueryClient();
  const [id] = useState(() => crypto.randomUUID());
  const [started, setStarted] = useState(false);
  const begin = useMutation({
    mutationFn: () =>
      product<RegistrationState>(`feishu/provision/${id}`, "POST", {
        action: "begin",
        name: "Parsar",
      }),
    onSuccess: (value) => {
      cache.setQueryData(["feishu-registration", id], value);
      setStarted(true);
    },
  });
  const state = useQuery({
    queryKey: ["feishu-registration", id],
    enabled: started,
    queryFn: () =>
      product<RegistrationState>(`feishu/provision/${id}`, "POST", {
        action: "poll",
      }),
    refetchInterval: (query) =>
      query.state.data?.status === "pending"
        ? query.state.data.interval * 1000
        : false,
  });
  const status = state.data?.status;
  return (
    <div className="flex flex-col items-center gap-5 py-2 text-center">
      <ErrorNotice error={begin.error || state.error} className="my-0" />
      {status === "pending" ? (
        <>
          <div className="rounded-2xl border border-line p-4">
            <QRCodeSVG
              value={state.data!.verification_url}
              size={200}
              marginSize={1}
              title={tr("飞书授权二维码")}
            />
          </div>
          <p className="text-sm text-fg-muted">{tr("用飞书扫码，确认后自动完成")}</p>
          <a
            className="text-sm font-medium underline-offset-4 hover:underline"
            href={state.data!.verification_url}
            target="_blank"
            rel="noreferrer"
          >
            {tr("在浏览器打开授权页面")}
          </a>
        </>
      ) : status === "success" ? (
        <>
          <p className="text-base font-medium">{tr("机器人已创建")}</p>
          <Button
            onClick={() => {
              void cache.invalidateQueries({ queryKey: ["feishu"] });
              close();
            }}
          >
            {tr("继续配置")}
          </Button>
        </>
      ) : (
        <>
          <p className="text-sm text-fg-muted">
            {status === "error"
              ? tr("授权已结束，请重新获取二维码。")
              : tr("扫码后会自动创建一个飞书应用和机器人。")}
          </p>
          <Button disabled={begin.isPending} onClick={() => begin.mutate()}>
            <QrCode />
            {begin.isPending ? tr("正在生成二维码…") : tr("获取二维码")}
          </Button>
        </>
      )}
    </div>
  );
}

function ManualCreate({ close }: { close: () => void }) {
  const cache = useQueryClient();
  const [form, setForm] = useState({
    name: "",
    app_id: "",
    app_secret: "",
    brand: "feishu" as "feishu" | "lark",
  });
  const save = useMutation({
    mutationFn: () =>
      product(`feishu/bots/${crypto.randomUUID()}`, "PUT", {
        ...form,
        revision: 0,
        enabled: false,
        agent_id: "",
        direct: false,
        groups: false,
      }),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["feishu"] });
      close();
    },
  });
  return (
    <form
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <Field label={tr("机器人名称")} id="new-bot-name">
        <Input
          id="new-bot-name"
          required
          maxLength={80}
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
      </Field>
      <Field label="App ID" id="new-bot-app">
        <Input
          id="new-bot-app"
          required
          autoComplete="off"
          placeholder="cli_…"
          value={form.app_id}
          onChange={(e) => setForm({ ...form, app_id: e.target.value })}
        />
      </Field>
      <Field label="App Secret" id="new-bot-secret">
        <Input
          id="new-bot-secret"
          required
          type="password"
          autoComplete="new-password"
          value={form.app_secret}
          onChange={(e) => setForm({ ...form, app_secret: e.target.value })}
        />
      </Field>
      <Field label={tr("地区")} id="new-bot-brand">
        <Select
          id="new-bot-brand"
          value={form.brand}
          onValueChange={(brand) =>
            setForm({ ...form, brand: brand as "feishu" | "lark" })
          }
        >
          <SelectOption value="feishu">{tr("飞书")}</SelectOption>
          <SelectOption value="lark">Lark</SelectOption>
        </Select>
      </Field>
      <ErrorNotice error={save.error} className="my-0" />
      <DialogFooter>
        <Button type="submit" disabled={save.isPending}>
          {tr("添加")}
        </Button>
      </DialogFooter>
    </form>
  );
}
