// Agent integration E2E: fake Codex and Claude Code CLIs drive chat authoring, execution, reports and gates.
import { chromium } from "playwright";
import { mkdir, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { startCore, waitFor } from "./demo.mjs";
// CI sets LOOPBEE_EVIDENCE_DIR to a fresh directory so uploaded screenshots always come from this run.
const EVIDENCE = process.env.LOOPBEE_EVIDENCE_DIR || "docs/evidence";

const fake = (n) => resolve(`tests/fake-agent/${n}.mjs`);
const core = await startCore(undefined, { LOOPBEE_CODEX_BIN: fake("codex"), LOOPBEE_CLAUDE_BIN: fake("claude"), LOOPBEE_GH_BIN: fake("gh") });
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
  await mkdir(`${EVIDENCE}/screens`, { recursive: true });
  await page.screenshot({ path: `${EVIDENCE}/screens/chat-authoring.en.light.png`, fullPage: true });

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
  await page.screenshot({ path: `${EVIDENCE}/screens/run-report.en.light.png`, fullPage: true });
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
  // The run page says why it stopped in the Run's own words (e.g. intake finding no issue), not a generic sentence.
  await page.goto(core.base + "/#/run/" + s.runs.find((r) => r.loopId === quietLoop.id).id);
  await page.locator(".row.top", { hasText: "Stop reason" }).locator(".sub", { hasText: "No actionable errors" }).waitFor();
  assert.equal(await page.getByText("All steps succeeded and the completion condition was met.").count(), 0);

  // The gate shows what Loopbee committed first, and approving closes it at once.
  const edit = await core.api("/loops", { ...loop, id: "", version: 0, name: "Branch fix", conversation: [], workspace: "worktree", baseRef: "", branch: "loopbee/e2e-{run}", requireClean: false, steps: [{ kind: "act", name: "Fix", prompt: "EDIT_FILE" }, { kind: "gate", name: "Review the diff" }] });
  const editLoop = edit.loops.find((l) => l.name === "Branch fix");
  await core.api(`/loops/${editLoop.id}/run`, {});
  s = await waitFor(core.api, (st) => st.runs.find((r) => r.loopId === editLoop.id && r.status === "waiting") && st);
  const editRun = s.runs.find((r) => r.loopId === editLoop.id);
  await page.goto(core.base + "/#/gate/" + editRun.id);
  await page.reload();
  const firstEvidence = page.locator('[data-action="gate-preview"]').first();
  await firstEvidence.getByText("Changes to review").waitFor();
  assert.match(await firstEvidence.getAttribute("class"), /selrow/, "the diff is selected for review");
  await page.locator("#ev-preview", { hasText: "+edited by the fake agent" }).waitFor();
  // The note survives the 3-second refresh after focus has left it (the desktop window swallows the first click).
  await page.locator('#gate-form [name="note"]').fill("LGTM");
  await page.locator(".gb-t").click();
  await page.waitForTimeout(3600);
  assert.equal(await page.locator('#gate-form [name="note"]').inputValue(), "LGTM", "the review note was wiped by a refresh");
  await page.getByRole("button", { name: "Approve and continue" }).click();
  await page.waitForURL(/#\/run\//); // the approval went through and the page moved to the run
  await waitFor(core.api, (st) => st.runs.find((r) => r.id === editRun.id && r.status === "completed"));
  await page.goto(core.base + "/#/gate/" + editRun.id);
  await page.reload();
  await page.getByText("This gate is closed").waitFor();

  // A draft above the device limit says what it needs and links to Settings (default limit: read only).
  await page.goto(core.base + "/#/new");
  await page.reload();
  await page.locator("#chat-dir").fill(project);
  await page.locator("#chat-input").fill("Fix on a branch and push it. NEEDS_NETWORK");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await page.locator(".chat-side").getByText("This Loop needs “Workspace write + network”, but this device allows at most “Read only”.").waitFor();
  assert.ok(await page.locator(".chat-side").getByRole("link", { name: "Raise the limit in Settings" }).isVisible());
  assert.equal(await page.getByRole("button", { name: "Save as draft" }).isDisabled(), true);
  assert.ok(await page.locator(".chat-side").getByText("→ loopbee/fix-{date}").isVisible(), "preview shows the Loopbee-managed branch");
  // A subscription limit reads as a sentence, not raw JSON.
  await page.locator("#chat-input").fill("HIT_LIMIT");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await page.locator(".msg.err", { hasText: "You've hit your session limit · resets 10:20pm" }).waitFor();
  assert.equal(await page.locator(".msg.err", { hasText: "is_error" }).count(), 0);

  // The rest of the editor checks run under a workspace-write device limit.
  await core.api("/state").then((st) => core.api("/settings", { ...st.settings, maxSandbox: "workspace-write" }));

  // Change by chat keeps the history; the form shows the new fields.
  await page.goto(core.base + "/#/loop/" + loop.id + "/chat");
  await page.reload();
  assert.equal(await page.locator(".msg.me").count(), 2);
  await page.goto(core.base + "/#/loop/" + loop.id + "/edit");
  for (const level of ["Read only", "Workspace write", "Workspace write + network", "Agent's own config"]) {
    assert.ok(await page.getByRole("radio", { name: level, exact: true }).isVisible(), level);
  }
  await page.getByRole("radio", { name: "Fresh git worktree" }).click();
  await page.locator("#f-baseRef").waitFor();
  // Agents never write Git metadata: the editor asks for a branch that Loopbee commits to.
  await page.locator("#f-agent").selectOption("codex");
  await page.getByRole("radio", { name: "Workspace write", exact: true }).click();
  const codexGit = page.getByText("Agents cannot commit in a worktree; set a branch so Loopbee commits the changes");
  await codexGit.waitFor();
  await page.locator("#f-branch").fill("loopbee/fix-{date}");
  await codexGit.waitFor({ state: "detached" });
  // Every step kind in the add menu has a label, and an added command step is a usable command step.
  await page.getByRole("button", { name: /Add step/ }).click();
  const labels = await page.locator("#menu [role=menuitem]").allTextContents();
  assert.ok(labels.every((l) => !/undefined/.test(l)) && labels.some((l) => l.startsWith("Command")), labels.join(" | "));
  await page.locator("#menu [role=menuitem]", { hasText: "(run)" }).click();
  await page.locator('[data-sf="argv"]').fill('["node","-v"]');
  await page.locator("#f-allowedTools").fill("node");
  await page.getByRole("switch", { name: "Continue and pass the output on" }).click();
  // A command between the agent and any gate runs the agent's changes outside the sandbox: the editor says so.
  await page.getByText("“Command” runs after the agent and before any review").waitFor();
  // Work from GitHub issues and open the pull request after review: Loopbee's own intake and delivery.
  await page.getByRole("radio", { name: "GitHub issues" }).click();
  await page.locator("#f-intake-label").fill("bug");
  assert.equal(await page.locator("#f-branch").inputValue(), "loopbee/issue-{issue}");
  await page.getByRole("radio", { name: "Draft, review on GitHub" }).click();
  await page.getByText("A draft pull request pushes the agent's change to GitHub before anyone has reviewed it").waitFor();
  await page.getByRole("radio", { name: "After my review" }).click();
  await page.getByRole("radio", { name: "When all checks pass" }).click();
  await page.getByText("After your approval Loopbee waits for the pull request's checks").waitFor();
  await page.locator("#ed-work").locator("..").getByText("gh version 2.99.0 (fake) · signed in").waitFor();
  await page.getByRole("button", { name: "Save as v2" }).click();
  await page.locator(".hd .chip", { hasText: "Version v2" }).waitFor();
  const v2 = (await core.api("/state")).loops.find((l) => l.id === loop.id);
  assert.equal(v2.branch, "loopbee/issue-{issue}");
  assert.deepEqual(v2.intake, { kind: "github-issues", label: "bug" });
  assert.deepEqual(v2.pullRequest, { mode: "after-review", merge: "after-checks" });
  await page.getByText("GitHub issues labelled bug → pull request after your review, merged when checks pass").waitFor();
  assert.equal(v2.agent, "codex");
  const cmd = v2.steps.find((st) => st.kind === "run");
  assert.ok(cmd && cmd.name === "Command" && cmd.continueOnError === true && cmd.argv.join(" ") === "node -v", JSON.stringify(cmd));
  await page.goto(core.base + "/#/loop/" + loop.id + "/edit");
  await page.reload();
  await page.locator("#f-baseRef").waitFor();
  await page.getByRole("button", { name: "Add variable" }).click();
  await page.locator('[data-env="name"]').first().fill("db_password");
  await page.locator('[data-env="secret"]').first().check();
  await page.getByText("DB_PASSWORD has no value yet").waitFor();
  await page.getByRole("radio", { name: "Workspace write + network" }).click();
  await page.getByText("Permission exceeds this device’s limit").waitFor();
  assert.equal(await page.getByRole("button", { name: "Save as v3" }).isDisabled(), true);
  // Traditional Chinese screenshots of the chat (with history) and the agent settings for review.
  await core.api("/state").then((st) => core.api("/settings", { ...st.settings, language: "zh-TW" }));
  for (const [name, hash] of [["chat-authoring.zh-TW.light", `#/loop/${loop.id}/chat`], ["settings-agents.zh-TW.light", "#/settings/agents"], ["run-report.zh-TW.light", `#/run/${run.id}`]]) {
    await page.goto(core.base + "/" + hash);
    await page.reload();
    await page.waitForTimeout(400);
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), name + " overflows");
    await page.screenshot({ path: `${EVIDENCE}/screens/${name}.png`, fullPage: true });
  }
  assert.deepEqual(errors, [], "browser errors");
  console.log("PASS: agent detection and default, chat authoring with follow-up, draft save with conversation, Claude-run agent step with report and gate, no_action outcome, form fields for permissions, worktree, branch, command steps and secrets, device-limit and usage-limit messages");
} finally {
  await browser?.close();
  await core.stop();
  await rm(core.data, { recursive: true, force: true });
}
