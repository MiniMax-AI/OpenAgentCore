import { test, expect, type Page } from "@playwright/test";
async function navigate(page: Page, name: string) {
  await page.getByRole("link", { name, exact: true }).click();
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
  await page.getByRole("button", { name: "添加运行时" }).click();
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
async function agent(page: Page) {
  await navigate(page, "Agents");
  await page.getByRole("button", { name: "新建 Agent" }).click();
  await page.getByLabel("Agent 名称").fill("审查助手");
  await page.getByLabel("模型", { exact: true }).click();
  await page
    .getByRole("option", { name: "Moonshot / Kimi", exact: true })
    .click();
  await page
    .getByLabel("指令", { exact: true })
    .fill("Read the code and report findings.");
  await page.getByLabel("code-review", { exact: true }).check();
  await page.getByLabel("文档服务", { exact: true }).check();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
}
async function session(page: Page, name = "代码审查") {
  await page.getByRole("button", { name: "开始会话", exact: true }).click();
  await page.getByLabel("会话名称").fill(name);
  await page.getByLabel("运行时", { exact: true }).click();
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
    "sessions",
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

for (const platform of [
  { label: "macOS", path: "/Users/example/project", command: "bash fixture-native-bootstrap.sh" },
  { label: "Windows", path: "C:\\Users\\example\\project", command: "& fixture-native-bootstrap.ps1" },
]) test(`self-hosted ${platform.label} uses Core's command and waits before sending`, async ({
  page,
  request,
}) => {
  await resources(page);
  await navigate(page, "模型");
  await page.getByRole("button", { name: "编辑 Provider Moonshot" }).click();
  await page.getByLabel("Base URL").fill("https://provider.example/v1");
  await page.getByRole("button", { name: "手动选择", exact: true }).click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await navigate(page, "运行时");
  await page.getByRole("button", { name: "添加运行时" }).click();
  await page.getByLabel("名称", { exact: true }).fill("我的 Mac");
  await page.getByLabel("环境类型").click();
  await page.getByRole("option", { name: "用户机器", exact: true }).click();
  await page.getByLabel("机器平台").click();
  await page.getByRole("option", { name: platform.label, exact: true }).click();
  await page
    .getByLabel("工作目录", { exact: true })
    .fill(platform.path);
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await navigate(page, "Agents");
  await page.getByRole("button", { name: "新建 Agent" }).click();
  await page.getByLabel("Agent 名称").fill("本机助手");
  await page.getByLabel("模型", { exact: true }).click();
  await page
    .getByRole("option", { name: "Moonshot / Kimi", exact: true })
    .click();
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await page.getByRole("button", { name: "开始会话", exact: true }).click();
  await page.getByLabel("会话名称").fill("本地会话");
  await page.getByLabel("运行时", { exact: true }).click();
  await page.getByRole("option", { name: "我的 Mac", exact: true }).click();
  await expect(page.getByLabel("第一条消息")).toHaveCount(0);
  await page.getByRole("button", { name: "开始", exact: true }).click();
  await expect(page.getByText("等待连接", { exact: true })).toBeVisible();
  await page.getByLabel("继续对话").fill("hello");
  await expect(
    page.getByRole("button", { name: "发送", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "连接用户机器", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText(platform.command);
  await expect(page.getByRole("dialog")).toContainText(
    platform.path,
  );
  const sessionRoute = "**/v1/agents/sessions/*";
  await page.route(sessionRoute, async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.x_agents_core.installation.expires_at = 1;
    await route.fulfill({ response, json: body });
  });
  await expect(page.getByRole("dialog")).toContainText("安装命令暂不可用");
  await expect(page.getByRole("button", { name: "复制命令", exact: true })).toHaveCount(0);
  await page.unroute(sessionRoute);
  await expect(page.getByRole("dialog")).toContainText(platform.command);
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await request.post("http://127.0.0.1:18181/connect-executor");
  await expect(page.getByText("已连接", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("button", { name: "发送", exact: true }),
  ).toBeEnabled();
  await page.reload();
  await expect(
    page.getByRole("button", { name: "连接用户机器", exact: true }),
  ).toBeVisible();
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
  await page.getByLabel("指令", { exact: true }).fill("New instructions");
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


test("Session uploads insert a file path and download binary artifacts without leaving the conversation", async ({ page }) => {
  await page.goto("/");
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await session(page);
  await expect(page.getByLabel("继续对话")).toBeVisible();
  await page.getByRole("button", { name: "文件", exact: true }).click();
  await page.getByLabel("选择上传文件").setInputFiles({ name: "large.bin", mimeType: "application/octet-stream", buffer: Buffer.alloc(5 * 1024 * 1024 + 1) });
  await expect(page.getByRole("alert")).toContainText("文件不能超过 5 MiB");
  await page.getByLabel("选择上传文件").setInputFiles({ name: "numbers.csv", mimeType: "text/csv", buffer: Buffer.from("a,b\n1,2") });
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByLabel("继续对话")).toHaveValue(/请处理文件：\.\/inputs\/.*numbers.csv/);
  await page.getByRole("button", { name: "文件", exact: true }).click();
  const uploaded = page.waitForRequest((request) => request.method() === "POST" && request.url().endsWith("/files"));
  await page.getByLabel("选择上传文件").setInputFiles({ name: "中".repeat(80) + ".txt", mimeType: "text/plain", buffer: Buffer.from("small") });
  const uploadedPath = (await uploaded).postDataJSON().path;
  expect(Buffer.byteLength(uploadedPath.split("/").at(-1), "utf8")).toBeLessThanOrEqual(255);
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
  await page.route(contentRoute, (route) => route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "文件暂不可用" } }) }));
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
    await route.fulfill({ json: { ...value, status: "failed", error: "运行环境准备失败，请检查配置。" } });
  });
  await expect(page.getByRole("alert")).toContainText("运行环境准备失败，请检查配置。");
});


test("unsupported runtime bindings are explained before Session creation", async ({ page, request }) => {
  await resources(page);
  await agent(page);
  const runtime = await request.put("http://127.0.0.1:18180/app/runtimes/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", {
    headers: { origin: "http://127.0.0.1:18180" },
    data: { name: "用户机器", environment: "self_hosted", platform: "linux", workspace_directory: "/home/user/project" },
  });
  expect(runtime.ok()).toBe(true);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await page.getByRole("button", { name: "开始会话", exact: true }).click();
  await page.getByLabel("会话名称").fill("Blocked");
  await page.getByLabel("运行时", { exact: true }).click();
  await page.getByRole("option", { name: "用户机器", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("不能绑定托管 Skill");
  await expect(page.getByRole("button", { name: "开始", exact: true })).toBeDisabled();
  const counts = await (await request.get("http://127.0.0.1:18181/counts")).json();
  expect(counts.sessions).toHaveLength(0);
});


test("a send completed after leaving the Session does not restore a sent draft", async ({ page }) => {
  await resources(page);
  await agent(page);
  await page.getByRole("link", { name: "打开", exact: true }).click();
  await session(page);
  await expect(page.getByLabel("继续对话")).toBeVisible();
  const sessionURL = page.url();
  let release!: () => void;
  let arrived!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const received = new Promise<void>((resolve) => { arrived = resolve; });
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
  await expect.poll(() => page.evaluate(() => Object.keys(sessionStorage).filter((key) => key.startsWith("oac-example-message-")).length)).toBe(0);
  await page.goto(sessionURL);
  await expect(page.getByLabel("继续对话")).toHaveValue("");
});
