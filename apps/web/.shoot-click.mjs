// Temporary: open a view, click through steps, screenshot. Delete before commit.
// node .shoot-click.mjs <outFile> <view> [zh-CN|en] <step>... ; step = "css=<selector>" | "text=<text>" | "row=<n>"
import { chromium } from "@playwright/test";
const [,, outFile, view, locale = "zh-CN", ...steps] = process.argv;
const THEME = process.env.THEME || "light";
const browser = await chromium.launch({ headless: true, executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome`, args: ["--no-sandbox", "--disable-gpu"], env: { ...process.env, LD_LIBRARY_PATH: `${process.env.HOME}/.local/chromelibs/root/usr/lib/x86_64-linux-gnu` } });
const page = await (await browser.newContext({ viewport: { width: 1440, height: Number(process.env.HEIGHT || 900) } })).newPage();
const errors = [];
page.on("pageerror", (error) => errors.push(String(error).slice(0, 200)));
await page.addInitScript(([l, th]) => { localStorage.setItem("agents-core-web.language", l); localStorage.setItem("agents-core-web.theme", th); }, [locale, THEME]);
await page.goto(`http://127.0.0.1:4274/#${view}`);
await page.waitForTimeout(2200);
for (const step of steps) {
  const [kind, value] = [step.slice(0, step.indexOf("=")), step.slice(step.indexOf("=") + 1)];
  if (kind === "css") await page.locator(value).first().click();
  else if (kind === "text") await page.getByText(value, { exact: true }).first().click();
  else if (kind === "row") await page.locator("tbody tr").nth(Number(value)).click();
  await page.waitForTimeout(900);
}
await page.mouse.move(0, 0);
await page.screenshot({ path: outFile, fullPage: Boolean(process.env.FULL) });
console.log(JSON.stringify(errors));
await browser.close();
process.exit(0);
