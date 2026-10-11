// Shared helpers for UI tests: start an isolated nogui core and seed realistic data through the API.
import { spawn } from "node:child_process";
import { mkdtemp, readFile, mkdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

export const binary = resolve(process.platform === "win32" ? "bin/loopbee.exe" : "bin/loopbee");

export async function startCore(data, env = {}, bin = binary) {
  data ||= await mkdtemp(join(tmpdir(), "loopbee ui 測試-"));
  const child = spawn(bin, ["-nogui", "-data", data], { windowsHide: true, stdio: "ignore", env: { ...process.env, ...env } });
  let url;
  for (let i = 0; i < 150 && !url; i++) {
    try {
      url = (await readFile(join(data, "launch-url.txt"), "utf8")).trim();
      const base = url.split("/#")[0];
      // launch-url.txt may be stale from a previous process; wait until the server answers.
      const r = await fetch(base + "/api/state").catch(() => null);
      if (!r) url = undefined;
    } catch {}
    if (!url) await new Promise((r) => setTimeout(r, 100));
  }
  if (!url) throw new Error("core did not start");
  const base = url.split("/#")[0];
  const token = new URL(url.replace("/#", "/?")).searchParams.get("token");
  const res = await fetch(base + "/api/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ token }) });
  const cookie = res.headers.get("set-cookie").split(";")[0];
  const api = async (path, body, method) => {
    const r = await fetch(base + "/api" + path, {
      method: method || (body === undefined ? "GET" : "POST"),
      headers: { Cookie: cookie, ...(body === undefined ? {} : { "Content-Type": "application/json" }) },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await r.text();
    if (!r.ok) throw new Error(path + ": " + text);
    return JSON.parse(text);
  };
  const stop = async () => {
    if (child.exitCode !== null) return;
    const done = new Promise((r) => child.once("exit", r));
    child.kill();
    await done;
  };
  return { data, url, base, api, stop, child };
}

export async function waitFor(api, fn, timeout = 15000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    const s = await api("/state");
    const v = fn(s);
    if (v) return v;
    await new Promise((r) => setTimeout(r, 150));
  }
  throw new Error("timeout waiting for state");
}

const node = process.execPath;
const base = (dir, extra) => ({
  id: "", version: 0, description: "", directory: dir, agent: "codex", sandbox: "read-only", cron: "", timezone: "Asia/Taipei",
  catchUp: true, enabled: false, private: false, requireClean: false, lock: "", doneWhen: "verify.passed", maxRounds: 1,
  wallMinutes: 30, stallRounds: 2, allowedTools: [node], steps: [], next: "0001-01-01T00:00:00Z", updated: "0001-01-01T00:00:00Z",
  ...extra,
});
const verify = (name, code, extra = {}) => ({ kind: "verify", name, argv: [node, "-e", code], ...extra });

// Creates a varied set of Loops and Runs: completed with metrics, budget used, waiting at a gate,
// running, sent back and cancelled. Returns ids for direct navigation.
export async function seed(api, dir) {
  await mkdir(dir, { recursive: true });
  const save = async (l) => (await api("/loops", l)).loops.find((x) => x.name === l.name);
  const run = async (id) => {
    const s = await api(`/loops/${id}/run`, {});
    return s.runs.filter((r) => r.loopId === id).sort((a, b) => b.started.localeCompare(a.started))[0];
  };
  const until = (id, statuses) => waitFor(api, (s) => s.runs.find((r) => r.id === id && statuses.includes(r.status)));

  const score = await save(base(dir, {
    name: "React Doctor 100/100", description: "Scan the web app, apply small fixes, re-test and re-scan until the score reaches 100 or the budget is used.",
    cron: "0 11 * * *", enabled: true, maxRounds: 3, stallRounds: 3, doneWhen: "metrics.score >= 100 && verify.passed",
    steps: [verify("Scan", 'console.log(JSON.stringify({score: 71 + Math.floor(Math.random()*20), issues: 9}))', { metric: "score", metricPath: "$.score" }), verify("Test", 'console.log("212 passed")')],
  }));
  const r1 = await run(score.id);
  await until(r1.id, ["budget_exhausted", "stalled", "completed"]);

  const logs = await save(base(dir, {
    name: "Production log ERROR/WARN", description: "Fingerprint new errors from the last hour and draft bug reports.", cron: "0 9 * * *", enabled: true,
    steps: [verify("Collect", 'console.log("14 fingerprints, 3 new\\ncheckout-timeout 212\\ncart-null-sku 47")'), verify("Summarize", 'console.log("# Summary\\n- checkout-timeout affects 3% of checkouts")')],
  }));
  await until((await run(logs.id)).id, ["completed"]);

  const crm = await save(base(dir, {
    name: "CRM win-back e-mails", description: "Segment lapsed customers and draft e-mails; a person approves before anything leaves.", cron: "0 14 * * 1-5", enabled: true, maxRounds: 2,
    steps: [verify("Segment", 'console.log("4 segments, 12 drafts")'), { kind: "gate", name: "Review drafts" }, verify("Package", 'console.log("drafts packaged")')],
  }));
  const r3 = await run(crm.id);
  await until(r3.id, ["waiting"]);

  const seo = await save(base(dir, {
    name: "SEO / GEO visibility", description: "Baseline snapshot, opportunity analysis and technical verification.", cron: "0 8 * * *", enabled: true, private: true, maxRounds: 3,
    steps: [verify("Baseline", 'console.log("baseline registered")'), verify("Technical check", "setTimeout(()=>console.log('ok'), 120000)"), { kind: "gate", name: "Approve changes" }],
  }));
  const r4 = await run(seo.id);
  await waitFor(api, (s) => s.runs.find((r) => r.id === r4.id && r.status === "running" && r.step === 1));

  const draft = await save(base(dir, { name: "Lighthouse daily check", description: "Disabled draft for the paused list.", cron: "30 7 * * *", enabled: false, steps: [verify("Audit", 'console.log("audit")')] }));
  const r5 = await run(draft.id);
  await until(r5.id, ["completed"]);

  // A second CRM run that is sent back, and a cancelled one, for history variety.
  const gate2 = await save(base(dir, { name: "Release notes", description: "Draft release notes from merged PRs.", steps: [verify("Collect PRs", 'console.log("18 merged PRs")'), { kind: "gate", name: "Approve notes" }] }));
  const r6 = await run(gate2.id);
  await until(r6.id, ["waiting"]);
  await api(`/runs/${r6.id}/gate`, { approve: false, note: "Tone is too formal; shorten the summary." });
  await until(r6.id, ["rejected"]);
  const r7 = await run(gate2.id);
  await until(r7.id, ["waiting"]);
  await api(`/runs/${r7.id}/cancel`, {});
  await until(r7.id, ["cancelled"]);
  return { score, logs, crm, seo, waiting: r3, running: r4, finished: r1 };
}
