import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { AgentCoreError } from "@oac/agents-client";
import { flowError, keyFlowReducer, type KeyFlow } from "./key-flows";
import { KeyFlowDialogs, PendingKeyNotice } from "./KeyFlowDialogs";
import type { KeyFlowControls } from "./use-key-flow";
import { type AdminIssuedKey, type Project } from "../../lib/admin-view";

const project: Project = { id: "proj_7f3a91c2", name: "Production", status: "active", created_at: 100, archived_at: null, active_key_count: 1 };
const secret = "pc_live_" + "s".repeat(40);
const issued: AdminIssuedKey = { id: "9f0e1d2c-3b4a-4c5d-8e6f-7a8b9c0d1e2f", project_id: "proj_7f3a91c2", name: "bob-laptop", prefix: "pc_live_Zq8", created_at: 300, revoked_at: null, key: secret };
const controls = (flow: KeyFlow): KeyFlowControls => ({ flow, dispatch: () => undefined, submit: async () => undefined });
// The how-to-call card under a new key reads the installation through the query cache.
const render = (element: ReactElement) => renderToStaticMarkup(<QueryClientProvider client={new QueryClient()}>{element}</QueryClientProvider>);

describe("one-time key display", () => {
  it("shows the plaintext read-only, without autofill, with a copy button and a saved confirmation", () => {
    const html = render(<KeyFlowDialogs controls={controls({ step: "issued", project, issued, open: true })} taken={[]} />);
    expect(html).toContain(`value="${secret}"`);
    expect(html).toContain("readOnly");
    expect(html).toContain('autoComplete="off"');
    expect(html).toContain("This key is shown only once.");
    expect(html).toContain("Copy key");
    expect(html).toContain("I&#x27;ve saved this key");
  });

  it("keeps a closed dialog's key on the page until it is confirmed saved", () => {
    const open = renderToStaticMarkup(<PendingKeyNotice controls={controls({ step: "issued", project, issued, open: true })} />);
    expect(open).toBe("");
    const closed = render(<PendingKeyNotice controls={controls({ step: "issued", project, issued, open: false })} />);
    expect(closed).toContain(`value="${secret}"`);
    expect(closed).toContain("is shown only until you confirm it was saved");
  });

  it("names the project and blocks a key name already used by an active key", () => {
    const html = renderToStaticMarkup(<KeyFlowDialogs controls={controls({ step: "issue", project, name: "alice", busy: false, error: null })} taken={["alice"]} />);
    expect(html).toContain("Issue a key for Production");
    expect(html).toContain("An active key of this project already has this name.");
    expect(html).toMatch(/<button class="button primary" type="submit" form="key-issue-form" disabled="">/);
  });
});


describe("typed name rejection", () => {
  it("binds the server error to the name input and leaves a rejected form usable", () => {
    const flow: KeyFlow = { step: "issue", project, name: "valid", busy: false, error: flowError(new AgentCoreError("raw", 400, "invalid_name", "name", undefined, { max_length: 80 })) };
    const html = renderToStaticMarkup(<KeyFlowDialogs controls={controls(flow)} taken={[]} />);
    expect(html).toContain('aria-invalid="true"');
    expect(html).toContain("80 characters");
    expect(html).not.toMatch(/form="key-issue-form" disabled/);
    expect(keyFlowReducer(flow, { type: "setName", name: "corrected" })).toMatchObject({ error: null, busy: false });
  });
});
