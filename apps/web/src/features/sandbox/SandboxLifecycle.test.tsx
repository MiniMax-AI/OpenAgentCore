import type { SandboxDeployment } from "@oac/agents-client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { node } from "../overview/test-fixtures";
import { SandboxLifecycle } from "./SandboxLifecycle";

const deployment = { suspension: { idle_seconds: 300, retention_seconds: 86400 } } as SandboxDeployment;
describe("sandbox lifecycle evidence", () => {
  it("uses online slot accounting while retaining suspended records from offline nodes", () => {
    const html = renderToStaticMarkup(<SandboxLifecycle deployment={deployment} nodes={[
      node("online", { active: 2, max_active: 4, retained: 5, max_retained: 8 }),
      node("offline", { online: false, active: 1, max_active: 10, retained: 3, max_retained: 12 }),
    ]} stale={false} />);
    expect(html).toContain("2 / 4");
    expect(html).toContain("5 / 8");
    expect(html).toContain("5 minutes");
    expect(html).not.toContain("3 / 14");
  });
  it("offers policy editing only when the owner supplies controls", () => {
    const nodes = [node("online", { active: 2, max_active: 4, retained: 5, max_retained: 8 })];
    const html = renderToStaticMarkup(<SandboxLifecycle deployment={deployment} nodes={nodes} stale={false} controls={{ disabled: false, onSave: async () => true }} />);
    expect(html).toContain("Capacity and suspension");
    expect(html).toContain('id="sandbox-idle-seconds"');
    expect(html).toContain('value="300"');
    const readonly = renderToStaticMarkup(<SandboxLifecycle deployment={deployment} nodes={nodes} stale={false} />);
    expect(readonly).not.toContain("sandbox-idle-seconds");
    expect(readonly).not.toContain("Edit node");
    const unsupported = renderToStaticMarkup(<SandboxLifecycle deployment={{ ...deployment, suspension: null }} nodes={nodes} stale={false} controls={{ disabled: true, onSave: async () => true }} />);
    expect(unsupported).not.toContain("sandbox-idle-seconds");
  });
  it("keeps node inventory visible when suspension is unsupported", () => {
    const html = renderToStaticMarkup(<SandboxLifecycle deployment={{ ...deployment, suspension: null }} nodes={[]} stale={false}><table><tbody><tr><td>node-inventory</td></tr></tbody></table></SandboxLifecycle>);
    expect(html).toContain("node-inventory");
    expect(html).not.toContain("sandbox-idle-seconds");
  });
  it("does not replace missing inventory with zero or claim fresh evidence after failure", () => {
    const html = renderToStaticMarkup(<SandboxLifecycle deployment={deployment} nodes={null} stale />);
    expect(html).toContain("current state is unconfirmed");
    expect(html).toContain("—");
    expect(html).not.toContain("0 / 0");
    expect(renderToStaticMarkup(<SandboxLifecycle deployment={{ ...deployment, suspension: null }} nodes={[]} stale={false} />)).toBe("");
  });
});
