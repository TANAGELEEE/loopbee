// Screenshot matrix for visual review against doc/loopbee-design/png (1280-wide boards).
// Usage: node tests/screens.mjs [outDir]
import { chromium } from "playwright";
import { mkdir, rm } from "node:fs/promises";
import { join, resolve } from "node:path";
import assert from "node:assert/strict";
import { startCore, seed, waitFor } from "./demo.mjs";

const out = process.argv[2] || (process.env.LOOPBEE_EVIDENCE_DIR ? process.env.LOOPBEE_EVIDENCE_DIR + "/screens" : "docs/evidence/screens");
await mkdir(out, { recursive: true });
const core = await startCore(undefined, { LOOPBEE_CODEX_BIN: resolve("tests/fake-agent/codex.mjs"), LOOPBEE_CLAUDE_BIN: resolve("tests/fake-agent/claude.mjs"), LOOPBEE_OPENCODE_BIN: "/nonexistent/opencode", LOOPBEE_PI_BIN: "/nonexistent/pi", LOOPBEE_GH_BIN: "/nonexistent/gh" });
let browser;
try {
  const ids = await seed(core.api, join(core.data, "work"));
  await waitFor(core.api, (s) => ["codex", "claude"].every((name) => s.agents.some((a) => a.name === name && a.status === "available")));
  // Real core execution and trace; only the CLI processes are fake. No model turns or external delivery.
  const reviewState = await core.api("/loops", { ...ids.logs, id: "", version: 0, name: "Review send-back", cron: "", enabled: false, steps: [
    { id: "plan", kind: "act", name: "Write plan", model: "gpt-fake", prompt: "REPORT_FEEDBACK" },
    { id: "review", kind: "review", name: "Review plan", agent: "claude", model: "sonnet", effort: "high", prompt: "REVIEW_ONCE", inputs: ["plan"], sendBackTo: "plan", maxAttempts: 2 },
    { id: "finish", kind: "summarize", name: "Summarize result", prompt: "Report completion" },
  ] });
  const reviewLoop = reviewState.loops.find((l) => l.name === "Review send-back");
  await core.api(`/loops/${reviewLoop.id}/run`, {});
  const reviewRun = await waitFor(core.api, (s) => s.runs.find((r) => r.loopId === reviewLoop.id && r.status === "completed"));
  assert.deepEqual(reviewRun.trace.map((x) => [x.step, x.attempt]), [[0, 1], [1, 1], [0, 2], [1, 2], [2, 1]]);
  await core.api("/onboarding/done", {});
  browser = await chromium.launch({ headless: true, executablePath: process.env.LOOPBEE_CHROMIUM || undefined });
  const page = await browser.newPage({ viewport: { width: 1280, height: 820 } });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
  await page.goto(core.url);
  await page.locator(".kpis").waitFor();
  const shots = [
    ["today", "#/today"],
    ["loops", "#/loops"],
    ["loop-detail-running", `#/loop/${ids.seo.id}`],
    ["loop-detail-metric", `#/loop/${ids.score.id}`],
    ["runs", "#/runs"],
    ["run-detail-ended", `#/run/${ids.finished.id}`],
    ["run-detail-running", `#/run/${ids.running.id}`],
    ["gate", `#/gate/${ids.waiting.id}`],
    ["artifacts", `#/artifact/${ids.finished.id}/round-01-step-01.txt`],
    ["digest", "#/digest"],
    ["editor", `#/loop/${ids.score.id}/edit`],
    ["new-loop", "#/new?template=1"],
    ["settings-general", "#/settings/general"],
    ["settings-agents", "#/settings/agents"],
    ["settings-notifications", "#/settings/notifications"],
    ["settings-team", "#/settings/team"],
    ["settings-data", "#/settings/data"],
    ["settings-about", "#/settings/about"],
    ["onboarding", "#/onboarding/1"],
  ];
  const setLang = (lang) => core.api("/state").then((s) => core.api("/settings", { ...s.settings, language: lang }));
  const setTheme = (theme) => core.api("/state").then((s) => core.api("/settings", { ...s.settings, theme }));
  for (const [lang, theme] of [["zh-TW", "light"], ["en", "light"], ["zh-TW", "dark"], ["zh-CN", "light"]]) {
    await setLang(lang);
    await setTheme(theme);
    for (const [name, hash] of shots) {
      if (lang !== "zh-TW" && theme === "light" && !["today", "run-detail-ended", "loops", "settings-general", "editor"].includes(name)) continue;
      if (theme === "dark" && !["today", "loop-detail-running", "run-detail-ended", "gate"].includes(name)) continue;
      await page.goto(core.base + "/" + hash);
      await page.reload();
      await page.locator("#app > *").first().waitFor();
      await page.waitForTimeout(450);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
      assert.ok(overflow <= 0, `${name} ${lang} has ${overflow}px horizontal overflow`);
      await page.screenshot({ path: join(out, `${name}.${lang}.${theme}.png`), fullPage: true });
    }
  }
  for (const lang of ["en", "zh-TW"]) {
    for (const theme of ["light", "dark"]) {
      await setLang(lang);
      await setTheme(theme);
      await page.goto(core.base + `/#/run/${reviewRun.id}`);
      await page.reload();
      await page.locator('.flow .node[data-action="select-node"][data-index="1"]').click();
      await page.locator(".side .step-attempt").nth(1).waitFor();
      assert.equal(await page.locator(".side .step-attempt").count(), 2);
      await page.waitForFunction(() => [...document.querySelectorAll(".flow svg path")].some((p) => !p.closest(".node") && /^M/i.test(p.getAttribute("d")?.trim() || "")));
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `review ${lang} ${theme} overflows`);
      await page.screenshot({ path: join(out, `run-review-send-back.${lang}.${theme}.png`), fullPage: true });
    }
  }
  await setLang("zh-TW");
  await setTheme("light");
  await page.setViewportSize({ width: 960, height: 640 });
  for (const [name, hash] of [["today-min", "#/today"], ["run-min", `#/run/${ids.finished.id}`]]) {
    await page.goto(core.base + "/" + hash);
    await page.reload();
    await page.waitForTimeout(400);
    assert.ok((await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)) <= 0, name + " overflow at 960px");
    await page.screenshot({ path: join(out, `${name}.png`), fullPage: true });
  }
  assert.deepEqual(errors, [], "browser errors");
  console.log("screens written to", out);
} finally {
  await browser?.close();
  await core.stop();
  await rm(core.data, { recursive: true, force: true });
}
