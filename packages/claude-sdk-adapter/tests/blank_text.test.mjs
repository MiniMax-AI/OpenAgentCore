import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { isBlankText, parseMessageInput } from "../dist/message_input.js";

// Core's Claude admission test reads the same table, so both sides must agree.
const table = JSON.parse(readFileSync(new URL("./blank-text.json", import.meta.url), "utf8"));
const codes = values => new Set(values.map(value => Number.parseInt(value.replace("U+", ""), 16)));
const members = codes(table.members);
const nonMembers = codes(table.non_members);

test("blank text uses exactly the shared union of ECMAScript trim and Go whitespace", () => {
  assert.equal(members.size, 26);
  for (const code of [0xfeff, 0x0085]) assert.ok(members.has(code));
  for (const code of [0x200b, 0x180e]) assert.ok(nonMembers.has(code));
  for (let code = 0; code <= 0x10ffff; code++) {
    if (code >= 0xd800 && code <= 0xdfff) continue;
    const text = String.fromCodePoint(code);
    assert.equal(isBlankText(text), members.has(code), `U+${code.toString(16)}`);
    if (text.trim() === "") assert.ok(members.has(code), `ECMAScript whitespace U+${code.toString(16)} missing`);
  }
  const all = String.fromCodePoint(...members);
  assert.throws(() => parseMessageInput([{ content: [{ type: "input_text", text: all }] }]), /Empty user message/);
  for (const code of nonMembers) {
    const text = " " + String.fromCodePoint(code) + "\ufeff";
    assert.equal(parseMessageInput([{ content: [{ type: "input_text", text }] }])[0].content[0].text, text);
  }
});
