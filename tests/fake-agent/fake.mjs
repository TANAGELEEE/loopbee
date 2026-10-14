// Fake agent CLIs (Codex, Claude Code, Gemini CLI, OpenCode, pi) for UI tests. Never contacts a model; answers
// deterministically in each CLI's own event format, with the token usage each one reports.
import { writeFileSync, appendFileSync } from "node:fs";

export async function main(name) {
  const args = process.argv.slice(2);
  const has = (f) => args.includes(f);
  if (has("--version")) return console.log(`${name} fake 9.9.9`);
  if (name === "codex" && has("mcp") && has("list")) return console.log("[]"); // `codex mcp list --json`
  if (has("--help")) {
    return console.log({
      codex: "--json --sandbox --output-last-message --output-schema --add-dir --skip-git-repo-check --model",
      claude: "--print --output-format stream-json --json-schema --add-dir --tools --settings --permission-mode --model",
      gemini: "--output-format [choices: text, json, stream-json] --approval-mode --sandbox --policy --include-directories --skip-trust --model",
      opencode: "--format json --model --auto",
      pi: "--mode json --tools --exclude-tools --no-session --model",
    }[name]);
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
  } else if (prompt.includes("MISSING_ACCESS")) {
    text = 'Could not read the credentials.\nLOOPBEE_RESULT {"status":"blocked","summary":"nexscope credentials unreadable","missing":["~/.pi/agent/private/nexscope-infra-access.json (EPERM)","network access to infra.example.com"]}';
  } else if (prompt.includes("NOTHING_TODO")) {
    text = 'Logs are clean.\nLOOPBEE_RESULT {"status":"no_action","summary":"No actionable errors"}';
  } else {
    text = 'Fixed checkout-timeout, merged to test and deployed.\nLOOPBEE_RESULT {"status":"done","summary":"Fixed 1 error, deployed to test","links":["https://example.com/pr/7"]}';
  }
  if (process.env.LOOPBEE_FAKE_DELAY) await new Promise((r) => setTimeout(r, Number(process.env.LOOPBEE_FAKE_DELAY)));
  const out = (v) => console.log(JSON.stringify(v));
  const mi = Math.max(args.indexOf("--model"), args.indexOf("-m"));
  const model = mi >= 0 ? args[mi + 1] : "";
  // Every CLI reports the same turn: 1,000 fresh input tokens, 200 cached, 300 output.
  if (name === "codex") {
    out({ type: "thread.started", thread_id: "t" });
    out({ type: "item.completed", item: { id: "i", type: "agent_message", text } });
    out({ type: "turn.completed", usage: { input_tokens: 1200, cached_input_tokens: 200, output_tokens: 300 } });
    const i = args.indexOf("--output-last-message");
    if (i >= 0) writeFileSync(args[i + 1], text);
  } else if (name === "claude") {
    out({ type: "system", subtype: "init", model: model || "claude-fake" });
    out({ type: "assistant", message: { content: [{ type: "text", text }] } });
    out({ type: "result", subtype: "success", is_error: false, result: text, total_cost_usd: 0.0123, usage: { input_tokens: 1000, cache_read_input_tokens: 200, cache_creation_input_tokens: 0, output_tokens: 300 }, ...(structured ? { structured_output: structured } : {}) });
  } else if (name === "gemini") {
    const m = model || "gemini-fake";
    out({ type: "init", timestamp: "t", session_id: "s", model: m });
    out({ type: "message", timestamp: "t", role: "assistant", content: text, delta: true });
    out({ type: "result", timestamp: "t", status: "success", stats: { total_tokens: 1500, input_tokens: 1200, output_tokens: 300, cached: 200, input: 1000, duration_ms: 1, tool_calls: 0, models: { [m]: {} } } });
  } else if (name === "opencode") {
    out({ type: "text", timestamp: 1, sessionID: "s", part: { type: "text", text } });
    out({ type: "step_finish", timestamp: 1, sessionID: "s", part: { type: "step-finish", cost: 0.01, tokens: { input: 1000, output: 300, reasoning: 0, cache: { read: 200, write: 0 } } } });
  } else if (name === "pi") {
    const msg = { role: "assistant", content: [{ type: "text", text }], provider: "anthropic", model: model || "fake-model", stopReason: "stop", usage: { input: 1000, output: 300, cacheRead: 200, cacheWrite: 0, cost: { total: 0.01 } } };
    out({ type: "message_end", message: msg });
    out({ type: "agent_end", willRetry: false, messages: [msg] });
  }
}
