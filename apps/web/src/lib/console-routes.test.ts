import { describe, expect, it } from "vitest";

import { hashWithParams, routeParamsFromHash } from "./console-navigation";
import { consoleHashForView, consoleNavGroups, consoleNavParent, consoleViewFromHash } from "./console-routes";

describe("console routes", () => {
  it("groups pages as Monitor, Resources and Platform with no playground", () => {
    expect(consoleNavGroups.map((group) => group.id)).toEqual(["monitor", "resources", "platform"]);
    expect(consoleNavGroups.flatMap((group) => group.views)).toEqual([
      "overview", "agent-metrics", "sandbox-metrics", "sessions",
      "agents", "templates", "skills", "files", "vaults",
      "api-keys", "nodes", "system",
    ]);
  });

  it("routes hashes, keeps old bookmarks and falls back to the overview", () => {
    expect(consoleViewFromHash("")).toBe("overview");
    expect(consoleViewFromHash("#vaults")).toBe("vaults");
    expect(consoleViewFromHash("#session?space=user_1&id=s1")).toBe("session");
    expect(consoleViewFromHash("#builder")).toBe("agents");
    expect(consoleViewFromHash("#playground")).toBe("sessions");
    expect(consoleViewFromHash("#workbench")).toBe("overview");
    expect(consoleViewFromHash("#sandbox")).toBe("nodes");
    expect(consoleViewFromHash("#unknown")).toBe("overview");
    expect(consoleHashForView("overview")).toBe("");
    expect(consoleHashForView("system")).toBe("#system");
  });

  it("highlights a Session under the Session log", () => {
    expect(consoleNavParent("session")).toBe("sessions");
    expect(consoleNavParent("files")).toBe("files");
  });

  it("carries only well-formed space and resource parameters", () => {
    expect(routeParamsFromHash("#session?space=user_1&id=s-1")).toEqual({ space: "user_1", id: "s-1" });
    expect(routeParamsFromHash("#session?space=bad%20id")).toEqual({ space: undefined, id: undefined });
    expect(hashWithParams("#session", { space: "user_1", id: "s1" })).toBe("#session?space=user_1&id=s1");
    expect(hashWithParams("#files")).toBe("#files");
  });
});
