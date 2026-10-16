import { product } from "./lib/product";
import { Select, SelectOption } from "./components/ui/select";
import { unzipSync, strFromU8 } from "fflate";
import { tr } from "./lib/i18n";
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
  const [directory, setDirectory] = useState(false);
  const [search, setSearch] = useState("");
  const upload = useMutation({
    mutationFn: (file: File) =>
      api.uploadSkill({ kind: "zip", file, filename: file.name }),
    onSuccess: () => cache.invalidateQueries({ queryKey: ["skills"] }),
  });
  return (
    <>
      <PageHeader title="Skills">
        <Button variant="outline" onClick={() => setDirectory(true)}>
          {tr("浏览目录")}
        </Button>
        <Help>
          {tr(
            "Skills 是可复用的执行方法。上传 ZIP，或用 SKILL.md 创建；绑定到 Agent 后，每个新会话自动加载。",
          )}
        </Help>
        <Button variant="outline" asChild>
          <label className="cursor-pointer">
            <Upload />
            {upload.isPending ? tr("上传中\u2026") : tr("上传 ZIP")}
            <input
              aria-label={tr("上传 Skill ZIP")}
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
          {tr("创建 Skill")}
        </Button>
      </PageHeader>
      <ErrorNotice error={query.error || upload.error} />
      <div className="border-b border-line px-6 py-2">
        <Input
          aria-label={tr("搜索 Skills")}
          placeholder={tr("搜索技能")}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
        />
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        <div className="flex flex-col divide-y divide-line-muted">
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
                className="flex min-h-9 items-center gap-6 px-6 text-left transition-colors hover:bg-surface-subtle focus-visible:ring-2 focus-visible:ring-accent"
              >
                <div className="flex min-w-0 flex-1 items-center gap-3">
                  <BookOpen className="h-3.5 w-3.5" />
                  <h2 className="min-w-0 flex-1 truncate text-sm font-medium">
                    {skill.name}
                  </h2>
                  <span className="text-base text-fg-muted">
                    v{skill.default_version}
                  </span>
                </div>
                <p className="w-1/2 truncate text-xs text-fg-muted">
                  {skill.description}
                </p>
              </button>
            ))}
        </div>
        {!query.isPending && !query.data?.length && (
          <EmptyState title={tr("把常用方法保存为 Skill")} />
        )}
      </div>
      {directory && <SkillsDirectory close={() => setDirectory(false)} />}
      {creating && <SkillCreate close={() => setCreating(false)} />}
      {selected && (
        <SkillDetail skill={selected} close={() => setSelected(null)} />
      )}
    </>
  );
}
export function SkillCreate({
  close,
  onSaved,
}: {
  close: () => void;
  onSaved?: (id: string) => void;
}) {
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
    onSuccess: (skill) => {
      onSaved?.(skill.id);
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
          <DialogTitle>{tr("创建 Skill")}</DialogTitle>
        </DialogHeader>
        <form
          id="skill-form"
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            e.stopPropagation();
            save.mutate();
          }}
        >
          <Field label={tr("技能名称")} id="skill-name">
            <Input
              id="skill-name"
              value={name}
              required
              maxLength={64}
              pattern="[a-z0-9]+(-[a-z0-9]+)*"
              placeholder={tr("例如 code-review")}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label={tr("用途")} id="skill-description">
            <Input
              id="skill-description"
              value={description}
              required
              maxLength={500}
              onChange={(e) => setDescription(e.target.value)}
            />
          </Field>
          <Field label={tr("执行方法")} id="skill-instructions">
            <Textarea
              id="skill-instructions"
              value={instructions}
              required
              rows={9}
              onChange={(e) => setInstructions(e.target.value)}
              placeholder={tr("什么时候使用、具体步骤、预期输出")}
            />
          </Field>
          <ErrorNotice error={save.error} />
        </form>
        <DialogFooter>
          <Button type="submit" form="skill-form" disabled={save.isPending}>
            {tr("保存")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
export function SkillDetail({
  skill,
  close,
}: {
  skill: Skill;
  close: () => void;
}) {
  const cache = useQueryClient();
  const [preview, setPreview] = useState<string>("");
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
        <h3 className="font-semibold">{tr("版本")}</h3>
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
              <Button
                variant="ghost"
                onClick={() => setPreview(version.version)}
              >
                {tr("查看文件")}
              </Button>
              {version.version === current.data.default_version ? (
                <span>{tr("默认版本")}</span>
              ) : (
                <Button
                  variant="outline"
                  disabled={change.isPending}
                  onClick={() => change.mutate(version.version)}
                >
                  {tr("设为默认")}
                </Button>
              )}
            </div>
          ))}
        </div>
        {preview && <SkillFiles id={skill.id} version={preview} />}
        <DialogFooter>
          <Help>
            {tr(
              "新任务解析默认版本，已启动任务保留技能快照。删除会影响绑定此 Skill 的新任务，请先解除 Agent 中的绑定。",
            )}
          </Help>
          <Button
            variant="ghost"
            aria-label={tr("删除 Skill")}
            disabled={remove.isPending}
            onClick={() => {
              if (
                confirm(
                  tr("删除此 Skill 及全部版本？请先解除 Agent 中的绑定。"),
                )
              )
                remove.mutate();
            }}
          >
            <Trash2 />
          </Button>
          <Button asChild>
            <label className="cursor-pointer">
              <Upload />
              {upload.isPending ? tr("上传中\u2026") : tr("上传新版本")}
              <input
                aria-label={tr("上传 Skill 新版本")}
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

function SkillFiles({ id, version }: { id: string; version: string }) {
  const [selected, setSelected] = useState("");
  const query = useQuery({
    queryKey: ["skill-content", id, version],
    queryFn: async () => {
      const content = await api.downloadSkillVersion(id, version);
      let total = 0;
      return unzipSync(new Uint8Array(await content.data.arrayBuffer()), {
        filter: (file) => {
          total += file.originalSize;
          if (total > 32 * 1024 * 1024)
            throw new Error(tr("文件预览超过 32 MiB 限制。"));
          return !file.name.endsWith("/");
        },
      });
    },
  });
  const names = Object.keys(query.data || {}).sort();
  const path = names.includes(selected)
    ? selected
    : names.find((name) => name.endsWith("SKILL.md")) || names[0];
  const bytes = path ? query.data?.[path] : undefined;
  return (
    <section className="border border-line">
      <div className="border-b px-3 py-2 text-sm">
        v{version} · {tr("文件预览")}
      </div>
      <ErrorNotice error={query.error} />
      <div className="flex h-64">
        <nav
          aria-label={tr("技能文件")}
          className="w-1/3 overflow-auto border-r border-line"
        >
          {names.map((name) => (
            <button
              type="button"
              key={name}
              className={`block w-full truncate px-2 py-1 text-left text-xs ${name === path ? "bg-surface-muted" : ""}`}
              onClick={() => setSelected(name)}
              title={name}
            >
              {name}
            </button>
          ))}
        </nav>
        <pre className="min-w-0 flex-1 overflow-auto whitespace-pre-wrap p-3 text-xs">
          {bytes
            ? bytes.length > 256 * 1024
              ? tr("文件过大，请下载查看。")
              : bytes.includes(0)
                ? tr("二进制文件")
                : strFromU8(bytes)
            : tr("正在加载…")}
        </pre>
      </div>
    </section>
  );
}

interface RegistryPreview {
  source: string;
  slug: string;
  archive: string;
  files: string[];
  instructions: string;
}
function SkillsDirectory({ close }: { close: () => void }) {
  const [search, setSearch] = useState(""),
    [source, setSource] = useState(""),
    [slug, setSlug] = useState(""),
    [target, setTarget] = useState("");
  const cache = useQueryClient(),
    skills = useSkills();
  const catalog = useQuery({
    queryKey: ["skills-directory"],
    staleTime: 300000,
    queryFn: async ({ signal }) => {
      const response = await fetch(
        "https://agent-skill-index.vercel.app/data/latest/skills.json",
        { signal, credentials: "omit" },
      );
      if (!response.ok)
        throw new Error(tr("技能目录暂不可用，可直接输入仓库和技能名称。"));
      const value = await response.json();
      if (!Array.isArray(value.data))
        throw new Error(tr("技能目录返回了无法识别的数据。"));
      return value.data.filter(
        (item: { source?: unknown; slug?: unknown; name?: unknown }) =>
          typeof item.source === "string" &&
          typeof item.slug === "string" &&
          typeof item.name === "string",
      ) as { source: string; slug: string; name: string }[];
    },
  });
  const preview = useMutation({
    mutationFn: () =>
      product<RegistryPreview>("skills/registry/preview", "POST", {
        source,
        slug,
      }),
  });
  const install = useMutation({
    mutationFn: async () => {
      const value = preview.data!;
      const file = new Blob(
        [Uint8Array.from(atob(value.archive), (char) => char.charCodeAt(0))],
        { type: "application/zip" },
      );
      const input = {
        kind: "zip" as const,
        file,
        filename: `${value.slug}.zip`,
      };
      return target
        ? api.uploadSkillVersion(target, input, { setDefault: true })
        : api.uploadSkill(input);
    },
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ["skills"] });
      if (target) {
        void cache.invalidateQueries({ queryKey: ["skill", target] });
        void cache.invalidateQueries({ queryKey: ["skill-versions", target] });
      }
      close();
    },
  });
  const busy = preview.isPending || install.isPending;
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) close();
      }}
    >
      <DialogContent
        aria-describedby={undefined}
        className="max-h-[90dvh] max-w-2xl overflow-auto"
      >
        <DialogHeader>
          <DialogTitle>{tr("Skills 目录")}</DialogTitle>
        </DialogHeader>
        <ErrorNotice error={catalog.error || preview.error || install.error} />
        <Input
          aria-label={tr("搜索技能目录")}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder={tr("搜索热门技能")}
        />
        <div className="max-h-40 divide-y divide-line overflow-auto">
          {catalog.data
            ?.filter((item) =>
              `${item.name} ${item.source}`
                .toLowerCase()
                .includes(search.toLowerCase()),
            )
            .slice(0, 40)
            .map((item) => (
              <button
                disabled={busy}
                className="flex w-full items-center justify-between gap-3 py-2 text-left text-sm hover:bg-surface-subtle"
                key={`${item.source}/${item.slug}`}
                onClick={() => {
                  setSource(item.source);
                  setSlug(item.slug);
                  preview.reset();
                }}
              >
                <span>{item.name}</span>
                <span className="truncate text-xs text-fg-muted">
                  {item.source}
                </span>
              </button>
            ))}
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={tr("GitHub 仓库")} id="registry-source">
            <Input
              id="registry-source"
              disabled={busy}
              value={source}
              placeholder="owner/repo"
              onChange={(event) => {
                setSource(event.target.value);
                preview.reset();
              }}
            />
          </Field>
          <Field label={tr("Skill 名称")} id="registry-slug">
            <Input
              id="registry-slug"
              disabled={busy}
              value={slug}
              onChange={(event) => {
                setSlug(event.target.value);
                preview.reset();
              }}
            />
          </Field>
        </div>
        <Button
          variant="outline"
          disabled={busy || !source || !slug}
          onClick={() => preview.mutate()}
        >
          {preview.isPending ? tr("正在下载…") : tr("下载并预览")}
        </Button>
        {preview.data && (
          <>
            <div className="text-xs text-fg-muted">
              {preview.data.files.join(" · ")}
            </div>
            <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded-lg border border-line p-3 text-xs">
              {preview.data.instructions}
            </pre>
            <Field label={tr("安装目标")} id="registry-target">
              <Select
                id="registry-target"
                value={target}
                disabled={busy}
                onValueChange={setTarget}
              >
                <SelectOption value="">{tr("创建新 Skill")}</SelectOption>
                {skills.data?.map((skill) => (
                  <SelectOption key={skill.id} value={skill.id}>
                    {skill.name} · {tr("上传为新版本")}
                  </SelectOption>
                ))}
              </Select>
            </Field>
            <Button disabled={busy} onClick={() => install.mutate()}>
              {tr("安装 Skill")}
            </Button>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
