import type { SandboxDeployment } from "@oac/agents-client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SuspensionForm } from "./SuspensionSettings";

const deployment = { suspension: { idle_seconds: 300, retention_seconds: 86400 } } as SandboxDeployment;
describe("suspension settings", () => {
  it("edits the idle time and shows retention", () => {
    const html = renderToStaticMarkup(<SuspensionForm deployment={deployment} disabled={false} onSave={async () => true} />);
    expect(html).toContain("Automatic suspension");
    expect(html).toContain('id="sandbox-idle-seconds"');
    expect(html).toContain('value="300"');
    expect(html).toContain("24 hours");
    expect(renderToStaticMarkup(<SuspensionForm deployment={{ ...deployment, suspension: null }} disabled onSave={async () => true} />)).toBe("");
  });
});
