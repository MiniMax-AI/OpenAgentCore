import { describe, expect, it } from "vitest";

import type {
  AgentCore,
  AgentSession,
  RuntimeObservation,
} from "@agents-core-web/agents-client";

import {
  loadRuntimeDashboardSnapshot,
  RuntimeSnapshotIncompleteError,
} from "./runtime-snapshot";

const session = { id: "11111111-1111-4111-8111-111111111111" } as AgentSession;
const observation = {
  id: session.id,
  session_id: session.id,
} as RuntimeObservation;

function core(
  sessions: AgentSession[],
  observations: RuntimeObservation[],
): Pick<AgentCore, "listSessions" | "listRuntimeObservations"> {
  return {
    listSessions: async () => ({ object: "list", data: sessions, has_more: false, first_id: session.id, last_id: session.id }),
    listRuntimeObservations: async () => ({ object: "list", data: observations, has_more: false, first_id: session.id, last_id: session.id }),
  };
}

describe("Runtime Dashboard snapshot coordination", () => {
  it("publishes only exact Session and observation identity sets", async () => {
    const value = await loadRuntimeDashboardSnapshot(core([session], [observation]));
    expect(value?.sessions).toEqual([session]);
    expect(value?.observations).toEqual([observation]);

    await expect(loadRuntimeDashboardSnapshot(core([session], []))).rejects.toBeInstanceOf(
      RuntimeSnapshotIncompleteError,
    );
  });

  it("does not discard a coherent API snapshot when unrelated local Session detail changes", async () => {
    const changing = core([session], [observation]);
    changing.listRuntimeObservations = async () => {
      return { object: "list", data: [observation], has_more: false, first_id: session.id, last_id: session.id };
    };

    await expect(loadRuntimeDashboardSnapshot(changing)).resolves.toMatchObject({
      sessions: [session],
      observations: [observation],
    });
  });

  it("fails closed when the target budget is exceeded", async () => {
    await expect(loadRuntimeDashboardSnapshot(core([session], [observation]), undefined, 0)).rejects.toThrow(
      "target budget",
    );
  });
});
