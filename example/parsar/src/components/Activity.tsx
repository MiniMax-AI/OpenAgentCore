import { withoutFeishuSource } from "../../shared/feishu-source.mjs";
import { MediaImage, ArtifactAttachment } from "./Media";
import { artifactContentPath, qrImagePattern } from "../../shared/media.mjs";
import { tr } from "../lib/i18n";
import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Check, Copy, Terminal, ChevronRight } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import type { SessionItem, SessionArtifact } from "@oac/agents-client";
import { Button } from "./ui/button";
export function Thinking() {
  const reduce = useReducedMotion();
  return (
    <div role="status" className="py-4 text-base text-fg-muted">
      <motion.span
        className="inline-block bg-clip-text"
        style={
          reduce
            ? {}
            : {
                color: "transparent",
                backgroundSize: "250% 100%",
                backgroundImage:
                  "linear-gradient(90deg,var(--color-fg-muted) 35%,var(--color-fg) 50%,var(--color-fg-muted) 65%)",
              }
        }
        animate={
          reduce ? {} : { backgroundPosition: ["100% center", "0% center"] }
        }
        transition={{ repeat: Infinity, duration: 2.5, ease: "linear" }}
      >
        {tr("Agent 正在执行\u2026")}
      </motion.span>
    </div>
  );
}
function CodeBlock({ children }: { children?: React.ReactNode }) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  return (
    <div className="not-prose group relative my-4 rounded-lg border border-line bg-surface-subtle">
      <pre className="m-0 overflow-x-auto p-4 pr-12 text-sm text-fg">
        {children}
      </pre>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="absolute right-2 top-2"
        aria-label={
          failed
            ? tr("复制失败，请手动选择代码")
            : copied
              ? tr("已复制")
              : tr("复制代码")
        }
        onClick={async (e) => {
          const content =
            e.currentTarget.parentElement?.querySelector("pre")?.textContent ||
            "";
          try {
            await navigator.clipboard.writeText(content);
            setCopied(true);
            setFailed(false);
          } catch {
            setFailed(true);
          }
        }}
      >
        {copied ? <Check /> : <Copy />}
      </Button>
      {failed && (
        <p role="alert" className="px-4 pb-3 text-sm">
          {tr("复制失败，请手动选择代码。")}
        </p>
      )}
    </div>
  );
}
function Reasoning({ item }: { item: SessionItem }) {
  const [expanded, setExpanded] = useState(false);
  const active = item.status === "in_progress";
  const text = item.summary?.map((part) => part.text).join("\n\n") || "";
  const contentId = `reasoning-${item.id}`;
  return (
    <section aria-label={tr("思考过程")} className="py-3 text-sm text-fg-muted">
      <button
        type="button"
        aria-expanded={expanded}
        aria-controls={contentId}
        onClick={() => setExpanded(!expanded)}
        className="flex cursor-pointer items-center gap-1.5 rounded py-1 hover:text-fg focus-visible:outline-2 focus-visible:outline-offset-4"
      >
        {active ? tr("正在思考…") : tr("思考过程")}
        <ChevronRight className={`h-3.5 w-3.5 transition-transform ${expanded ? "rotate-90" : ""}`} />
      </button>
      {active && !expanded && text && (
        <p className="mt-1 line-clamp-2 whitespace-pre-wrap break-words leading-relaxed">
          {text.length > 240 ? `…${text.slice(-240)}` : text}
        </p>
      )}
      <div id={contentId} hidden={!expanded} className="mt-2 whitespace-pre-wrap break-words leading-relaxed">
        {text}
      </div>
    </section>
  );
}

const artifactPaths = (file: SessionArtifact) => [file.path, file.path.replace(/^\/workspace\//, "./"), file.path.replace(/^\/workspace\//, "")];
export function Activity({ item, sessionId = "", artifacts = [] }: { item: SessionItem; sessionId?: string; artifacts?: SessionArtifact[] }) {
  const resolveArtifact = (path = "") => [...artifacts].reverse().find((file) => artifactPaths(file).includes(path));
  const mediaSource = (path = "") => {
    if (qrImagePattern.test(path)) return path;
    const artifact = resolveArtifact(path);
    return artifact ? artifactContentPath(sessionId, artifact.id) : "";
  };
  if (item.type === "reasoning") return <Reasoning item={item} />;
  if (item.type === "message") {
    const text =
      item.content
        ?.filter(
          (part) => part.type === "input_text" || part.type === "output_text",
        )
        .map((part) => part.text || "")
        .join("\n") || "";
    if (item.role === "user")
      return (
        <div className="flex justify-end py-3">
          <div className="max-w-[80%] whitespace-pre-wrap break-words rounded-3xl bg-surface-muted px-5 py-3 text-base leading-relaxed">
            {withoutFeishuSource(text)}
            {item.content?.filter((part) => part.type === "input_image").map((part, index) =>
              typeof part.image_url === "string" && /^data:image\/(png|jpeg|webp);base64,[A-Za-z0-9+/=]+$/.test(part.image_url)
                ? <img key={index} src={part.image_url} alt={tr("图片")} className="mt-2 max-h-80 max-w-full rounded-lg object-contain" /> : null)}
          </div>
        </div>
      );
    return (
      <article
        aria-label={tr("Agent 回复")}
        className="py-4 text-base leading-relaxed"
      >
        <div className="prose max-w-none text-base text-fg [overflow-wrap:anywhere] prose-headings:text-fg prose-headings:font-medium prose-strong:text-fg prose-strong:font-medium prose-code:text-fg prose-a:text-fg prose-a:underline prose-blockquote:text-fg-muted prose-blockquote:border-line prose-code:before:content-none prose-code:after:content-none">
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              h1: ({ children }) => <h2>{children}</h2>,
              pre: CodeBlock,
              a: ({ children, href }) => (
                <a href={resolveArtifact(href) ? mediaSource(href) : href} target="_blank" rel="noreferrer">
                  {children}
                </a>
              ),
              img: ({ alt, src }) => {
                const source = mediaSource(typeof src === "string" ? src : "");
                return source ? <MediaImage src={source} alt={alt || tr("图片")} /> : <span>{alt || tr("图片")}{tr("（请将图片保存为会话产物后展示）")}</span>;
              },
            }}
          >
            {text}
          </ReactMarkdown>
        </div>
      </article>
    );
  }
  const name =
    item.type === "command_execution"
      ? item.command
      : item.type === "web_search_call"
        ? [tr("搜索网页"), item.action?.query || item.action?.url]
            .filter(Boolean)
            .join(" · ")
        : item.name || item.type;
  const detail = item.output ?? item.arguments ?? item.action;
  return (
    <details className="group py-1 text-sm">
      <summary className="flex cursor-pointer list-none items-center gap-2 py-1.5 text-fg-muted hover:text-fg">
        <ChevronRight className="h-4 w-4 shrink-0 transition-transform group-open:rotate-90" />
        <Terminal className="h-4 w-4 shrink-0" />
        <span className="min-w-0 truncate text-fg">{name}</span>
        {item.status === "failed" && <span>{tr("失败")}</span>}
      </summary>
      <pre className="my-2 max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-xl bg-surface-subtle p-4 text-sm">
        {typeof detail === "string"
          ? detail
          : JSON.stringify(detail ?? item, null, 2)}
      </pre>
    </details>
  );
}

export function SessionTimeline({ items, sessionId = "", artifacts = [] }: { items: SessionItem[]; sessionId?: string; artifacts?: SessionArtifact[] }) {
  const groups: { id: string; entry?: SessionItem; steps: SessionItem[] }[] =
    [];
  for (const item of items) {
    if (item.type === "message" || item.type === "reasoning")
      groups.push({ id: item.id, entry: item, steps: [] });
    else {
      const last = groups.at(-1);
      if (last && !last.entry && last.steps.at(-1)?.turn_id === item.turn_id)
        last.steps.push(item);
      else groups.push({ id: item.id, steps: [item] });
    }
  }
  const turnOrder = new Map<string, number>();
  for (const item of items) if (!turnOrder.has(item.turn_id)) turnOrder.set(item.turn_id, turnOrder.size);
  const availableArtifacts = (item: SessionItem) => artifacts
    .filter((file) => (turnOrder.get(file.turn_id) ?? Infinity) <= (turnOrder.get(item.turn_id) ?? -1))
    .sort((a, b) => turnOrder.get(a.turn_id)! - turnOrder.get(b.turn_id)!);
  const lastGroup = new Map<string, string>();
  for (const group of groups) lastGroup.set((group.entry || group.steps.at(-1))?.turn_id || "", group.id);
  const files = (rows: SessionArtifact[]) => rows.map((file) => {
    const inline = items.some((item) => item.turn_id === file.turn_id && item.role === "assistant" && item.content?.some((part) =>
      typeof part.text === "string" && [...part.text.matchAll(/!\[[^\]\n]*\]\(([^)]+)\)/g)].some((match) => artifactPaths(file).includes(match[1] || ""))));
    return <ArtifactAttachment key={file.id} sessionId={sessionId} file={file} preview={!inline} />;
  });
  return <>{groups.map((group) => (
    <div
      id={`item-${group.id}`}
      data-turn-key={group.entry?.role === "user" ? group.id : undefined}
      key={group.id}
    >
      {group.entry ? (
        <Activity item={group.entry} sessionId={sessionId} artifacts={availableArtifacts(group.entry)} />
      ) : (
        <details className="group/steps my-3 rounded-2xl border border-line-muted px-4 py-2.5">
          <summary className="flex cursor-pointer list-none items-center gap-2 text-sm text-fg-muted hover:text-fg">
            <ChevronRight className="h-4 w-4 shrink-0 transition-transform group-open/steps:rotate-90" />
            {tr("执行过程")} · {group.steps.length} {tr("个步骤")}
            {group.steps.some((step) => step.status === "failed") && (
              <span className="text-danger">· {tr("有步骤失败")}</span>
            )}
          </summary>
          <div className="mt-2 border-l border-line pl-4">
            {group.steps.map((item) => (
              <Activity key={item.id} item={item} />
            ))}
          </div>
        </details>
      )}
      {files(artifacts.filter((file) => lastGroup.get(file.turn_id) === group.id))}
    </div>
  ))}{files(artifacts.filter((file) => !lastGroup.has(file.turn_id)))}</>;
}
