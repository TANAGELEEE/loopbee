// Keeps LESSONS.md honest: every lesson names evidence that exists, and patterns get promoted to the standards.
// Format: see the top of LESSONS.md. Run with `npm test`.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, existsSync, statSync } from "node:fs";
import { join } from "node:path";

const read = (p) => readFileSync(p, "utf8").replace(/\r\n/g, "\n");
const walk = (dir, out = []) => {
  for (const name of readdirSync(dir)) {
    if (["node_modules", ".git", "bin", ".tools", ".test-data"].includes(name)) continue;
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else out.push(p);
  }
  return out;
};
const goTests = new Set(
  walk(".").filter((f) => f.endsWith("_test.go")).flatMap((f) => [...read(f).matchAll(/^func (Test\w+)\(/gm)].map((m) => m[1])),
);

export function parseLessons(text) {
  const lessons = [];
  let section = "";
  let current = null;
  for (const line of text.split("\n")) {
    if (line.startsWith("## ")) {
      section = line.slice(3).trim();
      current = null;
    } else if (line.startsWith("### ")) {
      current = { section, rule: line.slice(4).trim(), evidence: null, seen: null };
      lessons.push(current);
    } else if (current && line.startsWith("- Evidence:")) {
      current.evidence = [...line.matchAll(/`([^`]+)`/g)].map((m) => m[1]);
    } else if (current && line.startsWith("- Seen:")) {
      current.seen = line.slice("- Seen:".length).split(",").map((s) => s.trim()).filter(Boolean);
    }
  }
  return lessons;
}

export function checkEvidence(item) {
  if (/^Test\w+$/.test(item)) return goTests.has(item) ? "" : `Go test ${item} does not exist`;
  const [path, ...rest] = item.split("#");
  const needle = rest.join("#");
  if (!existsSync(path)) return `${path} does not exist`;
  if (needle && !read(path).includes(needle)) return `${path} does not contain “${needle}”`;
  return "";
}

const lessons = parseLessons(read("LESSONS.md"));
const active = lessons.filter((l) => l.section !== "Moved to the standards");

test("LESSONS.md has lessons in the expected format", () => {
  assert.ok(active.length >= 30, `only ${active.length} lessons parsed`);
  for (const l of active) {
    assert.ok(l.evidence?.length, `“${l.rule}” has no Evidence line with items in backticks`);
    assert.ok(l.seen?.length, `“${l.rule}” has no Seen line`);
    for (const s of l.seen) assert.match(s, /^(\d{4}-\d{2}-\d{2}|initial build)$/, `“${l.rule}”: Seen “${s}” is not a date`);
  }
});

test("every lesson's evidence exists", () => {
  const problems = active.flatMap((l) => (l.evidence || []).map((e) => checkEvidence(e)).filter(Boolean).map((p) => `“${l.rule}”: ${p}`));
  assert.deepEqual(problems, []);
});

test("a lesson seen on three days has moved to docs/development.md", () => {
  const due = active.filter((l) => new Set(l.seen.filter((s) => s !== "initial build")).size >= 3);
  assert.deepEqual(due.map((l) => l.rule), [], "promote these rules to docs/development.md and list them under “Moved to the standards”");
});

test("release notes have the required section and no placeholders", () => {
  const dir = "docs/releases";
  for (const f of readdirSync(dir).filter((f) => /^v\d.*\.md$/.test(f))) {
    const text = read(join(dir, f));
    assert.match(text, /^## 下載與安裝/m, `${f} needs “## 下載與安裝”`);
    assert.doesNotMatch(text, /TODO|TBD|待補/, `${f} has placeholders`);
  }
});

test("the checker itself rejects missing evidence", () => {
  assert.notEqual(checkEvidence("TestDoesNotExist"), "");
  assert.notEqual(checkEvidence("tests/nope.mjs"), "");
  assert.notEqual(checkEvidence("LESSONS.md#this text is not in the file"), "");
  assert.equal(checkEvidence("LESSONS.md#How to add a lesson"), "");
  const parsed = parseLessons("## A\n### Rule\nwhy\n- Evidence: `TestX`, `a#b`\n- Seen: 2026-10-11, initial build\n");
  assert.deepEqual(parsed[0], { section: "A", rule: "Rule", evidence: ["TestX", "a#b"], seen: ["2026-10-11", "initial build"] });
});
