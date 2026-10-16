import { tr } from "./lib/i18n";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Plus,
  Columns3,
  List,
  Search,
  Circle,
  CircleCheck,
  Clock3,
  CircleAlert,
  CircleDot,
} from "lucide-react";
import {
  product,
  useTasks,
  useProjects,
  useAgents,
  useRuntimes,
  taskStatuses,
  taskPriorities,
  type TaskRecord,
} from "./lib/product";
import { PageHeader, Field, ErrorNotice } from "./components/shared";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { Select, SelectOption } from "./components/ui/select";
import { EmptyState } from "./components/ui/empty-state";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
export function TaskStatus({ status }: { status: TaskRecord["status"] }) {
  const Icon = {
    backlog: Circle,
    in_progress: CircleDot,
    blocked: CircleAlert,
    review: Clock3,
    done: CircleCheck,
  }[status];
  return (
    <span className="inline-flex items-center gap-2">
      <Icon
        className={`h-3.5 w-3.5 shrink-0 ${status === "in_progress" ? "text-status-running" : status === "done" ? "text-status-completed" : status === "blocked" ? "text-status-failed" : "text-fg-muted"}`}
      />
      {tr(taskStatuses[status])}
    </span>
  );
}
export function Tasks({ projectId = "" }: { projectId?: string }) {
  const tasks = useTasks(),
    projects = useProjects(),
    agents = useAgents();
  const [creating, setCreating] = useState(false);
  const [search, setSearch] = useState("");
  const [project, setProject] = useState(projectId);
  const [assignee, setAssignee] = useState("");
  const [view, setView] = useState<"board" | "list">("list");
  const selectedProject = projects.data?.find((row) => row.id === project);
  const filtered = (tasks.data || []).filter(
    (row) =>
      (!project || row.project_id === project) &&
      (!assignee || row.agent_id === assignee) &&
      `${row.id} ${row.name} ${row.description}`
        .toLowerCase()
        .includes(search.toLowerCase()),
  );
  const order = Object.keys(taskPriorities);
  filtered.sort(
    (a, b) =>
      order.indexOf(a.priority) - order.indexOf(b.priority) ||
      b.updated_at - a.updated_at,
  );
  return (
    <>
      <PageHeader title={selectedProject?.name || tr("任务")}>
        <Button onClick={() => setCreating(true)}>
          <Plus />
          {tr("新建任务")}
        </Button>
      </PageHeader>
      <div className="flex shrink-0 flex-wrap items-center gap-3 border-b border-line px-6 py-3">
        <div className="relative min-w-40 flex-1">
          <Search className="absolute left-3 top-2.5 h-4 w-4 text-fg-muted" />
          <Input
            aria-label={tr("搜索任务")}
            className="pl-9"
            placeholder={tr("搜索任务\u2026")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <div className="w-40 max-sm:w-full">
          <Select
            aria-label={tr("筛选项目")}
            value={project}
            onValueChange={setProject}
          >
            <SelectOption value="">{tr("所有项目")}</SelectOption>
            {projects.data?.map((row) => (
              <SelectOption key={row.id} value={row.id}>
                {row.name}
              </SelectOption>
            ))}
          </Select>
        </div>
        <div className="w-40 max-sm:w-full">
          <Select
            aria-label={tr("筛选 Agent")}
            value={assignee}
            onValueChange={setAssignee}
          >
            <SelectOption value="">{tr("所有 Agent")}</SelectOption>
            {agents.data?.map((row) => (
              <SelectOption key={row.id} value={row.id}>
                {row.name}
              </SelectOption>
            ))}
          </Select>
        </div>
        <div className="flex gap-1" role="group" aria-label={tr("任务视图")}>
          <Button
            size="icon"
            variant={view === "board" ? "secondary" : "ghost"}
            aria-label={tr("看板视图")}
            aria-pressed={view === "board"}
            onClick={() => setView("board")}
          >
            <Columns3 />
          </Button>
          <Button
            size="icon"
            variant={view === "list" ? "secondary" : "ghost"}
            aria-label={tr("列表视图")}
            aria-pressed={view === "list"}
            onClick={() => setView("list")}
          >
            <List />
          </Button>
        </div>
      </div>
      <ErrorNotice
        error={tasks.error || projects.error || agents.error}
        retry={() => void tasks.refetch()}
      />
      <div
        className={`min-h-0 flex-1 overflow-auto ${view === "board" ? "p-6" : ""}`}
      >
        {!tasks.isPending && !tasks.data?.length ? (
          <EmptyState
            icon={Columns3}
            title={tr("让每一项工作都有着落")}
            description={tr(
              "写下目标，分配给 Agent，跟踪执行过程，再由你验收。",
            )}
            action={
              <Button onClick={() => setCreating(true)}>
                {tr("创建第一个任务")}
              </Button>
            }
          />
        ) : tasks.isPending ? (
          <EmptyState title={tr("正在加载\u2026")} />
        ) : !filtered.length ? (
          <EmptyState
            title={tr("没有匹配的任务")}
            description={tr("试试其他关键词或筛选条件。")}
          />
        ) : view === "board" ? (
          <div className="grid min-w-[1000px] grid-cols-5 gap-4">
            {Object.entries(taskStatuses).map(([status, label]) => {
              const rows = filtered.filter((row) => row.status === status);
              return (
                <section
                  key={status}
                  aria-label={tr(label)}
                  className="min-w-0 rounded-lg bg-surface-subtle p-3"
                >
                  <h2 className="mb-4 flex items-center justify-between text-sm font-medium">
                    <TaskStatus status={status as TaskRecord["status"]} />
                    <span className="text-fg-muted">{rows.length}</span>
                  </h2>
                  <div className="space-y-3">
                    {rows.map((row) => (
                      <a
                        key={row.id}
                        href={`#/tasks/${row.id}`}
                        className="block rounded-lg border border-line bg-surface p-4 app-shadow-control hover:border-line-strong focus-visible:ring-2 focus-visible:ring-accent"
                      >
                        <span className="text-xs text-fg-muted">
                          {projects.data?.find((p) => p.id === row.project_id)
                            ?.name || tr("独立任务")}
                        </span>
                        <h3 className="my-2 break-words font-medium">
                          {row.name}
                        </h3>
                        <p className="mb-4 line-clamp-2 text-sm text-fg-muted">
                          {row.description}
                        </p>
                        <div className="flex items-center justify-between gap-2 text-xs text-fg-muted">
                          <span className="truncate">
                            {agents.data?.find((a) => a.id === row.agent_id)
                              ?.name || tr("未分配")}
                          </span>
                          <span>{tr(taskPriorities[row.priority])}</span>
                        </div>
                      </a>
                    ))}
                  </div>
                </section>
              );
            })}
          </div>
        ) : (
          <div className="overflow-hidden border-y border-line">
            {filtered.map((row) => (
              <a
                key={row.id}
                href={`#/tasks/${row.id}`}
                className="flex min-h-9 flex-wrap items-center gap-4 border-b border-line-muted px-6 text-sm last:border-0 hover:app-hover"
              >
                <span className="w-24 text-sm">
                  <TaskStatus status={row.status} />
                </span>
                <span className="min-w-40 flex-1 font-medium">{row.name}</span>
                <span className="text-sm text-fg-muted">
                  {agents.data?.find((a) => a.id === row.agent_id)?.name ||
                    tr("未分配")}
                </span>
                <span className="w-12 text-sm text-fg-muted">
                  {tr(taskPriorities[row.priority])}
                </span>
              </a>
            ))}
          </div>
        )}
      </div>
      {creating && (
        <TaskEditor
          value={{ project_id: project }}
          close={() => setCreating(false)}
        />
      )}
    </>
  );
}
export function TaskEditor({
  value,
  close,
}: {
  value: Partial<TaskRecord>;
  close: () => void;
}) {
  const cache = useQueryClient(),
    projects = useProjects(),
    agents = useAgents(),
    runtimes = useRuntimes();
  const [form, setForm] = useState(() => ({
    id: crypto.randomUUID(),
    name: "",
    description: "",
    project_id: "",
    agent_id: "",
    runtime_id: "",
    status: "backlog",
    priority: "normal",
    ...value,
  }));
  const save = useMutation({
    mutationFn: () => product<TaskRecord>(`tasks/${form.id}`, "PUT", form),
    onSuccess: (task) => {
      void cache.invalidateQueries({ queryKey: ["tasks"] });
      void cache.invalidateQueries({ queryKey: ["task", task.id] });
      void cache.invalidateQueries({ queryKey: ["task-activity", task.id] });
      close();
      if (!value.id) location.hash = `/tasks/${task.id}`;
    },
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) close();
      }}
    >
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>
            {value.id ? tr("编辑任务") : tr("新建任务")}
          </DialogTitle>
        </DialogHeader>
        <form
          id="task-form"
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate();
          }}
        >
          <Field label={tr("任务标题")} id="task-name">
            <Input
              id="task-name"
              autoFocus
              required
              maxLength={80}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <Field label={tr("任务说明")} id="task-description">
            <Textarea
              id="task-description"
              rows={6}
              maxLength={40000}
              placeholder={tr("要解决什么问题？期待的交付物和验收标准是什么？")}
              value={form.description}
              onChange={(e) =>
                setForm({ ...form, description: e.target.value })
              }
            />
          </Field>
          <div className="grid grid-cols-2 gap-4">
            <Field label={tr("所属项目")} id="task-project">
              <Select
                id="task-project"
                value={form.project_id}
                onValueChange={(project_id) => setForm({ ...form, project_id })}
              >
                <SelectOption value="">{tr("独立任务")}</SelectOption>
                {projects.data?.map((row) => (
                  <SelectOption key={row.id} value={row.id}>
                    {row.name}
                  </SelectOption>
                ))}
              </Select>
            </Field>
            <Field label={tr("优先级")} id="task-priority">
              <Select
                id="task-priority"
                value={form.priority}
                onValueChange={(priority) => setForm({ ...form, priority })}
              >
                {Object.entries(taskPriorities).map(([id, label]) => (
                  <SelectOption key={id} value={id}>
                    {tr(label)}
                  </SelectOption>
                ))}
              </Select>
            </Field>
            <Field label={tr("执行 Agent")} id="task-agent">
              <Select
                id="task-agent"
                value={form.agent_id}
                onValueChange={(agent_id) => setForm({ ...form, agent_id })}
              >
                <SelectOption value="">{tr("暂不分配")}</SelectOption>
                {agents.data?.map((row) => (
                  <SelectOption key={row.id} value={row.id}>
                    {row.name}
                  </SelectOption>
                ))}
              </Select>
            </Field>
            <Field label={tr("任务环境模板")} id="task-runtime">
              <Select
                id="task-runtime"
                value={form.runtime_id}
                onValueChange={(runtime_id) => setForm({ ...form, runtime_id })}
              >
                <SelectOption value="">{tr("稍后选择")}</SelectOption>
                {runtimes.data?.map((row) => (
                  <SelectOption key={row.id} value={row.id}>
                    {row.name}
                  </SelectOption>
                ))}
              </Select>
            </Field>
          </div>
          <p className="text-sm text-fg-muted">
            {tr(
              "保存不会启动执行。修改用于下一次分配，已有会话保留其原始配置。",
            )}
          </p>
          <ErrorNotice
            error={
              save.error || projects.error || agents.error || runtimes.error
            }
          />
        </form>
        <DialogFooter>
          <Button type="submit" form="task-form" disabled={save.isPending}>
            {save.isPending ? tr("保存中\u2026") : tr("保存任务")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
