// Flow strip and execution topology (spec §8). flowState is a pure function of the Run so the
// animation never depends on event volume; render functions only consume its output.
import { T, t, esc, bee, icon, cronText, sandboxLabel, agentLabel, agentCmd, agentColor, AGENT_KINDS } from "./ui.js";

// steps: definition steps; run: the Run to visualise or null for an idle Loop.
export function flowState(steps, run) {
  const n = steps.length;
  const nodes = steps.map(() => "");
  const links = Array(Math.max(0, n - 1)).fill("");
  let pose = "",
    link = -1,
    node = -1;
  if (!run) return { nodes, links, bee: { pose, link, node }, topo: "idle" };
  const cur = Math.min(Math.max(run.step || 0, 0), n - 1);
  const before = (i) => i < cur;
  switch (run.status) {
    case "queued":
      for (let i = 1; i < n; i++) nodes[i] = "todo";
      pose = "perch";
      node = 0;
      return { nodes, links, bee: { pose, link, node }, topo: "queued" };
    case "running":
      steps.forEach((_, i) => (nodes[i] = before(i) ? "done" : i === cur ? "on" : "todo"));
      for (let i = 0; i < cur - 1; i++) links[i] = "done";
      if (cur > 0) {
        links[cur - 1] = "live";
        link = cur - 1;
        pose = "fly";
      }
      return { nodes, links, bee: { pose, link, node }, topo: "running" };
    case "waiting":
      steps.forEach((_, i) => (nodes[i] = before(i) ? "done" : i === cur ? "wait" : "todo"));
      for (let i = 0; i < cur; i++) links[i] = "done";
      pose = "perch";
      node = cur;
      return { nodes, links, bee: { pose, link, node }, topo: "waiting" };
    case "interrupted":
      steps.forEach((_, i) => (nodes[i] = before(i) ? "done" : i === cur ? "lost" : "todo"));
      for (let i = 0; i < cur - 1; i++) links[i] = "done";
      if (cur > 0) links[cur - 1] = "lost";
      pose = "lost";
      node = cur;
      return { nodes, links, bee: { pose, link, node }, topo: "lost" };
    default: {
      // Finished runs: show what actually happened in the last round, from the step records.
      const trace = run.trace || [];
      const lastRound = trace.length ? trace[trace.length - 1].round : 0;
      const status = new Map(trace.filter((x) => x.round === lastRound).map((x) => [x.step, x.status]));
      steps.forEach((_, i) => {
        const st = status.get(i);
        nodes[i] = st === "ok" || st === "approved" ? "done" : st === "failed" || st === "rejected" ? "fail" : st === "interrupted" ? "lost" : "todo";
      });
      if (!trace.length && run.status === "completed") nodes.fill("done");
      for (let i = 0; i < n - 1; i++) links[i] = nodes[i] === "done" && nodes[i + 1] !== "todo" ? "done" : "";
      const failed = nodes.includes("fail") || ["failed", "blocked", "timed_out"].includes(run.status);
      return { nodes, links, bee: { pose, link, node }, topo: failed ? "failed" : "ended" };
    }
  }
}

const nodeIcon = { done: icon("ok"), on: icon("run"), wait: icon("warn"), fail: icon("err"), lost: icon("lost") };
const NODE = 152,
  LINK = 56;

// The model and effort an agent step runs with: from the Run's trace entry when there is one, else the step or Loop.
function stepModel(steps, run, loop, i) {
  const s = steps[i];
  if (!AGENT_KINDS.includes(s.kind)) return "";
  const tr = run ? (run.trace || []).filter((x) => x.step === i).at(-1) : null;
  const want = tr?.model || s.model || loop?.model || "";
  const reported = tr?.usage?.model || "";
  const model = reported && reported !== want ? `${want || t("Agent's default")} → ${reported}` : want || t("Agent's default");
  const effort = tr?.effort || s.effort || loop?.effort || t("CLI default");
  return `${model} · ${effort}`;
}

export function renderFlow(steps, state, { run, loop, maxRounds, selected = -1 } = {}) {
  const parts = steps.map((s, i) => {
    const cls = state.nodes[i];
    const link =
      i > 0
        ? `<span class="lk ${state.links[i - 1] === "lost" ? "live lostl" : state.links[i - 1]}">${state.bee.link === i - 1 && state.bee.pose === "fly" ? bee("fly", "M0 10 L56 10") : ""}</span>`
        : "";
    const perch = state.bee.node === i && (state.bee.pose === "perch" || state.bee.pose === "lost") ? bee(state.bee.pose === "lost" ? "perch lost" : "perch") : "";
    const mx = stepModel(steps, run, loop, i);
    return `${link}<button type="button" class="node ${cls} ${selected === i ? "sel" : ""}" data-action="select-node" data-index="${i}" ${cls === "on" || cls === "wait" ? 'aria-current="step"' : ""}>${nodeIcon[cls] || ""}<span><span class="k">${esc(s.kind)}</span><span class="n">${esc(s.name)}</span>${mx ? `<span class="flow-model" title="${esc(mx)}">${esc(mx)}</span>` : ""}</span>${perch}</button>`;
  });
  let back = "";
  if (steps.length > 1 && maxRounds > 1) {
    const live = run && run.status === "running" && run.round > 1;
    const label = run && !["queued"].includes(run.status) && run.round
      ? t("Round {r} / {max}", { r: run.round, max: maxRounds })
      : t("Condition not met and rounds < {max} → back to step 1", { max: maxRounds });
    back = `<div class="loopback ${live ? "live" : ""}" data-ml="${NODE / 2}px" data-w="${(steps.length - 1) * (NODE + LINK)}px"><i></i><span>${esc(label)}</span></div>`;
  }
  return `<div class="flow" role="list">${parts.join("")}</div>${back}`;
}

// Topology: schedule trigger → loopbee core → agents. Only the Loop's agent is drawn solid.
export function renderTopology({ loop, run, state, agents, activeCount, host }) {
  const topo = state.topo;
  const using = loop.agent;
  // The Loop's own agent, then one row for every other agent the core knows.
  const others = agents.filter((a) => a.name !== using);
  const list = [
    { id: using, name: agentLabel(using), cmd: agentCmd(using), color: agentColor(using) },
    { id: "", name: others.length ? t("{n} other agents", { n: others.length }) : t("No other agents"), cmd: "", color: "#76767f", others },
  ];
  const idx = 0;
  const paths = ["M0 69 C34 69 34 32 76 32", "M0 69 C34 69 34 106 76 106"];
  const running = topo === "running";
  const coreChip = {
    idle: `<span class="chip mut">${T("Idle {n} / {max}", { n: activeCount, max: 2 })}</span>`,
    queued: `<span class="chip mut">${T("Queued")}</span>`,
    running: `<span class="chip ok">${icon("run")}${T("Running {n} / {max}", { n: activeCount, max: 2 })}</span>`,
    waiting: `<span class="chip warn">${icon("warn")}${T("Waiting for you")}</span>`,
    lost: `<span class="chip warn">${icon("lost")}${T("Interrupted")}</span>`,
    failed: `<span class="chip err">${icon("err")}${T("Failed")}</span>`,
    ended: `<span class="chip mut">${T("Ended {n} / {max}", { n: activeCount, max: 2 })}</span>`,
  }[topo];
  const agentStatus = (a) => {
    if (a.others) return `<span class="sx muted">${T("{n} detected", { n: a.others.filter((x) => x.status === "available").length })}</span>`;
    const det = agents.find((x) => x.name === a.id);
    if (a.id !== using) return `<span class="sx muted">${T(det?.status === "available" ? "Standby" : det ? "Unverified" : "Not detected")}</span>`;
    if (topo === "running") return `<span class="sx">${icon("run")}${T("Running · round {r}", { r: run.round })}</span>`;
    if (topo === "waiting") return `<span class="sx">${icon("ok")}${T("Step finished · waiting for approval")}</span>`;
    if (topo === "lost") return `<span class="sx">${icon("lost")}${T("Process gone · needs review")}</span>`;
    if (topo === "failed") return `<span class="sx">${icon("err")}${T("Failed")}</span>`;
    if (topo === "ended") return `<span class="sx">${icon("ok")}${T("Ended")}</span>`;
    if (!det) return `<span class="sx">${icon("idle")}${T("Not detected")}</span>`;
    return `<span class="sx">${icon("idle")}${T(det.status === "available" ? "Standby" : "Unverified")}</span>`;
  };
  const pb = (a) => {
    if (a.id !== using) return "";
    if (topo === "running") return '<span class="pb run"><i></i></span>';
    if (topo === "idle" || topo === "queued") return '<span class="pb"></span>';
    return `<span class="pb"><i class="${topo === "failed" ? "pb-err" : ""}" data-w="100%"></i></span>`;
  };
  const ags = list
    .map((a, i) => {
      const me = [loop.model, loop.effort].filter(Boolean).join(" · ");
      const sub = i === idx ? `${a.cmd} · ${sandboxLabel(loop.sandbox)}${me ? " · " + me : ""}${loop.workspace === "worktree" ? " · worktree" : ""}` : (a.others || []).map((x) => x.label || agentLabel(x.name)).join(" · ") || t("Not used by this Loop");
      return `<div class="ag ${i === idx ? (topo === "running" ? "on" : topo === "failed" ? "err" : "") : "off"}"><span class="idot" data-c="${a.color}"></span><div class="grow"><div class="nm">${esc(a.name)}</div><div class="sub mono">${esc(sub)}</div></div>${agentStatus(a)}${pb(a)}</div>`;
    })
    .join("");
  const pathCls = (i) => (i !== idx ? "" : running ? "live" : topo === "lost" ? "lostp" : topo === "idle" || topo === "queued" ? "" : "donep");
  const label = t("Execution topology: schedule trigger, loopbee core, {agent}", { agent: list[idx].name }) + (run ? " · " + t(topo === "running" ? "Running" : topo === "waiting" ? "Waiting" : topo === "lost" ? "Interrupted" : "Ended") : "");
  return `<div class="topo" role="img" aria-label="${esc(label)}"><div class="tn"><div class="tn-ic">${'<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>'}</div><div class="nm">${T("Schedule trigger")}</div><span class="mono">${esc(cronText(loop.cron))} · ${T("Manual")}</span></div><span class="lk ${run && topo !== "idle" ? "done" : ""}"></span><div class="tn core"><span class="mark xl" aria-hidden="true"></span><div class="nm core-nm">${T("loopbee core")}</div><span class="mono">${esc(host)}</span><div class="core-chip">${coreChip}</div>${topo === "waiting" ? bee("perch") : ""}</div><div class="tl" aria-hidden="true"><svg viewBox="0 0 76 138">${paths.map((p, i) => `<path class="${pathCls(i)}" d="${p}"/>`).join("")}</svg>${running ? bee("fly", paths[idx]) : ""}</div><div class="ags">${ags}</div></div>`;
}
