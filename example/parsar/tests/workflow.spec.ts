import { test, expect, type Page } from "@playwright/test";
const appOrigin = `http://127.0.0.1:${process.env.OAC_EXAMPLE_PORT || "18180"}`;
async function navigate(page: Page, name: string) {
  if (["Skills", "MCP", "模型", "凭据"].includes(name)) {
    await page.getByRole("link", { name: "资源库", exact: true }).click();
    await page.getByRole("tab", { name, exact: true }).click();
  } else
    await page
      .getByRole("link", {
        name: ({ 运行时: "环境模板", 飞书: "飞书接入" } as Record<string, string>)[name] || name,
        exact: true,
      })
      .click();
}
async function resources(page: Page) {
  await page.goto("/#/models");
  await page
    .getByRole("button", { name: "添加 Provider", exact: true })
    .click();
  await page.getByLabel("Provider 名称").fill("Moonshot");
  await page.getByLabel("Base URL").fill("http://127.0.0.1:18181");
  await page.getByLabel("API Key").fill("provider-fixture-key");
  await page.getByRole("button", { name: "获取模型", exact: true }).click();
  await expect(page.getByText("获取到 3 个模型 · 已选 0 个")).toBeVisible();
  await page.getByLabel("kimi-k2.6", { exact: true }).check();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("button", { name: "编辑 Provider Moonshot" }).click();
  await page.getByLabel("Base URL").fill("https://provider.example/v1");
  await page.getByRole("button", { name: "手动选择", exact: true }).click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page
    .getByRole("button", { name: "编辑 kimi-k2.6", exact: true })
    .click();
  await page.getByLabel("显示名称", { exact: true }).fill("Kimi");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await navigate(page, "运行时");
  await page.getByRole("button", { name: "添加环境模板" }).click();
  await page.getByLabel("名称", { exact: true }).fill("开发沙箱");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await navigate(page, "MCP");
  await page.getByRole("button", { name: "添加MCP" }).click();
  await page.getByLabel("名称", { exact: true }).fill("文档服务");
  await page.getByLabel("服务标识").fill("docs");
  await page.getByLabel("MCP 地址").fill("https://mcp.example/docs");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await navigate(page, "Skills");
  await page.getByRole("button", { name: "创建 Skill" }).click();
  await page.getByLabel("技能名称").fill("code-review");
  await page.getByLabel("用途").fill("检查代码质量");
  await page
    .getByLabel("执行方法")
    .fill("Inspect changed files and report actionable issues.");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
}
async function agent(page: Page, name = "审查助手") {
  await page.goto("/#/agents/new");
  await page.getByLabel("名称", { exact: true }).fill(name);
  await page.getByRole("button", { name: "模型", exact: true }).click();
  await page
    .getByRole("radio", { name: "Moonshot / Kimi", exact: true })
    .click();
  await page.getByRole("button", { name: "环境模板", exact: true }).click();
  await page.getByRole("radio", { name: "开发沙箱", exact: true }).click();
  await page
    .getByLabel("主指令", { exact: true })
    .fill("Read the code and report findings.");
  await page
    .getByRole("region", { name: "Skills", exact: true })
    .getByRole("button", { name: "Skills", exact: true })
    .click();
  await page
    .getByRole("checkbox", { name: "code-review", exact: true })
    .check();
  await page.getByRole("button", { name: "完成", exact: true }).click();
  await page
    .getByRole("region", { name: "MCP", exact: true })
    .getByRole("button", { name: "MCP", exact: true })
    .click();
  await page.getByRole("checkbox", { name: "文档服务", exact: true }).check();
  await page.getByRole("button", { name: "完成", exact: true }).click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page).toHaveURL(/#\/agents\/[a-f0-9-]{36}$/);
  return page.url().split("/").at(-1)!;
}
async function converse(page: Page, text = "Review code") {
  await page.getByRole("link", { name: "新对话", exact: true }).click();
  await page.getByLabel("描述你想完成的工作").fill(text);
  await page.getByRole("button", { name: "发送", exact: true }).click();
}
test.beforeEach(async ({ request }) => {
  const { openStore, dataPath } = await import("../server/store.mjs");
  const { homedir } = await import("node:os");
  const store = openStore(
    dataPath(
      { target: "http://127.0.0.1:18181", key: "fixture-project-key" },
      {
        OAC_EXAMPLE_DATA_DIR: `${homedir()}/.oac/tests/parsar-example/fixture-store`,
      },
    ),
  );
  for (const kind of [
    "sessions",
    "feishu_bots",
    "feishu_registration",
    "audit",
    "settings",
    "capabilities",
    "mcp_oauth",
    "feishu_inbound",
    "feishu_outbound",
    "feishu_threads",
    "feishu_health",
    "agents",
    "models",
    "providers",
    "mcps",
    "runtimes",
  ])
    for (const row of store.list(kind)) store.remove(kind, row.id);
  store.close();
  await request.post("http://127.0.0.1:18181/reset");
});

test("environment template edits preparation and hides saved variable values", async ({
  page,
  request,
}) => {
  await resources(page);
  await navigate(page, "运行时");
  await page.getByRole("button", { name: "添加环境模板" }).click();
  await expect(page.getByLabel("环境类型")).toHaveCount(0);
  await page.getByLabel("名称", { exact: true }).fill("飞书办公");
  await page.getByLabel("用途说明").fill("预装办公工具");
  await page.getByLabel("npm 软件包").fill("typescript@5.8.3");
  await page.getByLabel("Python 软件包").fill("packaging==26.0");
  await page.getByLabel("初始化脚本").fill("set -eu\nmkdir -p outputs");
  await page.getByRole("button", { name: "添加变量" }).click();
  await page.getByLabel("变量名称").fill("OFFICE_TOKEN");
  await page.getByLabel("变量值").fill("private-fixture-token");
  await page.getByRole("button", { name: "添加机器人变量" }).click();
  await page.getByLabel("机器人变量名称").fill("FEISHU_APP_SECRET");
  await page.getByLabel("机器人字段").click();
  await page.getByRole("option", { name: "App Secret", exact: true }).click();
  await page.getByRole("button", { name: "添加文件", exact: true }).click();
  await page.getByLabel("文件路径").fill("/workspace/settings.txt");
  await page.getByLabel("文件内容").fill("office=true");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("button", { name: "编辑 飞书办公" }).click();
  await expect(page.getByLabel("初始化脚本")).toHaveValue(
    "set -eu\nmkdir -p outputs",
  );
  await expect(page.getByLabel("变量值")).toHaveValue("");
  await expect(page.getByLabel("变量值")).toHaveAttribute(
    "placeholder",
    "已配置，留空保留",
  );
  await page.getByLabel("变量值").fill("discard-this-replacement");
  await page.getByLabel("变量值").fill("");
  const saved = page.waitForRequest(
    (request) =>
      request.method() === "PUT" && request.url().includes("/app/runtimes/"),
  );
  await page.getByRole("button", { name: "保存", exact: true }).click();
  expect((await saved).postDataJSON().env).toEqual({ OFFICE_TOKEN: null });
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const rows = await (
    await request.get(`${appOrigin}/app/runtimes`)
  ).json();
  const template = rows.find(
    (row: { name: string }) => row.name === "飞书办公",
  );
  expect(template.env).toEqual({ OFFICE_TOKEN: null });
  expect(template.bot_env).toEqual({ FEISHU_APP_SECRET: "app_secret" });
  expect(template.files).toEqual([
    { path: "/workspace/settings.txt", content: "office=true" },
  ]);
});

test("model edits persist and bound resources cannot be removed", async ({
  page,
}) => {
  await resources(page);
  await agent(page);
  await navigate(page, "模型");
  await page.getByRole("button", { name: "编辑 Kimi" }).click();
  await page.getByLabel("显示名称").fill("常用 Kimi");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("heading", { name: "常用 Kimi" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("button", { name: "移除 常用 Kimi" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "移除 常用 Kimi" })).toHaveAttribute(
    "title",
    "仍在使用中，不能移除",
  );
});

test("Skill version upload and default selection", async ({ page }) => {
  await resources(page);
  await page.getByRole("button", { name: /code-review v1/ }).click();
  await page.getByRole("button", { name: "查看文件", exact: true }).click();
  await expect(
    page.getByText("Inspect changed files and report actionable issues.", {
      exact: false,
    }),
  ).toBeVisible();
  await page.getByLabel("上传 Skill 新版本").setInputFiles({
    name: "review.zip",
    mimeType: "application/zip",
    buffer: Buffer.from("fixture bundle"),
  });
  await expect(
    page.getByRole("dialog").getByText("v2", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "设为默认" }).click();
  await expect(
    page.getByRole("dialog").getByText("v1", { exact: true }).locator(".."),
  ).toContainText("默认版本");
});

test("Provider groups contain multiple models and retain manual names", async ({
  page,
  request,
}) => {
  await resources(page);
  await navigate(page, "模型");
  await page.getByRole("button", { name: "编辑 Provider Moonshot" }).click();
  await page.getByLabel("Base URL").fill("http://127.0.0.1:18181");
  await page.getByRole("button", { name: "获取模型", exact: true }).click();
  await expect(page.getByLabel("kimi-k2.6", { exact: true })).toBeChecked();
  await page.getByLabel("kimi-k2", { exact: true }).check();
  await page.getByRole("button", { name: "自定义模型", exact: true }).click();
  await page.getByLabel("显示名称（选填）").fill("Kimi Lite");
  await page.getByLabel("模型 ID").fill("kimi-lite");
  await page.getByRole("button", { name: "加入列表", exact: true }).click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const group = page.getByRole("region", { name: "Moonshot", exact: true });
  await expect(
    group.getByRole("heading", { name: "Kimi", exact: true }),
  ).toBeVisible();
  await expect(
    group.getByRole("heading", { name: "Kimi Lite", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "编辑 Provider Moonshot" }).click();
  await page.getByLabel("Provider 名称").fill("团队模型");
  await page.getByRole("button", { name: "手动选择", exact: true }).click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await page.reload();
  await expect(page.getByRole("region", { name: "团队模型" })).toBeVisible();
  await expect(page.getByText("3 个模型", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "kimi-k2", exact: true }),
  ).toBeVisible();
  const providers = await (await request.get("/app/providers")).json();
  expect(providers[0].has_api_key).toBe(true);
  expect(providers[0].api_key).toBeUndefined();
  await page.getByRole("button", { name: "编辑 Provider 团队模型" }).click();
  await expect(page.getByLabel("API Key")).toHaveValue("");
  await page.getByRole("button", { name: "获取模型", exact: true }).click();
  await expect(page.getByText("获取到 3 个模型 · 已选 3 个")).toBeVisible();
  await expect(page.getByLabel("kimi-lite", { exact: true })).toBeChecked();
  await page.getByLabel("kimi-k2", { exact: true }).uncheck();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByText("2 个模型", { exact: true })).toBeVisible();
  await page.route("**/app/models", (route) => route.fulfill({ json: [] }));
  await page.reload();
  await page.getByRole("button", { name: "编辑 Provider 团队模型" }).click();
  await page.getByRole("button", { name: "手动选择", exact: true }).click();
  await expect(page.getByLabel("kimi-k2.6", { exact: true })).toBeChecked();
  await expect(page.getByLabel("kimi-lite", { exact: true })).toBeChecked();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.unroute("**/app/models");
  await page.reload();
  await expect(page.getByText("2 个模型", { exact: true })).toBeVisible();
});

test("failed discovery can retry or use custom models; closing discards a new Provider", async ({
  page,
  request,
}) => {
  await page.goto("/#/models");
  await page
    .getByRole("button", { name: "添加 Provider", exact: true })
    .click();
  await page.getByLabel("Provider 名称").fill("自定义服务");
  await page.getByLabel("Base URL").fill("http://127.0.0.1:18181/v1");
  await page.getByLabel("API Key").fill("invalid-key");
  await page.getByRole("button", { name: "获取模型", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("HTTP 401");
  expect(await (await request.get("/app/providers")).json()).toEqual([]);
  await page.getByLabel("API Key").fill("provider-fixture-key");
  await page.getByRole("button", { name: "获取模型", exact: true }).click();
  await expect(page.getByText("获取到 3 个模型 · 已选 0 个")).toBeVisible();
  await page.getByLabel("kimi-k2", { exact: true }).check();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  expect(await (await request.get("/app/providers")).json()).toEqual([]);
  await page
    .getByRole("button", { name: "添加 Provider", exact: true })
    .click();
  await page.getByLabel("Provider 名称").fill("自定义服务");
  await page.getByRole("button", { name: "手动选择", exact: true }).click();
  await page.getByRole("button", { name: "自定义模型", exact: true }).click();
  await page.getByLabel("模型 ID", { exact: true }).fill("my-model");
  await page.getByRole("button", { name: "加入列表", exact: true }).click();
  await expect(page.getByLabel("my-model", { exact: true })).toBeChecked();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.reload();
  await expect(
    page
      .getByRole("region", { name: "自定义服务" })
      .getByRole("heading", { name: "my-model" }),
  ).toBeVisible();
});

test("unsupported environment placements reject before execution", async ({
  request,
}) => {
  for (const environment of ["none", "self_hosted"]) {
    const response = await request.put(
      `${appOrigin}/app/runtimes/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa`,
      {
        headers: { origin: appOrigin },
        data: { name: "Unsupported", environment },
      },
    );
    expect(response.status()).toBe(400);
  }
});

test("credential tokens remain write-only through creation, replacement, and deletion", async ({
  page,
}) => {
  await page.goto("/#/credentials");
  page.once("dialog", (dialog) => dialog.accept("Team secrets"));
  await page.getByRole("button", { name: "新建凭据库" }).click();
  await page.getByRole("button", { name: "添加凭据" }).click();
  await page.getByLabel("名称", { exact: true }).fill("Docs credential");
  await page.getByLabel("HTTPS MCP 地址").fill("https://mcp.example/docs");
  await page.getByLabel("Bearer Token").fill("fixture-write-only-token");
  await page.getByRole("button", { name: "保存凭据" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.reload();
  await expect(
    page.getByText("Docs credential", { exact: true }),
  ).toBeVisible();
  await expect(page.locator("body")).not.toContainText(
    "fixture-write-only-token",
  );
  await page.getByRole("button", { name: "更换密钥" }).click();
  await expect(page.getByLabel("Bearer Token")).toHaveValue("");
  await page.getByLabel("Bearer Token").fill("replacement-token");
  await page.getByRole("button", { name: "保存凭据" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "删除凭据 Docs credential" }).click();
  await expect(page.getByText("Docs credential", { exact: true })).toHaveCount(
    0,
  );
});

test("conversations start from the home page, continue, cancel and follow Agent changes", async ({
  page,
  request,
}, testInfo) => {
  await resources(page);
  const id = await agent(page);
  await converse(page);
  await expect(
    page.getByRole("heading", { name: "Review code", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("已检查登录流程并补充验证。")).toBeVisible();
  await expect(
    page
      .getByRole("navigation", { name: "最近对话" })
      .getByRole("link", { name: "Review code" }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Review code", exact: true }),
  ).toBeVisible();
  await page.getByLabel("继续对话").fill("Delayed response");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await expect(page.getByRole("button", { name: "取消执行" })).toBeVisible();
  await page.getByLabel("继续对话").fill("也看一下移动端");
  await expect(page.getByRole("button", { name: "发送", exact: true })).toBeEnabled();
  await page.getByLabel("继续对话").fill("");
  await expect(page.getByRole("button", { name: "发送", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "取消执行" }).click();
  await expect(page.getByText("已取消", { exact: true })).toBeVisible();
  await expect(page.getByLabel("继续对话")).toBeEnabled({ timeout: 15000 });
  await expect(page.getByLabel("继续对话")).toHaveValue("");
  await page.screenshot({ path: testInfo.outputPath("session-light.png") });
  await page.getByRole("button", { name: "切换深色" }).click();
  await page.screenshot({ path: testInfo.outputPath("session-dark.png") });
  const sessionURL = page.url();
  await page.goto(`/#/agents/${id}`);
  await page.getByLabel("主指令", { exact: true }).fill("New instructions");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("button", { name: "已保存" })).toBeVisible();
  await page.goto(sessionURL);
  await page.getByRole("link", { name: "用新配置开始新对话" }).click();
  await page.getByLabel("描述你想完成的工作").fill("另一段会话");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "另一段会话", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Agent 配置已更新")).toHaveCount(0);
  const core = await (
    await request.get("http://127.0.0.1:18181/counts")
  ).json();
  expect(core.sessions).toHaveLength(2);
  expect(core.sessions[0].environment.id).not.toBe(
    core.sessions[1].environment.id,
  );
  expect(core.sessions[0].agent.instructions).toBe(
    "Read the code and report findings.",
  );
  expect(core.sessions[1].agent.instructions).toBe("New instructions");
});

test("a lost creation response is recovered on its conversation page without a duplicate Session", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await request.post("http://127.0.0.1:18181/lose-creation");
  await converse(page);
  await expect(page.getByText("会话创建尚未确认")).toBeVisible();
  await page.reload();
  await page.getByRole("button", { name: "恢复创建" }).click();
  await expect(
    page.getByRole("heading", { name: "Review code", exact: true }),
  ).toBeVisible();
  expect(
    (await (await request.get("http://127.0.0.1:18181/counts")).json())
      .sessions,
  ).toHaveLength(1);
});

test("Agents are listed as cards, duplicated without sharing state and require an environment", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "Agent", exact: true }).click();
  await page.getByRole("link", { name: "复制", exact: true }).click();
  await expect(page.getByLabel("名称", { exact: true })).toHaveValue(
    "审查助手 副本",
  );
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page).toHaveURL(/#\/agents\/[a-f0-9-]{36}$/);
  const agents = await (await request.get("/app/agents")).json();
  expect(agents).toHaveLength(2);
  expect(new Set(agents.map((row: { id: string }) => row.id)).size).toBe(2);
  expect(agents[0].skill_ids).toEqual(agents[1].skill_ids);
  await page.goto("/#/agents/new");
  await page.getByLabel("名称", { exact: true }).fill("No environment");
  await page.getByRole("button", { name: "模型", exact: true }).click();
  await page
    .getByRole("radio", { name: "Moonshot / Kimi", exact: true })
    .click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByText("请选择环境模板", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "环境模板", exact: true })).toBeFocused();
  expect(await (await request.get("/app/agents")).json()).toHaveLength(2);
  await page.getByRole("link", { name: "Agent", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Agent", exact: true })).toBeVisible();
  await page.goto("/#/agents/new");
  await expect(page.getByLabel("名称", { exact: true })).toHaveValue("No environment");
  await expect(page.getByText("已恢复上次未保存的修改。")).toBeVisible();
  await page.getByRole("button", { name: "放弃修改" }).click();
  await expect(page.getByLabel("名称", { exact: true })).toHaveValue("");
});

test("mobile layouts never scroll horizontally", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  for (const route of ["/#/", "/#/agents", "/#/feishu", "/#/settings"]) {
    await page.goto(route);
    await page.waitForLoadState("networkidle");
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
  }
  await page.screenshot({ path: testInfo.outputPath("mobile.png") });
});

test("text arrives incrementally before durable completion, then survives reload without duplicates", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await converse(page);
  await expect(
    page.getByRole("heading", { name: "Review code", exact: true }),
  ).toBeVisible();
  await page.getByLabel("继续对话").fill("Stream reply");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  const reply = page
    .getByRole("article", { name: "Agent 回复" })
    .filter({ hasText: "流式第一段" });
  await expect(reply).toContainText("流式第一段", { timeout: 5000 });
  await expect(reply).not.toContainText("，第二段完成。");
  await request.post("http://127.0.0.1:18181/drop-streams");
  await expect(reply).toContainText("流式第一段，第二段完成。", {
    timeout: 10000,
  });
  await expect(reply).toHaveCount(1);
  await page.reload();
  await expect(reply).toHaveCount(1);
  await expect(reply).toContainText("流式第一段，第二段完成。");
});

test("an active reply without a replayed item baseline refreshes immediately on its next delta", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await converse(page);
  await expect(
    page.getByRole("heading", { name: "Review code", exact: true }),
  ).toBeVisible();
  // Hold automatic history polls after the initial read. A new SSE delta must
  // initiate its own history recovery, independently of the two-second timer.
  await page.addInitScript(() => {
    const original = window.setInterval;
    window.setInterval = ((
      handler: TimerHandler,
      delay?: number,
      ...args: unknown[]
    ) =>
      original(
        handler,
        delay === 2000 ? 60000 : delay,
        ...args,
      )) as typeof setInterval;
  });
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Review code", exact: true }),
  ).toBeVisible();
  await request.post("http://127.0.0.1:18181/resume-output");
  await expect(
    page
      .getByRole("article", { name: "Agent 回复" })
      .filter({ hasText: "断线前，断线后仍在生成" }),
  ).toBeVisible({ timeout: 1500 });
});

test("conversation uploads insert a file path and download binary artifacts without leaving the conversation", async ({
  page,
}) => {
  await resources(page);
  await agent(page);
  await converse(page);
  await expect(page.getByLabel("继续对话")).toBeVisible();
  await page.getByRole("button", { name: "文件", exact: true }).click();
  await page.getByLabel("选择上传文件").setInputFiles({
    name: "large.bin",
    mimeType: "application/octet-stream",
    buffer: Buffer.alloc(5 * 1024 * 1024 + 1),
  });
  await expect(page.getByRole("alert")).toContainText("文件不能超过 5 MiB");
  await page.getByLabel("选择上传文件").setInputFiles({
    name: "numbers.csv",
    mimeType: "text/csv",
    buffer: Buffer.from("a,b\n1,2"),
  });
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByLabel("继续对话")).toHaveValue(
    /请处理文件：\.\/inputs\/.*numbers.csv/,
  );
  await page.getByRole("button", { name: "文件", exact: true }).click();
  const uploaded = page.waitForRequest(
    (request) =>
      request.method() === "POST" && request.url().endsWith("/files"),
  );
  await page.getByLabel("选择上传文件").setInputFiles({
    name: "中".repeat(80) + ".txt",
    mimeType: "text/plain",
    buffer: Buffer.from("small"),
  });
  const uploadedPath = (await uploaded).postDataJSON().path;
  expect(
    Buffer.byteLength(uploadedPath.split("/").at(-1), "utf8"),
  ).toBeLessThanOrEqual(255);
  expect(uploadedPath).not.toContain("�");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const draft = await page.getByLabel("继续对话").inputValue();
  const originalSessionURL = page.url();
  await page.reload();
  await expect(page.getByLabel("继续对话")).toHaveValue(draft);
  await converse(page, "第二个文件会话");
  await expect(page.getByLabel("继续对话")).toHaveValue("");
  await page.goto(originalSessionURL);
  await expect(page.getByLabel("继续对话")).toHaveValue(draft);
  await page.getByRole("button", { name: "文件", exact: true }).click();
  const contentRoute = "**/artifacts/*/content";
  await page.route(contentRoute, (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { message: "文件暂不可用" } }),
    }),
  );
  await page.getByRole("button", { name: "下载 report.bin" }).click();
  await expect(page.getByRole("alert")).toContainText("文件暂不可用");
  await page.unroute(contentRoute);
  const downloading = page.waitForEvent("download");
  await page.getByRole("button", { name: "下载 report.bin" }).click();
  const download = await downloading;
  const stream = await download.createReadStream();
  const chunks = [];
  for await (const chunk of stream!) chunks.push(chunk);
  expect(Buffer.concat(chunks)).toEqual(Buffer.from([0, 255, 128, 13, 10]));
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await expect(page.getByLabel("继续对话")).toHaveValue(/numbers.csv/);
  await page.route(/\/v1\/agents\/sessions\/[a-f0-9-]+$/, async (route) => {
    const response = await route.fetch();
    const value = await response.json();
    await route.fulfill({
      json: {
        ...value,
        status: "failed",
        error: "运行环境准备失败，请检查配置。",
      },
    });
  });
  await expect(page.getByRole("alert")).toContainText(
    "运行环境准备失败，请检查配置。",
  );
});

test("a send completed after leaving the conversation does not restore a sent draft", async ({
  page,
}) => {
  await resources(page);
  await agent(page);
  await converse(page);
  await expect(page.getByLabel("继续对话")).toBeVisible();
  const sessionURL = page.url();
  let release!: () => void;
  let arrived!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const received = new Promise<void>((resolve) => {
    arrived = resolve;
  });
  await page.route("**/events", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    const response = await route.fetch();
    arrived();
    await gate;
    await route.fulfill({ response });
  });
  await page.getByLabel("继续对话").fill("Stream reply");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await received;
  await page.getByRole("link", { name: "新对话", exact: true }).click();
  release();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          Object.keys(sessionStorage).filter((key) =>
            key.startsWith("oac-example-message-"),
          ).length,
      ),
    )
    .toBe(0);
  await page.goto(sessionURL);
  await expect(page.getByLabel("继续对话")).toHaveValue("");
});

test("a Feishu bot binds one Agent, which then shows the bot as its identity", async ({
  page,
  request,
}, testInfo) => {
  await resources(page);
  const id = await agent(page);
  await navigate(page, "飞书");
  await page.getByRole("button", { name: "使用已有应用" }).click();
  await page.getByLabel("机器人名称").fill("工作助手");
  await page.getByLabel("App ID").fill("cli_0123456789abcdef");
  await page.getByLabel("App Secret").fill("fixture-bot-secret");
  await page.getByRole("button", { name: "添加", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const card = page.getByRole("region", { name: "工作助手" });
  await card.getByLabel("Agent", { exact: true }).click();
  await page.getByRole("option", { name: "审查助手", exact: true }).click();
  await card.getByRole("switch", { name: "私聊" }).check({ force: true });
  await card.getByRole("switch", { name: "群聊" }).check({ force: true });
  await card.getByRole("button", { name: "保存并启用" }).click();
  await expect(card.getByRole("button", { name: "停用" })).toBeVisible();
  const [bot] = await (await request.get("/app/feishu/bots")).json();
  expect(bot).toMatchObject({
    enabled: true,
    agent_id: id,
    direct: true,
    groups: true,
    has_app_secret: true,
  });
  expect(JSON.stringify(bot)).not.toContain("fixture-bot-secret");
  await page.screenshot({ path: testInfo.outputPath("feishu.png") });
  await page.goto(`/#/agents/${id}`);
  await expect(page.getByText("工作助手", { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Agent", exact: true }).click();
  await expect(page.getByText("工作助手", { exact: true })).toBeVisible();
});

test("workspace settings retain the name and switch the complete navigation between languages", async ({
  page,
}) => {
  await page.goto("/#/settings");
  await page.getByLabel("名称", { exact: true }).fill("Research studio");
  await page.getByRole("button", { name: "保存设置" }).click();
  await expect(
    page.getByText("Research studio", { exact: true }),
  ).toBeVisible();
  await page.getByRole("radio", { name: "English", exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(
    page.getByRole("link", { name: "New conversation", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Settings", exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue(
    "Research studio",
  );
  await page.getByRole("radio", { name: "简体中文", exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(
    page.getByRole("link", { name: "新对话", exact: true }),
  ).toBeVisible();
  await page.getByRole("radio", { name: "深色", exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
});

test("business questions appear in the conversation and use the public function-result event", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await converse(page);
  await expect(
    page.getByRole("heading", { name: "Review code", exact: true }),
  ).toBeVisible();
  const question = await request.post("http://127.0.0.1:18181/question", {
    headers: { Authorization: "Bearer fixture-project-key" },
  });
  expect(question.ok()).toBeTruthy();
  await expect(page.getByText("选择报告语言", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "English", exact: true }).click();
  await page.getByRole("button", { name: "提交回答", exact: true }).click();
  await expect(page.getByText("选择报告语言", { exact: true })).toHaveCount(0);
});

test("Agent page creates and edits shared resources inline without losing unsaved fields", async ({
  page,
  request,
}) => {
  await resources(page);
  const id = await agent(page);
  await page
    .getByLabel("主指令", { exact: true })
    .fill("Keep the main instructions.");
  await page.getByRole("button", { name: "新建指令片段", exact: true }).click();
  await page.getByLabel("名称", { exact: true }).last().fill("Report rules");
  await page
    .getByLabel("内容（修改后保存为新版本）")
    .fill("Use a short checklist.");
  await page.getByRole("button", { name: "保存能力", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const [before] = await (await request.get("/app/agents")).json();
  expect(before.instructions).toBe("Read the code and report findings.");
  await expect(page.getByLabel("主指令", { exact: true })).toHaveValue(
    "Keep the main instructions.",
  );
  await expect(
    page.getByRole("region", { name: "指令片段", exact: true }),
  ).toContainText("Report rules");
  await page
    .getByRole("button", { name: "查看或编辑 文档服务", exact: true })
    .click();
  await page
    .getByRole("dialog")
    .getByLabel("名称", { exact: true })
    .fill("Documentation MCP");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "保存", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page
    .getByRole("button", { name: "移除绑定 Documentation MCP", exact: true })
    .click();
  await page.getByRole("button", { name: "MCP", exact: true }).click();
  await page.getByLabel("搜索MCP", { exact: true }).fill("nonexistent");
  await expect(page.getByText("没有匹配的资源", { exact: true })).toBeVisible();
  await page.getByLabel("搜索MCP", { exact: true }).fill("Documentation");
  await page
    .getByRole("checkbox", { name: "Documentation MCP", exact: true })
    .check();
  await page.getByRole("button", { name: "完成", exact: true }).click();
  await page.getByLabel("Report rules 版本", { exact: true }).click();
  await page.getByRole("option", { name: "v1", exact: true }).click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("button", { name: "已保存" })).toBeVisible();
  const saved = await (await request.get(`/app/agents/${id}`)).json();
  expect(saved.instructions).toBe("Keep the main instructions.");
  expect(saved.capability_ids).toHaveLength(1);
  expect(saved.capability_versions[saved.capability_ids[0]]).toBe("1");
  expect(saved.mcp_ids).toHaveLength(1);
});

test("Agent resource selectors have no shared budget beyond Core's limits", async ({
  page,
  request,
}) => {
  await resources(page);
  const id = await agent(page);
  const a = await (await request.get(`/app/agents/${id}`)).json();
  const ids: string[] = [];
  for (let i = 0; i < 10; i++) {
    const capability = crypto.randomUUID();
    const response = await request.put(`/app/capabilities/${capability}`, {
      headers: { origin: appOrigin },
      data: {
        name: `Shared ${i}`,
        kind: i < 5 ? "prompt" : "knowledge",
        content: `Content ${i}`,
      },
    });
    expect(response.ok()).toBeTruthy();
    ids.push(capability);
  }
  expect(
    (
      await request.put(`/app/agents/${a.id}`, {
        headers: { origin: appOrigin },
        data: { ...a, capability_ids: ids.slice(0, 9) },
      })
    ).ok(),
  ).toBeTruthy();
  await page.reload();
  for (const name of ["指令片段", "Plugins", "知识"])
    await expect(
      page.getByRole("button", { name: `新建${name}`, exact: true }),
    ).toBeEnabled();
  await expect(page.getByText("9 / 8")).toHaveCount(0);
  await page.getByRole("button", { name: "知识", exact: true }).click();
  await page.getByRole("checkbox", { name: "Shared 9", exact: true }).check();
  await page.getByRole("button", { name: "完成", exact: true }).click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("button", { name: "已保存" })).toBeVisible();
  const saved = await (await request.get(`/app/agents/${a.id}`)).json();
  expect(saved.capability_ids).toEqual(ids);
});

test("Agent engine options narrow to the selected Harness and reach new conversations", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await page.getByLabel("执行引擎", { exact: true }).click();
  await page.getByRole("option", { name: "Codex", exact: true }).click();
  await page.getByLabel("推理强度", { exact: true }).click();
  await page.getByRole("option", { name: "极少", exact: true }).click();
  await page.getByLabel("网页搜索", { exact: true }).click();
  await page.getByRole("option", { name: "实时搜索", exact: true }).click();
  await page.getByLabel("执行引擎", { exact: true }).click();
  await page.getByRole("option", { name: "MiniMax Code", exact: true }).click();
  await page.getByLabel("执行引擎", { exact: true }).click();
  await page.getByRole("option", { name: "Claude Code", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("推理强度");
  await expect(page.getByLabel("网页搜索", { exact: true })).toContainText("关闭");
  await page.getByLabel("执行引擎", { exact: true }).click();
  await page.getByRole("option", { name: "Codex", exact: true }).click();
  await page.getByLabel("推理强度", { exact: true }).click();
  await page.getByRole("option", { name: "高", exact: true }).click();
  await page.getByLabel("网页搜索", { exact: true }).click();
  await page.getByRole("option", { name: "实时搜索", exact: true }).click();
  await page.getByText("高级", { exact: true }).click();
  await page.getByLabel("推理摘要", { exact: true }).click();
  await page.getByRole("option", { name: "自动", exact: true }).click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("button", { name: "已保存" })).toBeVisible();
  await converse(page);
  await expect(
    page.getByRole("heading", { name: "Review code", exact: true }),
  ).toBeVisible();
  const core = await (
    await request.get("http://127.0.0.1:18181/counts")
  ).json();
  const sent = core.sessions.at(-1).agent;
  expect(sent.x_agents_core.harness).toBe("codex");
  expect(sent.tools).toContainEqual({ type: "web_search", mode: "live" });
  expect(sent.reasoning).toMatchObject({ effort: "high", summary: "auto" });
  await page.getByRole("button", { name: "会话详情" }).click();
  await expect(page.getByRole("dialog")).toContainText("网页搜索实时搜索");
});
