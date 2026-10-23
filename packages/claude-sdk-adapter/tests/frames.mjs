import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// The Go side reads the same file in apps/daemon/internal/agent/claudesdk/bridge_frames_test.go.
export const frames = JSON.parse(readFileSync(new URL("../fixtures/bridge-frames.json", import.meta.url), "utf8"));

// A frame the bridge emits needs a fixture of its kind and may carry only fields the fixture lists.
export function assertFrame(frame, nested = ["message", "reasoning", "call", "withdrawn"]) {
  const fixture = frames.from_bridge.find(candidate => candidate.type === frame.type);
  assert.ok(fixture, `no fixture for bridge frame ${frame.type}`);
  for (const key of Object.keys(frame)) assert.ok(key in fixture, `${frame.type}: field ${key} is not in the fixture`);
  for (const key of nested) if (frame[key]) for (const field of Object.keys(frame[key])) assert.ok(field in fixture[key], `${frame.type}.${key}: field ${field} is not in the fixture`);
}
