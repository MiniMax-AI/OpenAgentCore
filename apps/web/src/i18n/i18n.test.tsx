import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it } from "vitest";

import { AppearanceMenu } from "../components/AppearanceMenu";
import { ThemeContext } from "../lib/theme";
import i18n, { resolveLanguage } from ".";
import { resources } from "./resources";

function keys(value: unknown, prefix = ""): string[] {
  if (typeof value === "string") return [prefix];
  if (!value || typeof value !== "object") return [];
  return Object.entries(value).flatMap(([key, child]) => keys(child, prefix ? `${prefix}.${key}` : key));
}

describe("Web internationalization", () => {
  afterEach(async () => {
    await i18n.changeLanguage("en");
  });

  it("keeps English and Chinese resource keys in sync by namespace", () => {
    for (const namespace of Object.keys(resources.en) as Array<keyof typeof resources.en>) {
      expect(keys(resources["zh-CN"][namespace])).toEqual(keys(resources.en[namespace]));
    }
  });

  it("renders the language control in the active language", async () => {
    await i18n.changeLanguage("zh-CN");
    const html = renderToStaticMarkup(<ThemeContext.Provider value={{ preference: "light", resolvedTheme: "light", setPreference: () => undefined }}><AppearanceMenu /></ThemeContext.Provider>);
    expect(html).toContain('aria-label="语言和外观"');
    expect(html).toContain('aria-expanded="false"');
    expect(html).toContain('<span>中</span>');
  });

  it("honors a saved choice and otherwise selects the first supported browser preference", () => {
    expect(resolveLanguage("zh", ["en-US"])).toBe("zh-CN");
    expect(resolveLanguage("en", ["zh-CN"])).toBe("en");
    expect(resolveLanguage(null, ["en-US", "zh-CN"])).toBe("en");
    expect(resolveLanguage(null, ["fr-FR", "zh-Hans", "en-US"])).toBe("zh-CN");
  });

  it("does not fall back to English plural variants in Chinese", async () => {
    await i18n.changeLanguage("zh-CN");
    expect(i18n.t("turns.linkedItems", { ns: "sessions", count: 1 })).toBe("已关联 1 个条目");
    expect(i18n.t("turns.observed", { ns: "sessions", count: 2 })).toBe("已观测 2 个 Turn");
    expect(i18n.t("turns.unassociated", { ns: "sessions", count: 2 })).toBe("有 2 个条目尚未关联到已观测 Turn。");
  });
});
