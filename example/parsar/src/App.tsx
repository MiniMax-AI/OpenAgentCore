import { Connections } from "./Connections";
import { ListSlot } from "./components/plugin/SlotRenderer";
import { Inbox } from "./Questions";
import { tr } from "./lib/i18n";
import { useEffect, useState } from "react";
import {
  Bot,
  BookOpen,
  Server,
  Moon,
  Sun,
  Columns3,
  Folder,
  MessageSquare,
  Clock3,
  Play,
  ChartBar,
  ScrollText,
  Settings2,
  Monitor,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Capabilities } from "./Capabilities";
import { Records, Audit } from "./Records";
import { Settings, type WorkspaceSettings } from "./Settings";
import { product } from "./lib/product";
import { useResizableWidth } from "./lib/layout-width";
import { ResizeHandle } from "./components/ui/resize-handle";
import { Schedules } from "./Schedules";
import { Feishu } from "./Feishu";
import { Tasks } from "./Tasks";
import { TaskDetail } from "./TaskDetail";
import { Projects } from "./Projects";
import { Runtimes } from "./Resources";
import { Agents } from "./Agents";
import { SessionPage } from "./SessionPage";
import { AgentDetail } from "./AgentDetail";
import { Button } from "./components/ui/button";
export function App() {
  const [route, setRoute] = useState(location.hash.slice(1) || "/tasks");
  const workspace = useQuery({
    queryKey: ["settings"],
    queryFn: () => product<WorkspaceSettings>("settings"),
  });
  const sidebar = useResizableWidth({
    storageKey: "sidebar",
    defaultWidth: 232,
    min: 200,
    max: 360,
    edge: "right",
  });
  const [theme, setTheme] = useState(() => {
    try {
      return localStorage.getItem("oac-example-theme") || "system";
    } catch {
      return "system";
    }
  });
  const [systemDark, setSystemDark] = useState(
    matchMedia("(prefers-color-scheme: dark)").matches,
  );
  const dark = theme === "dark" || (theme === "system" && systemDark);
  useEffect(() => {
    const media = matchMedia("(prefers-color-scheme: dark)");
    const change = () => setSystemDark(media.matches);
    media.addEventListener("change", change);
    return () => media.removeEventListener("change", change);
  }, []);
  useEffect(() => {
    const listener = () => setRoute(location.hash.slice(1) || "/tasks");
    window.addEventListener("hashchange", listener);
    return () => window.removeEventListener("hashchange", listener);
  }, []);
  useEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    try {
      localStorage.setItem("oac-example-theme", theme);
    } catch {
      /* Theme remains usable without storage. */
    }
  }, [dark, theme]);
  const agentId = route
    .split("?")[0]!
    .match(/^\/agents\/([a-f0-9-]{36})$/)?.[1];
  const sessionId = route.match(/^\/sessions\/([a-f0-9-]{36})$/)?.[1];
  const taskId = route.match(/^\/tasks\/([a-f0-9-]{36})$/)?.[1];
  const projectId =
    new URLSearchParams(route.split("?")[1]).get("project") || "";
  const navigation = [
    { href: "/tasks", label: tr("任务"), icon: Columns3, group: tr("工作") },
    { href: "/projects", label: tr("项目"), icon: Folder, group: tr("工作") },
    {
      href: "/conversations",
      label: tr("对话"),
      icon: MessageSquare,
      group: tr("活动"),
    },
    {
      href: "/inbox",
      label: tr("收件箱"),
      icon: MessageSquare,
      group: tr("活动"),
    },
    { href: "/runs", label: tr("运行记录"), icon: Play, group: tr("活动") },
    {
      href: "/schedules",
      label: tr("定时任务"),
      icon: Clock3,
      group: tr("活动"),
    },
    { href: "/usage", label: tr("用量"), icon: ChartBar, group: tr("活动") },
    {
      href: "/audit",
      label: tr("审计日志"),
      icon: ScrollText,
      group: tr("活动"),
    },
    { href: "/agents", label: tr("Agent 模板"), icon: Bot, group: tr("构建") },
    {
      href: "/runtimes",
      label: tr("环境模板"),
      icon: Server,
      group: tr("构建"),
    },
    {
      href: "/capabilities",
      label: tr("资源库"),
      icon: BookOpen,
      group: tr("构建"),
    },
    {
      href: "/connections",
      label: tr("我的连接"),
      icon: Settings2,
      group: tr("接入与设置"),
    },
    {
      href: "/feishu",
      label: tr("飞书接入"),
      icon: MessageSquare,
      group: tr("接入与设置"),
    },
    {
      href: "/settings",
      label: tr("设置"),
      icon: Settings2,
      group: tr("接入与设置"),
    },
  ];
  return (
    <div className="flex h-dvh flex-col overflow-hidden bg-surface text-fg sm:flex-row">
      <aside
        style={
          { "--sidebar-width": `${sidebar.width}px` } as React.CSSProperties
        }
        className="relative flex shrink-0 flex-wrap items-center gap-2 border-b border-line bg-surface-subtle p-2.5 text-fg sm:w-[var(--sidebar-width)] sm:flex-col sm:flex-nowrap sm:items-stretch sm:gap-0 sm:border-b-0 sm:border-r"
      >
        <a href="#/agents" className="flex h-8 min-w-0 items-center gap-2 px-2">
          <img
            src={dark ? "/parsar-mark-dark.png" : "/parsar-mark-light.png"}
            alt=""
            width={20}
            height={20}
          />
          <span className="truncate text-sm font-medium">
            {workspace.data?.name || "Parsar"}
          </span>
        </a>
        <nav
          aria-label={tr("主导航")}
          className="order-last flex w-full min-w-0 gap-1 overflow-x-auto sm:order-none sm:min-h-0 sm:flex-1 sm:flex-col sm:gap-px sm:overflow-y-auto sm:pt-3.5"
        >
          {navigation.map(({ href, label, icon: Icon, group }, index) => {
            const selected =
              route.split("?")[0] === href ||
              (href === "/capabilities" &&
                ["/models", "/skills", "/mcps", "/credentials"].includes(
                  route,
                )) ||
              (href === "/tasks" && !!taskId) ||
              (href === "/agents" && !!agentId) ||
              (href === "/conversations" && !!sessionId);
            return (
              <div key={href} className="shrink-0">
                {group && group !== navigation[index - 1]?.group && (
                  <div className="hidden px-2 pb-1 pt-3.5 text-xs text-fg-muted sm:block">
                    {group}
                  </div>
                )}
                <a
                  href={`#${href}`}
                  aria-current={selected ? "page" : undefined}
                  className={`flex h-[30px] items-center gap-2 rounded-md px-2 text-base focus-visible:ring-2 focus-visible:ring-accent ${selected ? "bg-surface-muted font-medium text-fg" : "text-fg-muted hover:bg-surface-muted hover:text-fg"}`}
                >
                  <Icon strokeWidth={1.5} className="h-4 w-4 shrink-0" />
                  <span>{label}</span>
                </a>
              </div>
            );
          })}
          <ListSlot slotId="layout.nav.bottom" />
        </nav>
        <div className="ml-auto flex items-center gap-1 sm:ml-0 sm:mt-3 sm:border-t sm:border-line sm:pt-2.5">
          <span className="hidden flex-1 px-2 text-xs text-fg-muted sm:block">
            Parsar
          </span>
          <Button
            variant="ghost"
            size="icon"
            aria-label={dark ? tr("切换浅色") : tr("切换深色")}
            onClick={() => setTheme(dark ? "light" : "dark")}
          >
            {dark ? <Sun /> : <Moon />}
          </Button>
          <Button
            variant="ghost"
            size="icon"
            aria-label={tr("跟随系统主题")}
            aria-pressed={theme === "system"}
            onClick={() => setTheme("system")}
          >
            <Monitor />
          </Button>
        </div>
        <ResizeHandle
          className="hidden sm:block"
          edge="right"
          dragging={sidebar.dragging}
          label={tr("调整侧栏宽度")}
          {...sidebar.handleProps}
        />
        {sidebar.dirty && (
          <div className="absolute bottom-12 left-2 z-40 rounded-md border border-line bg-surface p-2 app-shadow-floating">
            <Button variant="ghost" onClick={sidebar.save}>
              {tr("保存宽度")}
            </Button>
            <Button variant="ghost" onClick={sidebar.restore}>
              {tr("还原")}
            </Button>
          </div>
        )}
      </aside>
      <main className="flex min-h-0 min-w-0 flex-1 flex-col">
        <ListSlot slotId="layout.header.actions" />
        {["/conversations", "/runs", "/usage"].includes(route) ? (
          <Records
            key={route}
            view={route.slice(1) as "conversations" | "runs" | "usage"}
          />
        ) : route === "/inbox" ? (
          <Inbox />
        ) : route === "/audit" ? (
          <Audit />
        ) : route === "/credentials" ? (
          <Capabilities key={route} initialTab="credentials" />
        ) : route === "/settings" ? (
          <Settings />
        ) : taskId ? (
          <TaskDetail key={taskId} id={taskId} />
        ) : route.split("?")[0] === "/tasks" ? (
          <Tasks key={projectId} projectId={projectId} />
        ) : route === "/projects" ? (
          <Projects />
        ) : sessionId ? (
          <SessionPage key={sessionId} id={sessionId} />
        ) : agentId ? (
          new URLSearchParams(route.split("?")[1]).get("view") === "full" ? (
            <AgentDetail key={agentId} id={agentId} />
          ) : (
            <Agents selectedId={agentId} />
          )
        ) : route === "/models" ? (
          <Capabilities key={route} initialTab="models" />
        ) : route === "/capabilities" ? (
          <Capabilities key={route} />
        ) : route === "/skills" ? (
          <Capabilities key={route} initialTab="skills" />
        ) : route === "/mcps" ? (
          <Capabilities key={route} initialTab="mcps" />
        ) : route === "/schedules" ? (
          <Schedules />
        ) : route === "/connections" ? (
          <Connections />
        ) : route === "/feishu" ? (
          <Feishu />
        ) : route === "/runtimes" ? (
          <Runtimes />
        ) : (
          <Agents />
        )}
      </main>
    </div>
  );
}
