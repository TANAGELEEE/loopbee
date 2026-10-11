// Agent integration E2E: fake Codex and Claude Code CLIs drive chat authoring, execution, reports and gates.
import { chromium } from "playwright";
import { mkdir, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { startCore, waitFor } from "./demo.mjs";

const fake = (n) => resolve(`tests/fake-agent/${n}.mjs`);
const core = await startCore(undefined, { LOOPBEE_CODEX_BIN: fake("codex"), LOOPBEE_CLAUDE_BIN: fake("claude") });
let browser;
try {
  const s0 = await waitFor(core.api, (s) => s.agents.length === 2 && s.agents.every((a) => a.status === "available") && s);
  assert.equal(s0.settings.defaultAgent, "codex", "first available agent becomes the default");
  await core.api("/settings", { ...s0.settings, language: "en" });
  await core.api("/onboarding/done", {});
  const project = join(core.data, "project");
  await mkdir(project, { recursive: true });
  // The chat draft requires a clean Git checkout by default, as a real project would be.
  for (const args of [["init", "-q"], ["commit", "-q", "--allow-empty", "-m", "init"]]) {
    execFileSync("git", args, { cwd: project, env: { ...process.env, GIT_AUTHOR_NAME: "t", GIT_AUTHOR_EMAIL: "t@example.invalid", GIT_COMMITTER_NAME: "t", GIT_COMMITTER_EMAIL: "t@example.invalid" } });
  }

  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 820 } });
  page.setDefaultTimeout(15000);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(core.url);
  await page.locator(".kpis").waitFor();

  // Settings › Agents: both CLIs connected; switch the default to Claude Code.
  await page.goto(core.base + "/#/settings/agents");
  await page.getByRole("button", { name: "Set as default" }).click();
  await waitFor(core.api, (s) => s.settings.defaultAgent === "claude");
  assert.ok(await page.getByText("--sandbox workspace-write -c sandbox_workspace_write.network_access=true").isVisible());

  // New Loop opens the chat. Describe it, answer the agent's question, watch the preview update.
  await page.goto(core.base + "/#/loops");
  await page.getByRole("link", { name: "New Loop" }).first().click();
  await page.locator("#chat-dir").fill(project);
  await page.locator(".ex").first().click();
  assert.match(await page.locator("#chat-input").inputValue(), /fix-prod-errors/);
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await page.locator(".chat-side h1", { hasText: "Fix production errors" }).waitFor();
  assert.equal(await page.locator(".chat-side .flow .node").count(), 2);
  assert.ok(await page.locator(".chat-side").getByText("Weekdays 09:00 · Asia/Taipei").isVisible());
  await page.getByRole("button", { name: "I do it myself" }).click();
  await page.locator(".chat-side").getByText("Daily 02:00 · Asia/Taipei").waitFor();
  assert.equal(await page.locator(".msg.me").count(), 2);
  await mkdir("docs/evidence/screens", { recursive: true });
  await page.screenshot({ path: "docs/evidence/screens/chat-authoring.en.light.png", fullPage: true });

  // Save: a disabled draft that keeps the conversation and the claude binding.
  await page.getByRole("button", { name: "Save as draft" }).click();
  await page.locator(".hd h1", { hasText: "Fix production errors" }).waitFor();
  let s = await core.api("/state");
  const loop = s.loops.find((l) => l.name === "Fix production errors");
  assert.equal(loop.enabled, false);
  assert.equal(loop.agent, "claude");
  assert.equal(loop.directory, project);
  assert.equal(loop.conversation.filter((m) => m.role === "user").length, 2);

  // Run it: the agent step reports through the result contract, then the run waits at the gate.
  await page.getByRole("button", { name: "Run now" }).first().click();
  await page.getByRole("link", { name: "Go to review" }).waitFor();
  await page.locator(".report").getByText("Fixed 1 error, deployed to test").waitFor();
  assert.ok(await page.locator(".report").getByText("https://example.com/pr/7").isVisible());
  await page.screenshot({ path: "docs/evidence/screens/run-report.en.light.png", fullPage: true });
  await page.getByRole("link", { name: "Go to review" }).click();
  await page.locator('#gate-form [name="note"]').fill("Checked on test, looks good");
  await page.getByRole("button", { name: "Approve and continue" }).click();
  s = await waitFor(core.api, (st) => st.runs.find((r) => r.loopId === loop.id && r.status === "completed") && st);
  const run = s.runs.find((r) => r.loopId === loop.id);
  assert.ok(run.artifacts.includes("round-01-step-01.report.md"));
  assert.equal(run.trace[1].note, "Checked on test, looks good");

  // A run where the agent finds nothing to do ends as "Nothing to do" and skips the gate.
  const quiet = await core.api("/loops", { ...loop, id: "", version: 0, name: "Quiet sweep", conversation: [], steps: [{ kind: "act", name: "Sweep", prompt: "NOTHING_TODO" }, { kind: "gate", name: "Approve" }] });
  const quietLoop = quiet.loops.find((l) => l.name === "Quiet sweep");
  await core.api(`/loops/${quietLoop.id}/run`, {});
  s = await waitFor(core.api, (st) => st.runs.find((r) => r.loopId === quietLoop.id && r.status === "no_action") && st);
  await page.goto(core.base + "/#/runs");
  await page.locator(".row.r", { hasText: "Quiet sweep" }).getByText("Nothing to do").waitFor();

  // Change by chat keeps the history; the form shows the new fields.
  await page.goto(core.base + "/#/loop/" + loop.id + "/chat");
  assert.equal(await page.locator(".msg.me").count(), 2);
  await page.goto(core.base + "/#/loop/" + loop.id + "/edit");
  for (const level of ["Read only", "Workspace write", "Workspace write + network", "Agent's own config"]) {
    assert.ok(await page.getByRole("radio", { name: level, exact: true }).isVisible(), level);
  }
  await page.getByRole("radio", { name: "Fresh git worktree" }).click();
  await page.locator("#f-baseRef").waitFor();
  await page.getByRole("button", { name: "Add variable" }).click();
  await page.locator('[data-env="name"]').first().fill("db_password");
  await page.locator('[data-env="secret"]').first().check();
  await page.getByText("DB_PASSWORD has no value yet").waitFor();
  await page.getByRole("radio", { name: "Workspace write + network" }).click();
  await page.getByText("Permission exceeds this device’s limit").waitFor();
  assert.equal(await page.getByRole("button", { name: "Save as v2" }).isDisabled(), true);
  // Traditional Chinese screenshots of the chat (with history) and the agent settings for review.
  await core.api("/state").then((st) => core.api("/settings", { ...st.settings, language: "zh-TW" }));
  for (const [name, hash] of [["chat-authoring.zh-TW.light", `#/loop/${loop.id}/chat`], ["settings-agents.zh-TW.light", "#/settings/agents"], ["run-report.zh-TW.light", `#/run/${run.id}`]]) {
    await page.goto(core.base + "/" + hash);
    await page.reload();
    await page.waitForTimeout(400);
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), name + " overflows");
    await page.screenshot({ path: `docs/evidence/screens/${name}.png`, fullPage: true });
  }
  assert.deepEqual(errors, [], "browser errors");
  console.log("PASS: agent detection and default, chat authoring with follow-up, draft save with conversation, Claude-run agent step with report and gate, no_action outcome, form fields for permissions, worktree and secrets");
} finally {
  await browser?.close();
  await core.stop();
  await rm(core.data, { recursive: true, force: true });
}
