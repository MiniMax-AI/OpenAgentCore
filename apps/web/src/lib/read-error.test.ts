import { AgentCoreError } from "@oac/agents-client";
import { describe, expect, it } from "vitest";
import i18n from "../i18n";
import { readError } from "./read-error";

describe("read failure localization", () => {
  it("uses typed HTTP status in each language without parsing backend English", () => {
    const error = new AgentCoreError("An intermediary's arbitrary text", 502);
    expect(readError(error, i18n.getFixedT("en", "common"))).toBe("Core request failed (502).");
    expect(readError(error, i18n.getFixedT("zh-CN", "common"))).toBe("Core 请求失败（502）。");
    expect(readError(new TypeError("Failed to fetch"), i18n.getFixedT("zh-CN", "common"))).toBe("无法从 Core 读取数据，请重试更新。");
  });
});
