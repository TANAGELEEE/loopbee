// Shared helpers: escaping, translation, formatting, status mapping and small components.
// Rendering uses strings; all dynamic values go through esc(). Inline style attributes are
// blocked by the page CSP, so per-element CSS variables are set from data-* in applyVars().
import { translate } from "./i18n.js";

export const $ = (s, root = document) => root.querySelector(s);
export const $$ = (s, root = document) => [...root.querySelectorAll(s)];
export const esc = (v) =>
  String(v ?? "").replace(
    /[&<>"']/g,
    (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c],
  );

export const ctx = { state: null, lang: "zh-TW", now: Date.now(), gateNotes: {} };

export function t(key, params) {
  let s = translate(key, ctx.lang);
  if (params) {
    if (s.includes("|")) {
      const [one, many] = s.split("|");
      s = params.n === 1 ? one : many;
    }
    s = s.replace(/\{(\w+)\}/g, (_, k) => (params[k] ?? `{${k}}`));
  }
  return s;
}
export const T = (key, params) => esc(t(key, params));

// ---------- time ----------
const valid = (d) => d && !String(d).startsWith("0001");
export const toDate = (d) => (valid(d) ? new Date(d) : null);
export function hm(d) {
  d = toDate(d);
  return d ? d.toLocaleTimeString(ctx.lang, { hour: "2-digit", minute: "2-digit", hour12: false }) : "—";
}
export function hms(d) {
  d = toDate(d);
  return d ? d.toLocaleTimeString(ctx.lang, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }) : "—";
}
export const dayKey = (d) => {
  d = toDate(d) || new Date(d);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
};
export const isToday = (d) => valid(d) && dayKey(d) === dayKey(new Date(ctx.now));
export function dayLabel(d, withWeekday = true) {
  d = toDate(d) || new Date(d);
  const diff = Math.round((startOfDay(d) - startOfDay(new Date(ctx.now))) / 86400000);
  const base = d.toLocaleDateString(ctx.lang, { month: "long", day: "numeric", ...(withWeekday ? { weekday: "short" } : {}) });
  if (diff === 0) return t("Today") + " · " + base;
  if (diff === -1) return t("Yesterday") + " · " + base;
  return base;
}
function startOfDay(d) {
  const x = new Date(d);
  x.setHours(0, 0, 0, 0);
  return x;
}
// "Today 18:00" / "Tomorrow 08:00" / "10-12 08:00"
export function when(d) {
  d = toDate(d);
  if (!d) return "—";
  const diff = Math.round((startOfDay(d) - startOfDay(new Date(ctx.now))) / 86400000);
  const prefix = diff === 0 ? t("Today") : diff === 1 ? t("Tomorrow") : diff === -1 ? t("Yesterday") : `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  return `${prefix} ${hm(d)}`;
}
export function duration(ms) {
  if (ms == null || ms < 0) return "—";
  const s = Math.round(ms / 1000);
  if (s < 60) return t("{n} s", { n: s });
  const m = Math.round(s / 60);
  if (m < 60) return t("{n} min", { n: m });
  const h = Math.floor(m / 60);
  return m % 60 ? t("{h} h {m} min", { h, m: m % 60 }) : t("{n} h", { n: h });
}
export function bytes(n) {
  if (n == null) return "";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${Math.max(1, Math.round(n / 1024))} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// Human description of a five-field cron expression (falls back to the raw expression).
export function cronText(cron) {
  if (!cron) return t("Manual only");
  const f = cron.trim().split(/\s+/);
  if (f.length === 5 && /^\d+$/.test(f[0]) && /^\d+$/.test(f[1]) && f[2] === "*" && f[3] === "*") {
    const time = `${f[1].padStart(2, "0")}:${f[0].padStart(2, "0")}`;
    if (f[4] === "*") return t("Daily {time}", { time });
    if (f[4] === "1-5") return t("Weekdays {time}", { time });
  }
  if (f.length === 5 && /^\d+$/.test(f[0]) && f[1] === "*" && f[2] === "*" && f[3] === "*" && f[4] === "*")
    return t("Hourly at :{m}", { m: f[0].padStart(2, "0") });
  return cron;
}

// ---------- identity ----------
const palette = ["#2f9e8f", "#c95b9a", "#5b8def", "#d9774a", "#8a6fd1", "#b58a1b", "#4c9a3f", "#76767f"];
export function loopColor(id) {
  let h = 0x811c9dc5; // FNV-1a keeps neighbouring ids apart
  for (const c of String(id)) h = Math.imul(h ^ c.charCodeAt(0), 16777619) >>> 0;
  return palette[h % palette.length];
}

// Core error strings are English codes-in-prose; show the known ones in the interface language.
const coreErrors = {
  "context canceled": "Cancelled by the operator",
  "context deadline exceeded": "Stopped at the time limit",
  "verification did not pass within the round limit": "Verification did not pass within the round limit",
  "verification output did not improve": "Verification results stopped improving",
  "working tree must be a clean Git repository": "The working tree must be a clean Git repository",
  "the working directory is not a Git repository; turn off “Require clean Git tree” or point the Loop at a repository": "The working directory is not a Git repository. Turn off “Require clean Git tree” in the Loop, or point it at a repository.",
  "the working directory has uncommitted changes; commit or stash them, or turn off “Require clean Git tree”": "The working directory has uncommitted changes. Commit or stash them, or turn off “Require clean Git tree” in the Loop.",
  "agent version unsupported; run detection first": "The agent was not detected. Run detection in Settings › Agents.",
  "Application stopped; review evidence before starting a new run": "loopbee stopped while this run was active; review the evidence before starting a new run",
  "agent reported turn.failed; inspect the captured evidence": "The agent reported a failed turn; inspect the captured evidence",
  "Claude Code adapter is experimental and awaits captured live fixtures": "Claude Code is experimental; its steps are blocked until verified",
  "application stopped before delivery was confirmed": "loopbee stopped before the delivery was confirmed",
};
export const errText = (msg) => (msg && coreErrors[msg] ? t(coreErrors[msg]) : msg || "");
export const dot = (id, cls = "") => `<span class="idot ${cls}" data-c="${loopColor(id)}" aria-hidden="true"></span>`;
export const short = (id) => String(id || "").slice(0, 10);

// ---------- status (spec §5: lifecycle badge + business outcome chip) ----------
export function lifecycle(r) {
  return ["queued", "running", "waiting", "interrupted"].includes(r.status) ? r.status : "ended";
}
const outcomes = {
  completed: ["done", "ok", "Done"],
  failed: ["agent_error", "err", "Agent error"],
  blocked: ["blocked", "warn", "Blocked"],
  rejected: ["rejected", "warn", "Sent back"],
  cancelled: ["canceled", "mut", "Cancelled"],
  timed_out: ["timed_out", "warn", "Time limit"],
  budget_exhausted: ["budget_exhausted", "warn", "Budget used"],
  stalled: ["no_progress", "warn", "No progress"],
  no_action: ["no_action", "mut", "Nothing to do"],
};
export function outcome(r) {
  return outcomes[r.status]?.[0] || "";
}
const sxIcon = { ok: "✓", err: "×", warn: "!", run: "●", idle: "", lost: "?" };
export const icon = (cls) => `<span class="st ${cls}" aria-hidden="true">${sxIcon[cls] ?? ""}</span>`;
export function badge(r) {
  const life = lifecycle(r);
  const [cls, label] = {
    queued: ["idle", "Queued"],
    running: ["run", "Running"],
    waiting: ["warn", "Waiting"],
    interrupted: ["lost", "Interrupted"],
    ended: [outcomes[r.status]?.[1] === "err" ? "err" : "ok", "Ended"],
  }[life];
  return `<span class="sx">${icon(cls)}${T(label)}</span>`;
}
export function chip(r) {
  const life = lifecycle(r);
  if (life === "queued") return `<span class="chip mut">${T("Not started")}</span>`;
  if (life === "running") return `<span class="chip mut">${T("No result yet")}</span>`;
  if (life === "waiting") return `<span class="chip warn">${T("Human gate")}</span>`;
  if (life === "interrupted") return r.acked ? `<span class="chip mut">${T("Reviewed")}</span>` : `<span class="chip warn">${T("Needs review")}</span>`;
  const [, cls, label] = outcomes[r.status] || ["", "mut", r.status];
  return `<span class="chip ${cls}">${T(label)}</span>`;
}
export const outcomeLabel = (r) => (outcomes[r.status] ? t(outcomes[r.status][2]) : t(r.status));
export const active = (r) => ["queued", "running", "waiting"].includes(r.status);

export function deliveryBadge(d) {
  if (d.status === "sent") return `<span class="chip ok">${icon("ok")}${T("Sent")}</span>`;
  if (d.status === "failed") return `<span class="chip err">${icon("err")}${T("Failed")}</span>`;
  if (d.status === "unknown") return `<span class="chip warn">${icon("lost")}${T("Result unknown")}</span>`;
  if (d.status === "retried") return `<span class="chip mut">${T("Retried")}</span>`;
  return `<span class="chip mut">${T("Pending")}</span>`;
}
export const channelName = (k) => ({ desktop: t("Desktop"), feishu: t("Feishu"), telegram: "Telegram", bark: "Bark" })[k] || k;

// ---------- components ----------
export const btn = (label, action, { cls = "", attrs = "", raw = false } = {}) =>
  `<button type="button" class="btn ${cls}" data-action="${action}" ${attrs}>${raw ? label : T(label)}</button>`;
export const sec = (title, { note = "", act = "", id = "" } = {}) =>
  `<div class="sec"${id ? ` id="${id}"` : ""}><h2>${title}</h2>${note ? `<span class="note">${note}</span>` : ""}${act ? `<div class="act">${act}</div>` : ""}</div>`;
export const empty = (title, body = "", action = "") =>
  `<div class="empty"><div class="nm">${title}</div>${body ? `<div class="sub">${body}</div>` : ""}${action ? `<div class="empty-act">${action}</div>` : ""}</div>`;
export const toggle = (on, action, label, attrs = "") =>
  `<button type="button" class="tg" role="switch" aria-checked="${on ? "true" : "false"}" aria-label="${esc(label)}" data-action="${action}" ${attrs}></button>`;
export function seg(name, value, options, { action = "seg", label = "" } = {}) {
  return `<span class="seg" role="radiogroup" aria-label="${esc(label)}">${options
    .map(([v, l]) => `<button type="button" role="radio" aria-checked="${v === value}" class="${v === value ? "on" : ""}" data-action="${action}" data-name="${name}" data-value="${esc(v)}">${esc(l)}</button>`)
    .join("")}</span>`;
}
export const fld = (label, control, help = "") =>
  `<div class="fld"><div class="lb">${label}</div><div class="ctl">${control}</div>${help ? `<div class="hp">${help}</div>` : ""}</div>`;

export const icons = {
  refresh: '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12a9 9 0 1 1-3-6.7"/><path d="M21 4v5h-5"/></svg>',
  gear: '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/></svg>',
  clock: '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>',
  info: '<span class="st ok" aria-hidden="true">i</span>',
  more: '<svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="1.6"/><circle cx="12" cy="12" r="1.6"/><circle cx="19" cy="12" r="1.6"/></svg>',
  // the sidebar's button: a window with its left pane, filled while the sidebar shows (loop.css .sbt)
  sidebar: '<svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true"><rect class="sbt-pane" x="2.5" y="3.5" width="4" height="9" rx="1"/><g fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round"><rect x="1.75" y="2.75" width="12.5" height="10.5" rx="2.25"/><path d="M6.75 2.75v10.5"/></g></svg>',
};
// the pages' icons in the sidebar
const navIcon = (d) => `<svg class="sb-ic" width="16" height="16" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
export const navIcons = {
  today: navIcon('<circle cx="10" cy="10" r="3.2"/><path d="M10 2.5v1.8M10 15.7v1.8M2.5 10h1.8M15.7 10h1.8M4.7 4.7l1.3 1.3M14 14l1.3 1.3M4.7 15.3 6 14M14 6l1.3-1.3"/>'),
  loops: navIcon('<path d="M15.5 7.5A6 6 0 0 0 4.6 7M4.5 12.5A6 6 0 0 0 15.4 13"/><path d="M15.8 3.8v3.7h-3.7M4.2 16.2v-3.7h3.7"/>'),
  runs: navIcon('<path d="M6.5 4.5v11l9-5.5z"/>'),
  artifacts: navIcon('<path d="M3.5 6.5 10 3l6.5 3.5v7L10 17l-6.5-3.5z"/><path d="M3.5 6.5 10 10l6.5-3.5M10 10v7"/>'),
  digest: navIcon('<path d="M4.5 3.5h11v13h-11z"/><path d="M7.5 7h5M7.5 10h5M7.5 13h3"/>'),
  settings: navIcon('<circle cx="10" cy="10" r="2.5"/><path d="M10 2.5v2M10 15.5v2M2.5 10h2M15.5 10h2M4.7 4.7l1.4 1.4M13.9 13.9l1.4 1.4M4.7 15.3l1.4-1.4M13.9 6.1l1.4-1.4"/>'),
};
export const bee = (cls, path) =>
  `<span class="bee ${cls}"${path ? ` data-p="path('${path}')"` : ""} aria-hidden="true"><svg viewBox="0 0 24 24" aria-hidden="true"><g class="wg"><ellipse cx="9.2" cy="7.4" rx="3.2" ry="4.6" transform="rotate(-24 9.2 7.4)"/><ellipse cx="14.6" cy="6.8" rx="3.2" ry="4.4" transform="rotate(14 14.6 6.8)"/></g><ellipse class="bd" cx="12.2" cy="14.6" rx="7.2" ry="4.7"/><path class="sp" d="M10.1 10.5v8.2M14.1 10.4v8.4"/><circle class="hd" cx="19.8" cy="14.2" r="2.3"/><path class="sg" d="M5.1 14.6H2.6"/></svg></span>`;

// Apply CSS custom properties that the CSP forbids writing as inline style attributes.
export function applyVars(root = document) {
  for (const el of root.querySelectorAll("[data-c]")) el.style.setProperty("--c", el.dataset.c);
  for (const el of root.querySelectorAll("[data-p]")) el.style.setProperty("--p", el.dataset.p);
  for (const el of root.querySelectorAll("[data-w]")) el.style.width = el.dataset.w;
}

// ---------- artifacts ----------
export function artifactKind(name) {
  const ext = (name.split(".").pop() || "").toLowerCase();
  return { md: "MD", json: "JSON", patch: "DIFF", diff: "DIFF", txt: "TXT", log: "LOG", yaml: "YAML", yml: "YAML" }[ext] || "TXT";
}
// Describe a step artifact ("round-02-step-03.txt") using the Run's definition.
export function artifactTitle(r, name) {
  const m = /^round-(\d+)-step-(\d+)\./.exec(name);
  if (!m) return name;
  const step = r.definition.steps[Number(m[2]) - 1];
  const title = t("Round {r} · {step}", { r: Number(m[1]), step: step?.name || "#" + Number(m[2]) });
  return name.endsWith(".report.md") ? t("{title} · report", { title }) : title;
}
export function allArtifacts(runs) {
  return runs.flatMap((r) =>
    (r.artifacts || []).map((name) => {
      const tr = (r.trace || []).find((x) => x.artifact === name);
      return { r, name, at: tr?.finished || tr?.started || r.finished || r.started, size: tr?.bytes };
    }),
  );
}

// ---------- agents & permissions ----------
export const SANDBOXES = ["read-only", "workspace-write", "workspace-network", "agent-config"];
export const sandboxRank = (s) => SANDBOXES.indexOf(s);
export const sandboxLabel = (s) => t({ "read-only": "Read only", "workspace-write": "Workspace write", "workspace-network": "Workspace write + network", "agent-config": "Agent's own config" }[s] || s);
export const sandboxHelp = (s) => t({
  "read-only": "Reads the working directory; cannot change files or use the network.",
  "workspace-write": "Edits files and runs commands inside the working directory; no network.",
  "workspace-network": "As workspace write, plus network: git push, deployments, databases, APIs, e-mail.",
  "agent-config": "loopbee adds no sandbox flags; the agent's own config, profile or settings decide.",
}[s] || "");
// Agents come from the core (one adapter per CLI); these fallbacks cover a viewer without detection data.
const AGENT_FALLBACK = { codex: "Codex CLI", claude: "Claude Code", opencode: "OpenCode", pi: "pi" };
export const agentLabel = (n) => ctx.state?.agents?.find((a) => a.name === n)?.label || AGENT_FALLBACK[n] || n || "—";
export const agentColor = (n) => ({ codex: "#5b8def", claude: "#c95b9a", opencode: "#8a6fe0", pi: "#d0892e" })[n] || "#76767f";
export const agentCmd = (n) => ({ codex: "codex exec", claude: "claude -p", opencode: "opencode run", pi: "pi --mode json" })[n] || n;
// The permission levels an agent enforces itself (always includes agent-config).
export const agentLevels = (n) => ctx.state?.agents?.find((a) => a.name === n)?.levels || ["read-only", "workspace-write", "workspace-network", "agent-config"];
// Token counts as 950, 12.3k, 1.2M.
export const tokens = (n) => (n >= 1e6 ? (n / 1e6).toFixed(n >= 1e7 ? 0 : 1) + "M" : n >= 1e3 ? (n / 1e3).toFixed(n >= 1e4 ? 0 : 1) + "k" : String(n || 0));
export const usageTotal = (u) => (u ? (u.input || 0) + (u.cacheRead || 0) + (u.cacheWrite || 0) + (u.output || 0) : 0);
export const money = (v) => (v >= 1 ? "$" + v.toFixed(2) : v > 0 ? "$" + v.toFixed(v >= 0.01 ? 2 : 4) : "");
// Sum of the usage of several Runs.
export const sumUsage = (runs) => {
  const s = { input: 0, cacheRead: 0, cacheWrite: 0, output: 0, costUsd: 0, models: new Set(), agents: new Set(), runs: 0 };
  for (const r of runs) {
    if (!r.usage) continue;
    s.runs++;
    for (const k of ["input", "cacheRead", "cacheWrite", "output", "costUsd"]) s[k] += r.usage[k] || 0;
    for (const m of (r.usage.model || "").split(", ").filter(Boolean)) s.models.add(m);
    for (const a of (r.usage.agent || "").split(", ").filter(Boolean)) s.agents.add(a);
  }
  return s;
};
// One line: "12.3k tokens · $0.04 · claude-sonnet" (cost only when the CLI reported it).
export const usageLine = (u, { model = true } = {}) =>
  u ? [t("{n} tokens", { n: tokens(usageTotal(u)) }), money(u.costUsd), model ? u.model : ""].filter(Boolean).join(" · ") : "";
export const usageDetail = (u) =>
  u ? t("input {i} · cached {c} · output {o}", { i: tokens(u.input || 0), c: tokens((u.cacheRead || 0) + (u.cacheWrite || 0)), o: tokens(u.output || 0) }) : "";
export const resultChip = (res) => {
  if (!res) return "";
  const [cls, label] = { done: ["ok", "Agent: done"], no_action: ["mut", "Agent: nothing to do"], blocked: ["warn", "Agent: blocked"], needs_approval: ["warn", "Agent: needs approval"] }[res.status] || ["mut", res.status];
  return `<span class="chip ${cls}">${T(label)}</span>`;
};
