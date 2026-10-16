import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Clock3, Play, History } from "lucide-react";
import { product, useTasks } from "./lib/product";
import { PageHeader, Field, ErrorNotice } from "./components/shared";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Select, SelectOption } from "./components/ui/select";
import { EmptyState } from "./components/ui/empty-state";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
import { dateTime } from "./lib/api";
import { Ledger, LedgerHeader, LedgerRow } from "./components/ui/ledger";
interface Schedule {
  delivery?: {
    bot_id: string;
    chat_id: string;
  };
  cron?: string;
  timezone?: string;
  id: string;
  name: string;
  revision: number;
  task_id: string;
  interval_minutes: number;
  enabled: boolean;
  next_at: number;
  last_task_id?: string;
  last_error?: string;
  pending?: {
    task_id: string;
  };
}
export function Schedules() {
  const schedules = useQuery({
      queryKey: ["schedules"],
      queryFn: () => product<Schedule[]>("schedules"),
      refetchInterval: 5000,
    }),
    tasks = useTasks(),
    cache = useQueryClient();
  const [history, setHistory] = useState<string | null>(null);
  const run = useMutation({
    mutationFn: async (id: string) => {
      const key = `parsar-schedule-request:${id}`;
      const request_id = sessionStorage.getItem(key) || crypto.randomUUID();
      sessionStorage.setItem(key, request_id);
      const result = await product(`schedules/${id}/run`, "POST", {
        request_id,
      });
      sessionStorage.removeItem(key);
      return result;
    },
    onSettled: () => cache.invalidateQueries({ queryKey: ["schedules"] }),
  });
  const [editing, setEditing] = useState<Partial<Schedule> | null>(null);
  const toggle = useMutation({
    mutationFn: (row: Schedule) =>
      product(`schedules/${row.id}`, "PUT", { ...row, enabled: !row.enabled }),
    onSettled: () => void cache.invalidateQueries({ queryKey: ["schedules"] }),
  });
  return (
    <>
      <PageHeader title={tr("定时任务")}>
        <Button onClick={() => setEditing({})}>
          <Plus />
          {tr("新建计划")}
        </Button>
      </PageHeader>
      <ErrorNotice
        error={schedules.error || tasks.error || toggle.error || run.error}
      />
      <Ledger columns="16px minmax(160px,2fr) minmax(140px,1fr) minmax(130px,1fr) minmax(120px,1fr) 170px">
        <LedgerHeader>
          <span />
          <span>{tr("名称")}</span>
          <span>{tr("计划")}</span>
          <span>{tr("任务模板")}</span>
          <span>{tr("下次执行")}</span>
          <span />
        </LedgerHeader>
        {schedules.data?.map((row) => (
          <LedgerRow key={row.id}>
            <Clock3 className="h-3.5 w-3.5 text-fg-muted" />
            <span title={row.last_error}>
              {row.name}
              {row.last_error ? tr(" \u00B7 需关注") : ""}
            </span>
            <span className="font-mono text-xs" title={row.timezone}>
              {row.cron ||
                tr("每 {{value0}} 分钟", { value0: row.interval_minutes })}
            </span>
            <span>
              {tasks.data?.find((task) => task.id === row.task_id)?.name || "—"}
            </span>
            <span className="text-xs text-fg-muted">
              {row.enabled ? dateTime(row.next_at / 1000) : tr("已暂停")}
            </span>
            <div className="flex justify-end gap-1">
              <Button
                variant="ghost"
                size="icon"
                aria-label={tr("立即运行 {{value0}}", { value0: row.name })}
                disabled={run.isPending}
                onClick={() => run.mutate(row.id)}
              >
                <Play />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={tr("历史记录 {{value0}}", { value0: row.name })}
                onClick={() => setHistory(row.id)}
              >
                <History />
              </Button>
              <Button variant="ghost" onClick={() => setEditing(row)}>
                {tr("编辑计划")}
              </Button>
              <Button
                variant="ghost"
                disabled={toggle.isPending}
                onClick={() => toggle.mutate(row)}
              >
                {row.enabled ? tr("暂停") : tr("启用")}
              </Button>
            </div>
          </LedgerRow>
        ))}
        {!schedules.isPending && !schedules.data?.length && (
          <EmptyState
            icon={Clock3}
            title={tr("把重复的工作交给 Agent")}
            description={tr("选择任务模板，设置间隔或 Cron 与时区。")}
          />
        )}
      </Ledger>
      {history && (
        <ScheduleHistory id={history} close={() => setHistory(null)} />
      )}
      {editing && (
        <ScheduleEditor value={editing} close={() => setEditing(null)} />
      )}
    </>
  );
}
function ScheduleEditor({
  value,
  close,
}: {
  value: Partial<Schedule>;
  close: () => void;
}) {
  const tasks = useTasks(),
    cache = useQueryClient();
  const bots = useQuery({
    queryKey: ["feishu-bots"],
    queryFn: () =>
      product<
        {
          id: string;
          name: string;
          enabled: boolean;
          routes: {
            chat_id: string;
          }[];
        }[]
      >("feishu/bots"),
  });
  const [form, setForm] = useState(() => ({
    id: crypto.randomUUID(),
    name: "",
    task_id: "",
    interval_minutes: 1440,
    cron: "",
    timezone: "Asia/Shanghai",
    enabled: true,
    ...value,
  }));
  const finish = () => {
    void cache.invalidateQueries({ queryKey: ["schedules"] });
    close();
  };
  const save = useMutation({
    mutationFn: () => product(`schedules/${form.id}`, "PUT", form),
    onSuccess: finish,
  });
  const remove = useMutation({
    mutationFn: () => product(`schedules/${form.id}`, "DELETE", form),
    onSuccess: finish,
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending && !remove.isPending) close();
      }}
    >
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>
            {value.id ? tr("编辑计划") : tr("新建计划")}
          </DialogTitle>
        </DialogHeader>
        <form
          id="schedule-form"
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field label={tr("计划名称")} id="schedule-name">
            <Input
              id="schedule-name"
              required
              maxLength={80}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <Field label={tr("任务模板")} id="schedule-task">
            <Select
              id="schedule-task"
              value={form.task_id}
              onValueChange={(task_id) => setForm({ ...form, task_id })}
            >
              <SelectOption value="">{tr("选择任务")}</SelectOption>
              {tasks.data?.map((row) => (
                <SelectOption key={row.id} value={row.id}>
                  {row.name}
                </SelectOption>
              ))}
            </Select>
          </Field>
          <Field label={tr("飞书结果投递")} id="schedule-delivery">
            <Select
              id="schedule-delivery"
              value={
                form.delivery
                  ? `${form.delivery.bot_id}:${form.delivery.chat_id}`
                  : ""
              }
              onValueChange={(value) => {
                const [bot_id, chat_id] = value.split(":");
                setForm({
                  ...form,
                  delivery: bot_id && chat_id ? { bot_id, chat_id } : undefined,
                });
              }}
            >
              <SelectOption value="">{tr("不发送")}</SelectOption>
              {bots.data
                ?.filter((bot) => bot.enabled)
                .flatMap((bot) =>
                  bot.routes
                    .filter((route) => route.chat_id !== "p2p")
                    .map((route) => (
                      <SelectOption
                        key={`${bot.id}:${route.chat_id}`}
                        value={`${bot.id}:${route.chat_id}`}
                      >
                        {bot.name} / {route.chat_id}
                      </SelectOption>
                    )),
                )}
            </Select>
          </Field>
          <Field label={tr("计划类型")} id="schedule-kind">
            <Select
              id="schedule-kind"
              value={form.cron ? "cron" : "interval"}
              onValueChange={(value) =>
                setForm({ ...form, cron: value === "cron" ? "0 9 * * *" : "" })
              }
            >
              <SelectOption value="interval">{tr("固定间隔")}</SelectOption>
              <SelectOption value="cron">{tr("Cron / 日历计划")}</SelectOption>
            </Select>
          </Field>
          {form.cron && (
            <>
              <Field label={tr("Cron 表达式")} id="schedule-cron">
                <Input
                  id="schedule-cron"
                  required
                  value={form.cron}
                  onChange={(e) => setForm({ ...form, cron: e.target.value })}
                />
              </Field>
              <div className="flex gap-2">
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => setForm({ ...form, cron: "0 9 * * *" })}
                >
                  {tr("每天 09:00")}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => setForm({ ...form, cron: "0 9 * * 1-5" })}
                >
                  {tr("工作日")}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => setForm({ ...form, cron: "0 9 * * 1" })}
                >
                  {tr("每周一")}
                </Button>
              </div>
              <Field label={tr("时区")} id="schedule-timezone">
                <Input
                  id="schedule-timezone"
                  required
                  value={form.timezone}
                  onChange={(e) =>
                    setForm({ ...form, timezone: e.target.value })
                  }
                  list="schedule-zones"
                />
                <datalist id="schedule-zones">
                  {Intl.supportedValuesOf("timeZone").map((zone) => (
                    <option key={zone} value={zone} />
                  ))}
                </datalist>
              </Field>
            </>
          )}
          {!form.cron && (
            <Field label={tr("间隔（分钟）")} id="schedule-interval">
              <Input
                id="schedule-interval"
                type="number"
                min={5}
                max={525600}
                required
                value={form.interval_minutes}
                onChange={(e) =>
                  setForm({ ...form, interval_minutes: Number(e.target.value) })
                }
              />
            </Field>
          )}
          <p className="text-sm text-fg-muted">
            {tr(
              "服务器运行时生效。每次读取模板的当前配置；已发出的请求保持不变。暂时断线会自动恢复原执行；配置被拒绝时暂停，修正后可重新启用。",
            )}
          </p>
          <ErrorNotice error={save.error || remove.error || bots.error} />
        </form>
        <DialogFooter>
          {value.id && (
            <Button
              variant="ghost"
              disabled={remove.isPending || save.isPending}
              onClick={() => {
                if (confirm(tr("删除此计划？已创建的任务会保留。")))
                  remove.mutate();
              }}
            >
              {tr("删除计划")}
            </Button>
          )}
          <Button
            type="submit"
            form="schedule-form"
            disabled={save.isPending || remove.isPending}
          >
            {tr("保存计划")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
function ScheduleHistory({ id, close }: { id: string; close: () => void }) {
  const query = useQuery({
    queryKey: ["schedule-history", id],
    queryFn: () =>
      product<
        {
          id: string;
          task_id: string;
          session_id: string;
          delivery_error?: string;
          outcome?: string;
          created_at: number;
        }[]
      >(`schedules/${id}/history`),
    refetchInterval: 5000,
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{tr("执行历史")}</DialogTitle>
        </DialogHeader>
        <ErrorNotice error={query.error} />
        <div className="max-h-96 divide-y divide-line overflow-auto">
          {query.data?.map((row) => (
            <div
              key={row.id}
              className="flex min-h-9 flex-wrap items-center gap-3 py-2 text-sm"
            >
              <span className="flex-1 text-fg-muted">
                {dateTime(row.created_at / 1000)}
              </span>
              {row.outcome && <span>{tr(row.outcome)}</span>}
              <a href={`#/tasks/${row.task_id}`}>{tr("查看任务")}</a>
              <a href={`#/sessions/${row.session_id}`}>{tr("查看执行")}</a>
              {row.delivery_error && (
                <p className="w-full text-xs text-danger">
                  {row.delivery_error}
                </p>
              )}
            </div>
          ))}
          {!query.data?.length && <EmptyState title={tr("暂无执行历史")} />}
        </div>
      </DialogContent>
    </Dialog>
  );
}
