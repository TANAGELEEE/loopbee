// Page renderers. Each returns an HTML string for <main>; app.js owns routing and actions.
import {
  ctx, t, T, esc, hm, hms, when, dayKey, dayLabel, isToday, duration, bytes, cronText,
  dot, short, lifecycle, outcome, badge, chip, outcomeLabel, active, icon, deliveryBadge, channelName,
  btn, sec, empty, toggle, seg, fld, icons, errText, sandboxLabel, agentLabel, agentColor, resultChip, artifactKind, artifactTitle, allArtifacts, toDate, loopColor,
  tokens, usageTotal, usageLine, usageDetail, sumUsage, money,
} from "./ui.js";
import { flowState, renderFlow, renderTopology } from "./flow.js";

const S = () => ctx.state;
export const viewer = () => S().settings.role === "viewer";
export const runsSorted = () => [...S().runs].sort((a, b) => String(b.started).localeCompare(String(a.started)));
export const loopById = (id) => S().loops.find((l) => l.id === id);
const runsOf = (loopId) => runsSorted().filter((r) => r.loopId === loopId);
// Projects group Loops and Runs on the boards. A Run started before projects existed takes its Loop's project; ""
// is "No project". The filter value "-" picks that group.
export const projectOf = (x) => (x.definition ? x.definition.project || loopById(x.loopId)?.project : x.project) || "";
export const projectsInUse = () => [...new Set([...S().loops, ...S().runs].map(projectOf))].sort((a, b) => (a === "") - (b === "") || a.localeCompare(b));
const inProject = (q) => (x) => !q.project || projectOf(x) === (q.project === "-" ? "" : q.project);
const projectLabel = (p) => p || t("No project");
function projectPicker(q, id) {
  const list = projectsInUse();
  if (list.length < 2 && !q.project) return "";
  return `<select class="btn sm" id="${id}" data-q="project" aria-label="${T("Project")}"><option value="">${T("Project: all")}</option>${list.map((p) => `<option value="${esc(p || "-")}" ${q.project === (p || "-") ? "selected" : ""}>${esc(projectLabel(p))}</option>`).join("")}</select>`;
}
// Cards on an unfiltered board with several projects say which project they belong to.
let cardProject = false;
const projectChip = (x) => (cardProject ? `<span class="chip mut ell">${esc(projectLabel(projectOf(x)))}</span>` : "");
const sourceLabel = (r) =>
  ({ schedule: t("Scheduled"), catchup: t("Catch-up"), manual: t("Manual"), rerun: t("Rerun of {id}", { id: short(r.rerunOf) }) })[r.source] || "";
const host = () => location.host;
// While a Run delivers (pushes its branch, opens the pull request) no step is running; say what it does instead.
const stepName = (r) =>
  r.phase === "delivering" ? t("Pushing the branch and opening the pull request") : r.phase === "merging" ? t("Waiting for the pull request's checks, then merging") : r.definition.steps[r.step]?.name || "";
const metricsText = (m) =>
  m && Object.keys(m).length ? Object.entries(m).map(([k, v]) => `${k} ${v}`).join(" · ") : "";

// ---------- shared rows ----------
function runFacts(r) {
  const max = r.definition.maxRounds;
  const life = lifecycle(r);
  const round = t("Round {r} / {max}", { r: r.round || 1, max });
  if (life === "running" || life === "queued") return [`${round} · ${stepName(r)}`, sourceLabel(r)];
  if (life === "waiting") return [`${round} · ${stepName(r)}`, t("Waiting for your decision")];
  if (life === "interrupted") return [t("Process exited · last update {time}", { time: hm(r.finished) }), t("Stopped at round {r} · {step}", { r: r.round || 1, step: stepName(r) })];
  const m = metricsText(r.metrics);
  const first = m ? `${m} · ${round}` : round;
  const second = r.result?.summary ? r.result.summary : r.error ? errText(r.error) : t("{n} artifact|{n} artifacts", { n: r.artifacts?.length || 0 });
  return [first, second];
}
function runTimes(r) {
  const life = lifecycle(r);
  const start = toDate(r.started);
  const end = toDate(r.finished);
  const top = t("from {time}", { time: hm(r.started) });
  if (life === "running" || life === "queued") return [top, duration(ctx.now - start)];
  if (life === "waiting") return [top, t("{d} + waiting", { d: duration(ctx.now - start) })];
  if (life === "interrupted") return [top, t("lost {d} ago", { d: duration(ctx.now - (end || start)) })];
  return [top, duration((end || start) - start)];
}
export function runRow(r, { dateTitle = false } = {}) {
  const [f1, f2] = runFacts(r);
  const [t1, t2] = runTimes(r);
  const sub = [short(r.id), sourceLabel(r)].filter(Boolean).join(" · ");
  const title = dateTitle ? `${String(toDate(r.started).getMonth() + 1).padStart(2, "0")}-${String(toDate(r.started).getDate()).padStart(2, "0")} ${hm(r.started)}` : r.definition.name;
  return `<a class="row r link" href="#/run/${r.id}"><div class="c-name">${dateTitle ? "" : dot(r.loopId)}<div class="min0"><div class="nm ell">${esc(title)}</div><div class="sub mono ell">${esc(dateTitle ? `${short(r.id)} · v${r.definition.version}` : sub)}</div></div></div>${badge(r)}${chip(r)}<div class="min0"><div class="sub t2 ell">${esc(f1)}</div><div class="sub ell">${esc(f2)}</div></div><div class="c-time"><div class="sub t2">${esc(t1)}</div><div class="sub">${esc(t2)}${r.usage ? ` · <span class="tok" title="${esc(usageLine(r.usage))}">${esc(tokens(usageTotal(r.usage)))}</span>` : ""}</div></div></a>`;
}
function artifactRow(a, { selected = false, showLoop = true } = {}) {
  return `<a class="row link ${selected ? "selrow" : ""}" href="#/artifact/${a.r.id}/${encodeURIComponent(a.name)}"><span class="tag">${artifactKind(a.name)}</span><div class="grow min0"><div class="nm ell">${esc(artifactTitle(a.r, a.name))}</div><div class="sub ell">${esc([showLoop ? a.r.definition.name : "", hm(a.at), a.size != null ? bytes(a.size) : ""].filter(Boolean).join(" · "))}</div></div>${a.r.definition.private ? `<span class="chip mut">${T("Private")}</span>` : ""}</a>`;
}
function footer(extra = []) {
  const s = S();
  const items = [
    t("Data {dir}", { dir: s.info?.dataDir || "—" }),
    viewer() ? t("Viewer (read only)") : t("Executor"),
    ...extra,
  ];
  return `<div class="src">${items.map((x) => `<span>${esc(x)}</span>`).join("")}</div>`;
}
const crumbs = (items) =>
  `<div class="sec crumbs"><h2>${items.map(([label, href]) => (href ? `<a href="${href}">${esc(label)}</a>` : esc(label))).join(" › ")}</h2></div>`;

// ---------- attention (spec §4.1) ----------
export function attentionItems() {
  const s = S();
  const items = [];
  for (const r of runsSorted()) {
    if (r.status === "waiting") items.push({ kind: "gate", since: r.trace?.at(-1)?.started || r.started, r });
    if (r.status === "interrupted" && !r.acked) items.push({ kind: "interrupted", since: r.finished || r.started, r });
  }
  for (const d of s.deliveries) if (d.status === "unknown" || d.status === "failed") items.push({ kind: "delivery", since: d.updated, d });
  const usedAgents = new Set(s.loops.filter((l) => l.enabled).map((l) => l.agent));
  for (const name of usedAgents) {
    const ag = s.agents.find((a) => a.name === name);
    if (s.agents.length && (!ag || ag.status !== "available")) items.push({ kind: "agent", since: "", name });
  }
  const order = { gate: 0, interrupted: 1, delivery: 2, agent: 3 };
  return items.sort((a, b) => order[a.kind] - order[b.kind] || String(a.since).localeCompare(String(b.since)));
}
// ---------- Today (board) ----------
// Four columns, left to right as work moves: coming up → running → needs you → done today. Failed or blocked runs
// of today wait in "Needs you" until someone looks; everything else that ended today is "Done today".
const NEEDS_LOOK = ["failed", "blocked", "timed_out", "budget_exhausted", "stalled"];
function boardCol(id, title, cards, emptyTitle, emptySub) {
  return `<section class="bcol" data-col="${id}" ${id === "needs" ? 'id="attention"' : ""} aria-label="${esc(title)}"><div class="sec"><h2>${esc(title)}</h2><span class="cnt">${cards.length}</span></div><div class="grp">${cards.length ? cards.join("") : empty(emptyTitle, emptySub)}</div></section>`;
}
function runCard(r, extra = "") {
  const [f1, f2] = runFacts(r);
  const [t1, t2] = runTimes(r);
  return `<div class="row bcard"><span class="bc-dot">${dot(r.loopId)}</span><div class="grow min0"><a class="nm ell" href="#/run/${r.id}">${esc(r.definition.name)}</a><div class="sub ell">${esc(f1)}</div><div class="sub ell">${esc(f2)}</div><div class="bmeta">${badge(r)}${chip(r)}${projectChip(r)}<span class="sub">${esc(t1)} · ${esc(t2)}</span>${r.usage ? `<span class="sub tok" title="${esc(usageLine(r.usage))}">${esc(tokens(usageTotal(r.usage)))}${r.usage.costUsd ? " · " + esc(money(r.usage.costUsd)) : ""}</span>` : ""}</div>${extra}</div></div>`;
}
function attentionCard(it) {
  const ro = viewer();
  if (it.kind === "gate") return runCard(it.r, ro ? "" : `<div class="bact"><a class="btn sm pri" href="#/gate/${it.r.id}">${T("Review")}</a><span class="sub">${T("since {time} · waited {d}", { time: hm(it.since), d: duration(ctx.now - toDate(it.since)) })}</span></div>`);
  if (it.kind === "interrupted") return runCard(it.r, ro ? "" : `<div class="bact">${btn("Mark reviewed", "ack", { cls: "sm", attrs: `data-id="${it.r.id}"` })}</div>`);
  if (it.kind === "run") return runCard(it.r, `<div class="bact"><a class="btn sm" href="#/run/${it.r.id}">${T("Open")}</a></div>`);
  if (it.kind === "delivery") {
    const d = it.d;
    const unknown = d.status === "unknown";
    return `<div class="row bcard"><span class="idot" data-c="#76767f"></span><div class="grow min0"><div class="nm ell">${T(unknown ? "{channel} delivery result unknown" : "{channel} delivery failed", { channel: channelName(d.channel) })}</div><div class="sub ell">${esc(errText(d.error))}</div><div class="bmeta">${deliveryBadge(d)}<span class="sub">${esc(when(d.updated))}</span></div>${ro ? "" : `<div class="bact">${unknown ? btn("Check", "check-delivery", { cls: "sm", attrs: `data-id="${d.id}"` }) : ""}${btn("Resend", "retry", { cls: "sm", attrs: `data-id="${d.id}"` })}</div>`}</div></div>`;
  }
  return `<div class="row bcard"><span class="idot" data-c="#d64545"></span><div class="grow min0"><div class="nm">${T("Agent {name} is not available", { name: agentLabel(it.name) })}</div><div class="sub">${T("Enabled Loops using it will be blocked until detection passes.")}</div>${ro ? "" : `<div class="bact"><a class="btn sm" href="#/settings/agents">${T("Agent settings")}</a></div>`}</div></div>`;
}
// Today's tokens and reported cost by agent and model.
function todayUsage(todays) {
  const by = new Map();
  for (const r of todays) for (const x of r.trace || []) if (x.usage) {
    const key = `${x.usage.agent}\u0000${x.usage.model || ""}`;
    const cur = by.get(key) || { agent: x.usage.agent, model: x.usage.model, runs: new Set(), n: 0, cost: 0 };
    cur.runs.add(r.id);
    cur.n += usageTotal(x.usage);
    cur.cost += x.usage.costUsd || 0;
    by.set(key, cur);
  }
  const rows = [...by.values()].sort((a, b) => b.n - a.n);
  if (!rows.length) return `<div class="grp" id="today-usage">${empty(T("No usage today"), T("Agent steps record the model and tokens their CLI reports."))}</div>`;
  return `<div class="grp" id="today-usage">${rows.map((u) => `<div class="row"><span class="idot" data-c="${agentColor(u.agent)}"></span><div class="grow min0"><div class="nm ell">${esc(agentLabel(u.agent))}</div><div class="sub mono ell">${esc(u.model || t("model not reported"))}</div></div><div class="right"><div class="nm mono">${esc(tokens(u.n))}</div><div class="sub">${esc([t("{n} runs", { n: u.runs.size }), money(u.cost)].filter(Boolean).join(" · "))}</div></div></div>`).join("")}</div>`;
}
export function today(q = {}) {
  const s = S();
  const mine = inProject(q);
  cardProject = !q.project && projectsInUse().length > 1;
  const runs = runsSorted().filter(mine);
  const todays = runs.filter((r) => isToday(r.started));
  const running = runs.filter((r) => r.status === "running" || r.status === "queued");
  // Deliveries and agents belong to no project: they show only on the unfiltered board.
  const att = attentionItems().filter((x) => (x.r ? mine(x.r) : !q.project));
  const listed = new Set(att.filter((x) => x.r).map((x) => x.r.id));
  const needsLook = todays.filter((r) => NEEDS_LOOK.includes(r.status) && !listed.has(r.id)).map((r) => ({ kind: "run", r }));
  const needs = [...att, ...needsLook];
  const done = todays.filter((r) => !active(r) && !NEEDS_LOOK.includes(r.status) && r.status !== "interrupted");
  const arts = allArtifacts(todays).sort((a, b) => String(b.at).localeCompare(String(a.at)));
  const bad = todays.filter((r) => ["failed", "interrupted", "timed_out", "blocked"].includes(r.status));
  const count = (st) => todays.filter((r) => r.status === st).length;
  const breakdown = [
    [count("completed"), "done"], [count("budget_exhausted") + count("stalled"), "unfinished"],
    [count("waiting"), "waiting"], [count("interrupted"), "interrupted"], [count("running") + count("queued"), "running"],
    [count("failed") + count("blocked") + count("timed_out") + count("rejected"), "failed"], [count("cancelled"), "cancelled"],
  ].filter(([n]) => n).map(([n, k]) => t("{n} " + k, { n })).join(" · ");
  const kpi = (v, label, b, href, id = "") =>
    `<a class="kpi link" href="${href}" ${id ? `id="${id}"` : ""}><div class="v">${v}</div><div class="l">${esc(label)}</div><div class="b">${esc(b || "—")}</div></a>`;
  const gates = att.filter((x) => x.kind === "gate").length;
  const dels = att.filter((x) => x.kind === "delivery").length;
  const ints = att.filter((x) => x.kind === "interrupted").length;
  const used = sumUsage(todays);
  const upcoming = s.loops
    .filter(mine)
    .filter((l) => l.enabled && l.cron && toDate(l.next) && toDate(l.next) - ctx.now < 36 * 3600e3)
    .map((l) => ({ at: l.next, name: l.name, sub: cronText(l.cron) + " · " + agentLabel(l.agent) + (l.model ? " · " + l.model : ""), href: `#/loop/${l.id}`, id: l.id, loop: l }));
  if (s.settings.digestEnabled && !q.project) upcoming.push({ at: nextDigest(), name: t("Daily digest"), sub: t("{n} channels", { n: s.settings.channels.filter((c) => c.enabled).length }), href: "#/digest" });
  upcoming.sort((a, b) => String(a.at).localeCompare(String(b.at)));
  const upcomingCards = upcoming.slice(0, 8).map((u) => `<a class="row link bcard" href="${u.href}">${u.id ? `<span class="bc-dot">${dot(u.id)}</span>` : `<span class="idot" data-c="#76767f"></span>`}<div class="grow min0"><div class="nm ell">${esc(u.name)}</div><div class="sub ell">${esc(u.sub)}</div><div class="bmeta"><span class="chip mut mono">${esc(when(u.at))}</span>${u.loop ? projectChip(u.loop) : ""}</div></div></a>`);
  const firstRun = !s.loops.length;
  const pq = q.project ? "&project=" + encodeURIComponent(q.project) : "";
  return `
  <div class="sec top-sec"><h2>${T("Today")}</h2><span class="note">${esc(new Date(ctx.now).toLocaleDateString(ctx.lang, { dateStyle: "full" }))} · ${esc(Intl.DateTimeFormat().resolvedOptions().timeZone)}</span><div class="act">${projectPicker(q, "today-project")}<span class="sx">${icon(ctx.connected ? "ok" : "lost")}${T(ctx.connected ? "Updated {time}" : "Connection lost — retrying", { time: hm(s.updated) })}</span></div></div>
  ${firstRun ? `<div class="card hero"><span class="mark xl" aria-hidden="true"></span><div class="grow"><h1>${T("Create your first Loop")}</h1><p class="sub t2">${T("Schedule local agents, review their work, and keep the evidence.")}</p></div>${viewer() ? `<a class="btn" href="#/settings/team">${T("Team sync")}</a>` : `<a class="btn pri" href="#/new">${T("New Loop")}</a><a class="btn" href="#/loops">${T("From a template")}</a>`}</div>` : ""}
  <div class="kpis">
    ${kpi(running.length, t("Running"), running.map((r) => `${r.definition.name} · ${t("round {r}", { r: r.round })}`).join(" · "), "#/runs?status=running" + pq)}
    ${kpi(needs.length, t("Needs you"), [gates && t("{n} gate|{n} gates", { n: gates }), ints && t("{n} interrupted", { n: ints }), dels && t("{n} delivery unknown|{n} deliveries unknown", { n: dels }), needsLook.length && t("{n} to look at", { n: needsLook.length })].filter(Boolean).join(" · "), "#/today?focus=attention" + pq)}
    ${kpi(todays.length, t("Today’s runs"), breakdown, "#/runs?range=today" + pq)}
    ${kpi(esc(tokens(used.input + used.cacheRead + used.cacheWrite + used.output)), t("Today’s tokens"), [money(used.costUsd) && t("{cost} reported cost", { cost: money(used.costUsd) }), used.runs && t("{n} runs with usage", { n: used.runs })].filter(Boolean).join(" · "), "#/today?focus=usage" + pq, "kpi-tokens")}
    ${kpi(bad.length, t("Interrupted / failed"), t("{a} interrupted · {b} failed", { a: bad.filter((r) => r.status === "interrupted").length, b: bad.length - bad.filter((r) => r.status === "interrupted").length }), "#/runs?status=attention" + pq)}
  </div>
  <div class="board" id="board">
    ${boardCol("upcoming", t("Coming up"), upcomingCards, T("Nothing scheduled"), s.settings.paused ? T("All schedules are paused.") : T("Enable a Loop with a schedule to see it here."))}
    ${boardCol("running", t("Running"), running.map((r) => runCard(r)), T("Nothing running"), T("Runs appear here while they execute."))}
    ${boardCol("needs", t("Needs you"), needs.map(attentionCard), T("Nothing needs you"), T("Gates, interrupted runs and unknown deliveries appear here."))}
    ${boardCol("done", t("Done today"), done.slice(0, 20).map((r) => runCard(r)), T("No runs today"), runs.length ? T("Earlier runs are in the Runs list.") : T("Run a Loop manually or wait for its schedule."))}
  </div>
  <div class="cols">
    <div class="main">
      ${sec(T("Today’s usage"), { note: T("Tokens and cost as each agent CLI reported them; loopbee does not estimate prices"), id: "usage" })}
      ${todayUsage(todays)}
    </div>
    <div class="side">
      ${sec(T("Today’s artifacts"), { note: T("{n} item|{n} items", { n: arts.length }) })}
      <div class="grp">${arts.length ? arts.slice(0, 6).map((a) => artifactRow(a)).join("") + `<a class="foot" href="#/artifacts">${T("Open artifact library")}</a>` : empty(T("No artifacts today"), T("Every step stores its output as evidence."))}</div>
    </div>
  </div>
  ${footer([t("{n} loops · {m} runs stored", { n: s.loops.length, m: s.runs.length })])}`;
}
function nextDigest() {
  const s = S().settings;
  const f = (s.digestCron || "").split(/\s+/);
  const d = new Date(ctx.now);
  if (/^\d+$/.test(f[0]) && /^\d+$/.test(f[1])) {
    d.setHours(Number(f[1]), Number(f[0]), 0, 0);
    if (d.getTime() < ctx.now) d.setDate(d.getDate() + 1);
  }
  return d.toISOString();
}

// ---------- Loops ----------
export function loops(q) {
  const s = S();
  const filter = q.filter || "all";
  const needs = (l) => runsOf(l.id).some((r) => r.status === "waiting" || (r.status === "interrupted" && !r.acked));
  const sets = {
    all: () => true,
    enabled: (l) => l.enabled,
    paused: (l) => !l.enabled,
    attention: needs,
    private: (l) => l.private,
  };
  const search = (q.search || "").toLowerCase();
  const base = s.loops.filter(inProject(q));
  const list = base
    .filter(sets[filter] || sets.all)
    .filter((l) => (l.name + " " + l.description).toLowerCase().includes(search))
    .sort((a, b) => Number(b.enabled) - Number(a.enabled) || a.name.localeCompare(b.name));
  const shown = q.showDisabled || filter !== "all" ? list : list.filter((l) => l.enabled);
  const hidden = list.length - shown.length;
  const counts = Object.fromEntries(Object.keys(sets).map((k) => [k, base.filter(sets[k]).length]));
  // Several projects and no project picked: one section per project.
  const byProject = new Map();
  for (const l of shown) byProject.set(projectOf(l), [...(byProject.get(projectOf(l)) || []), l]);
  const grouped = !q.project && byProject.size > 1 ? [...byProject.entries()].sort(([a], [b]) => (a === "") - (b === "") || a.localeCompare(b)) : null;
  const foot = hidden > 0 ? `<button type="button" class="foot" data-action="show-disabled">${T("Show {n} paused Loop|Show {n} paused Loops", { n: hidden })} ⌄</button>` : q.showDisabled && filter === "all" && counts.paused ? `<button type="button" class="foot" data-action="show-disabled">${T("Hide paused Loops")} ⌃</button>` : "";
  const row = (l) => {
    const last = runsOf(l.id)[0];
    const steps = l.steps.map((x) => x.name).join("、");
    return `<div class="row lrow link" data-href="#/loop/${l.id}">${dot(l.id)}<div class="grow min0"><div class="nm ell"><a href="#/loop/${l.id}">${esc(l.name)}</a>${l.private ? ` <span class="chip mut">${T("Private")}</span>` : ""}</div><div class="sub ell">${esc(steps)} · ${esc(cronText(l.cron))}</div></div><div class="l-last">${last ? `${chip(last)}<div class="sub mt3 ell">${esc(when(last.started))} · ${esc(runFacts(last)[0])}</div>` : `<span class="chip mut">${T("Never run")}</span>`}</div><div class="l-next"><div class="sub t2">${l.enabled && l.cron ? T("Next {when}", { when: when(l.next) }) : l.cron ? T("Schedule off") : T("Manual only")}</div><div class="sub">v${l.version}</div></div>${viewer() ? "" : toggle(l.enabled, "toggle-loop", t("Enable schedule"), `data-id="${l.id}" ${l.cron ? "" : `aria-disabled="true" title="${T("Add a schedule first")}"`}`)}<button type="button" class="ib" data-action="loop-menu" data-id="${l.id}" aria-haspopup="menu" aria-label="${T("More")}">${icons.more}</button></div>`;
  };
  return `
  <div class="narrow">
  <div class="sec top-sec"><h2>Loops</h2><span class="note">${T("{a} enabled · {b} paused", { a: counts.enabled, b: counts.paused })}</span><div class="act">${projectPicker(q, "loops-project")}${seg("filter", filter, [["all", t("All")], ["enabled", t("Enabled")], ["paused", t("Paused")], ["attention", t("Needs you") + (counts.attention ? ` ${counts.attention}` : "")], ["private", t("Private")]], { action: "loops-filter", label: t("Filter") })}<input class="inp search" id="search" type="search" value="${esc(q.search || "")}" placeholder="${T("Search name or purpose")}" aria-label="${T("Search")}"></div></div>
  ${grouped ? grouped.map(([p, ls]) => `${sec(esc(projectLabel(p)), { note: T("{n} Loop|{n} Loops", { n: ls.length }) })}<div class="grp">${ls.map(row).join("")}</div>`).join("") + (foot ? `<div class="grp mt14">${foot}</div>` : "") : `<div class="grp">${shown.length ? shown.map(row).join("") : s.loops.length ? empty(T("No matching Loops"), "", `<button class="btn sm" data-action="clear-filter">${T("Clear filters")}</button>`) : empty(T("No Loops yet"), T("Start from a template below or paste your own definition."), viewer() ? "" : `<a class="btn pri sm" href="#/new">${T("Add your first Loop")}</a>`)}${foot}</div>`}
  ${viewer() ? "" : `${sec(T("Start from a template"), { note: T("Templates still go through the editor checks before saving"), act: `<button class="linkbtn" data-action="import">${T("Paste your own definition")} ›</button>` })}
  <div class="tpls">${templates().map((tp, i) => `<a class="card sm tpl" href="#/new?template=${i}"><div class="nm">${esc(tp.title)}</div><div class="sub t2">${esc(tp.sub)}</div><span class="chip mut">${T("Template · unverified content")}</span></a>`).join("")}<button type="button" class="card sm tpl" data-action="import-archive"><div class="nm">${T("Import archive")}</div><div class="sub t2">${T("Restore Loops from a loopbee ZIP; imports are private and disabled")}</div><span class="chip mut">ZIP</span></button></div>`}
  ${footer()}
  </div>`;
}
export function templates() {
  return [
    {
      title: t("Read-only code review"),
      sub: t("Observe a repository and summarise risks; no file changes"),
      loop: { name: t("Read-only code review"), description: t("Observe a repository and summarise risks; no file changes"), sandbox: "read-only", cron: "0 9 * * 1-5", steps: [{ kind: "observe", name: t("Scan"), prompt: t("Review recent changes in this repository and list the three most important risks with file references. Do not modify files.") }, { kind: "summarize", name: t("Summarize"), prompt: t("Write a short report in Markdown.") }] },
    },
    {
      title: t("Fix until tests pass"),
      sub: t("Small fixes, run tests, repeat up to three rounds"),
      loop: { name: t("Fix until tests pass"), description: t("Small fixes, run tests, repeat up to three rounds"), sandbox: "workspace-write", maxRounds: 3, doneWhen: "verify.passed", allowedTools: ["npm"], steps: [{ kind: "act", name: t("Small fix"), prompt: t("Fix the smallest failing test. Keep the change minimal.") }, { kind: "verify", name: t("Test"), argv: ["npm", "test"] }, { kind: "gate", name: t("Review changes") }] },
    },
    {
      title: t("Metric target"),
      sub: t("Improve a score reported as JSON until it reaches the target"),
      loop: { name: t("Metric target"), description: t("Improve a score reported as JSON until it reaches the target"), sandbox: "workspace-write", maxRounds: 3, doneWhen: "metrics.score >= 90 && verify.passed", allowedTools: ["npm"], steps: [{ kind: "act", name: t("Improve"), prompt: t("Make one focused improvement toward a higher score.") }, { kind: "verify", name: t("Measure"), argv: ["npm", "run", "score", "--silent"], metric: "score", metricPath: "$.score" }] },
    },
  ];
}

// ---------- Loop detail ----------
export function loopDetail(id) {
  const l = loopById(id);
  if (!l) return notFound(t("This Loop no longer exists."));
  const s = S();
  const runs = runsOf(l.id);
  const current = runs.find(active) || runs.find((r) => r.status === "interrupted" && !r.acked) || null;
  const last = runs[0];
  const state = flowState(l.steps, current);
  const activeCount = s.runs.filter(active).length;
  const versions = [...new Map(runs.map((r) => [r.definition.version, r])).values()].filter((r) => r.definition.version !== l.version);
  const metricRuns = runs.filter((r) => r.metrics && Object.keys(r.metrics).length && !active(r)).slice(0, 7).reverse();
  const tools = l.allowedTools?.length ? l.allowedTools.map((x) => `<span class="chip">${esc(x)}</span>`).join("") : `<span class="sub">${T("No verification executables allowed")}</span>`;
  return `
  ${crumbs([["Loops", "#/loops"], [l.name]])}
  <div class="card">
    <div class="hd">${dot(l.id, "lg")}<h1 class="ell">${esc(l.name)}</h1><span class="chip">${T("Version v{n}", { n: l.version })}</span><span class="sx">${l.enabled && l.cron ? icon("ok") + T("Schedule on") : l.cron ? icon("idle") + T("Schedule off") : icon("idle") + T("Manual only")}</span>${l.private ? `<span class="chip mut">${T("Private")}</span>` : ""}
      ${viewer() ? `<span class="chip mut hd-r">${T("Read only")}</span>` : `<div class="hd-r actions">${btn("Run now", "start", { cls: "pri", attrs: `data-id="${l.id}" ${current && active(current) ? `disabled title="${T("A run of this Loop is active")}"` : ""}` })}${l.cron ? btn(l.enabled ? "Pause schedule" : "Resume schedule", "toggle-loop", { attrs: `data-id="${l.id}"` }) : ""}<a class="btn" href="#/loop/${l.id}/chat">${T("Change by chat")}</a><a class="btn ghost" href="#/loop/${l.id}/edit">${T("Edit")}</a><button type="button" class="ib" data-action="loop-menu" data-id="${l.id}" data-detail="1" aria-haspopup="menu" aria-label="${T("More")}">${icons.more}</button></div>`}
    </div>
    ${l.description ? `<div class="sub t2 mt6">${esc(l.description)}</div>` : ""}
    <div class="kv mt10"><span><b>${T("Schedule")}</b>${esc(cronText(l.cron))}${l.cron ? " · " + esc(l.timezone) : ""}</span><span><b>${T("Next run")}</b>${esc(l.enabled && l.cron ? when(l.next) : "—")}</span><span><b>${T("Last result")}</b>${last ? esc(when(last.started) + " · " + (active(last) ? t(lifecycle(last) === "waiting" ? "Waiting" : "Running") : outcomeLabel(last))) : "—"}</span><span><b>${T("Updated")}</b>${esc(when(l.updated))}</span></div>
  </div>
  ${sec(T("Flow definition"), { note: T(current ? "The bee follows the active run" : "Same definition drives the board · the bee flies while a run is active") })}
  <div class="card sm flowcard">${renderFlow(l.steps, state, { run: current, loop: l, maxRounds: l.maxRounds })}<hr class="hair">${renderTopology({ loop: l, run: current, state, agents: s.agents, activeCount, host: host() })}${current ? `<div class="sub mt6"><a href="#/run/${current.id}">${T("Open the active run")} ›</a></div>` : ""}</div>
  <div class="cols">
    <div class="main">
      ${sec(T("Definition summary"), { note: esc(`v${l.version} · ${short(l.id)}`) })}
      <div class="grp">
        ${defRow(t("Target"), esc(l.directory), `${l.requireClean ? T("Requires a clean Git working tree") : T("Runs even with uncommitted changes")}${l.lock ? " · " + T("lock {lock}", { lock: l.lock }) : ""}`)}
        ${defRow(t("Agent & permission"), `${esc(agentLabel(l.agent))}${l.model ? " · " + esc(l.model) : ""} · ${esc(sandboxLabel(l.sandbox))}${l.agentMcp ? " · " + T("its own MCP tools") : ""}`, T("This device allows at most: {max}", { max: sandboxLabel(s.settings.maxSandbox) }) + (l.agentMcp ? " · " + T("MCP tools run outside the sandbox with your rights and are approved in advance.") : ""))}
        ${defRow(t("Workspace"), l.workspace === "worktree" ? T("Fresh git worktree from {ref} for every run", { ref: l.baseRef || "HEAD" }) + (l.branch ? " → " + l.branch : "") : T("Runs directly in the working directory"), l.workspace === "worktree" ? T("Your own checkout and uncommitted work are never touched.") + (l.branch ? " " + T("loopbee commits the agent's changes on this branch; it never pushes or merges.") : "") : "")}
        ${l.env?.length ? defRow(t("Environment"), `<div class="chips">${l.env.map((e) => `<span class="chip ${e.secret && !e.value ? "warn" : ""}">${esc(e.name)}${e.secret ? " · " + T(e.value ? "secret" : "secret missing") : ""}</span>`).join("")}</div>`, T("Injected into agent and command steps; secrets come from the system keychain.")) : ""}
        ${defRow(t("Work & delivery"), esc(workSummary(l)), l.intake ? T("Only issues opened by people with write access; others' comments never reach the agent.") : "")}
        ${defRow(t("Notifications"), esc(notifySummary(l)), "")}
        ${defRow(t("Tools"), `<div class="chips">${tools}</div>`, T("Verification commands run without a shell; the first argument must be on this list."))}
        ${defRow(t("Done when"), `<span class="mono">${esc(l.doneWhen || "verify.passed")}</span>`, T("Metrics come only from verification output, never from the agent’s own claims."))}
        ${defRow(t("Limits"), T("At most {r} rounds · {m} minutes · stop after {s} rounds without progress", { r: l.maxRounds, m: l.wallMinutes, s: l.stallRounds }), T("Time waiting at a gate does not count; a gate expires after {h} hours.", { h: l.gateHours || 72 }))}
        ${defRow(t("Steps"), esc(l.steps.map((x, i) => `${i + 1}. ${x.name} (${x.kind})`).join("  ")), "")}
      </div>
      ${sec(T("History"), { note: T("Last {n}", { n: Math.min(5, runs.length) }), act: runs.length ? `<a href="#/runs?loop=${l.id}">${T("All runs")} ›</a>` : "" })}
      <div class="grp">${runs.length ? runs.slice(0, 5).map((r) => runRow(r, { dateTitle: true })).join("") : empty(T("Not run yet"), T("Run it manually once to check the flow before enabling the schedule."), viewer() ? "" : btn("Run now", "start", { cls: "sm", attrs: `data-id="${l.id}"` }))}</div>
    </div>
    <div class="side">
      ${sec(T("Usage"), { note: T("As the agent reported it") })}
      ${usageCard(runs)}
      ${sec(T("Trend"), { note: metricRuns.length ? T("Metric at the end of each run · last {n}", { n: metricRuns.length }) : T("Runs per day · last 7 days") })}
      <div class="card sm">${metricRuns.length ? metricChart(metricRuns) : dailyChart(runs)}</div>
      ${sec(T("Schedule"))}
      <div class="grp">
        <div class="row"><div class="grow"><div class="nm">${esc(cronText(l.cron))}</div><div class="sub">${esc(l.cron ? `${l.timezone} · ${t("triggered by the built-in scheduler")}` : t("Run manually from loopbee"))}</div></div>${l.cron && !viewer() ? toggle(l.enabled, "toggle-loop", t("Enable schedule"), `data-id="${l.id}"`) : ""}</div>
        <div class="row"><div class="grow"><div class="nm">${T("Agent")}</div><div class="sub">${esc(agentLabel(l.agent))}</div></div><a class="btn sm" href="#/settings/agents">${T("Agent settings")}</a></div>
        <div class="row"><div class="grow"><div class="nm">${T("Missed runs")}</div><div class="sub">${T(l.catchUp ? "After wake-up, run the latest missed occurrence once; others are marked missed" : "Missed occurrences are skipped and recorded")}</div></div></div>
      </div>
      ${sec(T("Versions"))}
      <div class="grp"><div class="row"><span class="chip ok">${T("Current")}</span><div class="grow"><div class="nm">v${l.version}</div><div class="sub">${esc(when(l.updated))}</div></div></div>${versions.slice(0, 4).map((r) => `<div class="row"><span class="chip">v${r.definition.version}</span><div class="grow"><div class="nm">v${r.definition.version}</div><div class="sub">${T("Snapshot kept by run {id}", { id: short(r.id) })}</div></div><button class="btn sm" data-action="show-snapshot" data-id="${r.id}">${T("View")}</button></div>`).join("")}</div>
    </div>
  </div>
  ${footer([t("Each run keeps its own definition snapshot; edits never change history")])}`;
}
// Tokens and reported cost of a Loop's runs over the last 7 and 30 days, by model.
function usageCard(runs) {
  const since = (days) => runs.filter((r) => ctx.now - toDate(r.started) < days * 864e5);
  const rows = [[t("Last 7 days"), sumUsage(since(7))], [t("Last 30 days"), sumUsage(since(30))]];
  if (!rows[1][1].runs) return `<div class="card sm" id="loop-usage">${empty(T("No usage recorded yet"), T("Agent steps record the model and tokens their CLI reports."))}</div>`;
  const byModel = new Map();
  for (const r of since(30)) for (const x of r.trace || []) if (x.usage) {
    const k = `${agentLabel(x.usage.agent)} · ${x.usage.model || t("model not reported")}`;
    const cur = byModel.get(k) || { n: 0, cost: 0 };
    byModel.set(k, { n: cur.n + usageTotal(x.usage), cost: cur.cost + (x.usage.costUsd || 0) });
  }
  return `<div class="grp" id="loop-usage">${rows.map(([label, u]) => `<div class="row"><div class="grow"><div class="nm">${esc(label)}</div><div class="sub">${T("{n} runs with usage", { n: u.runs })}</div></div><div class="right"><div class="nm mono">${esc(tokens(u.input + u.cacheRead + u.cacheWrite + u.output))}</div><div class="sub">${esc([money(u.costUsd), t("tokens")].filter(Boolean).join(" · "))}</div></div></div>`).join("")}${[...byModel.entries()].sort((a, b) => b[1].n - a[1].n).slice(0, 4).map(([k, v]) => `<div class="row"><div class="grow min0"><div class="sub ell">${esc(k)}</div></div><div class="sub mono">${esc([tokens(v.n), money(v.cost)].filter(Boolean).join(" · "))}</div></div>`).join("")}</div>`;
}
export function workSummary(l) {
  const from = l.intake ? t("GitHub issues labelled {label}", { label: l.intake.label || "agent-ready" }) + (l.intake.repo ? ` (${l.intake.repo})` : "") : t("The prompt");
  const pr = l.pullRequest?.merge ? t("pull request after your review, merged when checks pass") : { "after-review": t("pull request after your review"), draft: t("draft pull request") }[l.pullRequest?.mode] || (l.branch ? t("branch only") : t("no branch"));
  return `${from} → ${pr}`;
}
function notifySummary(l) {
  const ev = l.notify?.events?.length ? l.notify.events : ["done", "attention", "failed"];
  const names = { done: t("done"), attention: t("needs you"), failed: t("failed"), no_action: t("nothing to do") };
  const ch = l.notify?.channels?.length ? l.notify.channels.join(", ") : t("all enabled channels");
  return `${ev.map((e) => names[e] || e).join(" · ")} → ${ch}${l.notify?.includeReport ? " · " + t("with report") : ""}`;
}
const defRow = (label, main, sub) =>
  `<div class="row top"><div class="sub dl">${esc(label)}</div><div class="grow min0"><div class="nm wrap">${main}</div>${sub ? `<div class="sub">${sub}</div>` : ""}</div></div>`;

function metricChart(runs) {
  const key = Object.keys(runs.at(-1).metrics)[0];
  const vals = runs.map((r) => r.metrics[key]).filter((v) => typeof v === "number");
  const max = Math.max(...vals, 1),
    min = Math.min(...vals, 0);
  const x = (i) => 30 + (i * 290) / Math.max(1, runs.length - 1);
  const y = (v) => 130 - ((v - min) / (max - min || 1)) * 100;
  const pts = runs.map((r, i) => (typeof r.metrics[key] === "number" ? [x(i), y(r.metrics[key]), r] : null));
  const line = pts.filter(Boolean).map(([a, b]) => `${a.toFixed(1)},${b.toFixed(1)}`).join(" ");
  const lastV = vals.at(-1);
  return `<svg class="chart" viewBox="0 0 340 160" role="img" aria-label="${esc(t("{metric} trend: {first} to {last}", { metric: key, first: vals[0], last: lastV }))}"><line class="grid" x1="20" y1="30" x2="330" y2="30"/><line class="grid" x1="20" y1="80" x2="330" y2="80"/><line class="grid" x1="20" y1="130" x2="330" y2="130"/><text class="ax" x="20" y="22">${esc(key)} ${esc(max)}</text><polyline class="ln" points="${line}"/>${pts.filter(Boolean).map(([a, b, r], i, arr) => `<a href="#/run/${r.id}"><circle class="${i === arr.length - 1 ? "pt last" : "pt"}" cx="${a.toFixed(1)}" cy="${b.toFixed(1)}" r="${i === arr.length - 1 ? 4.5 : 3.5}"><title>${esc(`${short(r.id)} · ${key} ${r.metrics[key]}`)}</title></circle></a>`).join("")}<text class="val" x="${x(runs.length - 1)}" y="${y(lastV) - 9}" text-anchor="middle">${esc(lastV)}</text>${runs.map((r, i) => `<text class="ax" x="${x(i)}" y="150" text-anchor="middle">${esc(dayKey(r.started).slice(5))}</text>`).join("")}</svg><div class="sub mt6">${T("Values come from the verification command’s JSON output.")}</div>`;
}
function dailyChart(runs) {
  const days = Array.from({ length: 7 }, (_, i) => {
    const d = new Date(ctx.now);
    d.setDate(d.getDate() - (6 - i));
    const k = dayKey(d);
    const rs = runs.filter((r) => dayKey(r.started) === k);
    return { k, n: rs.length, ok: rs.filter((r) => r.status === "completed").length };
  });
  const max = Math.max(1, ...days.map((d) => d.n));
  return `<svg class="chart" viewBox="0 0 340 150" role="img" aria-label="${esc(t("Runs in the last 7 days: {list}", { list: days.map((d) => d.n).join(", ") }))}"><line class="grid" x1="20" y1="120" x2="330" y2="120"/>${days.map((d, i) => { const h = (d.n / max) * 90, ho = (d.ok / max) * 90, cx = 40 + i * 44; return `<rect class="bar" x="${cx - 12}" y="${120 - h}" width="24" height="${h}" rx="3"/><rect class="bar ok" x="${cx - 12}" y="${120 - ho}" width="24" height="${ho}" rx="3"/><text class="ax" x="${cx}" y="138" text-anchor="middle">${d.k.slice(8)}</text>${d.n ? `<text class="val" x="${cx}" y="${112 - h}" text-anchor="middle">${d.n}</text>` : ""}`; }).join("")}</svg><div class="sub mt6">${T("Dark segment: runs that ended with Done.")}</div>`;
}

// ---------- Runs ----------
export function runsPage(q) {
  const s = S();
  const range = q.range || "7d";
  const since = { today: dayKey(new Date(ctx.now)), "7d": dayKey(new Date(ctx.now - 6 * 864e5)), "30d": dayKey(new Date(ctx.now - 29 * 864e5)), all: "0000" }[range] || "0000";
  let runs = runsSorted().filter((r) => dayKey(r.started) >= since).filter(inProject(q));
  const inRange = runs;
  const statusSets = {
    all: () => true,
    running: (r) => ["running", "queued"].includes(r.status),
    attention: (r) => r.status === "waiting" || (r.status === "interrupted" && !r.acked) || ["failed", "timed_out", "blocked"].includes(r.status),
    ended: (r) => lifecycle(r) === "ended",
    interrupted: (r) => r.status === "interrupted",
  };
  const outcomeSets = {
    done: (r) => r.status === "completed",
    budget: (r) => ["budget_exhausted", "stalled", "timed_out"].includes(r.status),
    failed: (r) => ["failed", "blocked", "rejected"].includes(r.status),
    stopped: (r) => ["interrupted", "cancelled"].includes(r.status),
  };
  const status = q.status || "all";
  runs = runs.filter(statusSets[status] || statusSets.all);
  if (q.outcome && outcomeSets[q.outcome]) runs = runs.filter(outcomeSets[q.outcome]);
  if (q.loop) runs = runs.filter((r) => r.loopId === q.loop);
  if (q.source) runs = runs.filter((r) => (r.source || "manual") === q.source);
  const search = (q.search || "").toLowerCase();
  if (search) runs = runs.filter((r) => (r.definition.name + " " + r.id + " " + (r.error || "")).toLowerCase().includes(search));
  const limit = Number(q.limit) || 50;
  const kp = (key, v, l, b) => `<button type="button" class="kpi link ${q.outcome === key ? "on" : ""}" data-action="runs-outcome" data-value="${key}" aria-pressed="${q.outcome === key}"><div class="v">${v}</div><div class="l">${esc(l)}</div><div class="b">${esc(b)}</div></button>`;
  const cnt = (f) => inRange.filter(f).length;
  const statusCount = (k) => inRange.filter(statusSets[k]).length;
  const groups = new Map();
  for (const r of runs.slice(0, limit)) {
    const k = dayKey(r.started);
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(r);
  }
  const loopsInRuns = [...new Map(runsSorted().map((r) => [r.loopId, r.definition.name])).entries()];
  const filters = [q.outcome, q.loop, q.source, q.project, status !== "all" ? status : ""].filter(Boolean).length;
  return `
  <div class="sec top-sec"><h2>Runs</h2><span class="note">${T("Every run is kept; finished runs are never rewritten")}</span><div class="act">${seg("range", range, [["today", t("Today")], ["7d", t("7 days")], ["30d", t("30 days")], ["all", t("All")]], { action: "runs-q", label: t("Time range") })}<input class="inp search" id="search" type="search" value="${esc(q.search || "")}" placeholder="${T("Search id, name or error")}" aria-label="${T("Search")}"></div></div>
  <div class="kpis">
    ${kp("", inRange.length, t("All"), t({ today: "Today", "7d": "Last 7 days", "30d": "Last 30 days", all: "All time" }[range]))}
    ${kp("done", cnt(outcomeSets.done), t("Done"), t("Finished and met the condition"))}
    ${kp("budget", cnt(outcomeSets.budget), t("Budget used / no progress"), t("Can continue with a rerun"))}
    ${kp("failed", cnt(outcomeSets.failed), t("Failed / blocked"), t("Agent error, blocked or sent back"))}
    ${kp("stopped", cnt(outcomeSets.stopped), t("Interrupted / cancelled"), t("{n} need review", { n: cnt((r) => r.status === "interrupted" && !r.acked) }))}
  </div>
  <div class="filters">${seg("status", status, [["all", t("All")], ["running", `${t("Running")} ${statusCount("running") || ""}`], ["attention", `${t("Needs you")} ${statusCount("attention") || ""}`], ["ended", t("Ended")], ["interrupted", `${t("Interrupted")} ${statusCount("interrupted") || ""}`]], { action: "runs-q", label: t("Status") })}
    ${projectPicker(q, "runs-project")}
    <select class="btn sm" id="runs-loop" aria-label="Loop"><option value="">${T("Loop: all")}</option>${loopsInRuns.map(([id, name]) => `<option value="${id}" ${q.loop === id ? "selected" : ""}>${esc(name)}</option>`).join("")}</select>
    <select class="btn sm" id="runs-source" aria-label="${T("Source")}"><option value="">${T("Source: all")}</option>${[["schedule", "Scheduled"], ["catchup", "Catch-up"], ["manual", "Manual"], ["rerun", "Rerun"]].map(([v, l]) => `<option value="${v}" ${q.source === v ? "selected" : ""}>${T(l)}</option>`).join("")}</select>
    <span class="sub flt">${filters ? `${T("{n} filters applied", { n: filters })} · <button class="linkbtn" data-action="clear-filter">${T("Clear")}</button>` : T("No filters applied")}</span></div>
  ${groups.size ? [...groups.entries()].map(([k, rs]) => `${sec(esc(dayLabel(k + "T12:00:00")), { note: T("{n} runs", { n: rs.length }) })}<div class="grp">${rs.map((r) => runRow(r)).join("")}</div>`).join("") : `<div class="grp mt14">${s.runs.length ? empty(T("No matching runs"), "", `<button class="btn sm" data-action="clear-filter">${T("Clear filters")}</button>`) : empty(T("No runs yet"), T("Runs appear here when a Loop starts."))}</div>`}
  ${runs.length > limit ? `<div class="grp mt14"><button type="button" class="foot" data-action="more-runs">${T("Load more ({n} per page)", { n: 50 })}</button></div>` : ""}
  ${footer([t("Finished runs older than {n} days can be pruned in Settings", { n: s.settings.retentionDays })])}`;
}

// ---------- Run detail ----------
export function runDetail(id, ui) {
  const s = S();
  const r = s.runs.find((x) => x.id === id);
  if (!r) return notFound(t("This run no longer exists."));
  const l = r.definition;
  const life = lifecycle(r);
  const trace = r.trace || [];
  const sel = ui.node ?? (trace.length ? trace.length - 1 : -1);
  const round = trace[sel]?.round || r.round || trace.at(-1)?.round || 1;
  const state = flowState(l.steps, r, round);
  const ro = viewer();
  const actions = ro ? `<span class="chip mut">${T("Read only")}</span>` : [
    r.status === "waiting" ? `<a class="btn pri" href="#/gate/${r.id}">${T("Go to review")}</a>` : "",
    active(r) ? btn("Cancel this run", "cancel-run", { cls: "danger-o", attrs: `data-id="${r.id}"` }) : "",
    r.status === "interrupted" && !r.acked ? btn("Mark reviewed", "ack", { attrs: `data-id="${r.id}"` }) : "",
    !active(r) ? btn("Run again", "rerun", { cls: r.status === "interrupted" ? "" : "", attrs: `data-id="${r.id}" ${loopById(r.loopId) ? "" : 'disabled title="' + T("The Loop was deleted") + '"'}` }) : "",
  ].join("");
  const rounds = new Map();
  trace.forEach((x, i) => {
    if (!rounds.has(x.round)) rounds.set(x.round, []);
    rounds.get(x.round).push([x, i]);
  });
  const stIcon = { ok: "ok", approved: "ok", changes_requested: "warn", failed: "err", rejected: "err", running: "run", waiting: "warn", cancelled: "idle", interrupted: "lost" };
  const stText = { ok: "Done", approved: "Approved", changes_requested: "Sent back", failed: "Failed", rejected: "Sent back", running: "Running", waiting: "Waiting", cancelled: "Cancelled", interrupted: "Interrupted" };
  const timeline = [...rounds.entries()].map(([n, items]) => {
    const start = items[0][0].started, end = items.at(-1)[0].finished;
    const failed = items.some(([x]) => x.status === "failed");
    return `<div class="round"><div class="rh"><span class="rt">${T("Round {n}", { n })}</span><span class="sub">${esc(hm(start))} – ${esc(end ? hm(end) : "…")}</span>${failed ? `<span class="chip err">${T("Verification failed")}</span>` : items.every(([x]) => x.finished) ? `<span class="chip ok">${T("Round complete")}</span>` : ""}</div>${items.map(([x, i]) => `<button type="button" class="step ${i === sel ? "sel" : ""}" data-action="select-trace" data-index="${i}"><span class="kd">${esc(x.kind)}</span>${icon(stIcon[x.status] || "idle")}<span class="min0"><span class="nm ell">${esc(x.name)}</span><span class="sub ell">${esc([t(stText[x.status] || x.status), x.finished ? duration(toDate(x.finished) - toDate(x.started)) : t("since {time}", { time: hm(x.started) }), x.usage ? usageLine(x.usage, { model: false }) : "", x.note].filter(Boolean).join(" · "))}</span></span>${x.artifact ? `<span class="ev mono">${esc(x.artifact)} · ${esc(bytes(x.bytes))}</span>` : "<span></span>"}</button>`).join("")}</div>`;
  }).join("");
  const selX = trace[sel];
  const selStep = selX ? l.steps[selX.step] : null;
  const attempts = selX ? trace.filter((x) => x.round === round && x.step === selX.step).map((x) => {
    const stem = `round-${String(x.round).padStart(2, "0")}-step-${String(x.step + 1).padStart(2, "0")}${x.attempt > 1 ? `-attempt-${String(x.attempt).padStart(2, "0")}` : ""}`;
    const artifacts = (r.artifacts || []).filter((name) => [stem + ".txt", stem + ".report.md", stem + ".diff"].includes(name));
    return `<section class="step-attempt"><hr class="hair"><div class="nm">${T("attempt {n}", { n: x.attempt || 1 })} · ${T(x.kind === "review" && x.note?.startsWith("changes requested:") ? "Sent back" : stText[x.status] || x.status)}</div><div class="sub">${esc(duration((x.finished ? toDate(x.finished) : new Date()) - toDate(x.started)))}</div>${x.note ? `<div class="mono pre">${esc(x.note)}</div>` : ""}${x.usage ? `<div class="step-usage">${esc(usageLine(x.usage))}<div class="sub">${esc(usageDetail(x.usage))}</div></div>` : ""}${artifacts.map((name) => `<div><a href="#/artifact/${r.id}/${encodeURIComponent(name)}">${T("Open {name}", { name })}</a></div>`).join("")}<button type="button" class="btn sm" data-action="select-trace" data-index="${trace.indexOf(x)}">${T("Evidence")}</button></section>`;
  }).join("") : "";
  const selectedReport = selX?.artifact?.replace(/\.txt$/, ".report.md");
  const selectedEvidence = (r.artifacts || []).includes(selectedReport) ? selectedReport : selX?.artifact;
  const selCard = selX
    ? `<div class="card sm node-detail"><div class="hd">${icon(stIcon[selX.status] || "idle")}<span class="nm">${esc(selX.name)} (${esc(selX.kind)})</span><span class="chip ${selX.status === "failed" || selX.status === "rejected" ? "err" : selX.status === "ok" || selX.status === "approved" ? "ok" : "mut"}">${T(stText[selX.status] || selX.status)}</span></div><hr class="hair mt10">
      <div class="kvb"><div class="sub">${T("Input")}</div><div class="mono pre">${esc(selStep?.kind === "verify" ? (selStep.argv || []).join(" ") : selStep?.kind === "gate" ? t("Human decision") : selStep?.prompt || "—")}</div>
      <div class="sub">${T("Result")}</div><div>${esc([t(stText[selX.status] || selX.status), selX.note, selX.finished ? t("took {d}", { d: duration(toDate(selX.finished) - toDate(selX.started)) }) : ""].filter(Boolean).join(" · "))}</div>
      ${selectedEvidence ? `<div class="sub">${T("Evidence")}</div><pre class="ev-pre" id="ev-preview" data-run="${r.id}" data-name="${esc(selectedEvidence)}">${T("Loading…")}</pre><a class="btn sm" href="#/artifact/${r.id}/${encodeURIComponent(selectedEvidence)}">${T("Open {name}", { name: selectedEvidence })}</a>` : ""}</div>${attempts}</div>`
    : `<div class="card sm">${empty(T("Nothing selected"), T(trace.length ? "Pick a step in the timeline." : "Steps appear here once the run starts."))}</div>`;
  const nextLoop = loopById(r.loopId);
  const stop = life === "ended" ? `<div class="row top">${icon(outcome(r) === "done" ? "ok" : outcome(r) === "agent_error" ? "err" : "warn")}<div class="grow"><div class="nm">${T("Stop reason: {reason}", { reason: outcomeLabel(r) })}</div><div class="sub">${esc(stopDetail(r))}</div>${["budget_exhausted", "stalled"].includes(r.status) ? `<div class="sub">${T("A normal end does not mean the goal was reached.")}</div>` : ""}</div></div>` : life === "interrupted" ? `<div class="row top">${icon("lost")}<div class="grow"><div class="nm">${T("Interrupted — review before continuing")}</div><div class="sub">${esc(errText(r.error))}</div></div></div>` : `<div class="row top">${icon(life === "waiting" ? "warn" : "run")}<div class="grow"><div class="nm">${T(life === "waiting" ? "Waiting at a human gate" : "In progress")}</div><div class="sub">${T("Round {r} / {max} · {step}", { r: r.round, max: l.maxRounds, step: stepName(r) })}</div></div></div>`;
  return `
  ${crumbs([[t("Runs"), "#/runs"], [l.name, `#/loop/${r.loopId}`], [short(r.id)]])}
  <div class="card">
    <div class="hd">${dot(r.loopId, "lg")}<h1 class="ell">${esc(l.name)}</h1><span class="mono muted">${esc(short(r.id))}</span>${badge(r)}${life === "ended" ? `<span class="chip ${outcome(r) === "done" ? "ok" : outcome(r) === "agent_error" ? "err" : outcome(r) === "canceled" ? "mut" : "warn"}">${T("Outcome: {o}", { o: outcomeLabel(r) })}</span>` : chip(r)}<div class="hd-r actions">${actions}</div></div>
    <div class="kv mt10"><span><b>${T("Started")}</b>${esc(hms(r.started))}</span><span><b>${T("Ended")}</b>${esc(r.finished ? `${hms(r.finished)} (${duration(toDate(r.finished) - toDate(r.started))})` : "—")}</span><span><b>${T("Trigger")}</b>${esc(sourceLabel(r) || t("Manual"))}${r.rerunOf ? ` · <a href="#/run/${r.rerunOf}">${esc(short(r.rerunOf))}</a>` : ""}</span><span><b>${T("Definition")}</b>v${l.version} · ${T("snapshot")}</span><span><b>${T("Agent")}</b>${esc(agentLabel(l.agent))} · ${esc(l.sandbox)}</span>${r.usage ? `<span class="run-usage"><b>${T("Usage")}</b>${esc(usageLine(r.usage))}</span>` : ""}${metricsText(r.metrics) ? `<span><b>${T("Metrics")}</b>${esc(metricsText(r.metrics))}</span>` : ""}</div>
    ${ro ? "" : `<div class="sub mt6">${T("“Run again” creates a new run from the current definition. Cancelling stops the process tree but does not revert file changes.")}</div>`}
  </div>
  ${sec(T("Flow"), { note: T(active(r) ? "Click a node to inspect it · the bee shows where data is going" : "Click a node to inspect it · finished runs are static") })}
  <div class="card sm flowcard">${renderFlow(l.steps, state, { run: r, loop: l, maxRounds: l.maxRounds, round, selected: selX ? selX.step : -1 })}<hr class="hair">${renderTopology({ loop: l, run: r, state, agents: s.agents, activeCount: s.runs.filter(active).length, host: host() })}</div>
  <div class="cols">
    <div class="main">
      ${sec(T("Round timeline"), { note: T("{r} rounds · {n} steps", { r: rounds.size, n: trace.length }) })}
      <div class="grp">${trace.length ? timeline : empty(T(active(r) ? "Starting…" : "No step records"), T(active(r) ? "Steps appear as soon as they start." : "This run was recorded before step tracing existed or stopped before its first step."))}<button type="button" class="foot" data-action="toggle-events">${T(ui.events ? "Hide raw events" : "Show raw events")}</button>${ui.events ? `<div id="events" class="event-list">${T("Loading…")}</div>` : ""}</div>
      ${r.issue ? `<div class="bn mt10">${icon("run")}<span>${T("Issue")} <span class="mono">#${esc(r.issue.number)}</span> ${esc(r.issue.title)} · <span class="mono">${esc(r.issue.url)}</span></span></div>` : ""}
      ${r.prUrl ? `<div class="bn mt10">${icon("ok")}<span>${T(r.merged ? "Pull request (merged)" : "Pull request")} <span class="mono">${esc(r.prUrl)}</span></span>${btn("Copy links", "copy-links", { cls: "sm", attrs: `data-id="${r.id}"` })}</div>` : ""}
      ${r.branch ? `<div class="bn mt10">${icon("ok")}<span>${T("Branch {branch}", { branch: "" })}<span class="mono">${esc(r.branch)}</span>${(r.commits || []).length ? " · " + T("commits {list}", { list: "" }) + `<span class="mono">${esc(r.commits.join(", "))}</span>` : " · " + T((r.artifacts || []).some((n) => n.endsWith(".diff")) ? "work from an earlier Run" : "no changes committed")}</span></div>` : ""}
      ${missingBanner(r, ro)}
      ${r.report ? `${sec(T("Agent report"), { note: T("Written by the agent; not translated") })}<div class="card sm report">${r.result ? `<div class="hd">${resultChip(r.result)}<span class="nm">${esc(r.result.summary || "")}</span></div>${(r.result.links || []).length ? `<div class="links">${r.result.links.map((u) => `<span class="mono">${esc(u)}</span>`).join("")}${btn("Copy links", "copy-links", { cls: "sm", attrs: `data-id="${r.id}"` })}</div>` : ""}<hr class="hair mt10">` : ""}<pre class="report-text">${esc(stripMarker(r.report))}</pre></div>` : ""}
      ${sec(T("Outcome and next steps"))}
      <div class="grp">${stop}<div class="row top">${icon("idle")}<div class="grow"><div class="nm">${T("Artifacts: {n}", { n: r.artifacts?.length || 0 })}</div><div class="sub">${T("Each executed step stores its combined output as text evidence.")}</div></div></div>${nextLoop ? `<div class="row top">${icon("idle")}<div class="grow"><div class="nm">${nextLoop.enabled && nextLoop.cron ? T("Next scheduled run: {when}", { when: when(nextLoop.next) }) : T("No upcoming scheduled run")}</div><div class="sub">${esc(cronText(nextLoop.cron))}</div></div></div>` : ""}</div>
    </div>
    <div class="side">
      ${sec(T("Selected step"), { note: selX ? esc(t("Round {n}", { n: selX.round }) + " · " + selX.name) : "" })}
      ${selCard}
      ${sec(T("Artifacts"), { note: T("{n} item|{n} items", { n: r.artifacts?.length || 0 }) })}
      <div class="grp">${(r.artifacts || []).length ? allArtifacts([r]).map((a) => artifactRow(a, { showLoop: false })).join("") : empty(T("No artifacts yet"))}</div>
    </div>
  </div>
  ${footer([`run ${r.id}`, t("{n} trace records", { n: trace.length })])}`;
}

// What the agent needed and could not get (from its result line), with the ways to give a Loop access.
const missingBanner = (r, ro) =>
  r.result?.missing?.length
    ? `<div class="bn warn mt10" id="run-missing">${icon("warn")}<span class="grow"><b>${T("The agent could not get")}</b>${r.result.missing.map((m) => `<span class="mono missing-item">${esc(m)}</span>`).join("")}<span class="sub">${T("Give the Loop access: a higher permission for commands or network, “Use the agent's own MCP tools” for a browser, database or console, read paths for files outside the project, copied files for Git-ignored project files in a worktree, or environment variables. Then run it again.")}</span></span>${ro ? "" : `<a class="btn sm" href="#/loop/${r.loopId}/edit">${T("Edit definition")}</a>`}</div>`
    : "";

// Why an ended Run stopped: the error, else what the Run itself reported (e.g. intake finding no issue), else the
// generic sentence for a completed Run.
const stopDetail = (r) =>
  errText(r.error) || r.result?.summary || (r.status === "completed" ? t("All steps succeeded and the completion condition was met.") : outcomeLabel(r));

const stripMarker = (t) => {
  const i = t.lastIndexOf("LOOPBEE_RESULT");
  return (i >= 0 ? t.slice(0, i) : t).trim();
};

// ---------- Gate ----------
export function gatePage(id) {
  const r = S().runs.find((x) => x.id === id);
  if (!r) return notFound(t("This run no longer exists."));
  const l = r.definition;
  const gate = (r.trace || []).filter((x) => x.kind === "gate" || x.kind === "deliver" && x.status === "waiting").at(-1);
  const reviewPrefix = "These changes need your review before they are merged: ";
  const reviewFiles = gate?.note?.startsWith(reviewPrefix) ? gate.note.slice(reviewPrefix.length) : "";
  const open = r.status === "waiting";
  // What the reviewer acts on comes first: the committed change, then the agent's report, then raw output.
  const tag = `round-${String(r.round).padStart(2, "0")}-`;
  const review = (r.artifacts || [])
    .filter((n) => n.startsWith(tag) && (n.endsWith(".diff") || n.endsWith(".report.md")))
    .sort((a, b) => Number(b.endsWith(".diff")) - Number(a.endsWith(".diff")))
    .map((n) => ({ artifact: n, round: r.round, status: "ok", kind: n.endsWith(".diff") ? "diff" : "report", name: t(n.endsWith(".diff") ? "Changes to review" : "Agent report") }));
  const evidence = [...review, ...(r.trace || []).filter((x) => x.artifact && x.round === r.round)];
  const prior = evidence.length ? evidence : (r.trace || []).filter((x) => x.artifact).slice(-3);
  const sel = ctx.gateSel || (review[0] || prior.at(-1))?.artifact || "";
  const ro = viewer();
  return `
  ${crumbs([[t("Today"), "#/today"], [l.name, `#/run/${r.id}`], [t("Gate review")]])}
  <div class="card">
    <div class="hd">${dot(r.loopId, "lg")}<h1 class="ell">${esc(gate?.name || stepName(r))}</h1><span class="mono muted">${esc(short(r.id))}</span>${open ? `<span class="chip warn">${icon("warn")}${T("Waiting at human gate")}</span>` : `<span class="chip mut">${T("Gate closed")}</span>`}<span class="chip mut hd-r">${T(ro ? "Only the executor can decide" : "Executor · this device")}</span></div>
    <div class="kv mt10"><span><b>${T("Opened")}</b>${esc(gate ? `${hm(gate.started)} (${t("waited {d}", { d: duration(ctx.now - toDate(gate.started)) })})` : "—")}</span><span><b>Loop</b>${esc(l.name)} · v${l.version}</span><span><b>${T("Round")}</b>${r.round} / ${l.maxRounds}</span><span><b>${T("Expires")}</b>${esc(gate ? when(new Date(toDate(gate.started).getTime() + (l.gateHours || 72) * 3600e3).toISOString()) : "—")}</span></div>
  </div>
  ${reviewFiles ? `<div class="bn warn mt10"><span>${T("These changes need your review before they are merged:")} <span class="mono">${esc(reviewFiles)}</span></span></div>` : ""}
  ${r.result ? `<div class="bn mt10">${resultChip(r.result)}<span class="grow">${esc(r.result.summary || "")}${(r.result.links || []).map((u) => ` <span class="mono">${esc(u)}</span>`).join("")}</span></div>` : ""}
  <div class="bn mt10">${icon("idle")}<span>${T("Approving lets loopbee continue with the next step. Sending back ends this run as “Sent back”; nothing is reverted. Your note is stored with the decision.")}</span></div>
  <div class="cols">
    <div class="main">
      ${sec(T("Evidence from this round"), { note: T("{n} item|{n} items", { n: prior.length }) })}
      <div class="grp">${prior.length ? prior.map((x) => `<button type="button" class="row link ${x.artifact === sel ? "selrow" : ""}" data-action="gate-preview" data-name="${esc(x.artifact)}"><span class="tag">${artifactKind(x.artifact)}</span><div class="grow min0"><div class="nm ell">${esc(x.name)}</div><div class="sub ell">${esc([x.kind, x.artifact, bytes(x.bytes)].filter(Boolean).join(" · "))}</div></div>${icon(x.status === "failed" ? "err" : "ok")}</button>`).join("") : empty(T("No evidence before this gate"), T("The gate is the first step of this round."))}</div>
      ${sec(T("Flow"))}
      <div class="card sm flowcard">${renderFlow(l.steps, flowState(l.steps, r), { run: r, loop: l, maxRounds: l.maxRounds })}</div>
    </div>
    <div class="side wide">
      ${sec(T("Preview"), { note: T("Plain text; nothing is executed or loaded") })}
      <div class="card sm"><pre class="ev-pre tall" id="ev-preview" data-full="1" data-run="${r.id}" data-name="${esc(sel)}">${prior.length ? T("Loading…") : T("No evidence to preview.")}</pre></div>
    </div>
  </div>
  <form id="gate-form" class="card gatebar">
    <div class="gb-t"><div class="nm">${T(open ? "Decision for round {r}" : "This gate is closed", { r: r.round })}</div><div class="sub">${T(open ? "A note is required; it is stored with the decision." : "The run is no longer waiting.")}</div></div>
    <textarea class="inp" name="note" rows="1" maxlength="4000" placeholder="${T("Review note (required)")}" aria-label="${T("Review note")}" ${open && !ro ? "" : "disabled"}>${esc((open && ctx.gateNotes[r.id]) || "")}</textarea>
    <button class="btn" name="decision" value="reject" ${open && !ro ? "" : "disabled"}>${T("Send back")}</button>
    <button class="btn pri" name="decision" value="approve" ${open && !ro ? "" : "disabled"}>${T("Approve and continue")}</button>
  </form>`;
}

// ---------- Artifacts ----------
export function artifactsPage(q, runId, name) {
  const s = S();
  const range = q.range || "all";
  let items = allArtifacts(runsSorted()).sort((a, b) => String(b.at).localeCompare(String(a.at)));
  const total = items.length;
  if (range === "today") items = items.filter((a) => isToday(a.at));
  if (range === "7d") items = items.filter((a) => dayKey(a.at) >= dayKey(new Date(ctx.now - 6 * 864e5)));
  if (q.loop) items = items.filter((a) => a.r.loopId === q.loop);
  const search = (q.search || "").toLowerCase();
  if (search) items = items.filter((a) => (artifactTitle(a.r, a.name) + " " + a.name + " " + a.r.definition.name).toLowerCase().includes(search));
  const limit = Number(q.limit) || 60;
  const groups = new Map();
  for (const a of items.slice(0, limit)) {
    const k = dayKey(a.at);
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(a);
  }
  const cur = runId ? items.find((a) => a.r.id === runId && a.name === name) || allArtifacts(s.runs.filter((r) => r.id === runId)).find((a) => a.name === name) : null;
  const loopsIn = [...new Map(runsSorted().map((r) => [r.loopId, r.definition.name])).entries()];
  const totalBytes = allArtifacts(s.runs).reduce((n, a) => n + (a.size || 0), 0);
  return `
  <div class="sec top-sec"><h2>${T("Artifacts")}</h2><span class="note">${T("Text evidence from every step · {n} total", { n: total })}</span><div class="act">${seg("range", range, [["all", t("All")], ["today", t("Today")], ["7d", t("7 days")]], { action: "art-q", label: t("Time range") })}<select class="btn sm" id="art-loop" aria-label="Loop"><option value="">${T("Loop: all")}</option>${loopsIn.map(([id, n]) => `<option value="${id}" ${q.loop === id ? "selected" : ""}>${esc(n)}</option>`).join("")}</select><input class="inp search" id="search" type="search" value="${esc(q.search || "")}" placeholder="${T("Search titles")}" aria-label="${T("Search")}"></div></div>
  <div class="bn">${icon("ok")}<span class="grow">${T("Evidence on this device: {size}. Finished runs older than {n} days and their evidence can be pruned; running and waiting runs are always kept.", { size: bytes(totalBytes) || "0 B", n: s.settings.retentionDays })}</span>${viewer() ? "" : btn("Prune expired", "prune", { cls: "sm" })}</div>
  <div class="split ${cur ? "has-sel" : ""}">
    <div class="alist">${groups.size ? [...groups.entries()].map(([k, as]) => `${sec(esc(dayLabel(k + "T12:00:00", false)), { note: T("{n} item|{n} items", { n: as.length }) })}<div class="grp">${as.map((a) => artifactRow(a, { selected: cur && a.r.id === cur.r.id && a.name === cur.name })).join("")}</div>`).join("") + (items.length > limit ? `<div class="grp mt14"><button type="button" class="foot" data-action="more-arts">${T("Load more")}</button></div>` : "") : `<div class="grp mt14">${total ? empty(T("No matching artifacts"), "", `<button class="btn sm" data-action="clear-filter">${T("Clear filters")}</button>`) : empty(T("No artifacts yet"), T("Each step of a run stores its output here."))}</div>`}</div>
    <div class="aview">
      ${sec(T("Inspect"), { note: cur ? "" : T("Pick an item on the left") })}
      ${cur ? `<div class="card sm"><div class="hd"><span class="tag">${artifactKind(cur.name)}</span><h1 class="h2 ell">${esc(artifactTitle(cur.r, cur.name))}</h1>${cur.r.definition.private ? `<span class="chip mut">${T("Private")}</span>` : ""}<a class="btn sm hd-r mobile-back" href="#/artifacts">${T("Back")}</a></div>
        <div class="kv mt10"><span><b>${T("Source")}</b><a class="ul" href="#/run/${cur.r.id}">${esc(short(cur.r.id))}</a></span><span><b>Loop</b>${esc(cur.r.definition.name)}</span><span><b>${T("Created")}</b>${esc(when(cur.at))}</span>${cur.size != null ? `<span><b>${T("Size")}</b>${esc(bytes(cur.size))}</span>` : ""}<span><b>${T("File")}</b><span class="mono">${esc(cur.name)}</span></span></div>
        <div class="actions mt10"><a class="btn sm" href="/api/artifacts/${cur.r.id}/${encodeURIComponent(cur.name)}" download="${esc(cur.name)}">${T("Download")}</a>${btn("Copy content", "copy-artifact", { cls: "sm" })}<a class="btn sm" href="#/run/${cur.r.id}">${T("Open source run")}</a></div></div>
        <div class="card sm mt10"><pre class="ev-pre tall" id="ev-preview" data-run="${cur.r.id}" data-name="${esc(cur.name)}" data-full="1">${T("Loading…")}</pre></div>
        <div class="sub mt6">${T("Shown as plain text: nothing is executed and no external resources are loaded.")}</div>` : `<div class="card sm">${empty(T("Nothing selected"), T("Choose an artifact to see its content."))}</div>`}
    </div>
  </div>
  ${footer()}`;
}

// ---------- Digest ----------
export function digestPage(date) {
  const s = S();
  const todayK = dayKey(new Date(ctx.now));
  const sel = date || todayK;
  const lang = s.settings.digestLanguage;
  const days = Array.from({ length: 15 }, (_, i) => {
    const d = new Date(ctx.now);
    d.setDate(d.getDate() - (14 - i));
    const k = dayKey(d);
    const rs = s.runs.filter((r) => dayKey(r.started) === k);
    const bad = rs.some((r) => ["failed", "interrupted", "blocked", "timed_out"].includes(r.status));
    const wait = rs.some((r) => r.status === "waiting" || ["budget_exhausted", "stalled", "rejected"].includes(r.status));
    return { k, d, n: rs.length, cls: !rs.length ? "idle" : bad ? "err" : wait ? "warn" : "ok" };
  });
  const runs = runsSorted().filter((r) => dayKey(r.started) === sel);
  const idx = days.findIndex((d) => d.k === sel);
  const deliveries = s.deliveries.slice().sort((a, b) => String(b.updated).localeCompare(String(a.updated)));
  const channels = s.settings.channels.filter((c) => c.enabled);
  return `
  <div class="sec top-sec"><h2>${T("Digest")}</h2><span class="note">${T("One per day; delivery results are recorded per channel")}</span><div class="act">${btn("Copy", "copy-digest", { cls: "sm" })}</div></div>
  <div class="days" role="tablist" aria-label="${T("Last 15 days")}">${days.map((d) => `<a role="tab" aria-selected="${d.k === sel}" class="day ${d.k === sel ? "on" : ""}" href="#/digest/${d.k}" title="${esc(t("{n} runs", { n: d.n }))}"><span>${d.d.getDate()}</span>${icon(d.cls)}</a>`).join("")}</div>
  <div class="sub mt6">${T("Legend: ✓ all done · ! needs attention · × failed or interrupted · empty: no runs")}</div>
  <div class="cols">
    <div class="main">
      ${sec(esc(dayLabel(sel + "T12:00:00")), { note: T(sel === todayK ? "Live preview — not stored or sent" : "Composed from stored runs"), act: `${idx > 0 ? `<a href="#/digest/${days[idx - 1].k}">‹ ${T("Previous day")}</a>` : ""}${idx >= 0 && idx < days.length - 1 ? `<a href="#/digest/${days[idx + 1].k}">${T("Next day")} ›</a>` : ""}` })}
      <div class="card digest"><div class="hd"><h1>${esc(translate2("Daily digest", lang))} · ${esc(new Date(sel + "T12:00:00").toLocaleDateString(lang, { month: "long", day: "numeric" }))}</h1><span class="sub">${T("Language: {lang}", { lang: { "zh-TW": "繁體中文", "zh-CN": "简体中文", en: "English" }[lang] })}</span></div>
      <pre id="digest-text" class="digest-text">${esc(composeDigest(runs, sel, lang))}</pre></div>
    </div>
    <div class="side">
      ${sec(T("Deliveries"), { note: T("{n} channels enabled", { n: channels.length }) })}
      <div class="grp">${deliveries.length ? deliveries.slice(0, 8).map((d) => `<div class="row top">${dot(d.channel)}<div class="grow min0"><div class="nm">${esc(channelName(d.channel))}</div><div class="sub">${esc(when(d.updated))}${d.attempts > 1 ? " · " + T("{n} attempts", { n: d.attempts }) : ""}${d.error ? " · " + esc(errText(d.error)) : ""}</div></div><div class="right">${deliveryBadge(d)}${viewer() || !["failed", "unknown"].includes(d.status) ? "" : `<div class="actions mt3">${d.status === "unknown" ? btn("Check", "check-delivery", { cls: "sm", attrs: `data-id="${d.id}"` }) : ""}${btn("Resend", "retry", { cls: "sm", attrs: `data-id="${d.id}"` })}</div>`}</div></div>`).join("") : empty(T("No deliveries yet"), T("Enable a channel in Settings › Notifications."))}</div>
      ${sec(T("Schedule"))}
      <div class="grp">
        <div class="row"><div class="sub dl">${T("Send at")}</div><div class="grow nm">${esc(cronText(s.settings.digestCron))} (${esc(s.settings.digestTimezone)})</div></div>
        <div class="row"><div class="sub dl">${T("Automatic")}</div><div class="grow">${s.settings.digestEnabled ? `<span class="chip ok">${T("On")}</span>` : `<span class="chip mut">${T("Off")}</span>`}</div></div>
        <div class="row"><div class="sub dl">${T("Channels")}</div><div class="grow chips">${channels.length ? channels.map((c) => `<span class="chip">${esc(channelName(c.kind))}</span>`).join("") : `<span class="sub">${T("None")}</span>`}</div></div>
        <div class="row"><div class="sub dl">${T("Language")}</div><div class="grow"><div class="nm">${esc({ "zh-TW": "繁體中文", "zh-CN": "简体中文", en: "English" }[lang])}</div><div class="sub">${T("Change in Settings › General")}</div></div></div>
      </div>
    </div>
  </div>
  ${footer()}`;
}
import { translate } from "./i18n.js";
const translate2 = (k, lang, p) => {
  let s = translate(k, lang);
  if (p) {
    if (s.includes("|")) s = p.n === 1 ? s.split("|")[0] : s.split("|")[1];
    s = s.replace(/\{(\w+)\}/g, (_, x) => p[x] ?? "");
  }
  return s;
};
export function composeDigest(runs, day, lang) {
  const tr = (k, p) => translate2(k, lang, p);
  const done = runs.filter((r) => r.status === "completed").length;
  const att = runs.filter((r) => r.status === "waiting" || (r.status === "interrupted" && !r.acked));
  const arts = runs.reduce((n, r) => n + (r.artifacts?.length || 0), 0);
  const lines = [tr("{a} runs · {b} done · {c} need you · {d} artifacts", { a: runs.length, b: done, c: att.length, d: arts }), ""];
  lines.push(tr("Needs you ({n})", { n: att.length }));
  lines.push(...(att.length ? att.map((r) => `• ${r.definition.name} — ${tr(r.status === "waiting" ? "Waiting at human gate" : "Interrupted")}`) : ["• " + tr("Nothing")]));
  lines.push("", tr("Runs ({n})", { n: runs.length }));
  const label = (r) => {
    const map = { completed: "Done", failed: "Agent error", blocked: "Blocked", rejected: "Sent back", cancelled: "Cancelled", timed_out: "Time limit", budget_exhausted: "Budget used", stalled: "No progress", running: "Running", waiting: "Waiting", queued: "Queued", interrupted: "Interrupted" };
    return tr(map[r.status] || r.status);
  };
  lines.push(...(runs.length ? runs.map((r) => `• ${hm(r.started)} ${r.definition.name} — ${label(r)}${metricsText(r.metrics) ? " · " + metricsText(r.metrics) : ""}`) : ["• " + tr("No runs")]));
  lines.push("", tr("Artifacts ({n})", { n: arts }));
  if (day === dayKey(new Date(ctx.now))) {
    const next = S().loops.filter((l) => l.enabled && l.cron).sort((a, b) => String(a.next).localeCompare(String(b.next))).slice(0, 5);
    lines.push("", tr("Coming up"));
    lines.push(...(next.length ? next.map((l) => `• ${when(l.next)} ${l.name}`) : ["• " + tr("Nothing scheduled")]));
  }
  return lines.join("\n");
}

// ---------- shared ----------
export function notFound(msg) {
  return `<div class="card mt14">${empty(T("Not found"), esc(msg), `<a class="btn sm" href="#/today">${T("Back to Today")}</a>`)}</div>`;
}
export { loopColor };
