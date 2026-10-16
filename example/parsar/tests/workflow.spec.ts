import { zipSync, strToU8 } from "fflate";
import { test, expect, type Page } from "@playwright/test";
async function navigate(page: Page, name: string) {
  if (["Skills", "MCP", "模型", "凭据"].includes(name)) {
    await page.getByRole("link", { name: "资源库", exact: true }).click();
    await page.getByRole("tab", { name, exact: true }).click();
  } else
    await page
      .getByRole("link", {
        name:
          (
            {
              Agents: "Agent 模板",
              运行时: "环境模板",
              飞书: "飞书接入",
              能力: "资源库",
            } as Record<string, string>
          )[name] || name,
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
async function agent(page: Page, withEnvironment = false) {
  await navigate(page, "Agents");
  await page.getByRole("button", { name: "新建 Agent" }).click();
  await page.getByLabel("Agent 名称").fill("审查助手");
  await page.getByRole("button", { name: "模型", exact: true }).click();
  await page
    .getByRole("radio", { name: "Moonshot / Kimi", exact: true })
    .click();
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
  if (withEnvironment) {
    await page
      .getByRole("button", { name: "默认环境模板", exact: true })
      .click();
    await page.getByRole("radio", { name: "开发沙箱", exact: true }).check();
  }
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
}
async function session(page: Page, name = "代码审查") {
  await page.getByRole("button", { name: "开始会话", exact: true }).click();
  await page.getByLabel("会话名称").fill(name);
  await page.getByLabel("环境模板", { exact: true }).click();
  await page.getByRole("option", { name: "开发沙箱", exact: true }).click();
  await page.getByLabel("第一条消息").fill("Review code");
  await page.getByRole("button", { name: "开始", exact: true }).click();
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
    "connection_logins",
    "connection_bindings",
    "user_connections",
    "sessions",
    "projects",
    "tasks",
    "activity",
    "feishu_bots",
    "feishu_registration",
    "audit",
    "settings",
    "capabilities",
    "mcp_oauth",
    "agent_mcp_tokens",
    "agent_mcp_runs",
    "schedule_occurrences",
    "schedule_requests",
    "feishu_inbound",
    "feishu_outbound",
    "feishu_threads",
    "feishu_selections",
    "feishu_health",
    "schedules",
    "agents",
    "instances",
    "templates",
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
    await request.get("http://127.0.0.1:18180/app/runtimes")
  ).json();
  const template = rows.find(
    (row: { name: string }) => row.name === "飞书办公",
  );
  expect(template.env).toEqual({ OFFICE_TOKEN: null });
  expect(template.files).toEqual([
    { path: "/workspace/settings.txt", content: "office=true" },
  ]);
});
test("Agent to multiple independent Sessions, continuation and cancellation", async ({
  page,
  request,
}, testInfo) => {
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await session(page);
  await expect(
    page.getByRole("heading", { name: "代码审查", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("已检查登录流程并补充验证。")).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "代码审查", exact: true }),
  ).toBeVisible();
  await page.getByRole("textbox").fill("Delayed response");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await expect(page.getByRole("button", { name: "取消执行" })).toBeVisible();
  await page.getByRole("button", { name: "取消执行" }).click();
  await expect(page.getByText("已取消", { exact: true })).toBeVisible();
  await expect(page.getByRole("textbox")).toBeEnabled({ timeout: 15000 });
  await expect(page.getByRole("textbox")).toHaveValue("");
  await page.screenshot({ path: testInfo.outputPath("session-light.png") });
  await page.getByRole("button", { name: "切换深色" }).click();
  await page.screenshot({ path: testInfo.outputPath("session-dark.png") });
  await page.getByRole("link", { name: "返回 Agent", exact: true }).click();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await page.getByLabel("主指令", { exact: true }).fill("New instructions");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await session(page, "另一段会话");
  await expect(
    page.getByRole("heading", { name: "另一段会话", exact: true }),
  ).toBeVisible();
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
test("lost creation response can be recovered from the Agent without a duplicate Session", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await request.post("http://127.0.0.1:18181/lose-creation");
  await session(page);
  await expect(page.getByRole("alert")).toContainText("lost");
  await page.keyboard.press("Escape");
  await page.getByRole("tab", { name: "动态", exact: true }).click();
  await page.getByRole("link", { name: /代码审查.*待恢复/ }).click();
  await page.reload();
  await page.getByRole("button", { name: "恢复创建" }).click();
  await expect(
    page.getByRole("heading", { name: "代码审查", exact: true }),
  ).toBeVisible();
  expect(
    (await (await request.get("http://127.0.0.1:18181/counts")).json())
      .sessions,
  ).toHaveLength(1);
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
  page.on("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "移除 常用 Kimi" }).click();
  await expect(page.getByRole("alert")).toContainText("仍被引用");
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
test("mobile navigation, help and single creation action", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/#/agents");
  await expect(
    page.getByRole("button", { name: "新建 Agent", exact: true }),
  ).toHaveCount(1);
  await page.getByRole("button", { name: "新建 Agent", exact: true }).focus();
  await page.keyboard.press("Shift+Tab");
  await expect(page.getByRole("tooltip")).toBeVisible();
  await page.keyboard.press("Escape");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("mobile.png") });
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
test("text arrives incrementally before durable completion, then survives reload without duplicates", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await session(page);
  await expect(
    page.getByRole("heading", { name: "代码审查", exact: true }),
  ).toBeVisible();
  await page.getByRole("textbox").fill("Stream reply");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  const reply = page
    .getByRole("article", { name: "Agent 回复" })
    .filter({ hasText: "流式第一段" });
  await expect(reply).toContainText("流式第一段", { timeout: 5000 });
  await expect(reply).not.toContainText("，第二段完成。");
  await expect(page.getByTestId("first-text-time")).not.toHaveText("—");
  await expect(page.getByTestId("response-time")).toHaveText("—");
  await expect(page.getByTestId("response-time")).not.toHaveText("—");
  const responseTime = await page.getByTestId("response-time").innerText();
  const firstTextTime = await page.getByTestId("first-text-time").innerText();
  expect(parseFloat(responseTime)).toBeGreaterThan(parseFloat(firstTextTime));
  await request.post("http://127.0.0.1:18181/drop-streams");
  await expect(reply).toContainText("流式第一段，第二段完成。", {
    timeout: 10000,
  });
  await expect(reply).toHaveCount(1);
  await page.reload();
  await expect(reply).toHaveCount(1);
  await expect(reply).toContainText("流式第一段，第二段完成。");
  await expect(page.getByTestId("response-time")).toHaveText(responseTime);
  await expect(page.getByTestId("first-text-time")).toHaveText(firstTextTime);
});

test("an active reply without a replayed item baseline refreshes immediately on its next delta", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await session(page);
  await expect(
    page.getByRole("heading", { name: "代码审查", exact: true }),
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
    page.getByRole("heading", { name: "代码审查", exact: true }),
  ).toBeVisible();
  await request.post("http://127.0.0.1:18181/resume-output");
  await expect(
    page
      .getByRole("article", { name: "Agent 回复" })
      .filter({ hasText: "断线前，断线后仍在生成" }),
  ).toBeVisible({ timeout: 1500 });
});

test("Session uploads insert a file path and download binary artifacts without leaving the conversation", async ({
  page,
}) => {
  await page.goto("/");
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await session(page);
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
  await page.getByRole("link", { name: "返回 Agent" }).click();
  await session(page, "第二个文件会话");
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

test("unsupported environment placements reject before execution", async ({
  request,
}) => {
  for (const environment of ["none", "self_hosted"]) {
    const response = await request.put(
      "http://127.0.0.1:18180/app/runtimes/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      {
        headers: { origin: "http://127.0.0.1:18180" },
        data: { name: "Unsupported", environment },
      },
    );
    expect(response.status()).toBe(400);
  }
});

test("a send completed after leaving the Session does not restore a sent draft", async ({
  page,
}) => {
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await session(page);
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
  await page.getByRole("link", { name: "返回 Agent" }).click();
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

test("project task dispatch, discussion, review, schedules and Feishu configuration form a complete product flow", async ({
  page,
}, testInfo) => {
  await resources(page);
  await agent(page, true);
  await navigate(page, "项目");
  await page.getByRole("button", { name: "新建项目", exact: true }).click();
  await page.getByLabel("项目名称").fill("Website launch");
  await page.getByLabel("项目背景").fill("Accessible engineering workspace");
  await page
    .getByLabel("仓库地址（可选）")
    .fill("https://github.com/example/website");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await page.getByRole("link", { name: "Website launch", exact: true }).click();
  await page.getByRole("button", { name: "新建任务", exact: true }).click();
  await page.getByLabel("任务标题").fill("Verify login keyboard flow");
  await page
    .getByLabel("任务说明")
    .fill("Check focus order and return a concise verification report.");
  await page.getByLabel("执行 Agent", { exact: true }).click();
  await page.getByRole("option", { name: "审查助手", exact: true }).click();
  await page.getByLabel("任务环境模板", { exact: true }).click();
  await page.getByRole("option", { name: "开发沙箱", exact: true }).click();
  await page.getByRole("button", { name: "保存任务", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      name: "Verify login keyboard flow",
      exact: true,
    }),
  ).toBeVisible();
  await page
    .getByLabel("任务评论")
    .fill("Acceptance: every control is keyboard accessible.");
  await page.getByRole("button", { name: "添加评论", exact: true }).click();
  await page.getByRole("button", { name: "分配执行", exact: true }).click();
  await expect(page.getByText("已检查登录流程并补充验证。")).toBeVisible();
  await page.getByRole("link", { name: "返回任务", exact: true }).click();
  await expect(
    page.getByText("Acceptance: every control is keyboard accessible."),
  ).toBeVisible();
  await page.getByLabel("任务状态", { exact: true }).click();
  await page.getByRole("option", { name: "待验收", exact: true }).click();
  await page.getByLabel("任务状态", { exact: true }).click();
  await page.getByRole("option", { name: "已完成", exact: true }).click();
  await page.reload();
  await expect(
    page.getByRole("button", { name: "重新执行", exact: true }),
  ).toBeDisabled();
  await navigate(page, "任务");
  await page.getByLabel("搜索任务").fill("keyboard");
  await page.getByRole("button", { name: "看板视图", exact: true }).click();
  await expect(
    page.getByRole("region", { name: "已完成", exact: true }),
  ).toContainText("Verify login keyboard flow");
  await page.screenshot({ path: testInfo.outputPath("tasks-light.png") });
  await page.getByRole("button", { name: "切换深色" }).click();
  await page.screenshot({ path: testInfo.outputPath("tasks-dark.png") });
  await page.getByRole("button", { name: "列表视图", exact: true }).click();
  await expect(
    page.getByRole("link", { name: /Verify login keyboard flow/ }),
  ).toBeVisible();
  await navigate(page, "定时任务");
  await page.getByRole("button", { name: "新建计划", exact: true }).click();
  await page.getByLabel("计划名称").fill("Daily accessibility review");
  await page.getByLabel("任务模板").click();
  await page
    .getByRole("option", { name: "Verify login keyboard flow", exact: true })
    .click();
  await page.getByLabel("间隔（分钟）").fill("1440");
  await page.getByRole("button", { name: "保存计划", exact: true }).click();
  await expect(
    page.getByText("Daily accessibility review", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "暂停", exact: true }).click();
  await navigate(page, "飞书");
  await page.getByRole("button", { name: "添加机器人", exact: true }).click();
  await page.getByLabel("机器人名称", { exact: true }).fill("Team bot");
  await page.getByLabel("App ID", { exact: true }).fill("cli_0123456789abcdef");
  await page.getByLabel("App Secret", { exact: true }).fill("fixture-secret");
  await page.getByRole("button", { name: "添加群", exact: true }).click();
  await page.getByLabel("群 ID 1", { exact: true }).fill("oc_fixture");
  await page.getByLabel("群 Agent 1", { exact: true }).click();
  await page.getByRole("option", { name: "审查助手", exact: true }).click();
  await page.getByLabel("群环境模板 1", { exact: true }).click();
  await page.getByRole("option", { name: "开发沙箱", exact: true }).click();
  await page.getByRole("checkbox", { name: /允许群成员切换 Agent/ }).check();
  await page.getByRole("button", { name: "保存飞书配置", exact: true }).click();
  await page.reload();
  await page.getByRole("button", { name: "配置机器人", exact: true }).click();
  await page
    .getByRole("button", {
      name: "高级设置：群聊、应用凭证与更多选项",
      exact: true,
    })
    .click();
  await expect(page.getByLabel("App ID", { exact: true })).toHaveValue(
    "cli_0123456789abcdef",
  );
  await expect(page.getByLabel("App Secret", { exact: true })).toHaveValue("");
  await expect(page.getByLabel("群 ID 1", { exact: true })).toHaveValue(
    "oc_fixture",
  );
  await expect(
    page.getByRole("checkbox", { name: /允许群成员切换 Agent/ }),
  ).toBeChecked();
  await page.getByRole("button", { name: "收起高级设置", exact: true }).click();
  await expect(page.getByLabel("App ID", { exact: true })).not.toBeVisible();
  await expect(
    page.getByRole("button", {
      name: "启用机器人（私聊和已有群聊）",
      exact: true,
    }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "回复私聊的 Agent", exact: true })
    .click();
  await page.getByRole("radio", { name: "审查助手", exact: true }).check();
  await expect(
    page.getByText(
      "此机器人还有 1 个群聊接入。启用后，私聊和这些群聊会一起恢复。",
      { exact: true },
    ),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "启用机器人（私聊和已有群聊）", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "保存私聊设置", exact: true }),
  ).toBeVisible();
  const bots = await (await page.request.get("/app/feishu/bots")).json();
  const bot = bots.find((row: { name: string }) => row.name === "Team bot");
  expect(bot.routes).toHaveLength(2);
  expect(
    bot.routes.find((row: { chat_id: string }) => row.chat_id === "oc_fixture")
      .allow_agent_selection,
  ).toBe(true);
  expect(
    bot.routes.find((row: { chat_id: string }) => row.chat_id === "p2p")
      .runtime_id,
  ).toBe("");
  await expect(
    page.getByText("已连接，可以去飞书聊天了", { exact: true }),
  ).not.toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({ path: testInfo.outputPath("feishu-desktop.png") });
  await page
    .getByRole("button", {
      name: "高级设置：群聊、应用凭证与更多选项",
      exact: true,
    })
    .click();
  await expect(
    page.getByRole("button", { name: "保存飞书配置", exact: true }),
  ).toBeVisible();
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
  await page.getByRole("button", { name: "English", exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(
    page.getByRole("link", { name: "Conversation", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Settings", exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue(
    "Research studio",
  );
  await page.getByRole("button", { name: "简体中文", exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(
    page.getByRole("link", { name: "对话", exact: true }),
  ).toBeVisible();
});

test("business questions appear in Inbox and use the public function-result event", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await session(page);
  await expect(
    page.getByRole("button", { name: "发送", exact: true }),
  ).toBeVisible();
  const question = await request.post("http://127.0.0.1:18181/question", {
    headers: { Authorization: "Bearer fixture-project-key" },
  });
  expect(question.ok()).toBeTruthy();
  await navigate(page, "收件箱");
  await expect(page.getByText("选择报告语言", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "English", exact: true }).click();
  await page.getByRole("button", { name: "提交回答", exact: true }).click();
  await expect(
    page.getByText("没有待回答的问题", { exact: true }),
  ).toBeVisible();
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

test("installed frontend extension renders a Parsar slot and disappears when disabled", async ({
  page,
}) => {
  await page.goto("/#/capabilities");
  await page.getByRole("tab", { name: "Plugins", exact: true }).click();
  await page.getByRole("button", { name: "添加能力", exact: true }).click();
  await page.getByLabel("名称", { exact: true }).fill("Workspace helper");
  await page.getByLabel("Plugin ZIP（新版本）").setInputFiles({
    name: "helper.zip",
    mimeType: "application/zip",
    buffer: Buffer.from(
      zipSync({
        ".codex-plugin/plugin.json": strToU8(
          JSON.stringify({ name: "helper" }),
        ),
      }),
    ),
  });
  const code = `const {React,definePlugin}=window.__PARSAR_PLUGIN_API__;definePlugin('helper',ctx=>ctx.slots.register('layout.header.actions',{key:'helper',component:()=>React.createElement('div',{'data-testid':'plugin-banner'},'Workspace extension ready')}));`;
  await page.getByLabel("前端扩展 JavaScript（可选）").setInputFiles({
    name: "client.js",
    mimeType: "text/javascript",
    buffer: Buffer.from(code),
  });
  await page.getByRole("button", { name: "保存能力", exact: true }).click();
  await expect(page.getByTestId("plugin-banner")).toHaveText(
    "Workspace extension ready",
  );
  await page.reload();
  await expect(page.getByTestId("plugin-banner")).toBeVisible();
  await page.getByRole("tab", { name: "Plugins", exact: true }).click();
  await page
    .getByRole("button", { name: "Workspace helper", exact: true })
    .click();
  await page.getByLabel("停用此能力").check();
  await page.getByRole("button", { name: "保存能力", exact: true }).click();
  await expect(page.getByTestId("plugin-banner")).toHaveCount(0);
});

test("template editor preserves drafts through inline resource creation and reload", async ({
  page,
  request,
}) => {
  await resources(page);
  await navigate(page, "Agents");
  await page.getByRole("button", { name: "新建 Agent", exact: true }).click();
  await page.getByLabel("Agent 名称", { exact: true }).fill("Draft agent");
  await page
    .getByLabel("主指令", { exact: true })
    .fill("Keep these instructions");
  await page.getByRole("button", { name: "添加环境模板", exact: true }).click();
  await page.getByLabel("名称", { exact: true }).fill("Inline environment");
  await page
    .getByRole("dialog")
    .last()
    .getByRole("button", { name: "保存", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toHaveCount(1);
  await expect(
    page.getByRole("region", { name: "默认环境模板", exact: true }),
  ).toContainText("Inline environment");
  await expect(page.getByLabel("主指令", { exact: true })).toHaveValue(
    "Keep these instructions",
  );
  expect(await (await request.get("/app/agents")).json()).toHaveLength(0);
  await page.reload();
  await expect(page.getByLabel("Agent 名称", { exact: true })).toHaveValue(
    "Draft agent",
  );
  await expect(
    page.getByRole("region", { name: "默认环境模板", exact: true }),
  ).toContainText("Inline environment");
  await page.getByRole("button", { name: "添加MCP", exact: true }).click();
  await page.getByLabel("名称", { exact: true }).fill("Inline tool");
  await page.getByLabel("服务标识").fill("inline-tool");
  await page.getByLabel("MCP 地址").fill("https://example.com/mcp");
  await page
    .getByRole("dialog")
    .last()
    .getByRole("button", { name: "保存", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "移除绑定 Inline tool", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "模型", exact: true }).click();
  await page
    .getByRole("radio", { name: "Moonshot / Kimi", exact: true })
    .click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await expect(
    page.getByRole("tab", { name: "配置", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await expect(page.getByText("Inline tool", { exact: true })).toBeVisible();
});

test("an existing Agent draft restores once in the detail rail", async ({
  page,
}) => {
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await page
    .getByLabel("主指令", { exact: true })
    .fill("Restored detail draft");
  await page.reload();
  await expect(page.getByRole("dialog")).toHaveCount(1);
  await expect(page.getByLabel("主指令", { exact: true })).toHaveValue(
    "Restored detail draft",
  );
  await page.getByRole("button", { name: "放弃草稿", exact: true }).click();
  await page.reload();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("drafts for different Agents survive editing and copying other templates", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  const [first] = await (await request.get("/app/agents")).json();
  const secondId = crypto.randomUUID();
  const created = await request.put(`/app/agents/${secondId}`, {
    headers: { origin: "http://127.0.0.1:18180" },
    data: { ...first, id: secondId, revision: 0, name: "Second Agent" },
  });
  expect(created.ok()).toBeTruthy();
  await page.goto(`/#/agents/${first.id}?view=full`);
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await page.getByLabel("主指令", { exact: true }).fill("First unsaved draft");
  await page.keyboard.press("Escape");
  await page.goto(`/#/agents/${secondId}?view=full`);
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await page.getByLabel("主指令", { exact: true }).fill("Second unsaved draft");
  await page.keyboard.press("Escape");
  await page.goto(`/#/agents/${first.id}?view=full`);
  await expect(page.getByLabel("主指令", { exact: true })).toHaveValue(
    "First unsaved draft",
  );
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await page.goto(`/#/agents/${secondId}?view=full`);
  await expect(page.getByLabel("主指令", { exact: true })).toHaveValue(
    "Second unsaved draft",
  );
  await page.keyboard.press("Escape");
  await page.goto("/#/agents");
  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: "复制 审查助手", exact: true })
    .click();
  await page.getByLabel("Agent 名称", { exact: true }).fill("Copied draft");
  await page.keyboard.press("Escape");
  await page.reload();
  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: "恢复草稿: Second Agent", exact: true })
    .click();
  await expect(page.getByLabel("主指令", { exact: true })).toHaveValue(
    "Second unsaved draft",
  );
  await page.getByRole("button", { name: "放弃草稿", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "恢复草稿: Copied draft", exact: true }),
  ).toBeVisible();
});

test("personal connections show an authorization link and QR, and waiting tasks can retry or cancel", async ({
  page,
}) => {
  const botId = crypto.randomUUID(),
    runId = crypto.randomUUID();
  let cancelled = false,
    retries = 0;
  const login = {
    id: crypto.randomUUID(),
    status: "pending",
    verification_url: "https://accounts.feishu.cn/device?code=TEST",
    user_code: "TEST",
    expires_at: Date.now() + 240000,
  };
  await page.route("**/app/connections", (route) =>
    route.fulfill({
      json: [
        {
          service: "feishu",
          bot_id: botId,
          app_id: "cli_fixture",
          name: "Office",
          required_scopes: "calendar:calendar:readonly",
          login,
        },
      ],
    }),
  );
  await page.goto("/#/connections");
  await expect(
    page.getByRole("heading", { name: "我的连接", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: login.verification_url, exact: true }),
  ).toHaveAttribute("href", login.verification_url);
  await expect(
    page.locator("svg").filter({ has: page.locator("path") }),
  ).not.toHaveCount(0);
  await page.route(`**/app/sessions/${runId}`, async (route) => {
    if (route.request().method() === "DELETE") {
      cancelled = true;
      await route.fulfill({ json: { deleted: true } });
      return;
    }
    await route.fulfill({
      json: {
        id: runId,
        name: "查看日程",
        agent_id: crypto.randomUUID(),
        runtime_id: crypto.randomUUID(),
        created_at: 0,
        ...(cancelled
          ? { deleted_at: Date.now() }
          : {
              connection_wait: {
                ...login,
                status: retries ? "pending" : "expired",
              },
            }),
      },
    });
  });
  await page.route(`**/app/sessions/${runId}/connection`, (route) => {
    retries++;
    return route.fulfill({ json: login });
  });
  await page.goto(`/#/sessions/${runId}`);
  await page.getByRole("button", { name: "重新连接", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "等待连接账号", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "取消任务", exact: true }).click();
  await expect(page.getByText("此会话已删除", { exact: true })).toBeVisible();
  expect(retries).toBe(1);
  expect(cancelled).toBe(true);
});

test("environment connection mappings persist through the ordinary template editor", async ({
  page,
  request,
}) => {
  const id = crypto.randomUUID();
  const created = await request.put(`/app/runtimes/${id}`, {
    headers: { Origin: "http://127.0.0.1:18180" },
    data: { name: "Mapped environment" },
  });
  expect(created.ok()).toBeTruthy();
  const runtime = await created.json();
  await page.goto("/#/runtimes");
  await page
    .getByRole("button", { name: "编辑 Mapped environment", exact: true })
    .click();
  await page.getByRole("button", { name: "添加连接变量", exact: true }).click();
  await page
    .getByLabel("连接变量名称", { exact: true })
    .fill("CUSTOM_ACCOUNT_TOKEN");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await (await request.get(`/app/runtimes/${runtime.id}`)).json())
          .connection_env,
    )
    .toEqual({ CUSTOM_ACCOUNT_TOKEN: "access_token" });
  await page.reload();
  await page
    .getByRole("button", { name: "编辑 Mapped environment", exact: true })
    .click();
  await expect(page.getByLabel("连接变量名称", { exact: true })).toHaveValue(
    "CUSTOM_ACCOUNT_TOKEN",
  );
  await expect(page.getByLabel("连接字段", { exact: true })).toContainText(
    "访问令牌",
  );
});

test("Agent form unifies prompts and searches, edits and unbinds resources without losing the draft", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await page.getByRole("button", { name: "配置", exact: true }).click();
  const editor = page.getByRole("dialog").first();
  await expect(
    editor.getByRole("heading", { name: "系统提示词", exact: true }),
  ).toHaveCount(1);
  await page
    .getByLabel("主指令", { exact: true })
    .fill("Keep the main instructions.");
  await page.getByRole("button", { name: "新建指令片段", exact: true }).click();
  await page.getByLabel("名称", { exact: true }).fill("Report rules");
  await page
    .getByLabel("内容（修改后保存为新版本）")
    .fill("Use a short checklist.");
  await page
    .getByRole("dialog")
    .last()
    .getByRole("button", { name: "保存能力", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toHaveCount(1);
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
  await page.getByLabel("名称", { exact: true }).fill("Documentation MCP");
  await page
    .getByRole("dialog")
    .last()
    .getByRole("button", { name: "保存", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toHaveCount(1);
  await expect(
    page.getByRole("button", {
      name: "移除绑定 Documentation MCP",
      exact: true,
    }),
  ).toBeVisible();
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
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const [saved] = await (await request.get("/app/agents")).json();
  expect(saved.instructions).toBe("Keep the main instructions.");
  expect(saved.capability_ids).toHaveLength(1);
  expect(saved.capability_versions[saved.capability_ids[0]]).toBe("1");
  expect(saved.mcp_ids).toHaveLength(1);
  await page.getByRole("button", { name: "配置", exact: true }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("dialog").getByRole("button", { name: "保存", exact: true }),
  ).toBeInViewport();
  const bounds = await page.getByRole("dialog").boundingBox();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390);
  await expect(page.getByLabel("主指令", { exact: true })).toBeVisible();
  await page.screenshot({
    path: process.env.HOME + "/.oac/tasks/parsar-agent-form/mobile.png",
  });
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(
    page.getByRole("heading", { name: "模型与环境", exact: true }),
  ).toBeInViewport();
  await expect(
    page.getByRole("button", { name: "保存", exact: true }),
  ).toBeInViewport();
  await page.screenshot({
    path: process.env.HOME + "/.oac/tasks/parsar-agent-form/desktop.png",
  });
  await page
    .getByLabel("主指令", { exact: true })
    .fill("Preserve the draft during account management");
  await page.getByRole("link", { name: "管理连接", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "我的连接", exact: true }),
  ).toBeVisible();
  await page.goto(`/#/agents/${saved.id}?view=full`);
  await expect(page.getByLabel("主指令", { exact: true })).toHaveValue(
    "Preserve the draft during account management",
  );
});

test("Agent resource selectors enforce the shared capability budget before saving", async ({
  page,
  request,
}) => {
  await resources(page);
  await agent(page);
  const [a] = await (await request.get("/app/agents")).json();
  const ids: string[] = [];
  for (let i = 0; i < 9; i++) {
    const id = crypto.randomUUID();
    const response = await request.put(`/app/capabilities/${id}`, {
      headers: { origin: "http://127.0.0.1:18180" },
      data: {
        name: `Shared ${i}`,
        kind: i < 4 ? "prompt" : "knowledge",
        content: `Content ${i}`,
      },
    });
    expect(response.ok()).toBeTruthy();
    ids.push(id);
  }
  expect(
    (
      await request.put(`/app/agents/${a.id}`, {
        headers: { origin: "http://127.0.0.1:18180" },
        data: { ...a, capability_ids: ids.slice(0, 8) },
      })
    ).ok(),
  ).toBeTruthy();
  await page.goto(`/#/agents/${a.id}?view=full`);
  await page.getByRole("button", { name: "配置", exact: true }).click();
  for (const name of ["指令片段", "Plugins", "知识"])
    await expect(
      page.getByRole("button", { name: `新建${name}`, exact: true }),
    ).toBeDisabled();
  await page.getByRole("button", { name: "知识", exact: true }).click();
  await expect(
    page.getByRole("checkbox", { name: "Shared 8", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "完成", exact: true }).click();
  await page
    .getByRole("button", { name: "移除绑定 Shared 0", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "新建Plugins", exact: true }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "知识", exact: true }).click();
  await page.getByRole("checkbox", { name: "Shared 8", exact: true }).check();
  await page.getByRole("button", { name: "完成", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "新建Plugins", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const saved = await (await request.get(`/app/agents/${a.id}`)).json();
  expect(saved.capability_ids).toEqual(ids.slice(1));
  expect((await (await request.get("/app/capabilities")).json()).length).toBe(
    9,
  );
});
