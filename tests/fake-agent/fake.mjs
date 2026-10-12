// Fake Codex / Claude Code CLI for UI tests. Never contacts a model; answers deterministically.
import { writeFileSync, appendFileSync } from "node:fs";

export async function main(name) {
  const args = process.argv.slice(2);
  const has = (f) => args.includes(f);
  if (has("--version")) return console.log(`${name} fake 9.9.9`);
  if (has("--help")) {
    return console.log(name === "codex" ? "--json --sandbox --output-last-message --output-schema --add-dir --skip-git-repo-check" : "--print --output-format stream-json --json-schema --add-dir --tools --settings --permission-mode");
  }
  let prompt = "";
  for await (const chunk of process.stdin) prompt += chunk;
  if (process.env.LOOPBEE_FAKE_LOG) appendFileSync(process.env.LOOPBEE_FAKE_LOG, JSON.stringify({ name, args, cwd: process.cwd() }) + "\n");
  let text;
  let structured;
  if (prompt.includes("HIT_LIMIT")) {
    // Real Claude Code 2.1.296 output when the subscription's session limit is reached.
    console.log(JSON.stringify({ type: "result", subtype: "success", is_error: true, api_error_status: 429, api_error: "usage_limit_reached", result: "You've hit your session limit · resets 10:20pm (Asia/Taipei)" }));
    process.exit(1);
  }
  const network = prompt.includes("NEEDS_NETWORK");
  if (prompt.includes("You are helping a developer configure Loopbee")) {
    const turns = (prompt.match(/^\[user\]/gm) || []).length;
    const proposal = {
      reply: turns > 1 ? "Moved it to 02:00 and kept the gate before master." : "Drafted a weekday Loop that uses your fix-prod-errors skill and stops before master.",
      questions: turns > 1 ? [] : [{ text: "Who merges to master after approval?", options: ["I do it myself", "Loopbee continues after approval"] }],
      loop: {
        name: "Fix production errors", description: "Check production errors, fix on a branch, deploy to test; stop before master.",
        cron: turns > 1 ? "0 2 * * *" : "0 9 * * 1-5", timezone: "Asia/Taipei", sandbox: network ? "workspace-network" : "read-only", workspace: network ? "worktree" : "direct", baseRef: network ? "origin/master" : "", branch: network ? "loopbee/fix-{date}" : "", lock: "env:test",
        maxRounds: 1, wallMinutes: 90, stallRounds: 2, gateHours: 48, doneWhen: "verify.passed", allowedTools: [],
        steps: [
          { kind: "act", name: "Fix and deploy to test", prompt: "Use the fix-prod-errors skill. Stop before merging to master.", argv: [], metric: "", metricPath: "" },
          { kind: "gate", name: "Developer approves production", prompt: "", argv: [], metric: "", metricPath: "" },
        ],
        notify: { events: ["done", "attention", "failed"], includeReport: true },
        env: [],
      },
    };
    text = JSON.stringify(proposal);
    if (has("--json-schema")) structured = proposal;
  } else if (prompt.includes("EDIT_FILE")) {
    writeFileSync("fixed.txt", "edited by the fake agent\n");
    text = 'Changed fixed.txt.\nLOOPBEE_RESULT {"status":"done","summary":"Fixes #1: edited fixed.txt"}';
  } else if (prompt.includes("NOTHING_TODO")) {
    text = 'Logs are clean.\nLOOPBEE_RESULT {"status":"no_action","summary":"No actionable errors"}';
  } else {
    text = 'Fixed checkout-timeout, merged to test and deployed.\nLOOPBEE_RESULT {"status":"done","summary":"Fixed 1 error, deployed to test","links":["https://example.com/pr/7"]}';
  }
  if (process.env.LOOPBEE_FAKE_DELAY) await new Promise((r) => setTimeout(r, Number(process.env.LOOPBEE_FAKE_DELAY)));
  if (name === "codex") {
    console.log(JSON.stringify({ type: "thread.started", thread_id: "t" }));
    console.log(JSON.stringify({ type: "item.completed", item: { id: "i", type: "agent_message", text } }));
    console.log(JSON.stringify({ type: "turn.completed", usage: {} }));
    const i = args.indexOf("--output-last-message");
    if (i >= 0) writeFileSync(args[i + 1], text);
  } else {
    console.log(JSON.stringify({ type: "system", subtype: "init" }));
    console.log(JSON.stringify({ type: "assistant", message: { content: [{ type: "text", text }] } }));
    console.log(JSON.stringify({ type: "result", subtype: "success", is_error: false, result: text, ...(structured ? { structured_output: structured } : {}) }));
  }
}
