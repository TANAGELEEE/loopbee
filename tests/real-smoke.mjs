// Real smoke test: the packaged loopbee binary + the real agent CLIs on this machine (Codex CLI, Claude Code,
// OpenCode, pi).
// It uses your agent subscription (a few short agent runs) and touches nothing outside a temp folder:
// a throw-away sample Git repository with a bare "origin", and an isolated loopbee data directory.
//
//   node tests/real-smoke.mjs                       # packaged app on macOS, bin/loopbee.exe on Windows
//   node tests/real-smoke.mjs --bin bin/loopbee --agents claude --scenarios report
//
// Scenarios, per agent:
//   report — chat: "weekday report of TODOs and failing tests, read-only" → run → a report with the result marker
//   fix    — chat: "fix the failing test on a branch, commit, stop before master for my review"
//            → run in a fresh worktree → gate → the branch has a commit that makes the tests pass,
//              the developer's checkout is untouched → approve → done
//            (only for agents that enforce workspace-write; OpenCode and pi have no sandbox and skip it)
//   review — once, not per agent: the first agent writes a plan, another agent reviews it with a rule the first plan
//            cannot meet → the review sends it back with feedback → the revised plan passes (read-only)
// Every run must also record the model and tokens the CLI reported.
import { execFileSync } from "node:child_process";
import { mkdtemp, readFile, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { startCore, waitFor } from "./demo.mjs";

const args = process.argv.slice(2);
const opt = (name, def) => {
  const i = args.indexOf("--" + name);
  return i >= 0 ? args[i + 1] : def;
};
const defaultBin = process.platform === "darwin" ? "bin/loopbee.app/Contents/MacOS/loopbee" : process.platform === "win32" ? "bin/loopbee.exe" : "bin/loopbee";
const bin = resolve(opt("bin", defaultBin));
const scenarios = opt("scenarios", "report,fix,review").split(",");
const keep = args.includes("--keep");
const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
const results = [];
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);

const git = (cwd, ...a) => execFileSync("git", a, { cwd, encoding: "utf8", env: { ...process.env, GIT_AUTHOR_NAME: "smoke", GIT_AUTHOR_EMAIL: "smoke@example.invalid", GIT_COMMITTER_NAME: "smoke", GIT_COMMITTER_EMAIL: "smoke@example.invalid" } }).trim();

async function sampleRepo(root, name) {
  const origin = join(root, name + "-origin.git");
  const proj = join(root, name);
  git(root, "init", "-q", "--bare", "-b", "master", origin);
  git(root, "init", "-q", "-b", "master", proj);
  git(proj, "remote", "add", "origin", origin);
  await writeFile(join(proj, "calc.js"), "// Tiny sample project for the loopbee smoke test.\nexport function add(a, b) { return a - b; }\nexport function mul(a, b) { return a * b; }\n");
  await writeFile(join(proj, "calc.test.mjs"), 'import assert from "node:assert/strict";\nimport { add, mul } from "./calc.js";\nassert.equal(add(2, 3), 5, "add");\nassert.equal(mul(2, 3), 6, "mul");\nconsole.log("all tests passed");\n');
  await writeFile(join(proj, "package.json"), '{ "name": "smoke-calc", "private": true, "type": "module", "scripts": { "test": "node calc.test.mjs" } }\n');
  await writeFile(join(proj, "README.md"), "# smoke-calc\n\nRun `npm test`. TODO: add a subtract function.\n");
  git(proj, "add", "-A");
  git(proj, "commit", "-qm", "init");
  git(proj, "push", "-q", "-u", "origin", "master");
  return proj;
}

const PROMPTS = {
  report: "每個工作日早上 9 點，讀一下這個專案，列出 README 裡的 TODO 和目前失敗的測試，寫成一份簡短報告給我。只讀，不要改任何檔案。",
  fix: "每個工作日早上 10 點：從最新的 origin/master 開一個新分支 loopbee/fix-tests，修正讓 npm test 失敗的 bug，跑 npm test 確認通過後在這個分支 commit。不要 push、不要合併到 master；做完就停下來等我審核，我會自己合併。",
};

async function author(core, agent, proj, scenario) {
  const t0 = Date.now();
  const messages = [{ role: "user", text: PROMPTS[scenario], at: new Date().toISOString() }];
  let reply = await core.api("/author", { agent, directory: proj, timezone: tz, messages });
  // Answer at most one round of clarifying questions with the first option, like a hurried user.
  if (reply.questions?.length) {
    log(`  ${agent}/${scenario}: agent asked:`, reply.questions.map((q) => q.text).join(" | "));
    messages.push({ role: "agent", text: reply.reply, at: new Date().toISOString() });
    messages.push({ role: "user", text: reply.questions.map((q) => q.options?.[0]).filter(Boolean).join("；") || "照你的建議", at: new Date().toISOString() });
    reply = await core.api("/author", { agent, directory: proj, timezone: tz, messages, draft: reply.loop });
  }
  const l = reply.loop;
  log(`  ${agent}/${scenario}: drafted in ${Math.round((Date.now() - t0) / 1000)} s — “${l.name}”, ${l.cron || "manual"} ${l.timezone}, ${l.sandbox}, ${l.workspace}${l.baseRef ? " @ " + l.baseRef : ""}${l.branch ? " → " + l.branch : ""}`);
  for (const s of l.steps) log(`     · ${s.kind.padEnd(9)} ${s.name}${s.argv?.length ? "  [" + s.argv.join(" ") + "]" : ""}${s.continueOnError ? " (continue on error)" : ""}`);
  if (reply.problem) log(`  ${agent}/${scenario}: problem: ${reply.problem}`);
  return reply;
}

async function runLoop(core, loop) {
  const saved = (await core.api("/loops", { ...loop, requireClean: false })).loops.find((l) => l.name === loop.name);
  const st = await core.api(`/loops/${saved.id}/run`, {});
  const run = st.runs.find((r) => r.loopId === saved.id);
  return waitFor(core.api, (s) => s.runs.find((r) => r.id === run.id && !["queued", "running"].includes(r.status)), 20 * 60000);
}

async function scenario(core, agent, root, name) {
  const proj = await sampleRepo(root, `${agent}-${name}`);
  const checks = [];
  const check = (ok, what) => {
    checks.push([ok, what]);
    log(`  ${ok ? "✓" : "✗"} ${what}`);
  };
  const reply = await author(core, agent, proj, name);
  const l = reply.loop;
  check(!reply.problem, "draft passes loopbee's checks");
  check(l.timezone === tz, `schedule uses your time zone (${tz})`);
  check(l.steps.some((s) => ["act", "observe", "summarize"].includes(s.kind)), "has an agent step");
  if (name === "report") check(l.sandbox === "read-only", "report Loop stays read-only");
  if (name === "fix") {
    check(l.steps.some((s) => s.kind === "gate"), "has a human gate before merging");
    check(["workspace-write", "workspace-network"].includes(l.sandbox), "fix Loop may edit files");
    check(l.workspace === "worktree", "fix Loop runs in a fresh worktree");
    check(!!l.branch, "loopbee creates the branch and commits (agent does not touch Git)");
  }
  if (reply.problem) return { agent, name, checks, status: "not run" };
  const t0 = Date.now();
  let r = await runLoop(core, l);
  log(`  ${agent}/${name}: run → ${r.status} in ${Math.round((Date.now() - t0) / 1000)} s ${r.error ? "— " + r.error : ""}`);
  if (r.result) log(`     result: ${r.result.status} — ${r.result.summary}`);
  if (name === "report") {
    check(["completed", "no_action"].includes(r.status), "run finished successfully");
    check(!!r.report && /subtract|減|TODO/i.test(r.report) && /add|5|-1/.test(r.report), "report names the TODO and the failing test");
  } else {
    check(r.status === "waiting", "run stopped at the gate");
    const branches = git(proj, "branch", "--list", "--format=%(refname:short)").split("\n").filter((b) => b && b !== "master");
    log(`     branches: ${branches.join(", ") || "(none)"} · run branch ${r.branch || "-"} · commits ${(r.commits || []).join(", ") || "-"}`);
    const branch = branches.find((b) => b.includes("fix")) || branches[0];
    check(!!branch, "a new branch exists in the repository");
    if (branch) {
      const calc = git(proj, "show", `${branch}:calc.js`);
      check(/a\s*\+\s*b/.test(calc), `branch ${branch} fixes add()`);
      check(git(proj, "rev-list", "--count", `master..${branch}`) !== "0", "the fix is committed on the branch");
    }
    check(git(proj, "rev-parse", "--abbrev-ref", "HEAD") === "master" && git(proj, "status", "--porcelain") === "" && /a - b/.test(git(proj, "show", "HEAD:calc.js")), "your own checkout is untouched (still on master, clean)");
    check(git(proj, "ls-remote", "origin", "refs/heads/*").split("\n").length === 1, "nothing was pushed to origin");
    if (r.status === "waiting") {
      await core.api(`/runs/${r.id}/gate`, { approve: true, note: "Smoke test: reviewed the branch." });
      r = await waitFor(core.api, (s) => s.runs.find((x) => x.id === r.id && x.status !== "waiting" && x.status !== "running"), 60000);
      check(r.status === "completed", "approving the gate completes the run");
    }
  }
  check(!!r.result, "agent returned the LOOPBEE_RESULT contract");
  const u = r.usage;
  const total = u ? (u.input || 0) + (u.cacheRead || 0) + (u.cacheWrite || 0) + (u.output || 0) : 0;
  log(`     usage: ${u ? `${u.model || "(model not reported)"} · ${total} tokens (input ${u.input || 0}, cache ${(u.cacheRead || 0) + (u.cacheWrite || 0)}, output ${u.output || 0})${u.costUsd ? ` · $${u.costUsd.toFixed(4)}` : ""}` : "none"}`);
  check(total > 0, "the run recorded the tokens the CLI reported");
  return { agent, name, checks, status: r.status };
}

// Two agents in one Loop: the review must send the plan back once, and the plan's second attempt must follow it.
async function reviewScenario(core, writer, reviewer, root) {
  const proj = await sampleRepo(root, `${writer}-${reviewer}-review`);
  const checks = [];
  const check = (ok, what) => {
    checks.push([ok, what]);
    log(`  ${ok ? "✓" : "✗"} ${what}`);
  };
  const loop = {
    id: "", version: 0, name: `review ${writer} by ${reviewer}`, description: "", directory: proj, agent: writer, sandbox: "read-only",
    cron: "", timezone: tz, catchUp: false, enabled: false, private: true, lock: "", doneWhen: "", maxRounds: 1, wallMinutes: 20,
    stallRounds: 1, allowedTools: [],
    steps: [
      { id: "plan", kind: "act", name: "Write plan", prompt: "Read README.md and write a numbered plan of three short items for its TODO. If you received reviewer feedback, follow it exactly. Do not change any file." },
      { id: "plan-review", kind: "review", name: "Review plan", agent: reviewer, inputs: ["plan"], sendBackTo: "plan", maxAttempts: 2,
        prompt: "Review the plan in the results of earlier steps. It passes only if its first line is exactly PLAN v2. If the first line is anything else, answer changes_requested with the summary: Make the first line exactly PLAN v2. Do not judge anything else. Do not change any file." },
    ],
  };
  const t0 = Date.now();
  const r = await runLoop(core, loop);
  log(`  review: run → ${r.status} in ${Math.round((Date.now() - t0) / 1000)} s ${r.error ? "— " + r.error : ""}`);
  for (const x of r.trace || []) log(`     · step ${x.step + 1} ${x.kind.padEnd(6)} attempt ${x.attempt || 1} · ${x.agent || "-"} · ${x.usage?.model || "(model not reported)"} · ${x.status} · ${(x.note || "").replace(/\s+/g, " ").slice(0, 100)}`);
  const trace = (r.trace || []).map((x) => `${x.step}:${x.attempt || 1}`).join(" ");
  check(r.status === "completed", "run completed after the review passed");
  check(trace === "0:1 1:1 0:2 1:2", `the review sent the plan back once (order ${trace || "-"})`);
  check((r.trace || []).every((x) => x.agent === (x.step === 0 ? writer : reviewer)), "each step ran its own agent");
  check(/changes requested/i.test(r.trace?.[1]?.note || ""), "the first review requested changes");
  const revised = await readFile(join(core.data, "runs", r.id, "round-01-step-01-attempt-02.report.md"), "utf8").catch(() => "");
  check(/^\s*PLAN v2/.test(revised), "the revised plan followed the feedback (its first line is PLAN v2)");
  check((r.trace || []).every((x) => (x.usage?.input || 0) + (x.usage?.cacheRead || 0) + (x.usage?.output || 0) > 0), "every attempt recorded the tokens its CLI reported");
  return { agent: `${writer}+${reviewer}`, name: "review", checks, status: r.status };
}

const root = await mkdtemp(join(tmpdir(), "loopbee-real-smoke-"));
log("binary:", bin);
log("scratch:", root);
const core = await startCore(join(root, "data"), {}, bin);
try {
  const s = await waitFor(core.api, (s) => s.agents.length && s, 60000);
  for (const a of s.agents) log(`agent ${a.name}: ${a.status} ${a.version || ""} caps ${(a.caps || []).join(",")} · levels ${(a.levels || []).join(",")}`);
  await core.api("/settings", { ...s.settings, maxSandbox: "workspace-write" });
  const wanted = (opt("agents", "") || s.agents.filter((a) => a.status === "available").map((a) => a.name).join(",")).split(",").filter(Boolean);
  if (!wanted.length) throw new Error("no agent CLI detected; install one (Codex CLI, Claude Code, OpenCode, pi) and sign in");
  for (const agent of wanted) {
    const levels = s.agents.find((a) => a.name === agent)?.levels || [];
    for (const name of scenarios.filter((n) => n !== "review")) {
      log(`— ${agent} / ${name}`);
      if (name === "fix" && !levels.includes("workspace-write")) {
        log(`  skipped: ${agent} cannot enforce workspace-write (levels: ${levels.join(", ")})`);
        continue;
      }
      try {
        results.push(await scenario(core, agent, root, name));
      } catch (e) {
        log(`  ✗ ${e.message}`);
        results.push({ agent, name, checks: [[false, e.message]], status: "error" });
      }
    }
  }
  if (scenarios.includes("review")) {
    // The reviewer is another agent when one is available, so two CLIs (and their models) share one Loop.
    const writer = wanted[0];
    const reviewer = wanted.find((a) => a !== writer) || writer;
    log(`— ${writer} writes, ${reviewer} reviews`);
    try {
      results.push(await reviewScenario(core, writer, reviewer, root));
    } catch (e) {
      log(`  ✗ ${e.message}`);
      results.push({ agent: `${writer}+${reviewer}`, name: "review", checks: [[false, e.message]], status: "error" });
    }
  }
} finally {
  await core.stop();
  if (!keep) await rm(root, { recursive: true, force: true }).catch(() => {});
}
console.log("\nSummary");
let failed = 0;
for (const r of results) {
  const bad = r.checks.filter(([ok]) => !ok);
  failed += bad.length;
  console.log(`${bad.length ? "FAIL" : "PASS"} ${r.agent}/${r.name} (${r.status})${bad.length ? ": " + bad.map(([, w]) => w).join("; ") : ""}`);
}
if (keep) console.log("kept:", root);
process.exit(failed ? 1 : 0);
