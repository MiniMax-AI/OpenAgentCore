import { describe, expect, it } from "vitest";

import { project, session, sessionLister } from "../overview/test-fixtures";
import { readProjectsSessions, readSessions } from "./project-sessions";

const many = (count: number) => Array.from({ length: count }, (_, index) => session(`s${index}`, { created_at: 10_000 - index }));

describe("readSessions", () => {
  it("walks pages newest first until the list ends", async () => {
    const lister = sessionLister(many(250));
    const read = await readSessions(lister, { maxSessions: 1_000 });
    expect(read.complete).toBe(true);
    expect(read.sessions).toHaveLength(250);
    expect(lister.calls).toEqual(["first", "s99", "s199"]);
  });

  it("stops early when enough was read and marks a capped read incomplete", async () => {
    const early = sessionLister(many(500));
    expect(await readSessions(early, { maxSessions: 1_000, enough: (sessions) => sessions.length >= 100 })).toMatchObject({ complete: true });
    expect(early.calls).toEqual(["first"]);
    const capped = await readSessions(sessionLister(many(500)), { maxSessions: 150 });
    expect(capped.complete).toBe(false);
    expect(capped.sessions).toHaveLength(150);
  });
});

describe("readProjectsSessions", () => {
  it("reads projects in parallel and reports a failing project by name", async () => {
    const { reads, failures } = await readProjectsSessions(
      [project("ok"), project("down")],
      (target) => (target.id === "ok" ? sessionLister(many(3)) : { listSessions: async () => { throw new Error("HTTP 503"); } }),
      () => ({ maxSessions: 100 }),
    );
    expect(reads.map((read) => [read.project.id, read.sessions.length])).toEqual([["ok", 3]]);
    expect(failures.map((failure) => [failure.project.id, failure.message])).toEqual([["down", "HTTP 503"]]);
  });
});
