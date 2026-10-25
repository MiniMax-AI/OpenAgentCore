// What a conversation shows about a running Turn. Web and Feishu cards read
// the same copy; Web translates it, Feishu shows it as is.
export const typicalWait = "通常约 {{value0}} 秒";

// Before the first output: where the time goes.
export function waitingHeadline(turn) {
  if (turn?.stage === "retrying") return "响应较慢，正在自动重试";
  if (turn?.slow) return "比平时慢，请稍候";
  if (turn?.stage === "preparing") return "正在准备工作环境";
  if (turn?.stage === "starting") return "正在启动";
  return "正在思考";
}

// Any stage of a running Turn, including after its first output.
export function stageLabel(turn) {
  if (turn?.stage === "waiting_user") return "等你回复";
  if (turn?.stage === "working") return "执行中";
  return waitingHeadline(turn);
}
