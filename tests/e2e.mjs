// Browser E2E against the real nogui core (HTTP + SQLite). Run `go build -tags nogui -o bin/loopbee ./cmd/loopbee` first.
import { chromium } from "playwright";
import { mkdir, rm } from "node:fs/promises";
import assert from "node:assert/strict";
import { startCore } from "./demo.mjs";
import { uiFlow } from "./ui-flow.mjs";
// CI sets LOOPBEE_EVIDENCE_DIR to a fresh directory so uploaded screenshots always come from this run.
const EVIDENCE = process.env.LOOPBEE_EVIDENCE_DIR || "docs/evidence";

// Real agent CLIs on this machine are ignored so the test never calls a model.
const core = await startCore(undefined, { LOOPBEE_CODEX_BIN: "/nonexistent/codex", LOOPBEE_CLAUDE_BIN: "/nonexistent/claude" });
let browser;
try {
  browser = await chromium.launch({ headless: true, channel: process.env.LOOPBEE_BROWSER || undefined });
  const page = await browser.newPage({ viewport: { width: 1280, height: 820 } });
  page.setDefaultTimeout(10000);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(core.url);
  const { gateLoop } = await uiFlow(page, { data: core.data, node: process.execPath });

  // Cancel through the confirmation dialog: the destructive action is not the default focus.
  await page.goto(core.base + "/#/loop/" + gateLoop.id);
  await page.getByRole("button", { name: "Run now", exact: true }).first().click();
  await page.getByRole("button", { name: "Cancel this run" }).click();
  assert.equal(await page.evaluate(() => document.activeElement?.textContent), "Keep running");
  await page.getByRole("dialog").getByRole("button", { name: "Cancel run" }).click();
  await page.waitForFunction(() => document.querySelector(".hd .chip")?.textContent.includes("Cancelled"));

  // Spec §8: while a run is active its detail page has one live link and at most two bees;
  // with motion off nothing animates.
  const node = process.execPath;
  const slow = await core.api("/loops", { ...gateLoop, id: "", version: 0, name: "Slow verify", allowedTools: [node], steps: [{ kind: "verify", name: "Prepare", argv: [node, "-e", "1"] }, { kind: "verify", name: "Wait", argv: [node, "-e", "setTimeout(()=>{},30000)"] }] });
  const slowLoop = slow.loops.find((l) => l.name === "Slow verify");
  const started = await core.api(`/loops/${slowLoop.id}/run`, {});
  const slowRun = started.runs.find((r) => r.loopId === slowLoop.id);
  await page.goto(core.base + "/#/run/" + slowRun.id);
  await page.reload();
  await page.waitForFunction(() => document.querySelectorAll(".flow .lk.live").length === 1, null, { timeout: 15000 });
  assert.ok((await page.locator(".bee").count()) <= 2, "at most two bees per screen");
  assert.equal(await page.locator(".bee.fly").first().evaluate((b) => getComputedStyle(b).animationName), "fly");
  await page.getByRole("link", { name: "Settings" }).click();
  await page.getByRole("radio", { name: "Off" }).click();
  await page.goto(core.base + "/#/run/" + slowRun.id);
  await page.locator(".bee.fly").first().waitFor();
  assert.equal(await page.locator(".bee.fly").first().evaluate((b) => getComputedStyle(b).animationName), "none");
  await core.api(`/runs/${slowRun.id}/cancel`, {});
  assert.equal((await fetch(core.base + "/api/state")).status, 401, "API must require the session cookie");

  await page.getByRole("link", { name: "Today", exact: true }).click();
  await mkdir(EVIDENCE, { recursive: true });
  await page.screenshot({ path: `${EVIDENCE}/dashboard-dark.png`, fullPage: true });
  await page.getByRole("link", { name: "Settings" }).click();
  await page.getByRole("radio", { name: "Light" }).click();
  await page.waitForFunction(() => !document.body.classList.contains("dark"));
  await page.getByRole("link", { name: "Today", exact: true }).click();
  await page.screenshot({ path: `${EVIDENCE}/dashboard-light.png`, fullPage: true });

  // Minimum desktop window and a phone-width browser stay free of horizontal scrolling.
  for (const [w, h] of [[960, 640], [390, 844]]) {
    await page.setViewportSize({ width: w, height: h });
    for (const hash of ["#/today", "#/runs", "#/loops", `#/loop/${gateLoop.id}`, "#/settings/general"]) {
      await page.goto(core.base + "/" + hash);
      await page.waitForTimeout(150);
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${hash} overflows at ${w}px`);
    }
  }
  await page.goto(core.base + "/#/today");
  await page.screenshot({ path: `${EVIDENCE}/mobile.png`, fullPage: true });
  assert.deepEqual(errors, [], "browser runtime errors");
  console.log("PASS: onboarding, editor, run, gate review, edit, evidence, artifact download, cancel dialog, three languages, themes, 960px and mobile layouts, no script errors");
} finally {
  await browser?.close();
  await core.stop();
  await rm(core.data, { recursive: true, force: true });
}
