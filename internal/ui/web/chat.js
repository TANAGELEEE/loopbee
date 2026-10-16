// "Describe your Loop": the bound local agent CLI drafts the definition; loopbee previews and checks it.
import { ctx, t, T, esc, icon, btn, sec, empty, seg, cronText, sandboxLabel, agentLabel, hm, errText } from "./ui.js";
import { flowState, renderFlow } from "./flow.js";
import { gapsPanel, gaps, kindLabel } from "./forms.js";
import { workSummary } from "./views.js";

const S = () => ctx.state;
export const EXAMPLES = [
  "Every weekday at 9:00, use our fix-prod-errors skill to check production errors, fix them on a branch from origin/master, merge to test and deploy there. Stop before master and notify me with the PR link.",
  "Every night at 2:00, merge master into test02 and run the SQL migrations that have not been applied to the test02 database yet.",
  "Every day at 18:00, analyse today's new sign-ups and draft follow-up e-mails. I approve them before anything is sent.",
  "Every Monday at 10:00, run the React Doctor repair loop on the web app until it scores 100/100 or stops improving.",
  "Every day at 18:30, write a daily report of merged PRs and incidents and push the full report to Feishu.",
];

export function newChat(key, loop) {
  const available = S().agents.filter((a) => a.status === "available").map((a) => a.name);
  const agent = loop?.agent && available.includes(loop.agent) ? loop.agent : available.includes(S().settings.defaultAgent) ? S().settings.defaultAgent : available[0] || "";
  return {
    key,
    loop: loop || null,
    messages: loop?.conversation ? structuredClone(loop.conversation) : [],
    draft: loop ? structuredClone(loop) : null,
    questions: [],
    problem: "",
    busy: false,
    started: 0,
    agent,
    directory: loop?.directory || "",
  };
}

export function chatPage(c) {
  const s = S();
  const available = s.agents.filter((a) => a.status === "available");
  const editing = !!c.loop;
  const msgs = c.messages.map((m) => `<div class="msg ${m.role === "user" ? "me" : "bot"} ${m.error ? "err" : ""}"><div class="who">${m.role === "user" ? T("You") : esc(agentLabel(m.agent || c.agent))}<span class="sub"> · ${esc(hm(m.at))}</span></div><div class="txt">${esc(m.text)}</div></div>`).join("");
  const busy = c.busy ? `<div class="msg bot busy"><div class="who">${esc(agentLabel(c.agent))}</div><div class="txt"><span class="sk"></span><span class="sub" id="busy-t">${T("Reading your project and drafting… {n} s", { n: Math.round((Date.now() - c.started) / 1000) })}</span> <button type="button" class="linkbtn" data-action="chat-cancel">${T("Cancel")}</button></div></div>` : "";
  const intro = !c.messages.length
    ? `<div class="chat-intro"><div class="nm">${T(editing ? "Tell your agent what to change" : "Describe the Loop in your own words")}</div><div class="sub">${T("Your agent reads the project (read-only) to find your skills, scripts and conventions, then drafts the Loop. Nothing runs until you save and start it.")}</div>${editing ? "" : `<div class="examples">${EXAMPLES.map((e, i) => `<button type="button" class="ex" data-action="chat-example" data-i="${i}">${T(e)}</button>`).join("")}</div>`}</div>`
    : "";
  const quick = c.questions.length && !c.busy ? `<div class="quick">${c.questions.map((q, qi) => `<div class="q"><div class="sub t2">${esc(q.text)}</div><div class="chips">${(q.options || []).map((o) => `<button type="button" class="chip tgl" data-action="chat-quick" data-q="${qi}" data-text="${esc(o)}">${esc(o)}</button>`).join("")}</div></div>`).join("")}</div>` : "";
  const cfg = `<div class="chat-cfg">${available.length > 1 ? seg("agent", c.agent, available.map((a) => [a.name, agentLabel(a.name)]), { action: "chat-agent", label: t("Agent") }) : `<span class="chip">${esc(agentLabel(c.agent))}</span>`}<input class="inp mono" id="chat-dir" value="${esc(c.directory)}" placeholder="${T("Project folder (absolute path)")}" aria-label="${T("Project folder")}" ${editing ? "" : ""}></div>`;
  const noAgent = !available.length
    ? `<div class="card sm">${empty(T("No agent CLI detected"), T("Install Codex CLI or Claude Code, then detect again. You can still build the Loop with the form."), `${btn("Detect again", "detect", { cls: "sm" })}<a class="btn sm" href="#/new?form=1">${T("Use the form")}</a>`)}</div>`
    : "";
  return `
  <div class="sec top-sec crumbs"><h2><a href="#/loops">Loops</a> › ${editing ? `<a href="#/loop/${c.loop.id}">${esc(c.loop.name)}</a> › ${T("Change by chat")}` : T("New Loop")}</h2><span class="note">${T("Your default agent drafts the Loop; loopbee checks it before anything runs")}</span><div class="act">${c.draft ? "" : `<a href="${editing ? `#/loop/${c.loop.id}/edit` : "#/new?form=1"}">${T("Use the form instead")} ›</a>`}</div></div>
  ${noAgent || `<div class="chat">
    <div class="chat-main card">
      ${cfg}
      <div class="msgs" id="msgs" aria-live="polite">${intro}${msgs}${busy}</div>
      ${quick}
      <form id="chat-form" class="chat-input"><textarea class="inp" id="chat-input" rows="3" placeholder="${T("e.g. Every weekday at 9:00 use our fix-prod-errors skill…")}" ${c.busy ? "disabled" : ""}></textarea><button class="btn pri" ${c.busy ? "disabled" : ""}>${T("Send")}</button></form>
      <div class="sub">${T("⌘/Ctrl + Enter to send · each message runs your agent CLI once and uses your own subscription")}</div>
    </div>
    <aside class="chat-side">${preview(c)}</aside>
  </div>`}`;
}

function preview(c) {
  const d = c.draft;
  if (!d) return `${sec(T("Preview"))}<div class="card sm">${empty(T("No draft yet"), T("The flow chart, schedule and checks appear after your agent's first answer."))}</div>`;
  const blocking = gaps(d).filter((g) => g.level === "block").length;
  const facts = [
    [t("Schedule"), d.cron ? `${cronText(d.cron)} · ${d.timezone}` : t("Manual only")],
    [t("Agent"), agentLabel(d.agent)],
    [t("Permission"), sandboxLabel(d.sandbox)],
    [t("Workspace"), d.workspace === "worktree" ? t("Fresh git worktree from {ref}", { ref: d.baseRef || "HEAD" }) + (d.branch ? " → " + d.branch : "") : d.directory || "—"],
    [t("Limits"), t("rounds {r} · {m} min · gate {h} h", { r: d.maxRounds, m: d.wallMinutes, h: d.gateHours || 72 })],
  ];
  if (d.workspace !== "worktree" && d.requireClean) facts.push([t("Before running"), t("The folder must be a Git repository without uncommitted changes")]);
  if (d.intake || d.pullRequest) facts.push([t("Work & delivery"), workSummary(d)]);
  if (d.branch) facts.push([t("Commits"), t("loopbee commits the agent's changes on {branch}; nothing is pushed or merged by loopbee", { branch: d.branch })]);
  if (d.env?.length) facts.push([t("Environment"), d.env.map((e) => e.name + (e.secret ? "*" : "") + (e.value?.startsWith("env:") ? " ← " + t("your shell") : "")).join(", ")]);
  if (d.readPaths?.length) facts.push([t("Read paths"), d.readPaths.join(", ")]);
  if (d.agentMcp) facts.push([t("MCP tools"), t("The agent's own MCP tools, approved in advance; they run outside the sandbox with your rights")]);
  if (d.copyFiles?.length) facts.push([t("Copy into the worktree"), d.copyFiles.join(", ")]);
  return `${sec(T("Preview"), { note: T(c.loop ? "Changes since v{n}" : "Draft · not saved", { n: c.loop?.version }) })}
  <div class="card sm"><div class="hd"><h1 class="h2 ell">${esc(d.name || t("Untitled Loop"))}</h1></div>${d.description ? `<div class="sub t2 mt3">${esc(d.description)}</div>` : ""}
  ${renderFlow(d.steps, flowState(d.steps, null), { maxRounds: d.maxRounds })}
  <div class="steps-brief">${d.steps.map((s, i) => `<div class="row top"><span class="tag">${esc(kindLabel(s.kind))}</span><div class="grow min0"><div class="nm">${i + 1}. ${esc(s.name)}</div><div class="sub pre-wrap">${esc(s.kind === "run" || s.kind === "verify" ? (s.argv || []).join(" ") + (s.continueOnError ? " · " + t("continues on failure") : "") : s.kind === "gate" ? t(i === d.steps.length - 1 ? "A person takes over from here" : "Waits for your approval") : s.prompt)}</div></div></div>`).join("")}</div></div>
  <div class="grp mt10">${facts.map(([k, v]) => `<div class="row"><div class="sub dl">${esc(k)}</div><div class="grow nm wrap">${esc(v)}</div></div>`).join("")}</div>
  ${c.problem ? `<div class="bn err mt10">${icon("err")}<span>${c.problem === "permission exceeds this device's limit" ? `${T("This Loop needs “{need}”, but this device allows at most “{max}”.", { need: sandboxLabel(d.sandbox), max: sandboxLabel(S().settings.maxSandbox) })} <a href="#/settings">${T("Raise the limit in Settings")}</a>` : esc(errText(c.problem))}</span></div>` : ""}
  <div id="gaps">${gapsPanel(d)}</div>
  <div class="card sm mt10"><button type="button" class="btn pri wide" data-action="chat-save" ${blocking || c.busy ? "disabled" : ""}>${T(c.loop ? "Save as v{n}" : "Save as draft", { n: (c.loop?.version || 0) + 1 })}</button><div class="sub mt6">${T(c.loop ? "Saving creates a new version; the schedule setting is kept." : "Saved with the schedule off. Run it once, then enable the schedule.")}</div><button type="button" class="btn ghost wide mt6" data-action="chat-form">${T("Adjust in the form")}</button></div>`;
}
