import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { translate, statuses, keys } from "../internal/ui/web/i18n.js";

const files = ["app.js", "views.js", "forms.js", "flow.js", "ui.js", "chat.js"];
const source = async (f) => readFile(new URL("../internal/ui/web/" + f, import.meta.url), "utf8");

// Every literal passed to t()/T()/btn() — and both branches of `t(cond ? "A" : "B")` — must have
// Traditional and Simplified Chinese translations (spec I18N-01).
test("every UI string used by the views has both Chinese translations", async () => {
  const known = new Set(keys());
  const missing = new Set();
  for (const f of files) {
    const src = await source(f);
    const found = [...src.matchAll(/\b(?:t|T|btn|tr)\(\s*"([^"\n]+)"/g)].map((m) => m[1]);
    for (const m of src.matchAll(/\b(?:t|T)\(\s*[\w.!=()\s"-]{1,80}?\?\s*"([^"\n]+)"\s*:\s*"([^"\n]+)"/g)) found.push(m[1], m[2]);
    for (const k of found) if (!known.has(k) && /[a-z]/.test(k) && !k.endsWith(" ")) missing.add(`${f}: ${k}`);
  }
  assert.deepEqual([...missing], []);
});

test("all persisted run statuses have three display languages", () => {
  for (const key of Object.values(statuses)) {
    assert.equal(translate(key, "en"), key);
    assert.notEqual(translate(key, "zh-TW"), key);
    assert.notEqual(translate(key, "zh-CN"), key);
  }
  assert.equal(translate("Today", "zh-TW"), "今日");
  assert.equal(translate("future key", "zh-CN"), "future key");
});

test("placeholders and plural forms match across languages", () => {
  for (const k of keys()) {
    const want = [...k.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join();
    for (const lang of ["zh-TW", "zh-CN"]) {
      const v = translate(k, lang);
      const got = [...new Set([...v.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort().join();
      const uniqWant = [...new Set(want.split(",").filter(Boolean))].sort().join();
      assert.equal(got, uniqWant, `${lang} placeholders for “${k}”`);
      assert.equal(v.split("|").length <= k.split("|").length, true, `${lang} plural parts for “${k}”`);
    }
  }
});

test("view code contains no hard-coded CJK sentences and files keep Unicode", async () => {
  for (const f of files) {
    const src = await source(f);
    // Only language names may appear in CJK outside i18n.js.
    const cjk = [...src.matchAll(/[一-鿿]+/g)].map((m) => m[0]).filter((w) => !["繁體中文", "简体中文", "繁", "简"].includes(w));
    assert.deepEqual(cjk, [], f);
  }
  const i18n = await readFile(new URL("../internal/ui/web/i18n.js", import.meta.url), "utf8");
  assert.doesNotMatch(i18n, /\?{3,}|�/);
  assert.equal(translate("Send daily digest", "zh-TW"), "自動投遞日報");
});
