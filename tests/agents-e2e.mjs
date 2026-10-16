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
const core = await startCore(undefined, { LOOPBEE_CODEX_BIN: fake("codex"), LOOPBEE_CLAUDE_BIN: fake("claude"), LOOPBEE_GEMINI_BIN: fake("gemini"), LOOPBEE_OPENCODE_BIN: fake("opencode"), LOOPBEE_PI_BIN: fake("pi"), LOOPBEE_GH_BIN: fake("gh") });
let browser;
try {
  const s0 = await waitFor(core.api, (s) => s.agents.length === 5 && s.agents.every((a) => a.status === "available") && s);
  assert.deepEqual(s0.agents.map((a) => a.name), ["codex", "claude", "gemini", "opencode", "pi"]);
  assert.deepEqual(s0.agents.find((a) => a.name === "pi").levels, ["read-only", "agent-config"], "pi has no sandbox: only read-only and its own config");
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
  await page.locator('[data-agent="claude"]').getByRole("button", { name: "Set as default" }).click();
  await waitFor(core.api, (s) => s.settings.defaultAgent === "claude");
  assert.ok(await page.getByText("--sandbox workspace-write -c sandbox_workspace_write.network_access=true").isVisible());
  // Every adapter is listed with what it enforces; a level an agent cannot enforce is not offered.
  for (const name of ["Gemini CLI", "OpenCode", "pi"]) await page.locator(`[data-agent] .nm`, { hasText: name }).first().waitFor();
  assert.ok(await page.getByText("Gemini CLI: --approval-mode auto_edit --sandbox (edits, no shell)").isVisible());
  assert.ok((await page.getByText("pi: not offered: this agent cannot enforce it").count()) >= 2);
  // How each CLI's own MCP tools are approved in advance (an unattended run cannot answer a prompt) or disabled.
  await page.getByText("MCP tools allowed", { exact: true }).waitFor();
  assert.ok(await page.getByText("Codex CLI: -c mcp_servers.<server>.enabled=false (and the same for plugin servers)").isVisible());

  // New Loop opens the chat. Describe it, answer the agent's question, watch the preview update.
  await page.goto(core.base + "/#/loops");
  await page.getByRole("link", { name: "New Loop" }).first().click();
  await page.locator("#chat-dir").fill(project);
  await page.locator(".ex").first().click();
  assert.match(await page.locator("#chat-input").inputValue(), /fix-prod-errors/);
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await page.locator(".chat-side h1", { hasText: "Fix production errors" }).waitFor();
  // The agent's reply is a message bubble: name above the text, every line inside the box (a design-system class
  // of the same name once made it a 64 px row, and long replies ran out of the bubble).
  const bubble = await page.locator(".msgs .msg:not(.me)").last().evaluate((m) => {
    const who = m.querySelector(".who").getBoundingClientRect(), txt = m.querySelector(".txt").getBoundingClientRect(), box = m.getBoundingClientRect();
    return { stacked: who.bottom <= txt.top + 1, inside: txt.bottom <= box.bottom + 1 };
  });
  assert.deepEqual(bubble, { stacked: true, inside: true }, "the agent's reply runs out of its bubble");
  assert.equal(await page.locator(".chat-side .flow .node").count(), 2);
  assert.ok(await page.locator(".chat-side").getByText("Weekdays 09:00 · Asia/Taipei").isVisible());
  // Two questions: picking one answer is not sending it; the message goes once every question has an answer.
  await page.getByRole("button", { name: "I do it myself" }).click();
  assert.equal(await page.getByRole("button", { name: "I do it myself" }).getAttribute("aria-pressed"), "true");
  assert.equal(await page.locator(".msg.me").count(), 1, "picking one of two answers sent the message");
  await page.getByRole("button", { name: "Only in the app" }).click();
  await page.locator(".chat-side").getByText("Daily 02:00 · Asia/Taipei").waitFor();
  assert.equal(await page.locator(".msg.me").count(), 2);
  assert.match(await page.locator(".msg.me .txt").last().textContent(), /Who merges to master after approval\? → I do it myself\nWhere should loopbee report\? → Only in the app/);
  await mkdir(`${EVIDENCE}/screens`, { recursive: true });
  await page.screenshot({ path: `${EVIDENCE}/screens/chat-authoring.en.light.png`, fullPage: true });

  // Save: a disabled draft that keeps the conversation and the claude binding.
  await page.getByRole("button", { name: "Save as draft" }).click();
  // The chat already shows the draft's title: wait for the saved Loop's own page, not for the title.
  await page.waitForFunction(() => /^#\/loop\/[0-9a-f]{24}$/.test(location.hash));
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
  // The run shows the model and tokens the agent reported, in total and for the step.
  await page.locator(".run-usage", { hasText: "1.5k tokens · $0.01 · claude-fake" }).waitFor();
  await page.locator('[data-action="select-trace"][data-index="0"]').click();
  await page.locator(".step-usage", { hasText: "claude-fake" }).waitFor();
  assert.ok(await page.locator(".step-usage").getByText("input 1.0k · cached 200 · output 300").isVisible());
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

  // A blocked agent lists what it could not get, and the run page says how to give the Loop access.
  const needy = await core.api("/loops", { ...loop, id: "", version: 0, name: "Needs access", conversation: [], steps: [{ kind: "act", name: "Use the infra skill", prompt: "MISSING_ACCESS" }] });
  const needyLoop = needy.loops.find((l) => l.name === "Needs access");
  await core.api(`/loops/${needyLoop.id}/run`, {});
  s = await waitFor(core.api, (st) => st.runs.find((r) => r.loopId === needyLoop.id && r.status === "blocked") && st);
  await page.goto(core.base + "/#/run/" + s.runs.find((r) => r.loopId === needyLoop.id).id);
  const missing = page.locator("#run-missing");
  await missing.getByText("The agent could not get").waitFor();
  assert.ok(await missing.getByText("~/.pi/agent/private/nexscope-infra-access.json (EPERM)").isVisible());
  assert.ok(await missing.getByText("network access to infra.example.com").isVisible());
  assert.equal(await missing.getByRole("link", { name: "Edit definition" }).getAttribute("href"), `#/loop/${needyLoop.id}/edit`);

  // Today is a board: coming up → running → needs you → done today, with today's tokens by agent and model.
  await page.goto(core.base + "/#/today");
  await page.reload();
  assert.deepEqual(await page.locator("#board .bcol").evaluateAll((cols) => cols.map((c) => c.dataset.col)), ["upcoming", "running", "needs", "done"]);
  await page.locator('#board [data-col="needs"]').getByText("Needs access").waitFor();
  await page.locator('#board [data-col="done"]').getByText("Quiet sweep").waitFor();
  await page.locator('#board [data-col="done"]').getByText("Fix production errors").first().waitFor();
  await page.locator("#kpi-tokens .v", { hasText: "4.5k" }).waitFor();
  await page.locator("#today-usage", { hasText: "Claude Code" }).getByText("claude-fake").waitFor();
  await page.screenshot({ path: `${EVIDENCE}/screens/today-board.en.light.png`, fullPage: true });
  await page.goto(core.base + "/#/loop/" + loop.id);
  await page.locator("#loop-usage", { hasText: "Last 7 days" }).getByText("1.5k").first().waitFor();

  // The gate shows what loopbee committed first, and approving closes it at once.
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
  assert.ok(await page.locator(".chat-side").getByText("→ loopbee/fix-{date}").isVisible(), "preview shows the loopbee-managed branch");
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
  // Agents never write Git metadata: the editor asks for a branch that loopbee commits to.
  await page.locator("#f-agent").selectOption("codex");
  await page.getByRole("radio", { name: "Workspace write", exact: true }).click();
  // An agent without a sandbox is not offered write levels.
  await page.locator("#f-agent").selectOption("pi");
  await page.getByText("pi cannot enforce “Workspace write” on its own").waitFor();
  await page.locator("#f-agent").selectOption("codex");
  await page.locator("#f-model").fill("gpt-fake");
  const codexGit = page.getByText("Agents cannot commit in a worktree; set a branch so loopbee commits the changes");
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
  // Work from GitHub issues and open the pull request after review: loopbee's own intake and delivery.
  await page.getByRole("radio", { name: "GitHub issues" }).click();
  await page.locator("#f-intake-label").fill("bug");
  assert.equal(await page.locator("#f-branch").inputValue(), "loopbee/issue-{issue}");
  await page.getByRole("radio", { name: "Draft, review on GitHub" }).click();
  await page.getByText("A draft pull request pushes the agent's change to GitHub before anyone has reviewed it").waitFor();
  await page.getByRole("radio", { name: "After my review" }).click();
  await page.getByRole("radio", { name: "When all checks pass" }).click();
  await page.getByText("After your approval loopbee waits for the pull request's checks").waitFor();
  await page.locator("#ed-work").locator("..").getByText("gh version 2.99.0 (fake) · signed in").waitFor();
  // Read paths: the agent may read a skill's folder outside the project, never the computer's credential stores.
  await page.locator("#f-readPaths").fill("~/.ssh");
  const credentials = page.getByText("Read path “~/.ssh” holds this computer's credentials");
  await credentials.waitFor();
  await page.locator("#f-readPaths").fill("~/.pi/agent/private");
  await credentials.waitFor({ state: "detached" });
  await page.getByText("This Loop works on issues others write and lets the agent read ~/.pi/agent/private").waitFor();
  // Git-ignored project files a fresh worktree lacks are copied from the checkout.
  await page.locator("#f-copyFiles").fill(".env\nconfig/local.yml\n");
  // The agent's own MCP tools (a browser for a web console): approved in advance, flagged for issue work.
  await page.getByRole("switch", { name: "Use the agent's own MCP tools" }).click();
  await page.getByText("This Loop works on issues others write and lets the agent use its MCP tools").waitFor();
  await page.getByRole("button", { name: "Save as v2" }).click();
  await page.locator(".hd .chip", { hasText: "Version v2" }).waitFor();
  const v2 = (await core.api("/state")).loops.find((l) => l.id === loop.id);
  assert.equal(v2.branch, "loopbee/issue-{issue}");
  assert.deepEqual(v2.intake, { kind: "github-issues", label: "bug" });
  assert.deepEqual(v2.pullRequest, { mode: "after-review", merge: "after-checks" });
  assert.deepEqual(v2.readPaths, ["~/.pi/agent/private"]);
  assert.deepEqual(v2.copyFiles, [".env", "config/local.yml"]);
  assert.equal(v2.agentMcp, true);
  await page.getByText("MCP tools run outside the sandbox with your rights and are approved in advance.").waitFor();
  await page.getByText("GitHub issues labelled bug → pull request after your review, merged when checks pass").waitFor();
  assert.equal(v2.agent, "codex");
  assert.equal(v2.model, "gpt-fake");
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
  // No project folder chosen, but the message names one: the draft and the folder field take it.
  await page.goto(core.base + "/#/new");
  await page.reload();
  assert.equal(await page.locator("#chat-dir").inputValue(), "");
  await page.locator("#chat-input").fill(`Sync the repositories in FOLDER=${project}`);
  await page.locator("#chat-form button").click();
  await page.locator(".chat-side h1", { hasText: "Fix production errors" }).waitFor();
  assert.equal(await page.locator("#chat-dir").inputValue(), project, "the folder named in the chat did not become the project folder");
  for (const [name, hash] of [["chat-authoring.zh-TW.light", `#/loop/${loop.id}/chat`], ["settings-agents.zh-TW.light", "#/settings/agents"], ["run-report.zh-TW.light", `#/run/${run.id}`]]) {
    await page.goto(core.base + "/" + hash);
    await page.reload();
    await page.waitForTimeout(400);
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), name + " overflows");
    await page.screenshot({ path: `${EVIDENCE}/screens/${name}.png`, fullPage: true });
  }
  assert.deepEqual(errors, [], "browser errors");
  console.log("PASS: agent detection and default, chat authoring with follow-up, draft save with conversation, Claude-run agent step with report and gate, no_action outcome, what a blocked agent could not get, five agent adapters, model and token usage, Today board, form fields for permissions, read paths, copied files, worktree, branch, command steps and secrets, device-limit and usage-limit messages");
} finally {
  await browser?.close();
  await core.stop();
  await rm(core.data, { recursive: true, force: true });
}
