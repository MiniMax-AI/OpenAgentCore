import { chromium, expect } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { homedir } from "node:os";
import assert from "node:assert/strict";
const base = process.env.OAC_EXAMPLE_LIVE_URL;
const model = process.env.OAC_EXAMPLE_LIVE_MODEL;
if (!base || !model)
  throw new Error("Set OAC_EXAMPLE_LIVE_URL and OAC_EXAMPLE_LIVE_MODEL.");
const directory = join(homedir(), ".oac", "tests", "parsar-agent-live");
await mkdir(directory, { recursive: true });
const browser = await chromium.launch({ channel: "chrome" });
const page = await browser.newPage({
  viewport: { width: 1440, height: 960 },
  reducedMotion: "reduce",
});
const suffix = Date.now().toString().slice(-6);
const modelName = `Kimi ${suffix}`,
  runtimeName = `开发沙箱 ${suffix}`,
  mcpName = `DeepWiki ${suffix}`,
  skillName = `code-review-${suffix}`,
  agentName = `代码助手 ${suffix}`;
const save = async () => {
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
};
const go = async (name) =>
  page.getByRole("link", { name, exact: true }).click();
try {
  await page.goto(`${base}/#/models`);
  await page
    .getByRole("button", { name: "添加 Provider", exact: true })
    .click();
  await page.getByLabel("Provider 名称").fill(`Moonshot ${suffix}`);
  await page.getByRole("button", { name: "手动选择", exact: true }).click();
  await page.getByRole("button", { name: "自定义模型", exact: true }).click();
  await page.getByLabel("显示名称（选填）").fill(modelName);
  await page.getByLabel("模型 ID").fill(model);
  await page.getByRole("button", { name: "加入列表", exact: true }).click();
  await save();
  await go("环境模板");
  await page.getByRole("button", { name: "添加环境模板" }).click();
  await page.getByLabel("名称", { exact: true }).fill(runtimeName);
  await save();
  await go("资源库");
  await page.getByRole("tab", { name: "MCP", exact: true }).click();
  await page.getByRole("button", { name: "添加MCP" }).click();
  await page.getByLabel("名称", { exact: true }).fill(mcpName);
  await page.getByLabel("服务标识").fill("deepwiki");
  await page.getByLabel("MCP 地址").fill("https://mcp.deepwiki.com/mcp");
  await save();
  await page.getByRole("tab", { name: "Skills", exact: true }).click();
  await page.getByRole("button", { name: "创建 Skill" }).click();
  await page.getByLabel("技能名称").fill(skillName);
  await page.getByLabel("用途").fill("检查代码修改，找出有依据的问题");
  await page
    .getByLabel("执行方法")
    .fill(
      "Read changed files. Report actionable correctness issues with file locations and explain the verification performed.",
    );
  await save();
  await page.goto(`${base}/#/agents/new`);
  await page.getByLabel("名称", { exact: true }).fill(agentName);
  await page.getByLabel("执行引擎", { exact: true }).click();
  await page.getByRole("option", { name: "Claude Code", exact: true }).click();
  await page.getByRole("button", { name: "模型", exact: true }).click();
  await page
    .getByRole("radio", { name: `Moonshot ${suffix} / ${modelName}`, exact: true })
    .click();
  await page.getByRole("button", { name: "环境模板", exact: true }).click();
  await page.getByRole("radio", { name: runtimeName, exact: true }).click();
  await page
    .getByLabel("主指令", { exact: true })
    .fill(
      "Follow the user's instructions. Use the workspace and installed capabilities when requested.",
    );
  for (const [field, name] of [
    ["Skills", skillName],
    ["MCP", mcpName],
  ]) {
    await page
      .getByRole("region", { name: field, exact: true })
      .getByRole("button", { name: field, exact: true })
      .click();
    await page.getByRole("checkbox", { name, exact: true }).check();
    await page.getByRole("button", { name: "完成", exact: true }).click();
  }
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page).toHaveURL(/#\/agents\/[a-f0-9-]{36}$/);
  const agentID = page.url().split("/").at(-1);
  const start = async (title, input) => {
    await page.goto(`${base}/#/new?agent=${agentID}`);
    await page.getByLabel("描述你想完成的工作").fill(`${title}\n\n${input}`);
    await page.getByRole("button", { name: "发送", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: title, exact: true }),
    ).toBeVisible({ timeout: 45000 });
    const localID = page.url().split("/").at(-1);
    return (await page.request.get(`${base}/app/sessions/${localID}`)).json();
  };
  const waitIdle = async (row) => {
    let session;
    await expect
      .poll(
        async () => {
          session = await (
            await page.request.get(
              `${base}/v1/agents/sessions/${row.core_session_id}`,
            )
          ).json();
          if (session.status === "failed")
            throw new Error(JSON.stringify(session.error));
          const turns = await (
            await page.request.get(
              `${base}/v1/agents/sessions/${row.core_session_id}/turns?order=desc&limit=1`,
            )
          ).json();
          if (turns.data[0]?.status === "failed")
            throw new Error(JSON.stringify(turns.data[0].error));
          return (
            session.status === "idle" && turns.data[0]?.status === "completed"
          );
        },
        { timeout: 240000, intervals: [2000] },
      )
      .toBe(true);
    return session;
  };
  const marker = `workspace-${suffix}`;
  const first = await start(
    `工作区验证 ${suffix}`,
    `Use a shell tool to write exactly ${marker} into /workspace/session-proof.txt. Also use the bound DeepWiki MCP tool to read the documentation structure of openai/openai-python. Report which operations succeeded.`,
  );
  const firstSession = await waitIdle(first);
  assert.equal(firstSession.environment.skills.length, 1);
  assert.equal(firstSession.environment.plugins.length, 0);
  await page.reload();
  await page
    .getByLabel("继续对话")
    .fill(
      "Read /workspace/session-proof.txt with a shell tool. Report its exact content and say WORKSPACE_REUSED. Do not recreate it.",
    );
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await expect
    .poll(
      async () =>
        (
          await (
            await page.request.get(
              `${base}/v1/agents/sessions/${first.core_session_id}/turns?limit=20`,
            )
          ).json()
        ).data.length,
      { timeout: 30000 },
    )
    .toBe(2);
  await waitIdle(first);
  const items = await (
    await page.request.get(
      `${base}/v1/agents/sessions/${first.core_session_id}/items?limit=100`,
    )
  ).json();
  assert.ok(
    items.data.some(
      (item) =>
        item.role === "assistant" &&
        JSON.stringify(item.content).includes("WORKSPACE_REUSED") &&
        JSON.stringify(item.content).includes(marker),
    ),
  );
  await page.screenshot({ path: join(directory, "session-light.png") });
  await page.getByRole("button", { name: "切换深色" }).click();
  await page.screenshot({ path: join(directory, "session-dark.png") });
  const second = await start(
    `独立会话 ${suffix}`,
    "Use a shell tool to check whether /workspace/session-proof.txt exists. Do not create it. Report WORKSPACE_ISOLATED if absent.",
  );
  const secondSession = await waitIdle(second);
  assert.notEqual(firstSession.environment.id, secondSession.environment.id);
  const secondItems = await (
    await page.request.get(
      `${base}/v1/agents/sessions/${second.core_session_id}/items?limit=100`,
    )
  ).json();
  assert.ok(
    secondItems.data.some(
      (item) =>
        item.role === "assistant" &&
        JSON.stringify(item.content).includes("WORKSPACE_ISOLATED"),
    ),
  );
  await writeFile(
    join(directory, "sessions-result.json"),
    JSON.stringify(
      {
        first,
        second,
        environments: [
          firstSession.environment.id,
          secondSession.environment.id,
        ],
        items: items.data,
        secondItems: secondItems.data,
      },
      null,
      2,
    ),
  );
  console.log(
    JSON.stringify({
      ok: true,
      first: first.core_session_id,
      second: second.core_session_id,
      directory,
    }),
  );
} finally {
  await browser.close();
}
