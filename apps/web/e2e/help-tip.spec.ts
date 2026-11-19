import { expect, test } from "@playwright/test";

import { expectManagementBoundary, openConsole, writes } from "./console";

test.afterEach(async ({ request }) => expectManagementBoundary(request));

for (const activation of ["keyboard", "pinned", "hover"] as const) {
  test(`Escape dismisses ${activation} help before closing an edited node`, async ({ page, request }) => {
    await openConsole(page, request, "nodes?id=node-local");
    const open = page.getByRole("button", { name: "Edit node", exact: true });
    await open.click();
    const edit = page.getByRole("dialog", { name: "Edit node" });
    const name = edit.getByLabel("Name", { exact: true });
    await name.fill("Unsubmitted node name");

    const help = edit.getByRole("button", { name: "Help", exact: true });
    if (activation === "keyboard") await page.keyboard.press("Tab");
    else if (activation === "hover") await help.hover();
    else await help.click();
    const focused = activation === "hover" ? name : help;
    await expect(focused).toBeFocused();
    await expect(page.locator(".help-tip-popover")).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(page.locator(".help-tip-popover")).toHaveCount(0);
    await expect(edit).toBeVisible();
    await expect(name).toHaveValue("Unsubmitted node name");
    await expect(focused).toBeFocused();
    await expect(help).toHaveAttribute("aria-expanded", "false");

    await page.keyboard.press("Escape");
    await expect(edit).toBeHidden();
    await expect(open).toBeFocused();
    expect(await writes(request)).toEqual([]);
  });
}
