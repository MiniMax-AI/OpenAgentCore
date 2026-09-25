import { describe, expect, it } from "vitest";

import { project, session, sessionLister } from "../overview/test-fixtures";
import { readProjectsSessions, readSessions } from "./project-sessions";

const many = (count: number) => Array.from({ length: count }, (_, index) => session(`s${index}`, { created_at: 10_000 - index }));

describe("readSessions", () => {
  it("walks pages newest first until the list ends", async () => {
    const lister = sessionLister(many(250));
    const read = await readSessions(lister, { maxSessions: 1_000 });
    expect(read).toMatchObject({ complete: true, unrecognized: 0 });
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

  it("counts unrecognized entries without keeping them", async () => {
    const read = await readSessions({
      listSessionsTolerant: async () => ({ object: "list", data: [session("a")], unrecognized: [{ index: 1, id: null }], has_more: false, first_id: "a", last_id: "a" }),
    }, { maxSessions: 100 });
    expect(read).toMatchObject({ unrecognized: 1, complete: true });
    expect(read.sessions).toHaveLength(1);
  });
});

describe("readProjectsSessions", () => {
  it("reads projects in parallel and reports a failing project by name", async () => {
    const { reads, failures } = await readProjectsSessions(
      [project("ok"), project("down")],
      (target) => (target.id === "ok" ? sessionLister(many(3)) : { listSessionsTolerant: async () => { throw new Error("HTTP 503"); } }),
      () => ({ maxSessions: 100 }),
    );
    expect(reads.map((read) => [read.project.id, read.sessions.length])).toEqual([["ok", 3]]);
    expect(failures.map((failure) => [failure.project.id, failure.message])).toEqual([["down", "HTTP 503"]]);
  });
});
