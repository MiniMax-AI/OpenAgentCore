import { tr } from "./lib/i18n";
import { useEffect, useState } from "react";
import {
  Bot,
  BookOpen,
  Server,
  Moon,
  Sun,
  Send,
  Settings2,
  SquarePen,
  type LucideIcon,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Capabilities } from "./Capabilities";
import { Records, Audit } from "./Records";
import { Settings, type WorkspaceSettings } from "./Settings";
import { product, useSessions } from "./lib/product";
import { Feishu } from "./Feishu";
import { Runtimes } from "./Resources";
import { Agents } from "./Agents";
import { AgentPage } from "./AgentPage";
import { SessionPage } from "./SessionPage";
import { Home } from "./Home";
import { cn } from "./lib/utils";
import { setTheme, useTheme } from "./lib/theme";

const currentRoute = () => location.hash.slice(1) || "/";

function NavLink({
  href,
  label,
  icon: Icon,
  selected,
}: {
  href: string;
  label: string;
  icon: LucideIcon;
  selected: boolean;
}) {
  return (
    <a
      href={`#${href}`}
      aria-current={selected ? "page" : undefined}
      className={cn(
        "flex h-9 shrink-0 items-center gap-3 rounded-[10px] px-3 text-sm transition-colors duration-150",
        selected
          ? "bg-surface font-medium text-fg app-shadow-control"
          : "text-fg-muted hover:app-hover hover:text-fg",
      )}
    >
      <Icon
        strokeWidth={1.7}
        className={cn("h-4 w-4 shrink-0", selected ? "text-fg" : "text-fg-subtle")}
      />
      <span>{label}</span>
    </a>
  );
}

function RecentConversations({ activeId }: { activeId?: string }) {
  const sessions = useSessions();
  const rows = [...(sessions.data || [])]
    .sort((a, b) => b.created_at - a.created_at)
    .slice(0, 40);
  if (!rows.length) return null;
  return (
    <nav aria-label={tr("最近对话")} className="hidden min-h-0 flex-1 flex-col sm:flex">
      <div className="px-3 pb-2 pt-6 text-sm text-fg-subtle">{tr("最近对话")}</div>
      <div className="min-h-0 space-y-0.5 overflow-y-auto pr-1">
        {rows.map((row) => (
          <a
            key={row.id}
            href={`#/sessions/${row.id}`}
            aria-current={row.id === activeId ? "page" : undefined}
            className={cn(
              "flex h-9 items-center gap-2 rounded-[10px] px-3 text-sm transition-colors duration-150",
              row.id === activeId
                ? "bg-surface font-medium text-fg app-shadow-control"
                : "text-fg-muted hover:app-hover hover:text-fg",
            )}
          >
            <span className="min-w-0 flex-1 truncate">{row.name}</span>
            {row.source === "feishu" && (
              <Send
                aria-label={tr("飞书")}
                strokeWidth={1.7}
                className="h-3.5 w-3.5 shrink-0 text-fg-subtle"
              />
            )}
          </a>
        ))}
      </div>
      <a
        href="#/conversations"
        className="mb-4 mt-1 flex h-9 shrink-0 items-center rounded-[10px] px-3 text-sm text-fg-subtle hover:app-hover hover:text-fg"
      >
        {tr("全部对话")}
      </a>
    </nav>
  );
}

export function App() {
  const [route, setRoute] = useState(currentRoute);
  const workspace = useQuery({
    queryKey: ["settings"],
    queryFn: () => product<WorkspaceSettings>("settings"),
  });
  const { dark } = useTheme();
  useEffect(() => {
    const listener = () => setRoute(currentRoute());
    window.addEventListener("hashchange", listener);
    return () => window.removeEventListener("hashchange", listener);
  }, []);
  const [path, search = ""] = route.split("?");
  const params = new URLSearchParams(search);
  const agentId = path!.match(/^\/agents\/([a-f0-9-]{36})$/)?.[1];
  const sessionId = path!.match(/^\/sessions\/([a-f0-9-]{36})$/)?.[1];
  const section = (prefixes: string[]) =>
    prefixes.some((prefix) => path === prefix || path!.startsWith(`${prefix}/`));
  const build = [
    { href: "/agents", label: tr("Agent"), icon: Bot, match: ["/agents"] },
    { href: "/runtimes", label: tr("环境模板"), icon: Server, match: ["/runtimes"] },
    {
      href: "/capabilities",
      label: tr("资源库"),
      icon: BookOpen,
      match: ["/capabilities", "/models", "/skills", "/mcps", "/credentials"],
    },
  ];
  const connect = [
    { href: "/feishu", label: tr("飞书接入"), icon: Send, match: ["/feishu"] },
    { href: "/settings", label: tr("设置"), icon: Settings2, match: ["/settings"] },
  ];
  const groups: [string, typeof build][] = [
    [tr("构建"), build],
    [tr("接入"), connect],
  ];
  const page =
    path === "/" || path === "/new" ? (
      <Home key={route} agentId={params.get("agent") || undefined} />
    ) : sessionId ? (
      <SessionPage key={sessionId} id={sessionId} />
    ) : ["/conversations", "/runs", "/usage"].includes(path!) ? (
      <Records key={path} view={path!.slice(1) as "conversations" | "runs" | "usage"} />
    ) : path === "/audit" ? (
      <Audit />
    ) : path === "/agents/new" ? (
      <AgentPage key={route} copyFrom={params.get("from") || undefined} />
    ) : agentId ? (
      <AgentPage key={agentId} id={agentId} />
    ) : path === "/agents" ? (
      <Agents />
    ) : path === "/runtimes" ? (
      <Runtimes />
    ) : path === "/feishu" ? (
      <Feishu />
    ) : path === "/settings" ? (
      <Settings />
    ) : path === "/models" ? (
      <Capabilities key={path} initialTab="models" />
    ) : path === "/skills" ? (
      <Capabilities key={path} initialTab="skills" />
    ) : path === "/mcps" ? (
      <Capabilities key={path} initialTab="mcps" />
    ) : path === "/credentials" ? (
      <Capabilities key={path} initialTab="credentials" />
    ) : path === "/capabilities" ? (
      <Capabilities key={path} />
    ) : (
      <Home />
    );
  return (
    <div className="flex h-dvh flex-col overflow-hidden bg-surface text-fg sm:flex-row">
      <aside className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line bg-surface-subtle px-3 py-3 sm:w-[264px] sm:flex-col sm:flex-nowrap sm:items-stretch sm:gap-0 sm:border-b-0 sm:border-r sm:px-3 sm:py-4">
        <a href="#/" className="flex h-10 min-w-0 items-center gap-2.5 px-3">
          <img
            src={dark ? "/parsar-mark-dark.png" : "/parsar-mark-light.png"}
            alt=""
            width={22}
            height={22}
          />
          <span className="truncate text-base font-semibold tracking-tight">
            {workspace.data?.name || "Parsar"}
          </span>
        </a>
        <a
          href="#/new"
          className={cn(
            "ml-auto flex h-10 shrink-0 items-center gap-2.5 rounded-[10px] border border-line-strong bg-surface px-3 text-sm font-medium text-fg app-shadow-control transition-colors hover:app-hover sm:ml-0 sm:mt-4",
            (path === "/" || path === "/new") && "ring-1 ring-fg/10",
          )}
        >
          <SquarePen strokeWidth={1.7} className="h-4 w-4" />
          {tr("新对话")}
        </a>
        <RecentConversations activeId={sessionId} />
        <nav
          aria-label={tr("主导航")}
          className="order-last flex w-full min-w-0 gap-1 overflow-x-auto sm:order-none sm:mt-auto sm:flex-col sm:gap-0.5 sm:overflow-visible sm:border-t sm:border-line sm:pt-3"
        >
          {groups.map(([label, items]) => (
            <div key={label} className="flex gap-1 sm:block sm:space-y-0.5 sm:pb-2">
              <div className="hidden px-3 pb-1 pt-2 text-sm text-fg-subtle sm:block">
                {label}
              </div>
              {items.map((item) => (
                <NavLink key={item.href} {...item} selected={section(item.match)} />
              ))}
            </div>
          ))}
        </nav>
        <div className="hidden items-center justify-between px-3 pt-2 sm:flex">
          <span className="text-sm text-fg-subtle">OpenAgentCore</span>
          <button
            type="button"
            aria-label={dark ? tr("切换浅色") : tr("切换深色")}
            onClick={() => setTheme(dark ? "light" : "dark")}
            className="inline-flex h-8 w-8 items-center justify-center rounded-lg text-fg-subtle hover:app-hover hover:text-fg"
          >
            {dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </button>
        </div>
      </aside>
      <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto">
        {page}
      </main>
    </div>
  );
}
