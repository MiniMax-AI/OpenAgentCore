import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BookOpen, Plus, Upload, Trash2 } from "lucide-react";
import type { Skill } from "@oac/agents-client";
import { api, readHistory } from "./lib/api";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Textarea } from "./components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./components/ui/dialog";
import { ErrorNotice, Field, Help, PageHeader } from "./components/shared";
import { EmptyState } from "./components/ui/empty-state";
export const useSkills = () =>
  useQuery({
    queryKey: ["skills"],
    queryFn: ({ signal }) =>
      readHistory((options) => api.listSkills(options), signal),
  });
export function Skills() {
  const query = useSkills();
  const cache = useQueryClient();
  const [selected, setSelected] = useState<Skill | null>(null);
  const [creating, setCreating] = useState(false);
  const [search, setSearch] = useState("");
  const upload = useMutation({
    mutationFn: (file: File) =>
      api.uploadSkill({ kind: "zip", file, filename: file.name }),
    onSuccess: () => cache.invalidateQueries({ queryKey: ["skills"] }),
  });
  return (
    <>
      <PageHeader title="Skills">
        <Help>
          Skills 是可复用的执行方法。上传 ZIP，或用 SKILL.md 创建。绑定到 Agent
          后，托管任务会加载这些技能；纯文本环境不支持。
        </Help>
        <Button variant="outline" asChild>
          <label className="cursor-pointer">
            <Upload />
            {upload.isPending ? "上传中…" : "上传 ZIP"}
            <input
              aria-label="上传 Skill ZIP"
              type="file"
              accept=".zip"
              className="sr-only"
              disabled={upload.isPending}
              onChange={(e) => {
                const file = e.target.files?.[0];
                if (file) upload.mutate(file);
                e.target.value = "";
              }}
            />
          </label>
        </Button>
        <Button onClick={() => setCreating(true)}>
          <Plus />
          创建 Skill
        </Button>
      </PageHeader>
      <ErrorNotice error={query.error || upload.error} />
      <div className="px-6 py-4">
        <Input
          aria-label="搜索 Skills"
          placeholder="搜索技能"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
        />
      </div>
      <div className="min-h-0 flex-1 overflow-auto px-6 pb-6">
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {query.data
            ?.filter((skill) =>
              `${skill.name} ${skill.description}`
                .toLowerCase()
                .includes(search.toLowerCase()),
            )
            .map((skill) => (
              <button
                key={skill.id}
                onClick={() => setSelected(skill)}
                className="rounded-xl border border-line p-5 text-left transition-colors hover:bg-surface-subtle focus-visible:ring-2 focus-visible:ring-accent"
              >
                <div className="flex items-center gap-3">
                  <BookOpen className="h-5 w-5" />
                  <h2 className="min-w-0 flex-1 truncate text-lg font-semibold">
                    {skill.name}
                  </h2>
                  <span className="text-base text-fg-muted">
                    v{skill.default_version}
                  </span>
                </div>
                <p className="mt-4 line-clamp-3 text-base leading-relaxed text-fg-muted">
                  {skill.description}
                </p>
              </button>
            ))}
        </div>
        {!query.isPending && !query.data?.length && (
          <EmptyState title="把常用方法保存为 Skill" />
        )}
      </div>
      {creating && <SkillCreate close={() => setCreating(false)} />}
      {selected && (
        <SkillDetail skill={selected} close={() => setSelected(null)} />
      )}
    </>
  );
}
function SkillCreate({ close }: { close: () => void }) {
  const cache = useQueryClient();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [instructions, setInstructions] = useState("");
  const save = useMutation({
    mutationFn: () =>
      api.uploadSkill({
        kind: "directory",
        files: [
          {
            path: `${name}/SKILL.md`,
            file: new Blob(
              [
                `---\nname: ${JSON.stringify(name)}\ndescription: ${JSON.stringify(description)}\n---\n\n${instructions}\n`,
              ],
              { type: "text/markdown" },
            ),
          },
        ],
      }),
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["skills"] });
      close();
    },
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) close();
      }}
    >
      <DialogContent
        aria-describedby={undefined}
        className="max-h-[90dvh] overflow-y-auto"
      >
        <DialogHeader>
          <DialogTitle>创建 Skill</DialogTitle>
        </DialogHeader>
        <form
          id="skill-form"
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field label="技能名称" id="skill-name">
            <Input
              id="skill-name"
              value={name}
              required
              maxLength={64}
              pattern="[a-z0-9]+(-[a-z0-9]+)*"
              placeholder="例如 code-review"
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label="用途" id="skill-description">
            <Input
              id="skill-description"
              value={description}
              required
              maxLength={500}
              onChange={(e) => setDescription(e.target.value)}
            />
          </Field>
          <Field label="执行方法" id="skill-instructions">
            <Textarea
              id="skill-instructions"
              value={instructions}
              required
              rows={9}
              onChange={(e) => setInstructions(e.target.value)}
              placeholder="什么时候使用、具体步骤、预期输出"
            />
          </Field>
          <ErrorNotice error={save.error} />
        </form>
        <DialogFooter>
          <Button type="submit" form="skill-form" disabled={save.isPending}>
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
function SkillDetail({ skill, close }: { skill: Skill; close: () => void }) {
  const cache = useQueryClient();
  const versions = useQuery({
    queryKey: ["skill-versions", skill.id],
    queryFn: ({ signal }) =>
      readHistory(
        (options) => api.listSkillVersions(skill.id, options),
        signal,
      ),
  });
  const current = useQuery({
    queryKey: ["skill", skill.id],
    queryFn: () => api.retrieveSkill(skill.id),
    initialData: skill,
  });
  const refresh = () => {
    void cache.invalidateQueries({ queryKey: ["skills"] });
    void cache.invalidateQueries({ queryKey: ["skill", skill.id] });
    void cache.invalidateQueries({ queryKey: ["skill-versions", skill.id] });
  };
  const change = useMutation({
    mutationFn: (version: string) =>
      api.updateSkillDefaultVersion(skill.id, version),
    onSuccess: refresh,
  });
  const upload = useMutation({
    mutationFn: (file: File) =>
      api.uploadSkillVersion(
        skill.id,
        { kind: "zip", file, filename: file.name },
        { setDefault: true },
      ),
    onSuccess: refresh,
  });
  const remove = useMutation({
    mutationFn: () => api.deleteSkill(skill.id),
    onSuccess: () => {
      refresh();
      close();
    },
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (
          !open &&
          !remove.isPending &&
          !upload.isPending &&
          !change.isPending
        )
          close();
      }}
    >
      <DialogContent
        aria-describedby={undefined}
        className="max-h-[90dvh] overflow-y-auto"
      >
        <DialogHeader>
          <DialogTitle>{current.data.name}</DialogTitle>
        </DialogHeader>
        <p className="text-base leading-relaxed">{current.data.description}</p>
        <h3 className="font-semibold">版本</h3>
        <ErrorNotice
          error={
            versions.error ||
            current.error ||
            change.error ||
            upload.error ||
            remove.error
          }
        />
        <div className="space-y-2">
          {versions.data?.map((version) => (
            <div
              key={version.id}
              className="flex items-center justify-between rounded-lg border border-line p-3"
            >
              <span>v{version.version}</span>
              {version.version === current.data.default_version ? (
                <span>默认版本</span>
              ) : (
                <Button
                  variant="outline"
                  disabled={change.isPending}
                  onClick={() => change.mutate(version.version)}
                >
                  设为默认
                </Button>
              )}
            </div>
          ))}
        </div>
        <DialogFooter>
          <Help>
            新任务解析默认版本，已启动任务保留技能快照。删除会影响绑定此 Skill
            的新任务，请先解除 Agent 中的绑定。
          </Help>
          <Button
            variant="ghost"
            aria-label="删除 Skill"
            disabled={remove.isPending}
            onClick={() => {
              if (confirm("删除此 Skill 及全部版本？请先解除 Agent 中的绑定。"))
                remove.mutate();
            }}
          >
            <Trash2 />
          </Button>
          <Button asChild>
            <label className="cursor-pointer">
              <Upload />
              {upload.isPending ? "上传中…" : "上传新版本"}
              <input
                aria-label="上传 Skill 新版本"
                type="file"
                accept=".zip"
                className="sr-only"
                disabled={upload.isPending}
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) upload.mutate(file);
                  e.target.value = "";
                }}
              />
            </label>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
