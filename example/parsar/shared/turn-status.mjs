// What a conversation shows while a Turn has no output yet. Web and Feishu
// cards read the same copy; Web translates it, Feishu shows it as is.
export const typicalWait = "通常约 {{value0}} 秒";

export function waitingHeadline(turn) {
  if (turn?.stage === "retrying") return "响应较慢，正在自动重试";
  if (turn?.slow) return "比平时慢，请稍候";
  if (turn?.stage === "preparing") return "正在准备工作环境";
  if (turn?.stage === "starting") return "正在启动";
  return "正在思考";
}
