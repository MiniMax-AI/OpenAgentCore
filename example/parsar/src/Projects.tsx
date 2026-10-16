import { tr } from "./lib/i18n";
import { Ledger, LedgerHeader, LedgerRow } from "./components/ui/ledger";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Folder, Plus, Pencil } from "lucide-react";
import {
  product,
  useProjects,
  useTasks,
  type ProjectRecord,
} from "./lib/product";
import { PageHeader, Field, ErrorNotice } from "./components/shared";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import { EmptyState } from "./components/ui/empty-state";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
export function Projects() {
  const projects = useProjects(),
    tasks = useTasks();
  const [editing, setEditing] = useState<Partial<ProjectRecord> | null>(null);
  return (
    <>
      <PageHeader title={tr("项目")}>
        <Button onClick={() => setEditing({})}>
          <Plus />
          {tr("新建项目")}
        </Button>
      </PageHeader>
      <ErrorNotice
        error={projects.error || tasks.error}
        retry={() => void projects.refetch()}
      />
      <Ledger columns="18px minmax(180px,2fr) minmax(180px,2fr) 100px 120px 30px">
        <LedgerHeader>
          <span />
          <span>{tr("项目")}</span>
          <span>{tr("背景")}</span>
          <span>{tr("任务")}</span>
          <span>{tr("完成进度")}</span>
          <span />
        </LedgerHeader>
        {projects.data?.map((project) => {
          const related =
              tasks.data?.filter((task) => task.project_id === project.id) ||
              [],
            done = related.filter((task) => task.status === "done").length;
          return (
            <LedgerRow key={project.id}>
              <Folder className="h-3.5 w-3.5 text-fg-muted" />
              <a
                href={`#/tasks?project=${project.id}`}
                className="truncate font-medium"
              >
                {project.name}
              </a>
              <span className="text-fg-muted">{project.description}</span>
              <span className="font-mono text-xs">
                {done} / {related.length}
              </span>
              <progress
                className="h-1.5 w-full accent-accent"
                max={Math.max(related.length, 1)}
                value={done}
                aria-label={tr("{{value0}} 完成进度", { value0: project.name })}
              />
              <Button
                variant="ghost"
                size="icon"
                aria-label={tr("编辑项目 {{value0}}", { value0: project.name })}
                onClick={() => setEditing(project)}
              >
                <Pencil />
              </Button>
            </LedgerRow>
          );
        })}
        {!projects.isPending && !projects.data?.length && (
          <EmptyState
            title={tr("从一个项目开始")}
            description={tr("记录项目背景，然后把具体工作交给 Agent。")}
          />
        )}
      </Ledger>
      {editing && (
        <ProjectEditor
          key={editing.id || "new"}
          value={editing}
          close={() => setEditing(null)}
        />
      )}
    </>
  );
}
function ProjectEditor({
  value,
  close,
}: {
  value: Partial<ProjectRecord>;
  close: () => void;
}) {
  const cache = useQueryClient();
  const [form, setForm] = useState(() => ({
    id: crypto.randomUUID(),
    name: "",
    description: "",
    repository: "",
    ...value,
  }));
  const save = useMutation({
    mutationFn: () => product(`projects/${form.id}`, "PUT", form),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["projects"] });
      close();
    },
  });
  const remove = useMutation({
    mutationFn: () => product(`projects/${form.id}`, "DELETE", form),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["projects"] });
      close();
    },
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
            {value.id ? tr("编辑项目") : tr("新建项目")}
          </DialogTitle>
        </DialogHeader>
        <form
          id="project-form"
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate();
          }}
        >
          <Field label={tr("项目名称")} id="project-name">
            <Input
              id="project-name"
              required
              maxLength={80}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </Field>
          <Field label={tr("项目背景")} id="project-description">
            <Textarea
              id="project-description"
              rows={6}
              maxLength={40000}
              placeholder={tr("目标、约束、交付标准和 Agent 需要了解的背景")}
              value={form.description}
              onChange={(e) =>
                setForm({ ...form, description: e.target.value })
              }
            />
          </Field>
          <Field label={tr("仓库地址（可选）")} id="project-repository">
            <Input
              id="project-repository"
              type="url"
              placeholder="https://github.com/team/project"
              value={form.repository}
              onChange={(e) => setForm({ ...form, repository: e.target.value })}
            />
          </Field>
          <p className="text-sm text-fg-muted">
            {tr(
              "项目背景会随任务发送。仓库地址作为上下文；访问权限和检出由执行环境负责。",
            )}
          </p>
          <ErrorNotice error={save.error || remove.error} />
        </form>
        <DialogFooter>
          {value.id && (
            <Button
              variant="ghost"
              disabled={save.isPending || remove.isPending}
              onClick={() => {
                if (confirm(tr("删除这个项目？项目有任务时不能删除。")))
                  remove.mutate();
              }}
            >
              {tr("删除项目")}
            </Button>
          )}
          <Button
            type="submit"
            form="project-form"
            disabled={save.isPending || remove.isPending}
          >
            {save.isPending ? tr("保存中\u2026") : tr("保存")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
