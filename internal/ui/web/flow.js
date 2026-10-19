// Shared plan/Run flow. Rendering is pure; mountFlows measures the mounted nodes after each render.
import { T, t, esc, bee, icon, icons, ctx, toDate, duration, usageLine, cronText, sandboxLabel, agentLabel, agentCmd, agentColor, AGENT_KINDS } from "./ui.js";

export const flowRound = (run) => run?.round || run?.trace?.at(-1)?.round || 1;
const roundTrace = (run, round) => (run?.trace || []).filter((x) => x.round === round);
// Older runners stored successful send-backs as ok + this machine-written note prefix.
const sentBack = (x) => x.status === "changes_requested" || x.status === "ok" && x.note?.startsWith("changes requested: ");
const traceState = (x) => sentBack(x) ? "back" : ({ ok: "done", approved: "done", failed: "fail", rejected: "fail", interrupted: "lost", running: "on", waiting: "wait" }[x.status] || "todo");
const traceLabel = (x) => sentBack(x) ? t("Sent back") : t({ ok: "Done", approved: "Approved", failed: "Failed", rejected: "Sent back", interrupted: "Interrupted", running: "Running", waiting: "Waiting", cancelled: "Cancelled" }[x.status] || x.status);

export function flowState(steps, run, round = flowRound(run)) {
  const nodes = steps.map(() => "");
  const links = Array(Math.max(0, steps.length - 1)).fill("");
  const mascot = { pose: "", link: -1, node: -1 };
  if (!run) return { nodes, links, bee: mascot, topo: "idle" };
  const trace = roundTrace(run, round);
  const current = round === flowRound(run);
  const live = current && ["queued", "running", "waiting", "interrupted"].includes(run.status);
  const cur = Math.min(Math.max(run.step || 0, 0), steps.length - 1);
  const latest = new Map(trace.map((x) => [x.step, x]));
  steps.forEach((_, i) => {
    const x = latest.get(i);
    nodes[i] = x ? traceState(x) : "todo";
    // Legacy Runs have no trace. Never infer success from position once records are available.
    if (!run.trace?.length && current) nodes[i] = run.status === "completed" || live && i < cur ? "done" : "todo";
  });
  if (live && cur >= 0) {
    nodes[cur] = { queued: "", running: "on", waiting: "wait", interrupted: "lost" }[run.status];
    // A review jump invalidates later successes for the next traversal, but keeps its feedback visible.
    if (run.status === "running" || run.status === "waiting") {
      for (let i = cur + 1; i < nodes.length; i++) if (nodes[i] === "done") nodes[i] = "todo";
    }
    if (run.status === "running" && cur > 0) {
      mascot.pose = "fly";
      mascot.link = cur - 1;
    } else if (run.status !== "running") {
      mascot.pose = run.status === "interrupted" ? "lost" : "perch";
      mascot.node = cur;
    }
  }
  for (let i = 0; i < links.length; i++) links[i] = nodes[i] === "done" && nodes[i + 1] !== "todo" ? "done" : "";
  if (mascot.link >= 0) links[mascot.link] = "live";
  if (live && run.status === "interrupted" && cur > 0) links[cur - 1] = "lost";
  const failed = nodes.includes("fail") || current && ["failed", "blocked", "timed_out"].includes(run.status);
  const topo = live ? run.status === "interrupted" ? "lost" : run.status : failed ? "failed" : "ended";
  return { nodes, links, bee: mascot, topo };
}

const nodeIcon = { done: icon("ok"), on: icon("run"), wait: icon("warn"), back: icon("warn"), fail: icon("err"), lost: icon("lost") };

// StepRecord's empty model/effort means CLI default, not today's Loop default. Legacy records lack Agent.
function configuration(s, loop, tr) {
  const recorded = !!tr?.agent;
  return {
    agent: tr?.agent || tr?.usage?.agent || s.agent || loop?.agent || "",
    model: recorded ? tr.model || "" : tr?.model || s.model || loop?.model || "",
    effort: recorded ? tr.effort || "" : tr?.effort || s.effort || loop?.effort || "",
    reported: tr?.usage?.model || "",
    sandbox: s.sandbox || loop?.sandbox || "",
  };
}
function modelLine(c) {
  const want = c.model || t("Agent's default");
  return `${c.reported && c.reported !== c.model ? `${want} → ${c.reported}` : want} · ${c.effort || t("CLI default")}`;
}

export function renderFlow(steps, state, { run, loop, maxRounds = loop?.maxRounds || 1, selected = -1, round = flowRound(run) } = {}) {
  const trace = roundTrace(run, round);
  const latest = new Map(trace.map((x) => [x.step, x]));
  const parts = steps.map((s, i) => {
    const cls = state.nodes[i] || "";
    const tr = latest.get(i);
    const config = AGENT_KINDS.includes(s.kind) ? configuration(s, loop, tr) : null;
    const mx = config ? `${agentLabel(config.agent)} · ${modelLine(config)}` : "";
    const inherited = !run && config ? [!s.agent && t("Agent"), !s.model && t("Model"), !s.effort && t("Effort")].filter(Boolean) : [];
    const start = toDate(tr?.started), end = toDate(tr?.finished);
    const elapsed = start ? duration((end?.getTime() ?? (round === flowRound(run) && ["running", "waiting"].includes(run?.status) ? ctx.now : toDate(run?.finished)?.getTime() ?? start.getTime())) - start.getTime()) : "";
    const meta = tr ? [t("attempt {n}", { n: tr.attempt || 1 }), traceLabel(tr), elapsed].filter(Boolean).join(" · ") : run ? t("Not started") : "";
    const returned = trace.filter((x) => x.step === i && sentBack(x)).at(-1);
    const feedback = returned ? (returned.note || "").replace(/^changes requested: /, "").split(/\r?\n/)[0] : "";
    const target = s.kind === "review" && s.sendBackTo ? steps.find((x) => x.id === s.sendBackTo) : null;
    const fallback = s.kind === "review" && s.sendBackTo ? `<span class="flow-sendback">${T("↩ sends back to {name}", { name: target?.name || s.sendBackTo })}</span>` : "";
    const link = i > 0 ? `<span aria-hidden="true" class="lk ${state.links[i - 1] === "lost" ? "live lostl" : state.links[i - 1] || ""}">${state.bee.link === i - 1 && state.bee.pose === "fly" ? bee("fly", "M0 10 L32 10") : ""}</span>` : "";
    const perch = state.bee.node === i && ["perch", "lost"].includes(state.bee.pose) ? bee(state.bee.pose === "lost" ? "perch lost" : "perch") : "";
    return `<div class="flow-item" role="listitem">${link}<button type="button" class="node ${cls} ${selected === i ? "sel" : ""}" data-kind="${esc(s.kind)}" data-action="select-node" data-index="${i}" aria-pressed="${selected === i}" ${cls === "on" || cls === "wait" ? 'aria-current="step"' : ""}>${nodeIcon[cls] || ""}<span class="flow-content"><span class="k">${i + 1} · ${esc(s.kind)}</span><span class="n">${esc(s.name)}</span>${mx ? `<span class="flow-model">${esc(mx)}</span>` : ""}${inherited.length ? `<span class="flow-inherit">${T("Loop default")}: ${esc(inherited.join(", "))}</span>` : ""}${meta ? `<span class="flow-meta">${esc(meta)}</span>` : ""}${tr?.usage ? `<span class="flow-usage">${esc(usageLine(tr.usage, { model: false }))}</span>` : ""}${feedback ? `<span class="flow-feedback">${esc(feedback)}</span>` : ""}${fallback}</span>${perch}</button></div>`;
  });
  const returns = steps.flatMap((s, i) => {
    if (s.kind !== "review" || !s.sendBackTo) return [];
    const target = steps.findIndex((x) => x.id === s.sendBackTo);
    const count = trace.filter((x) => x.step === i && sentBack(x)).length;
    const label = run ? t("sent back {n}×", { n: count }) : t("send back ≤ {n}×", { n: s.maxAttempts || 3 });
    return [{ from: i, to: target >= 0 && target < i ? target : -1, label, name: steps[target]?.name || s.sendBackTo }];
  });
  // Labels stay readable even without the mounting hook, SVG, or ResizeObserver.
  const fallback = returns.length ? `<ul class="flow-returns">${returns.map((e) => `<li data-from="${e.from}" data-to="${e.to}" data-label="${esc(e.label)}"><span>${e.from + 1}. ${esc(steps[e.from].name)} → ${e.to < 0 ? "" : `${e.to + 1}. `}${esc(e.name)}</span><b>${esc(e.label)}</b></li>`).join("")}</ul>` : "";
  const body = `<div class="flow" data-round="${round}"><div class="flow-nodes" role="list">${parts.join("")}</div><svg class="flow-arcs" aria-hidden="true" focusable="false"></svg>${fallback}</div>`;
  return maxRounds > 1 ? `<fieldset class="flow-frame"><legend>${T("Round {r} / {max}", { r: round, max: maxRounds })}</legend>${body}</fieldset>` : body;
}

// Explicit lifecycle: call after replacing root's contents. The previous observer is disconnected even when
// the next page has no flow. ResizeObserver also handles sidebar changes, font loading and node-height changes.
const mounts = new WeakMap();
export function mountFlows(root = document) {
  mounts.get(root)?.();
  const flows = [...root.querySelectorAll(".flow[data-round]")];
  const layout = () => flows.forEach(layoutFlow);
  layout();
  const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(layout);
  for (const flow of flows) {
    observer?.observe(flow);
    for (const node of flow.querySelectorAll(".node")) observer?.observe(node);
  }
  if (!observer && flows.length) window.addEventListener("resize", layout);
  const dispose = () => {
    observer?.disconnect();
    window.removeEventListener("resize", layout);
  };
  mounts.set(root, dispose);
  return dispose;
}

export function layoutFlow(flow) {
  const svg = flow.querySelector(".flow-arcs");
  if (!svg) return;
  const box = flow.getBoundingClientRect();
  if (!box.width || !box.height) return;
  const nodes = [...flow.querySelectorAll(".node")];
  const rects = nodes.map((node) => {
    const r = node.getBoundingClientRect();
    return { x: r.left - box.left, y: r.top - box.top, w: r.width, h: r.height };
  });
  // Keep the incoming connector with its node when the sequence wraps; on phones it points down.
  nodes.forEach((node, i) => {
    const wraps = i > 0 && rects[i].y > rects[i - 1].y + 2;
    node.parentElement.dataset.wrap = String(wraps);
    const mascot = node.parentElement.querySelector(".lk .bee");
    if (mascot) mascot.style.setProperty("--p", wraps ? "path('M10 0 L10 24')" : "path('M0 10 L32 10')");
  });
  const paths = [...flow.querySelectorAll(".flow-returns li")].map((el, i) => {
    const a = rects[Number(el.dataset.from)], b = rects[Number(el.dataset.to)];
    if (!a || !b) return "";
    const sx = a.x + a.w / 2, tx = b.x + b.w / 2;
    const lane = 18 + i % 3 * 18;
    let d, labelY;
    if (Math.abs(a.y - b.y) < 2) {
      const y = Math.min(a.y, b.y) - lane;
      d = `M${sx} ${a.y} C${sx} ${y} ${tx} ${y} ${tx} ${b.y}`;
      labelY = y - 3;
    } else {
      // A left-side gutter joins rows without running a diagonal through intermediate nodes.
      const left = 5 + i % 3 * 5, sy = a.y + a.h, bottom = sy + lane, top = b.y - lane;
      d = `M${sx} ${sy} Q${sx} ${bottom} ${sx - 10} ${bottom} H${left + 6} Q${left} ${bottom} ${left} ${bottom - 6} V${top + 6} Q${left} ${top} ${left + 6} ${top} H${tx - 6} Q${tx} ${top} ${tx} ${top + 6} V${b.y}`;
      labelY = bottom - 3;
    }
    return `<g><title>${esc(el.textContent)}</title><path class="flow-return-path" d="${d}"/><path class="flow-return-tip" d="M${tx - 4} ${b.y - 6} L${tx} ${b.y} L${tx + 4} ${b.y - 6}"/><text x="${Math.max(68, Math.min(box.width - 68, (sx + tx) / 2))}" y="${labelY}" text-anchor="middle">${esc(el.dataset.label)}</text></g>`;
  }).join("");
  svg.setAttribute("viewBox", `0 0 ${box.width} ${box.height}`);
  svg.innerHTML = paths;
}

// Plans list effective configurations; Runs list the configurations actually recorded, including earlier rounds.
export function renderTopology({ loop, run, state, agents = [], activeCount = 0, host = "" }) {
  const byAgent = new Map();
  const records = (run?.trace || []).filter((x) => AGENT_KINDS.includes(x.kind || loop.steps[x.step]?.kind));
  const items = [
    ...(loop.agent ? [{ s: {} }] : []),
    ...loop.steps.filter((s) => AGENT_KINDS.includes(s.kind)).map((s) => ({ s })),
    ...records.map((tr) => ({ s: loop.steps[tr.step] || {}, tr })),
  ];
  for (const { s, tr } of items) {
    const c = configuration(s, loop, tr);
    if (!byAgent.has(c.agent)) byAgent.set(c.agent, new Set());
    byAgent.get(c.agent).add(`${modelLine(c)} · ${sandboxLabel(c.sandbox)}`);
  }
  const topo = state.topo;
  const label = { idle: "Idle", queued: "Queued", running: "Running", waiting: "Waiting for you", lost: "Interrupted", failed: "Failed", ended: "Ended" }[topo] || "Idle";
  const cards = [...byAgent].map(([agent, configs]) => {
    const det = agents.find((a) => a.name === agent);
    const current = run && loop.steps[run.step];
    const currentTrace = roundTrace(run, flowRound(run)).filter((x) => x.step === run.step).at(-1);
    const running = topo === "running" && current && AGENT_KINDS.includes(current.kind) && configuration(current, loop, currentTrace).agent === agent;
    const used = records.some((tr) => configuration(loop.steps[tr.step] || {}, loop, tr).agent === agent);
    const status = running ? "Running" : used ? "Ended" : det?.status === "available" ? "Standby" : det ? "Unverified" : "Not detected";
    return `<div class="ag ${running ? "on" : ""}" data-agent="${esc(agent)}"><span class="idot" data-c="${agentColor(agent)}"></span><div class="grow"><div class="nm">${esc(agentLabel(agent))}</div><div class="sub mono">${esc(agentCmd(agent))}</div>${[...configs].map((c) => `<div class="sub mono topo-config">${esc(c)}</div>`).join("")}</div><span class="sx">${icon(running ? "run" : "idle")}${T(status)}</span>${running ? '<span class="pb run"><i></i></span>' : ""}</div>`;
  }).join("");
  return `<div class="topo flow-topology" role="group" aria-label="${T("Execution topology: schedule trigger, loopbee core, {agent}", { agent: [...byAgent.keys()].map(agentLabel).join(", ") || t("No agents used") })}"><div class="topo-source"><div class="tn"><div class="tn-ic">${icons.clock}</div><div class="nm">${T("Schedule trigger")}</div><span class="mono">${esc(cronText(loop.cron))} · ${T("Manual")}</span></div><span class="lk ${run ? "done" : ""}" aria-hidden="true"></span><div class="tn core"><span class="mark" aria-hidden="true"></span><div class="nm core-nm">${T("loopbee core")}</div><span class="mono">${esc(host)}</span><div class="core-chip"><span class="chip ${topo === "running" ? "ok" : topo === "waiting" ? "warn" : "mut"}">${T(label)} · ${activeCount}</span></div>${topo === "waiting" ? bee("perch") : ""}</div></div><div class="ags">${cards || `<div class="sub">${T("No agents used")}</div>`}</div></div>`;
}
