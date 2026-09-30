import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Check, Copy, Terminal, ChevronRight } from "lucide-react";
import { motion, useReducedMotion } from "motion/react";
import type { SessionItem } from "@oac/agents-client";
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
        Agent 正在执行…
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
          failed ? "复制失败，请手动选择代码" : copied ? "已复制" : "复制代码"
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
          复制失败，请手动选择代码。
        </p>
      )}
    </div>
  );
}

export function Activity({ item }: { item: SessionItem }) {
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
          <p className="max-w-[85%] whitespace-pre-wrap break-words rounded-2xl bg-surface-muted px-5 py-3 text-base leading-relaxed">
            {text}
          </p>
        </div>
      );
    return (
      <article
        aria-label="Agent 回复"
        className="py-4 text-base leading-relaxed"
      >
        <div className="prose prose-sm max-w-none text-fg [overflow-wrap:anywhere] prose-headings:text-fg prose-headings:font-medium prose-strong:text-fg prose-strong:font-medium prose-code:text-fg prose-a:text-fg prose-a:underline prose-blockquote:text-fg-muted prose-blockquote:border-line prose-code:before:content-none prose-code:after:content-none">
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              h1: ({ children }) => <h2>{children}</h2>,
              pre: CodeBlock,
              a: ({ children, href }) => (
                <a href={href} target="_blank" rel="noreferrer">
                  {children}
                </a>
              ),
              img: ({ alt }) => (
                <span>{alt || "图片"}（此示例不加载外部图片）</span>
              ),
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
      : item.type === "reasoning"
        ? "思考过程"
        : item.name || item.type;
  const detail =
    item.type === "reasoning"
      ? item.summary?.map((part) => part.text).join("\n")
      : (item.output ?? item.arguments ?? item.action);
  return (
    <details className="group my-2 border-b border-line py-2 text-base">
      <summary className="flex cursor-pointer list-none items-center gap-2 py-1 text-fg-muted">
        <ChevronRight className="h-4 w-4 shrink-0 transition-transform group-open:rotate-90" />
        <Terminal className="h-4 w-4 shrink-0" />
        <span className="min-w-0 truncate text-fg">{name}</span>
        {item.status === "failed" && <span>失败</span>}
      </summary>
      <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words py-3 text-sm">
        {typeof detail === "string"
          ? detail
          : JSON.stringify(detail ?? item, null, 2)}
      </pre>
    </details>
  );
}
