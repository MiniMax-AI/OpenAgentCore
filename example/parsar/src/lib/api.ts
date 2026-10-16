import { tr, currentLanguage } from "./i18n";
import {
  OpenAIAgentsClient,
  type AgentSession,
  type AgentTurn,
  type ListPage,
  type PageOptions,
} from "@oac/agents-client";
import type { StatusKind } from "../components/ui/status-icon";

export const api = new OpenAIAgentsClient();
export const application = "parsar-example";

export async function readHistory<T>(
  read: (options: PageOptions) => Promise<ListPage<T>>,
  signal: AbortSignal,
) {
  const data: T[] = [];
  let after: string | undefined;
  for (let pageNumber = 0; pageNumber < 100; pageNumber++) {
    const page = await read({ limit: 100, order: "asc", after, signal });
    data.push(...page.data);
    if (!page.has_more) return data;
    if (!page.last_id || page.last_id === after)
      throw new Error(tr("Core 返回了无效的历史分页游标。"));
    after = page.last_id;
  }
  throw new Error(tr("历史超过 10,000 条，当前示例无法完整展示。"));
}

export function sessionState(
  session: AgentSession,
  latest?: AgentTurn,
): { label: string; icon: StatusKind } {
  if (session.status === "failed") return { label: tr("失败"), icon: "failed" };
  if (session.status === "requires_action")
    return { label: tr("等待处理"), icon: "interrupted" };
  if (session.status === "in_progress")
    return { label: tr("执行中"), icon: "running" };
  if (latest?.status === "cancelled")
    return { label: tr("已取消"), icon: "cancelled" };
  if (latest?.status === "failed") return { label: tr("失败"), icon: "failed" };
  if (latest?.status === "completed")
    return { label: tr("已完成"), icon: "completed" };
  return { label: tr("空闲"), icon: "queued" };
}

export function sessionTitle(session: AgentSession) {
  return session.metadata.title || session.agent.name || tr("未命名会话");
}

export const errorText = (error: unknown) =>
  error instanceof Error
    ? error.message
    : typeof error === "string"
      ? error
      : tr("请求失败，请重试。");
export const dateTime = (seconds: number) =>
  new Intl.DateTimeFormat(currentLanguage === "en" ? "en-US" : "zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(seconds * 1000);
