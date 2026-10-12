// Editor, settings and onboarding views (form-heavy pages).
import { ctx, t, T, esc, when, cronText, icon, btn, sec, empty, toggle, seg, fld, short, bytes, deliveryBadge, channelName, toDate, hm, SANDBOXES, sandboxRank, sandboxLabel, sandboxHelp, agentLabel } from "./ui.js";
import { flowState, renderFlow } from "./flow.js";
import { viewer, runsSorted } from "./views.js";

const S = () => ctx.state;
export const STEP_KINDS = ["act", "observe", "summarize", "run", "verify", "gate"];
export const kindLabel = (k) => t({ observe: "Observe", act: "Agent task", verify: "Verify", gate: "Human gate", summarize: "Summarize", run: "Command" }[k] || k);

export function newDraft(base = {}) {
  return {
    id: "", version: 0, name: "", description: "", directory: "", agent: S().settings.defaultAgent || "codex", sandbox: "read-only",
    cron: "", timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, catchUp: true, enabled: false,
    private: true, requireClean: true, lock: "", doneWhen: "verify.passed", maxRounds: 1, wallMinutes: 60,
    stallRounds: 2, allowedTools: [], workspace: "direct", baseRef: "", gateHours: 72, env: [], notify: {},
    steps: [{ kind: "act", name: t("Agent task"), prompt: "" }],
    next: "0001-01-01T00:00:00Z", updated: "0001-01-01T00:00:00Z",
    ...base,
  };
}

// Client-side gap check mirroring core.Validate, plus advisory suggestions. The server remains
// the authority; this only lets the editor point at the field before saving.
export function gaps(d) {
  const out = [];
  const add = (level, title, field, detail = "") => out.push({ level, title, field, detail });
  if (!d.name.trim()) add("block", t("Name is required"), "name");
  if (!/^(\/|[A-Za-z]:[\\/]|\\\\)/.test(d.directory)) add("block", t("Working directory must be an absolute path"), "directory");
  if (!d.steps.length) add("block", t("The flow needs at least one step"), "steps");
  d.steps.forEach((s, i) => {
    if (!s.name.trim()) add("block", t("Step {n} needs a name", { n: i + 1 }), `step-${i}`);
    if (["observe", "act", "summarize"].includes(s.kind) && !(s.prompt || "").trim()) add("block", t("Step “{name}” needs a prompt", { name: s.name || i + 1 }), `step-${i}`);
    if (s.kind === "verify" || s.kind === "run") {
      if (!s.argv?.length) add("block", t("Verify step “{name}” needs a command", { name: s.name || i + 1 }), `step-${i}`);
      else if (!d.allowedTools.includes(s.argv[0])) add("block", t("“{cmd}” is not on the allowed tools list", { cmd: s.argv[0] }), "allowedTools", `flow[${i}].argv[0]`);
      if (!!s.metric !== !!s.metricPath) add("block", t("Metric name and JSON path go together"), `step-${i}`);
    }
  });
  if (!(d.maxRounds >= 1 && d.maxRounds <= 20)) add("block", t("Rounds must be 1–20"), "maxRounds");
  if (!(d.wallMinutes >= 1 && d.wallMinutes <= 1440)) add("block", t("Time limit must be 1–1440 minutes"), "wallMinutes");
  if (!(d.stallRounds >= 1 && d.stallRounds <= 20)) add("block", t("Stall rounds must be 1–20"), "stallRounds");
  if (d.cron && d.cron.trim().split(/\s+/).length !== 5) add("block", t("Cron needs five fields"), "cron");
  if (sandboxRank(d.sandbox) > sandboxRank(S().settings.maxSandbox)) add("block", t("Permission exceeds this device’s limit"), "sandbox", t("Raise it in Settings › General first"));
  if (d.steps.some((s) => !["gate", "run", "verify"].includes(s.kind)) && S().agents.length && !S().agents.some((a) => a.name === d.agent && a.status === "available")) add("warn", t("{agent} was not detected on this device", { agent: agentLabel(d.agent) }), "agent");
  for (const e of d.env || []) {
    if (!/^[A-Z_][A-Z0-9_]{0,63}$/.test(e.name)) add("block", t("Environment variable names use UPPER_SNAKE_CASE"), "env");
    else if (!e.value) add(e.secret ? "warn" : "block", t("{name} has no value yet; runs are blocked until it is set", { name: e.name }), "env");
  }
  if (d.workspace === "worktree" && !d.baseRef) add("warn", t("Worktree starts from HEAD of the working directory; set a base such as origin/master"), "baseRef");
  if (d.workspace === "worktree" && !d.branch && sandboxRank(d.sandbox) >= sandboxRank("workspace-write") && sandboxRank(d.sandbox) < sandboxRank("agent-config")) add("warn", t("Agents cannot commit in a worktree; set a branch so Loopbee commits the changes"), "branch");
  // A command after an agent step runs code the agent may have written, as you and outside any sandbox.
  const firstAgent = d.steps.findIndex((s) => !["run", "verify", "gate"].includes(s.kind));
  const unreviewed = firstAgent < 0 ? -1 : d.steps.findIndex((s, i) => i > firstAgent && (s.kind === "run" || s.kind === "verify") && !d.steps.slice(firstAgent, i).some((x) => x.kind === "gate"));
  if (unreviewed >= 0 && d.sandbox !== "agent-config") add("warn", t("“{name}” runs after the agent and before any review: it executes the agent's changes on this computer outside the sandbox. Fine for your own tasks; for issues, e-mail or web content put a gate first", { name: d.steps[unreviewed].name || kindLabel(d.steps[unreviewed].kind) }), `step-${unreviewed}`);
  // Work from issues and pull requests (Loopbee's own intake and delivery).
  if ((d.intake || d.pullRequest) && (d.workspace !== "worktree" || !d.branch)) add("block", t("Issues and pull requests need a fresh git worktree and a branch"), "branch");
  if (d.branch?.includes("{issue}") && !d.intake) add("block", t("{issue} in the branch name needs GitHub issues as the work source"), "branch");
  if (d.pullRequest?.mode === "after-review") {
    const lastAgent = d.steps.map((s) => !["run", "verify", "gate"].includes(s.kind)).lastIndexOf(true);
    const lastGate = d.steps.map((s) => s.kind === "gate").lastIndexOf(true);
    if (lastGate < 0 || lastGate < lastAgent) add("block", t("Add a gate after the agent step: the pull request opens after your review"), "pr");
  }
  if (d.pullRequest?.mode === "draft") add("warn", t("A draft pull request pushes the agent's change to GitHub before anyone has reviewed it"), "pr");
  if ((d.intake || d.pullRequest) && !S().github?.version) add("warn", t("GitHub CLI (gh) not found: install it, then detect again in Settings › Agents"), "intake");
  else if ((d.intake || d.pullRequest) && !S().github?.signedIn) add("warn", t("GitHub CLI is not signed in: run gh auth login"), "intake");
  if (d.branch && d.workspace !== "worktree") add("block", t("A branch needs the fresh git worktree workspace"), "branch");
  if (!d.steps.some((s) => s.kind === "verify" || ["act", "observe", "summarize"].includes(s.kind)) && (d.doneWhen || "verify.passed") !== "true") add("warn", t("No verify step: completion relies on “{c}”", { c: d.doneWhen || "verify.passed" }), "doneWhen");
  if (d.enabled && !d.cron) add("warn", t("Enabled without a schedule only allows manual runs"), "cron");
  if (!d.id) add("warn", t("Not run yet — run once manually before enabling the schedule"), "");
  return out;
}

export function gapsPanel(d) {
  const g = gaps(d);
  const b = g.filter((x) => x.level === "block").length;
  const w = g.length - b;
  return `${sec(T("Gaps and checks"), { note: `${b ? `<span class="chip err">${T("{n} blocking", { n: b })}</span>` : `<span class="chip ok">${T("No blocking gaps")}</span>`}${w ? ` <span class="chip warn">${T("{n} suggestion|{n} suggestions", { n: w })}</span>` : ""}` })}
  <div class="grp">${g.length ? g.map((x) => `<div class="row top">${icon(x.level === "block" ? "err" : "warn")}<div class="grow min0"><div class="nm">${esc(x.title)}</div>${x.detail ? `<div class="sub mono">${esc(x.detail)}</div>` : ""}</div>${x.field ? `<button type="button" class="btn sm" data-action="focus-field" data-field="${esc(x.field)}">${T("Fix")}</button>` : ""}</div>`).join("") : empty(T("Ready to save"))}</div>`;
}

export function editorPage(d, ui) {
  const isNew = !d.id;
  const nav = [["basic", "Basics"], ["target", "Target"], ["agent", "Agent & permission"], ["workspace", "Workspace"], ["work", "Work & delivery"], ["schedule", "Schedule"], ["flow", "Flow"], ["limits", "Limits & completion"], ["notify", "Notifications"]];
  const blocking = gaps(d).filter((x) => x.level === "block");
  const sel = Math.min(ui.step ?? 0, d.steps.length - 1);
  const s = d.steps[sel];
  const input = (f, v, attrs = "") => `<input class="inp" data-f="${f}" name="${f}" value="${esc(v)}" ${attrs}>`;
  const num = (f, v) => `<input class="inp num" type="number" min="1" data-f="${f}" name="${f}" value="${esc(v)}">`;
  const stepEditor = s
    ? `<div class="step-edit"><div class="nm">${esc(s.name || "—")} (${esc(s.kind)})</div>
      ${fld(T("Kind"), `<select class="inp narrowi" data-sf="kind" data-i="${sel}" name="kind-${sel}">${STEP_KINDS.map((k) => `<option value="${k}" ${k === s.kind ? "selected" : ""}>${esc(kindLabel(k))}</option>`).join("")}</select>`)}
      ${fld(T("Step name"), `<input class="inp" data-sf="name" data-i="${sel}" name="name-${sel}" value="${esc(s.name)}" id="step-${sel}">`)}
      ${s.kind === "gate" ? fld(T("Gate"), `<div class="sub t2">${T(sel === d.steps.length - 1 ? "Last step: the run waits for a person, who takes over from here. Approving marks the run done." : "The run waits here until the executor approves or sends it back with a note.")}</div>`) : s.kind === "run"
        ? fld(T("Command argv"), `<input class="inp mono" data-sf="argv" data-i="${sel}" name="argv-${sel}" value="${esc(JSON.stringify(s.argv || []))}">`, T("JSON array of strings; no shell. The first entry must be on the allowed tools list.")) +
          fld(T("If it fails"), toggle(!!s.continueOnError, "step-continue", t("Continue and pass the output on"), `data-i="${sel}"`), T("Off: a failing command fails the run. On: the failure is recorded and later agent steps receive its output, e.g. failing tests to report on."))
        : s.kind === "verify"
        ? fld(T("Command argv"), `<input class="inp mono" data-sf="argv" data-i="${sel}" name="argv-${sel}" value="${esc(JSON.stringify(s.argv || []))}">`, T("JSON array of strings; no shell. The first entry must be on the allowed tools list.")) +
          fld(T("Metric"), `<div class="pair"><input class="inp" data-sf="metric" data-i="${sel}" placeholder="score" value="${esc(s.metric || "")}" aria-label="${T("Metric name")}"><input class="inp mono" data-sf="metricPath" data-i="${sel}" placeholder="$.summary.score" value="${esc(s.metricPath || "")}" aria-label="${T("JSON metric path")}"></div>`, T("Parsed by Loopbee from the command’s JSON output; agent claims never count."))
        : fld(T("Prompt"), `<textarea class="inp mono" rows="5" data-sf="prompt" data-i="${sel}" name="prompt-${sel}">${esc(s.prompt || "")}</textarea>`, T("Sent to the agent as data for this step. Report language follows Settings."))}
      <div class="actions mt10">${btn("Move up", "step-up", { cls: "sm", attrs: `data-i="${sel}" ${sel === 0 ? "disabled" : ""}` })}${btn("Move down", "step-down", { cls: "sm", attrs: `data-i="${sel}" ${sel === d.steps.length - 1 ? "disabled" : ""}` })}${btn("Remove step", "step-remove", { cls: "sm danger-o", attrs: `data-i="${sel}" ${d.steps.length === 1 ? "disabled" : ""}` })}</div></div>`
    : "";
  const json = ui.mode === "json";
  return `
  <div class="sec top-sec crumbs"><h2><a href="#/loops">Loops</a> › ${isNew ? T("New Loop") : `<a href="#/loop/${d.id}">${esc(d.name)}</a> › ${T("Edit definition")}`}</h2><span class="note">${isNew ? T("Saved as private and disabled until you enable it") : T("Based on v{v} · saving creates v{n}; active runs keep their snapshot", { v: d.version, n: d.version + 1 })}</span><div class="act">${ui.dirty ? `<span class="chip warn">${T("Unsaved changes")}</span>` : ""}${seg("mode", json ? "json" : "form", [["form", t("Form")], ["json", "JSON"]], { action: "editor-mode", label: t("Editor mode") })}</div></div>
  <div class="ed">
    <nav class="ednav" aria-label="${T("Sections")}">${nav.map(([id, l]) => `<a href="#ed-${id}" data-action="ed-jump" data-target="ed-${id}">${T(l)}${id === "flow" && blocking.some((x) => x.field.startsWith("step") || x.field === "allowedTools") ? ` <span class="cnt">${blocking.filter((x) => x.field.startsWith("step") || x.field === "allowedTools").length}</span>` : ""}</a>`).join("")}</nav>
    <form id="loop-form" class="edmain" autocomplete="off">
    ${json ? `${sec("JSON", { note: T("Edit the whole definition; switching back validates it") })}<div class="card sm"><textarea class="inp mono definition-json" id="def-json" rows="28" spellcheck="false">${esc(JSON.stringify(definitionOnly(d), null, 2))}</textarea></div>` : `
      ${sec(T("Basics"), { id: "ed-basic" })}
      <div class="grp">${fld(T("Name"), input("name", d.name, 'maxlength="160" id="f-name"'))}${fld(T("Description"), `<textarea class="inp" rows="2" data-f="description" name="description">${esc(d.description)}</textarea>`)}${fld(T("Resource lock"), input("lock", d.lock, 'placeholder="repo:web-app" id="f-lock"'), T("Loops with the same lock never run at the same time; empty locks only this Loop against itself."))}${fld(T("Private"), toggle(d.private, "ed-toggle", t("Private"), 'data-f="private"'), T("Private Loops are never published to the team repository or exported."))}</div>
      ${sec(T("Target"), { id: "ed-target" })}
      <div class="grp">${fld(T("Working directory"), input("directory", d.directory, `placeholder="${esc(ctx.state.info?.os === "windows" ? "C:\\work\\repo" : "/Users/me/work/repo")}" id="f-directory"`), T("Absolute local path. The agent runs here; Loopbee never switches your branches."))}${fld(T("Require clean tree"), toggle(d.requireClean, "ed-toggle", t("Require clean Git tree"), 'data-f="requireClean"'), T("Refuse to start when git status shows uncommitted changes."))}</div>
      ${sec(T("Agent & permission"), { id: "ed-agent" })}
      <div class="grp">${fld(T("Agent"), `<select class="inp narrowi" data-f="agent" name="agent" id="f-agent">${["codex", "claude"].map((n) => `<option value="${n}" ${d.agent === n ? "selected" : ""}>${esc(agentLabel(n))}${S().agents.find((a) => a.name === n)?.status === "available" ? "" : " · " + T("not detected")}</option>`).join("")}</select>`)}${fld(T("Permission"), `<span id="f-sandbox">${seg("sandbox", d.sandbox, SANDBOXES.map((x) => [x, sandboxLabel(x)]), { action: "ed-seg", label: t("Permission") })}</span>`, `${esc(sandboxHelp(d.sandbox))} ${T("This device allows at most: {max}. A definition cannot raise it.", { max: sandboxLabel(S().settings.maxSandbox) })}`)}${fld(T("Allowed tools"), `<input class="inp mono" data-f="allowedTools" id="f-allowedTools" value="${esc(d.allowedTools.join(" "))}" placeholder="npm node git">`, T("Executables command and verify steps may call, separated by spaces. Commands run without a shell."))}</div>
      ${sec(T("Workspace"), { id: "ed-workspace" })}
      <div class="grp">${fld(T("Where it runs"), seg("workspace", d.workspace || "direct", [["direct", t("Working directory")], ["worktree", t("Fresh git worktree")]], { action: "ed-seg", label: t("Workspace") }), T("A worktree keeps your own checkout and uncommitted work untouched; use it when the Loop creates branches or commits."))}${d.workspace === "worktree" ? fld(T("Start from"), input("baseRef", d.baseRef || "", 'class="inp mono" placeholder="origin/master" id="f-baseRef"'), T("Loopbee fetches the remote first and creates the worktree at this ref for every run.")) + fld(T("Branch"), input("branch", d.branch || "", 'class="inp mono" placeholder="loopbee/fix-{date}" id="f-branch"'), T("Loopbee creates this branch and commits the agent's changes on it after each agent step, so the agent never touches Git itself. {date}, {time} and {run} are filled per run. Empty: a detached worktree.")) : ""}
      ${fld(T("Environment"), `<div id="f-env" class="envlist">${(d.env || []).map((e, i) => `<div class="pair envrow"><input class="inp mono" data-env="name" data-i="${i}" value="${esc(e.name)}" placeholder="DB_HOST" aria-label="${T("Name")}"><input class="inp ${e.secret ? "" : "mono"}" data-env="value" data-i="${i}" type="${e.secret ? "password" : "text"}" value="${esc(e.secret && e.value ? "" : e.value)}" placeholder="${esc(e.secret ? (e.value ? t("Saved in keychain · type to replace") : t("Secret value")) : t("Value"))}" aria-label="${T("Value")}"><label class="check sm"><input type="checkbox" data-env="secret" data-i="${i}" ${e.secret ? "checked" : ""}>${T("Secret")}</label>${btn("Remove", "env-remove", { cls: "sm ghost", attrs: `data-i="${i}"` })}</div>`).join("")}${btn("Add variable", "env-add", { cls: "sm" })}</div>`, T("Injected into agent and command steps. Secret values go to the system keychain; the definition keeps a reference only."))}</div>
      ${workSection(d)}
      ${sec(T("Schedule"), { id: "ed-schedule" })}
      <div class="grp">${fld(T("Rule"), `<div class="pair"><input class="inp mono" data-f="cron" id="f-cron" value="${esc(d.cron)}" placeholder="0 9 * * 1-5" aria-label="Cron"><input class="inp" data-f="timezone" value="${esc(d.timezone)}" aria-label="${T("Timezone")}"><span class="sub t2" id="cron-text">= ${esc(cronText(d.cron))}</span></div>`, T("Five-field cron; leave empty for manual runs only."))}${fld(T("Enabled"), toggle(d.enabled, "ed-toggle", t("Enable schedule"), 'data-f="enabled"'))}${fld(T("Missed runs"), seg("catchUp", d.catchUp ? "1" : "0", [["1", t("Catch up the latest once")], ["0", t("Skip")]], { action: "ed-seg", label: t("Missed runs") }))}</div>
      ${sec(T("Flow"), { id: "ed-flow", note: T("Steps run in order; a failed verify starts the next round from step 1"), act: `<button type="button" class="btn sm" data-action="step-add-menu" aria-haspopup="menu">＋ ${T("Add step")} ▾</button>` })}
      <div class="card sm"><div class="nm">${T("Preview")}</div><div class="sub">${T("Generated from the current draft; nothing runs")}</div>${renderFlow(d.steps, flowState(d.steps, null), { maxRounds: d.maxRounds, selected: sel })}</div>
      <div class="grp mt10" id="steps">${d.steps.map((x, i) => `<button type="button" class="row link srow ${i === sel ? "selrow" : ""}" data-action="select-step" data-i="${i}" aria-pressed="${i === sel}"><span class="tag">${esc(kindLabel(x.kind))}</span><span class="grow min0"><span class="nm ell">${esc(x.name || "—")}</span><span class="sub ell">${esc(x.kind === "verify" || x.kind === "run" ? (x.argv || []).join(" ") : x.kind === "gate" ? t("Waits for your decision") : (x.prompt || "").slice(0, 90))}</span></span><span class="sub">${i + 1}</span></button>`).join("")}${stepEditor}</div>
      ${sec(T("Limits & completion"), { id: "ed-limits" })}
      <div class="grp">${fld(T("Maximum rounds"), num("maxRounds", d.maxRounds))}${fld(T("Time limit (minutes)"), num("wallMinutes", d.wallMinutes), T("Only time spent running steps counts; waiting at a gate does not."))}${fld(T("Stall rounds"), num("stallRounds", d.stallRounds), T("Stop after this many rounds with identical results."))}${fld(T("Gate expiry (hours)"), num("gateHours", d.gateHours || 72), T("Waiting at a gate does not use the time limit; after this long without a decision the run ends."))}${fld(T("Done when"), input("doneWhen", d.doneWhen, 'class="inp mono" id="f-doneWhen"'), T("Small expression: metrics.name, verify.passed, comparisons, && || !. Unknown values count as not met."))}</div>
      ${sec(T("Notifications"), { id: "ed-notify" })}
      <div class="grp">${fld(T("Notify on"), `<div class="chips">${[["done", t("Done")], ["attention", t("Needs you")], ["failed", t("Failed or blocked")], ["no_action", t("Nothing to do")]].map(([v, l]) => chipToggle("events", v, l, (d.notify?.events?.length ? d.notify.events : ["done", "attention", "failed"]).includes(v))).join("")}</div>`)}${fld(T("Channels"), `<div class="chips">${chipToggle("channels", "", t("All enabled"), !d.notify?.channels?.length)}${["desktop", "feishu", "telegram", "bark"].map((c) => chipToggle("channels", c, channelName(c), (d.notify?.channels || []).includes(c))).join("")}</div>`, T("Channels are configured in Settings › Notifications."))}${fld(T("Include the agent report"), toggle(!!d.notify?.includeReport, "ed-notify-report", t("Include the agent report")), T("Sends the agent's final report with the notification — useful for daily reports."))}</div>
      <div class="sub mt10">${T("No automatic rollback: review workspace changes after a failure.")}</div>`}
    </form>
    <aside class="edside">
      <div id="gaps">${gapsPanel(d)}</div>
      <div class="card sm mt10"><button type="button" class="btn pri wide" data-action="save-loop" ${blocking.length ? "disabled" : ""}>${isNew ? T("Save Loop") : T("Save as v{n}", { n: d.version + 1 })}</button><div class="sub mt6">${T(blocking.length ? "Resolve blocking gaps to save." : "Saving validates again on this device.")}</div>${isNew ? "" : `<a class="btn ghost wide mt6" href="#/loop/${d.id}">${T("Discard changes")}</a>`}</div>
    </aside>
  </div>`;
}
const chipToggle = (group, value, label, on) =>
  `<button type="button" class="chip tgl ${on ? "ok" : ""}" aria-pressed="${on}" data-action="ed-notify" data-group="${group}" data-value="${value}">${on ? "✓ " : ""}${esc(label)}</button>`;
function workSection(d) {
  const intake = d.intake ? "github-issues" : "none";
  const mode = d.pullRequest?.mode || "none";
  const gh = S().github || {};
  const ghRow = d.intake || d.pullRequest
    ? fld(T("GitHub CLI"), gh.version ? `<span class="chip ${gh.signedIn ? "ok" : "warn"}">${icon(gh.signedIn ? "ok" : "warn")}${esc(gh.version)} · ${T(gh.signedIn ? "signed in" : "not signed in")}</span>` : `<span class="chip err">${icon("err")}${T("not found")}</span>`, T("Loopbee uses your own gh login; it is never stored by Loopbee."))
    : "";
  return `${sec(T("Work & delivery"), { id: "ed-work" })}
  <div class="grp">${fld(T("Work comes from"), seg("intake", intake, [["none", t("The prompt")], ["github-issues", t("GitHub issues")]], { action: "ed-intake", label: t("Work comes from") }), T(intake === "none" ? "Each run does what the agent step says." : "Each run takes the oldest open issue with the label, opened by someone with write access, marks it agent-working and gives it to the agent as untrusted text. No issue: the run ends as “Nothing to do” without an agent call."))}
  ${d.intake ? `${fld(T("Issue label"), `<input class="inp mono narrowi" data-intake="label" id="f-intake-label" value="${esc(d.intake.label || "")}" placeholder="agent-ready">`)}${fld(T("Repository"), `<input class="inp mono" data-intake="repo" value="${esc(d.intake.repo || "")}" placeholder="${T("owner/name — empty: this folder's origin")}">`)}${fld(T("Also trust"), `<input class="inp mono" data-intake="trustedUsers" value="${esc((d.intake.trustedUsers || []).join(" "))}" placeholder="login another-login">`, T("Comments from people without write access never reach the agent. Add logins to trust more people."))}` : ""}
  ${fld(T("Pull request"), `<span id="f-pr">${seg("prmode", mode, [["none", t("No, keep the branch")], ["after-review", t("After my review")], ["draft", t("Draft, review on GitHub")]], { action: "ed-pr", label: t("Pull request") })}</span>`, T(mode === "after-review" ? "You review the diff at the gate; then Loopbee pushes the branch and opens the pull request. The link is sent with the notification." : mode === "draft" ? "Loopbee pushes the branch and opens a draft pull request right away; you review it on GitHub. The link is sent with the notification." : "The branch stays local; push it yourself."))}
  ${d.pullRequest ? fld(T("Target branch"), `<input class="inp mono narrowi" data-pr="base" value="${esc(d.pullRequest.base || "")}" placeholder="${T("from the base ref")}">`) : ""}${mode === "after-review" ? fld(T("Merge"), `<span id="f-merge">${seg("prmerge", d.pullRequest.merge || "", [["", t("I merge on GitHub")], ["after-checks", t("When all checks pass")]], { action: "ed-merge", label: t("Merge") })}</span>`, T(d.pullRequest.merge ? "After your approval Loopbee waits for the pull request's checks and squash-merges it when all pass. Failing or missing checks leave it open and tell you why." : "The pull request stays open for you to merge.")) : ""}${ghRow}</div>`;
}

export function definitionOnly(d) {
  const { next, updated, ...rest } = d;
  return rest;
}

// ---------- Settings ----------
const tabs = [["general", "General"], ["agents", "Agents"], ["notifications", "Notifications"], ["team", "Team sync"], ["data", "Data & archive"], ["about", "About"]];
export function settingsPage(tab, ui) {
  const c = S().settings;
  const body = { general, agents, notifications, team, data, about }[tab] || general;
  return `<div class="sw"><nav class="smenu" aria-label="${T("Settings")}">${tabs.map(([id, l]) => `<a href="#/settings/${id}" class="${id === tab ? "on" : ""}" ${id === tab ? 'aria-current="page"' : ""}>${T(l)}</a>`).join("")}</nav><div class="spane">${body(c, ui)}</div></div>`;
}
const langs = [["zh-TW", "繁體中文"], ["zh-CN", "简体中文"], ["en", "English"]];
const ro = () => (viewer() ? "disabled" : "");
function general(c) {
  return `
  ${sec(T("Language"), { note: T("Takes effect immediately; no restart") })}
  <div class="grp">${fld(T("Interface language"), seg("language", c.language, langs, { action: "set", label: t("Interface language") }))}${fld(T("Digest and notification language"), seg("digestLanguage", c.digestLanguage, langs, { action: "set", label: t("Digest and notification language") }), T("Independent of the interface, e.g. Chinese UI with an English digest."))}${fld(T("Report language (sent to agents)"), seg("reportLanguage", c.reportLanguage, langs, { action: "set", label: t("Report language") }), T("Added to agent prompts. Agent reports are never translated afterwards."))}${fld(T("Timezone"), `<span class="nm">${esc(Intl.DateTimeFormat().resolvedOptions().timeZone)}</span>`, T("Display uses this device’s timezone; each Loop keeps its own schedule timezone."))}</div>
  <div class="bn mt10">${icon("ok")}<span>${T("Statuses, stop reasons and outcomes are stored as codes and translated only for display, so history follows the language switch. Loop names and descriptions are yours and are not translated.")}</span></div>
  ${sec(T("Appearance"))}
  <div class="grp">${fld(T("Theme"), seg("theme", c.theme, [["system", t("Follow system")], ["light", t("Light")], ["dark", t("Dark")]], { action: "set", label: t("Theme") }))}${fld(T("Connection animation"), seg("motion", c.motion ? "on" : "off", [["on", t("On")], ["off", t("Off")]], { action: "set", label: t("Connection animation") }), T("The bee flying along connections is decoration only. With “Reduce motion” enabled in the system it stays still at the middle of the line; animations pause while the window is hidden."))}</div>
  ${sec(T("Scheduling and running"))}
  <div class="grp">${fld(T("Pause all schedules"), toggle(c.paused, "pause-all", t("Pause all schedules"), ro()), T("Only affects future triggers; active runs continue and nothing is recorded as missed."))}${fld(T("Start at login"), toggle(c.startAtLogin, "set-toggle", t("Start at login"), `data-name="startAtLogin" ${ro()}`))}${fld(T("Keep awake"), toggle(c.keepAwake, "set-toggle", t("Keep awake"), `data-name="keepAwake" ${ro()}`), T("Prevents idle sleep only. Nothing runs while the computer sleeps or the lid is closed."))}${fld(T("Closing the window"), `<span class="nm">${T("Hide to the menu bar / tray; schedules continue")}</span>`, T("Use Quit in the tray menu to stop scheduling."))}${fld(T("Maximum permission"), seg("maxSandbox", c.maxSandbox, SANDBOXES.map((x) => [x, sandboxLabel(x)]), { action: "set", label: t("Maximum permission") }), `${esc(sandboxHelp(c.maxSandbox))} ${T("The highest level any Loop on this device may use.")}`)}${fld(T("Missed runs"), `<span class="nm">${T("Each Loop decides: catch up the latest once, or skip")}</span>`)}${fld(T("Concurrency"), `<span class="nm">${T("Loops with the same lock never run together")}</span>`)}</div>
  ${sec(T("Daily digest"))}
  <form class="grp" id="digest-form">${fld(T("Send automatically"), toggle(c.digestEnabled, "set-toggle", t("Send daily digest"), `data-name="digestEnabled" ${ro()}`))}${fld(T("Schedule"), `<div class="pair"><input class="inp mono" name="digestCron" value="${esc(c.digestCron)}" aria-label="Cron" ${ro()}><input class="inp" name="digestTimezone" value="${esc(c.digestTimezone)}" aria-label="${T("Timezone")}" ${ro()}><button class="btn" ${ro()}>${T("Save")}</button></div>`, esc(cronText(c.digestCron)))}</form>`;
}
function agentRows(selectable) {
  const list = S().agents;
  const c = S().settings;
  const known = [["codex", "Codex CLI", "#5b8def"], ["claude", "Claude Code", "#c95b9a"]];
  return known.map(([id, name, color]) => {
    const a = list.find((x) => x.name === id);
    const state = !a || a.status === "unavailable" ? ["mut", "Not detected"] : a.status === "available" ? ["ok", "Connected"] : ["warn", "Unverified"];
    const isDefault = c.defaultAgent === id;
    const capNames = { "last-message": t("final message"), schema: t("structured output"), "add-dir": t("extra directories"), settings: t("sandbox settings"), tools: t("tool limits") };
    return `<div class="row top"><span class="idot" data-c="${color}"></span><div class="grow min0"><div class="nm">${esc(name)} <span class="chip ${state[0]}">${state[0] === "ok" ? icon("ok") : state[0] === "warn" ? icon("warn") : ""}${T(state[1])}</span></div><div class="sub mono ell">${esc(a?.path ? [a.path, a.version].filter(Boolean).join(" · ") : t("Not found on PATH"))}</div>${(a?.caps || []).filter((x) => capNames[x]).length ? `<div class="chips mt3">${a.caps.filter((x) => capNames[x]).map((x) => `<span class="chip">${esc(capNames[x])}</span>`).join("")}</div>` : ""}<div class="sub mt3">${T(id === "codex" ? "Runs codex exec --json; skills and AGENTS.md from your Codex setup apply." : "Runs claude -p with stream-json output; your skills, plugins and CLAUDE.md apply.")}</div></div>${isDefault ? `<span class="chip ok">${icon("ok")}${T("Default")}</span>` : selectable && a?.status === "available" && !viewer() ? btn("Set as default", "set", { cls: "sm", attrs: `data-name="defaultAgent" data-value="${id}"` }) : ""}</div>`;
  }).join("");
}
function agents() {
  const c = S().settings;
  const flags = {
    "read-only": ["--sandbox read-only", "--permission-mode dontAsk --tools Read,Grep,Glob"],
    "workspace-write": ["--sandbox workspace-write", "--permission-mode acceptEdits + sandbox (no network)"],
    "workspace-network": ["--sandbox workspace-write -c sandbox_workspace_write.network_access=true", "--permission-mode acceptEdits + sandbox (allowedDomains *)"],
    "agent-config": [t("(no flags — your config.toml / profile)"), t("(no flags — your settings.json permissions)")],
  };
  return `
  ${sec(T("Agents"), { note: T("The default agent writes Loops from your description and runs new Loops"), act: viewer() ? "" : btn("Detect again", "detect", { cls: "sm" }) })}
  <div class="grp">${agentRows(true)}<div class="row"><span class="idot" data-c="#76767f"></span><div class="grow"><div class="nm">${T("Other agents")}</div><div class="sub">${T("Gemini, OpenCode and others have no adapter yet.")}</div></div><span class="chip mut">${T("No adapter")}</span></div></div>
  ${sec(T("GitHub"), { note: T("For Loops that work from issues or open pull requests") })}
  <div class="grp"><div class="row"><span class="idot" data-c="#24292f"></span><div class="grow"><div class="nm">GitHub CLI (gh)</div><div class="sub">${S().github?.version ? esc(S().github.version) : T("Install it from cli.github.com, run gh auth login, then detect again.")}</div></div><span class="chip ${S().github?.signedIn ? "ok" : S().github?.version ? "warn" : "mut"}">${T(S().github?.signedIn ? "signed in" : S().github?.version ? "not signed in" : "not found")}</span></div></div>
  <div class="bn mt10">${icon("ok")}<span>${T("Loopbee reads your login shell's PATH at start-up, so agents installed through nvm or Homebrew are found even when the app starts from the Dock or Start menu.")}</span></div>
  ${sec(T("Permission mapping"), { note: T("What each level passes to the agent CLI") })}
  <div class="grp perm">${SANDBOXES.map((x) => `<div class="fld"><div class="lb">${esc(sandboxLabel(x))}${sandboxRank(x) > sandboxRank(c.maxSandbox) ? ` <span class="chip mut">${T("above device limit")}</span>` : ""}</div><div class="ctl"><div class="sub">${esc(sandboxHelp(x))}</div><div class="mono sub">Codex: ${esc(flags[x][0])}</div><div class="mono sub">Claude Code: ${esc(flags[x][1])}</div></div></div>`).join("")}${fld(T("Device limit"), `<span class="nm">${esc(sandboxLabel(c.maxSandbox))}</span>`, `<a href="#/settings/general">${T("Change in General")}</a>`)}</div>
  ${sec(T("Create Loops from your agent"), { note: "beectl" })}
  <div class="grp">${fld(T("Install the skill"), `<span class="mono">beectl skill install</span>`, T("Then ask Codex or Claude Code to “make this a nightly Loopbee loop”. It creates a disabled draft you review here."))}${fld(T("Commands"), `<div class="mono sub">beectl status · beectl loops · beectl runs --loop NAME · beectl create -f loop.json · beectl run NAME · beectl schema</div>`)}</div>`;
}
function notifications(c, ui) {
  const kinds = [["desktop", t("Desktop"), t("System notification")], ["feishu", t("Feishu"), t("Feishu webhook")], ["telegram", "TG", "Telegram Bot"], ["bark", "Bark", t("Bark (iOS push)")]];
  const deliveries = S().deliveries.slice().sort((a, b) => String(b.updated).localeCompare(String(a.updated)));
  const card = ([kind, tag, title]) => {
    const ch = c.channels.find((x) => x.kind === kind) || { kind, enabled: false, endpoint: kind === "bark" ? "https://api.day.app" : "", secret: "", target: "" };
    const last = deliveries.find((d) => d.channel === kind);
    const open = ui.channel === kind;
    const desc = { desktop: t("Gates, failures and interruptions pop up on this device"), feishu: t("Webhook URL is stored in the system keychain"), telegram: t("Bot token in the keychain; messages go to one chat"), bark: t("Device key in the keychain; server configurable") }[kind];
    return `<div class="row chrow"><span class="tag">${esc(tag)}</span><div class="grow min0"><div class="nm">${esc(title)}</div><div class="sub ell">${esc(desc)}</div></div>${last ? deliveryBadge(last) : ""}${kind !== "desktop" && !viewer() ? btn(open ? "Close" : "Edit", "channel-edit", { cls: "sm", attrs: `data-kind="${kind}"` }) : ""}${viewer() ? "" : btn("Send test", "test-notify", { cls: "sm", attrs: `data-kind="${kind}" ${ch.enabled ? "" : `disabled title="${T("Enable and save the channel first")}"`}` })}${toggle(ch.enabled, "channel-toggle", title, `data-kind="${kind}" ${ro()}`)}</div>${open ? `<form class="chform" id="channel-form" data-kind="${kind}">${kind !== "telegram" ? fld(kind === "feishu" ? T("Webhook URL") : T("Server"), `<input class="inp" name="endpoint" value="${esc(ch.endpoint)}" placeholder="${kind === "feishu" ? "https://open.feishu.cn/open-apis/bot/v2/hook/…" : "https://api.day.app"}">`, ch.endpoint === "********" ? T("Saved · leave as is to keep it") : "") : ""}${kind !== "feishu" ? fld(kind === "telegram" ? "Bot Token" : T("Device key"), `<input class="inp" type="password" name="secret" value="${esc(ch.secret)}" autocomplete="off">`, T("Stored in the system keychain; never shown again or written to logs.")) : ""}${kind === "telegram" ? fld("Chat ID", `<input class="inp" name="target" value="${esc(ch.target)}">`) : ""}<div class="fld fact"><div class="sub">${last ? T("Last delivery: {when}", { when: when(last.updated) }) : T("No deliveries yet")}</div><div class="actions">${btn("Cancel", "channel-edit", { attrs: `data-kind="${kind}"` })}<button class="btn pri">${T("Save")}</button></div></div></form>` : ""}`;
  };
  return `
  ${sec(T("Notification channels"), { note: T("Each channel is configured, delivered and retried on its own") })}
  <div class="grp">${kinds.map(card).join("")}</div>
  ${sec(T("Delivery rules"), { note: T("Apply to every channel") })}
  <div class="grp">${fld(T("Daily digest"), `<span class="nm">${esc(cronText(c.digestCron))} · ${esc(c.digestTimezone)}</span>`, `<a href="#/settings/general">${T("Change in General")}</a>`)}${fld(T("Retries"), `<span class="nm">${T("Explicit rate limits are retried up to three times")}</span>`)}${fld(T("Unknown results"), `<span class="nm">${T("Timeouts are marked “result unknown” and are never resent automatically")}</span>`, T("Check the destination first; resending only repeats the notification, never the Loop."))}</div>
  ${sec(T("Recent deliveries"))}
  <div class="grp">${deliveries.length ? deliveries.slice(0, 20).map((d) => `<div class="row">${icon(d.status === "sent" ? "ok" : d.status === "failed" ? "err" : d.status === "unknown" ? "lost" : "idle")}<div class="grow min0"><div class="nm ell">${esc(channelName(d.channel))} · ${esc(when(d.updated))}</div><div class="sub ell">${esc(d.error || d.text)}</div></div>${!viewer() && ["failed", "unknown"].includes(d.status) ? `${d.status === "unknown" ? btn("Check", "check-delivery", { cls: "sm", attrs: `data-id="${d.id}"` }) : ""}${btn("Resend", "retry", { cls: "sm", attrs: `data-id="${d.id}"` })}` : ""}</div>`).join("") : empty(T("No deliveries yet"))}</div>`;
}
function team(c) {
  return `
  ${sec(T("Team sync"), { note: T("Share one board through a Git repository: executors publish, viewers read") })}
  <form class="grp" id="team-form">${fld(T("Role on this device"), seg("role", c.role, [["executor", t("Executor")], ["viewer", t("Viewer (read only)")]], { action: "team-role", label: t("Role") }), T("Viewers cannot run, approve, cancel or edit Loops; the core enforces this, not just the UI."))}<input type="hidden" name="role" value="${esc(c.role)}">${fld(T("Repository"), `<input class="inp mono" name="teamRemote" value="${esc(c.teamRemote)}" placeholder="git@github.com:team/all-docs.git">`, T("Uses your local Git credentials."))}${fld(T("Branch"), `<input class="inp mono" name="teamBranch" value="${esc(c.teamBranch)}">`, T("Loopbee uses its own checkout and never changes your project branches."))}${fld(T("Publisher ID"), `<span class="mono">${esc(c.publisher)}</span>`)}<div class="fld fact"><div class="sub">${T("Public metadata only. Private loops and credentials are excluded.")}</div><div class="actions">${btn(c.role === "viewer" ? "Pull now" : "Publish now", "sync", { attrs: c.teamRemote ? "" : "disabled" })}<button class="btn pri">${T("Save")}</button></div></div></form>
  ${sec(T("Before publishing"))}
  <div class="grp">${fld(T("Sensitive content scan"), `<span class="chip ok">${icon("ok")}${T("Always on")}</span>`, T("Common secrets and e-mail addresses block publishing. This is pattern matching, not complete DLP — only make Loops public if their content can be shared."))}${fld(T("Private Loops"), `<span class="nm">${T("Never published")}</span>`)}${fld(T("What is shared"), `<span class="nm">${T("Public definitions, runs, the latest 1,000 events and text evidence")}</span>`)}</div>`;
}
function data(c) {
  const info = S().info || {};
  return `
  ${sec(T("Data location"), { note: T("Loopbee’s own authoritative data") })}
  <div class="grp">${fld(T("Data directory"), `<span class="mono">${esc(info.dataDir || "—")}</span>`, T("Database, evidence files and launch link. Agents should not be given write access here."))}${fld(T("Full backup"), `<span class="nm">${T("Quit Loopbee and copy the whole data directory")}</span>`, T("Keychain entries for notification secrets are stored separately."))}</div>
  ${sec(T("Retention"))}
  <form class="grp" id="retention-form">${fld(T("Keep finished runs"), `<div class="pair"><input class="inp num" type="number" min="1" max="3650" name="retentionDays" value="${esc(c.retentionDays)}" ${ro()}><span class="sub t2">${T("days")}</span><button class="btn" ${ro()}>${T("Save")}</button></div>`, T("Older finished runs and their evidence can be pruned. Running and waiting runs are always kept."))}${fld(T("Prune now"), btn("Prune expired", "prune", { attrs: ro() }), T("Permanently removes expired runs, their events and files."))}</form>
  ${sec(T("Archive"), { note: T("A ZIP you can re-import on another computer") })}
  <div class="grp">${fld(T("Export"), `<a class="btn" href="/api/export" download>${T("Export archive")}</a>`, T("Public Loops and finished public runs with text evidence; never credentials. Blocked if secrets are detected."))}${fld(T("Import"), btn("Import archive", "import-archive", { attrs: ro() }), T("Imported Loops get new IDs and arrive private and disabled; nothing is overwritten."))}${fld(T("Definitions"), btn("Import JSON / YAML", "import", { attrs: ro() }), T("Opens the editor with the parsed definition."))}${fld(T("Claude scheduled tasks"), btn("Show Claude tasks", "claude-import"), T("Read-only view of ~/.claude/scheduled-tasks; nothing is converted automatically."))}</div>`;
}
function about() {
  const s = S();
  const info = s.info || {};
  return `
  ${sec(T("About Loopbee"))}
  <div class="grp">${fld(T("Version"), `<span class="mono">${esc(info.version || "—")}</span>`)}${fld(T("Platform"), `<span class="mono">${esc(`${info.os || "?"}/${info.arch || "?"}`)}</span>`)}${fld(T("Data directory"), `<span class="mono">${esc(info.dataDir || "—")}</span>`)}${fld(T("Local address"), `<span class="mono">${esc(location.host)}</span>`, T("Loopback only; the launch token becomes an HttpOnly session cookie."))}${fld(T("License"), `<span class="nm">${T("License pending")}</span>`)}<div class="fld fact"><div class="sub">${T("Diagnostics include versions, counts and paths — no Loop content or secrets.")}</div><div class="actions">${btn("Copy diagnostics", "copy-diagnostics")}</div></div></div>
  ${sec(T("Recent activity"), { note: T("Scheduler and system events") })}
  <div class="grp">${(s.activity || []).length ? s.activity.slice(0, 200).map((e) => `<div class="row"><span class="mono when">${esc(when(e.time))}</span><div class="grow min0"><div class="nm mono ell">${esc(e.type)}</div><div class="sub ell">${esc(e.text)}</div></div></div>`).join("") : empty(T("No activity yet"))}</div>`;
}

// ---------- Onboarding ----------
export const ONBOARD_STEPS = ["Language", "Agents", "Data location", "Notifications", "Team sync", "First Loop"];
export function onboardingPage(step) {
  const s = S();
  const c = s.settings;
  const steps = ONBOARD_STEPS;
  const body = [
    () => `${sec(T("Language"), { note: T("Step {n} / {m}", { n: 1, m: 6 }) })}<div class="grp">${fld(T("Interface language"), seg("language", c.language, langs, { action: "set", label: t("Interface language") }))}${fld(T("Digest and notification language"), seg("digestLanguage", c.digestLanguage, langs, { action: "set", label: t("Digest and notification language") }))}${fld(T("Report language (sent to agents)"), seg("reportLanguage", c.reportLanguage, langs, { action: "set", label: t("Report language") }))}</div>`,
    () => `${sec(T("Connect agents"), { note: T("Step {n} / {m} · Loopbee schedules; agents only execute", { n: 2, m: 6 }) })}<div class="grp">${agentRows(true)}</div><div class="actions mt10">${btn("Detect again", "detect", { cls: "sm" })}</div><div class="bn mt10">${icon("ok")}<span>${T("The default agent turns your description into a Loop and runs it. Without an agent you can still build Loops in the form editor.")}</span></div>`,
    () => `${sec(T("Data location"), { note: T("Step {n} / {m}", { n: 3, m: 6 }) })}<div class="grp">${fld(T("Data directory"), `<span class="mono">${esc(s.info?.dataDir || "—")}</span>`, T("Use -data <absolute path> when starting Loopbee to choose another directory."))}${fld(T("Retention"), `<span class="nm">${T("{n} days for finished runs", { n: c.retentionDays })}</span>`)}</div>`,
    () => `${sec(T("Notifications"), { note: T("Step {n} / {m} · optional", { n: 4, m: 6 }) })}<div class="grp">${fld(T("Desktop notifications"), toggle(c.channels.some((x) => x.kind === "desktop" && x.enabled), "channel-toggle", t("Desktop notifications"), 'data-kind="desktop"'), T("Gates, failures and interruptions."))}${fld(T("Feishu, Telegram, Bark"), `<a class="btn sm" href="#/settings/notifications">${T("Set up later in Settings")}</a>`)}</div>`,
    () => `${sec(T("Team sync"), { note: T("Step {n} / {m} · optional", { n: 5, m: 6 }) })}<div class="grp">${fld(T("Role on this device"), `<span class="nm">${T(c.role === "viewer" ? "Viewer (read only)" : "Executor")}</span>`, T("Change it in Settings › Team sync after connecting a repository."))}</div>`,
    () => `${sec(T("First Loop"), { note: T("Step {n} / {m}", { n: 6, m: 6 }) })}<div class="tpls">${["Read-only code review", "Fix until tests pass", "Metric target"].map((n, i) => `<a class="card sm tpl" href="#/new?template=${i}" data-action="finish-onboarding" data-href="#/new?template=${i}"><div class="nm">${T(n)}</div><span class="chip mut">${T("Template · unverified content")}</span></a>`).join("")}<a class="card sm tpl" href="#/new" data-action="finish-onboarding" data-href="#/new"><div class="nm">${T("Describe it to your agent")}</div><span class="chip ok">${T("Recommended")}</span></a><a class="card sm tpl" href="#/new?form=1" data-action="finish-onboarding" data-href="#/new?form=1"><div class="nm">${T("Blank Loop")}</div><span class="chip mut">${T("Start from scratch")}</span></a></div>`,
  ][step]();
  return `
  <div class="ob-head"><h1>${T("Welcome to Loopbee")}</h1><p class="sub t2">${T("Six short steps. Every step can be skipped and changed later in Settings.")}</p></div>
  <div class="sw"><nav class="smenu ob" aria-label="${T("Setup steps")}">${steps.map((n, i) => `<a href="#/onboarding/${i}" class="${i === step ? "on" : ""}" ${i === step ? 'aria-current="step"' : ""}>${i < step ? icon("ok") : `<span class="obn">${i + 1}</span>`}${T(n)}</a>`).join("")}</nav>
  <div class="spane">${body}<div class="ob-foot">${step > 0 ? `<a class="btn" href="#/onboarding/${step - 1}">${T("Back")}</a>` : "<span></span>"}<span class="grow"></span>${step < 5 ? `<a class="btn ghost" href="#/onboarding/${step + 1}">${T("Skip for now")}</a><a class="btn pri" href="#/onboarding/${step + 1}">${T("Next")}</a>` : `<button class="btn pri" data-action="finish-onboarding" data-href="#/today">${T("Finish")}</button>`}</div></div></div>`;
}
