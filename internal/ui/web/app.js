// Loopbee UI shell: routing, header, polling, dialogs, menus, toasts and actions.
import { ctx, t, T, esc, $, $$, applyVars, btn, icon, icons, active, hm, cronText, sandboxLabel } from "./ui.js";
import * as V from "./views.js";
import { editorPage, gapsPanel, gaps, newDraft, definitionOnly, settingsPage, onboardingPage, STEP_KINDS, kindLabel } from "./forms.js";
import { chatPage, newChat, EXAMPLES } from "./chat.js";

const ui = { node: undefined, events: false, step: 0, mode: "form", dirty: false, channel: "" };
let route = { path: ["today"], q: {} };
let draft = null,
  draftKey = "";
let lastHash = "";
let suppressHash = false;
const filters = {}; // per-page search strings (never written to the URL, spec §3)
ctx.connected = true;

// ---------- API ----------
async function api(path, body, method) {
  const res = await fetch("/api" + path, {
    method: method || (body === undefined ? "GET" : "POST"),
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    let message = await res.text();
    try {
      message = JSON.parse(message).error;
    } catch {}
    const err = new Error(message || res.statusText);
    err.status = res.status;
    throw err;
  }
  return res.json();
}
async function mutate(path, body = {}, method) {
  setState(await api(path, body, method));
  render();
}
function setState(s) {
  ctx.state = s;
  ctx.lang = s.settings.language;
  ctx.now = Date.now();
  try {
    localStorage.setItem("language", ctx.lang);
  } catch {}
}
const saveSettings = (patch) => mutate("/settings", { ...ctx.state.settings, ...patch });

// ---------- routing ----------
function parseHash() {
  const h = location.hash.replace(/^#\/?/, "");
  const [p, qs] = h.split("?");
  const q = Object.fromEntries(new URLSearchParams(qs || ""));
  const path = (p || "today").split("/").map(decodeURIComponent);
  return { path, q };
}
export function go(hash) {
  if (location.hash === hash) render();
  else location.hash = hash;
}
function setQuery(patch) {
  const q = { ...route.q, ...patch };
  for (const k of Object.keys(q)) if (q[k] === "" || q[k] == null) delete q[k];
  const qs = new URLSearchParams(q).toString();
  go("#/" + route.path.map(encodeURIComponent).join("/") + (qs ? "?" + qs : ""));
}
async function onHashChange() {
  if (suppressHash) {
    suppressHash = false;
    return;
  }
  const next = parseHash();
  const leavingEditor = isEditor(route) && ui.dirty && editorKey(next) !== draftKey;
  if (leavingEditor) {
    const ok = await confirmDialog({ title: t("Discard unsaved changes?"), body: t("Your edits to this Loop have not been saved."), ok: t("Discard changes"), cancel: t("Keep editing"), danger: true });
    if (!ok) {
      suppressHash = true;
      location.hash = lastHash;
      return;
    }
    ui.dirty = false;
  }
  const samePage = next.path.join("/") === route.path.join("/");
  route = next;
  lastHash = location.hash;
  if (!samePage) {
    ui.node = undefined;
    ui.events = false;
    ui.channel = "";
    ctx.gateSel = "";
    closeMenu();
  }
  render();
  if (!samePage) {
    window.scrollTo(0, 0);
    $("main h1, main h2")?.focus?.({ preventScroll: true });
  }
  if (route.q.focus === "attention") $("#attention")?.scrollIntoView({ behavior: "smooth" });
}
// /new opens the chat; /new?form=1 (or a template / import / copy) opens the form editor.
const isChat = (r) => (r.path[0] === "new" && !r.q.form && r.q.template == null && !r.q.import && !r.q.dup) || (r.path[0] === "loop" && r.path[2] === "chat");
const isEditor = (r) => (r.path[0] === "new" && !isChat(r)) || (r.path[0] === "loop" && r.path[2] === "edit");
const editorKey = (r) => (isEditor(r) || isChat(r) ? r.path.join("/") + "?" + (r.q.template || "") + (r.q.import || "") + (r.q.dup || "") + (r.q.form || "") : "");
let chat = null;

// ---------- render ----------
function render() {
  const s = ctx.state;
  if (!s) return;
  ctx.now = Date.now();
  document.documentElement.lang = ctx.lang;
  document.title = "Loopbee";
  const dark = s.settings.theme === "dark" || (s.settings.theme === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.body.classList.toggle("dark", dark);
  document.body.classList.toggle("nomo", !s.settings.motion);
  const [page, a, b] = route.path;
  const q = { ...route.q, search: filters[page] || "" };
  if (page === "onboarding") {
    $("#header").innerHTML = header("onboarding");
    $("#banners").innerHTML = "";
    $("#app").innerHTML = onboardingPage(Math.min(5, Math.max(0, Number(a) || 0)));
    return after();
  }
  let html;
  switch (page) {
    case "loops":
      html = V.loops(q);
      break;
    case "loop":
      html = b === "edit" ? editor() : b === "chat" ? chatView() : V.loopDetail(a);
      break;
    case "new":
      html = isChat(route) ? chatView() : editor();
      break;
    case "runs":
      html = V.runsPage(q);
      break;
    case "run":
      html = V.runDetail(a, ui);
      break;
    case "gate":
      html = V.gatePage(a);
      break;
    case "artifacts":
      html = V.artifactsPage(q);
      break;
    case "artifact":
      html = V.artifactsPage({ ...q, ...(route.q || {}) }, a, b);
      break;
    case "digest":
      html = V.digestPage(a);
      break;
    case "settings":
      html = settingsPage(a || "general", ui);
      break;
    default:
      html = V.today();
  }
  $("#header").innerHTML = header(page);
  $("#banners").innerHTML = banners();
  $("#app").innerHTML = html;
  after();
}
function after() {
  applyVars($("#header"));
  applyVars($("#app"));
  for (const el of $$(".loopback[data-ml]")) el.style.marginLeft = el.dataset.ml;
  loadPreview();
  if (ui.events) loadEvents();
}
function header(page) {
  const s = ctx.state;
  const nav = [["today", "Today"], ["loops", "Loops"], ["runs", "Runs"], ["artifacts", "Artifacts"], ["digest", "Digest"]];
  const cur = { loop: "loops", new: "loops", run: "runs", gate: "today", artifact: "artifacts" }[page] || page;
  const langShort = { "zh-TW": "繁", "zh-CN": "简", en: "EN" }[ctx.lang];
  const viewerTag = V.viewer() ? `<span class="chip mut">${T("Viewer")}</span>` : "";
  if (page === "onboarding")
    return `<a class="brand" href="#/today"><span class="mark" aria-hidden="true"></span>Loopbee</a><span></span><div class="tools"><button type="button" class="btn ghost sm" id="language" data-action="lang-menu" aria-haspopup="menu" aria-label="${T("Language")}">${langShort} ▾</button><button type="button" class="btn ghost sm" data-action="finish-onboarding" data-href="#/today">${T("Skip setup")}</button></div>`;
  return `<a class="brand" href="#/today"><span class="mark" aria-hidden="true"></span>Loopbee${viewerTag}</a><nav class="nav" aria-label="${T("Main")}">${nav.map(([id, l]) => `<a href="#/${id}" class="${cur === id ? "on" : ""}" ${cur === id ? 'aria-current="page"' : ""}>${T(l)}</a>`).join("")}</nav><div class="tools">${!V.viewer() && ["today", "loops", "loop"].includes(page) ? `<a class="btn" href="#/new">＋ <span class="new-label">${T("New Loop")}</span></a>` : ""}${s.settings.paused ? `<a class="chip warn" href="#/settings/general">${icon("warn")}${T("Paused")}</a>` : ""}<button type="button" class="btn ghost sm" id="language" data-action="lang-menu" aria-haspopup="menu" aria-label="${T("Language")}">${langShort} ▾</button><button type="button" class="ib" data-action="refresh" aria-label="${T("Refresh")}" title="${T("Refresh")}">${icons.refresh}</button><a class="ib ${page === "settings" ? "on" : ""}" href="#/settings/general" aria-label="${T("Settings")}" title="${T("Settings")}">${icons.gear}</a></div>`;
}
function banners() {
  const s = ctx.state;
  const list = [];
  if (!ctx.connected) list.push(`<div class="bn err">${icon("err")}<span class="grow">${T("Loopbee core is not responding. Showing data from {time}; reconnecting every 3 seconds.", { time: hm(s.updated) })}</span>${btn("Retry", "refresh", { cls: "sm" })}</div>`);
  if (s.settings.paused) list.push(`<div class="bn warn">${icon("warn")}<span class="grow">${T("All schedules are paused. Active runs continue; paused triggers are not recorded as missed.")}</span>${V.viewer() ? "" : btn("Resume", "pause-all", { cls: "sm" })}</div>`);
  if (V.viewer()) list.push(`<div class="bn">${icon("idle")}<span class="grow">${T("Viewer mode: this board shows the team snapshot. Running, approving and editing happen on the executor’s computer.")}</span>${btn("Pull now", "sync", { cls: "sm" })}</div>`);
  const shown = list.slice(0, 2);
  if (list.length > 2) shown.push(`<div class="sub">${T("{n} more notices", { n: list.length - 2 })}</div>`);
  return shown.join("");
}

// ---------- chat authoring ----------
function chatView() {
  const key = editorKey(route);
  if (!chat || chat.key !== key) {
    const loop = route.path[0] === "loop" ? V.loopById(route.path[1]) : null;
    if (route.path[0] === "loop" && !loop) return V.notFound(t("This Loop no longer exists."));
    chat = newChat(key, loop);
    draftKey = key;
    ui.dirty = false;
  }
  return chatPage(chat);
}
let chatAbort = null;
let busyTimer = null;
async function chatSend(text) {
  text = text.trim();
  if (!text || chat.busy) return;
  chat.messages.push({ role: "user", text, at: new Date().toISOString() });
  chat.busy = true;
  chat.started = Date.now();
  chat.questions = [];
  render();
  scrollChat();
  busyTimer = setInterval(() => {
    const el = $("#busy-t");
    if (el) el.textContent = t("Reading your project and drafting… {n} s", { n: Math.round((Date.now() - chat.started) / 1000) });
  }, 1000);
  chatAbort = new AbortController();
  const current = chat;
  try {
    const res = await fetch("/api/author", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent: chat.agent, directory: chat.directory, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "", messages: chat.messages.map(({ role, text, at }) => ({ role, text, at })), draft: chat.draft ? definitionOnly(chat.draft) : undefined }),
      signal: chatAbort.signal,
    });
    const body = await res.json();
    if (!res.ok) throw new Error(body.error || res.statusText);
    const questions = (body.questions || []).filter((q) => q.text);
    current.messages.push({ role: "agent", agent: body.agent, text: [body.reply, ...questions.map((q) => "• " + q.text)].filter(Boolean).join("\n"), at: new Date().toISOString() });
    current.draft = { ...body.loop, directory: body.loop.directory || current.directory };
    current.questions = questions;
    current.problem = body.problem || "";
    if (current === chat) ui.dirty = true;
  } catch (e) {
    if (e.name !== "AbortError") {
      current.messages.push({ role: "agent", agent: current.agent, text: friendly(e.message), error: true, at: new Date().toISOString() });
      toast(friendly(e.message), "err");
    } else current.messages.pop();
  } finally {
    clearInterval(busyTimer);
    current.busy = false;
    chatAbort = null;
    if (current === chat) {
      render();
      scrollChat();
      $("#chat-input")?.focus();
    }
  }
}
function scrollChat() {
  const m = $("#msgs");
  if (m) m.scrollTop = m.scrollHeight;
}

// ---------- editor state ----------
function editor() {
  const key = editorKey(route);
  if (key !== draftKey) {
    draftKey = key;
    ui.dirty = false;
    ui.step = 0;
    ui.mode = "form";
    if (route.path[0] === "new") {
      const tpl = route.q.template != null ? V.templates()[Number(route.q.template)] : null;
      draft = newDraft(tpl ? structuredClone(tpl.loop) : {});
      if (ctx.importDraft) {
        const keep = ctx.importDraft.id && route.q.from === "chat" ? { id: ctx.importDraft.id, version: ctx.importDraft.version, enabled: ctx.importDraft.enabled } : { id: "", version: 0, enabled: false };
        draft = newDraft({ ...ctx.importDraft, ...keep });
        ctx.importDraft = null;
      }
    } else {
      const l = V.loopById(route.path[1]);
      if (!l) return V.notFound(t("This Loop no longer exists."));
      draft = structuredClone(l);
      draft.allowedTools ||= [];
    }
  }
  return editorPage(draft, ui);
}
function refreshGaps() {
  $("#gaps").innerHTML = gapsPanel(draft);
  const blocked = gaps(draft).some((g) => g.level === "block");
  const save = $('[data-action="save-loop"]');
  if (save) save.disabled = blocked;
  const ct = $("#cron-text");
  if (ct) ct.textContent = "= " + cronText(draft.cron);
  markDirty();
}
function markDirty() {
  if (!ui.dirty) {
    ui.dirty = true;
    const act = $(".sec.top-sec .act");
    if (act && !$(".chip.warn", act)) act.insertAdjacentHTML("afterbegin", `<span class="chip warn">${T("Unsaved changes")}</span>`);
  }
}
function syncJson() {
  const ta = $("#def-json");
  if (!ta) return true;
  try {
    const v = JSON.parse(ta.value);
    draft = { ...draft, ...v, id: draft.id, version: draft.version };
    draft.allowedTools ||= [];
    return true;
  } catch (e) {
    toast(t("JSON is not valid: {msg}", { msg: e.message }), "err");
    return false;
  }
}

// ---------- evidence ----------
const evCache = new Map();
async function loadPreview() {
  const el = $("#ev-preview");
  if (!el || !el.dataset.name) return;
  const key = el.dataset.run + "/" + el.dataset.name;
  try {
    let text = evCache.get(key);
    if (text == null) {
      const res = await fetch(`/api/artifacts/${el.dataset.run}/${encodeURIComponent(el.dataset.name)}?inline=1`);
      if (!res.ok) throw new Error(res.status === 404 ? t("This evidence was pruned or is not available on this device.") : res.statusText);
      text = await res.text();
      evCache.set(key, text);
    }
    if (!document.body.contains(el)) return;
    let shown = text;
    try {
      shown = JSON.stringify(JSON.parse(text), null, 2);
    } catch {}
    if (!el.dataset.full) {
      const lines = shown.split("\n");
      shown = lines.slice(0, 14).join("\n") + (lines.length > 14 ? "\n… " + t("{n} more lines", { n: lines.length - 14 }) : "");
    }
    el.textContent = shown.trim() ? shown : t("(empty output)");
    el.classList.remove("errtext");
  } catch (e) {
    el.textContent = e.message;
    el.classList.add("errtext");
  }
}
async function loadEvents() {
  const el = $("#events");
  if (!el) return;
  try {
    const events = await api(`/runs/${route.path[1]}/events`);
    if (!document.body.contains(el)) return;
    el.innerHTML = events.length
      ? events.map((e) => `<div class="event"><div class="meta"><span class="mono">${esc(e.type)}</span><span class="sub">${esc(new Date(e.time).toLocaleTimeString(ctx.lang, { hour12: false }))}</span></div><pre>${esc(e.text)}</pre></div>`).join("")
      : `<div class="empty"><div class="sub">${T("No events recorded")}</div></div>`;
  } catch (e) {
    el.textContent = e.message;
  }
}

// ---------- toasts ----------
function toast(message, kind = "ok") {
  const box = $("#toasts");
  const el = document.createElement("div");
  el.className = "toast " + kind;
  el.setAttribute("role", kind === "err" ? "alert" : "status");
  el.innerHTML = `${icon(kind === "err" ? "err" : "ok")}<span class="grow">${esc(message)}</span><button type="button" class="ib sm" aria-label="${T("Close")}">×</button>`;
  box.append(el);
  while (box.children.length > 3) box.firstElementChild.remove();
  let left = kind === "err" ? 8000 : 4000,
    timer;
  const start = () => (timer = setTimeout(() => el.remove(), left));
  el.addEventListener("mouseenter", () => clearTimeout(timer));
  el.addEventListener("mouseleave", start);
  el.querySelector("button").addEventListener("click", () => el.remove());
  start();
}

// ---------- dialogs ----------
let dialogOpener = null;
function confirmDialog({ title, body = "", ok = t("Confirm"), cancel = t("Cancel"), danger = false, typed = "", choices = null, input = null }) {
  return new Promise((resolve) => {
    const d = $("#dialog");
    dialogOpener = document.activeElement;
    d.innerHTML = `<form method="dialog" class="dlg"><h2 id="dlg-title">${esc(title)}</h2>${body ? `<div class="dlg-body">${body.startsWith("<") ? body : `<p>${esc(body)}</p>`}</div>` : ""}${choices ? `<div class="choices">${choices.map(([v, l, sub], i) => `<label class="choice"><input type="radio" name="choice" value="${esc(v)}" ${i === 0 ? "checked" : ""}><span><span class="nm">${esc(l)}</span>${sub ? `<span class="sub">${esc(sub)}</span>` : ""}</span></label>`).join("")}</div>` : ""}${input ? input : ""}${typed ? `<input class="inp mt10" id="typed" autocomplete="off" placeholder="${T("Type {text} to confirm", { text: typed })}" aria-label="${T("Type {text} to confirm", { text: typed })}">` : ""}<div class="actions dlg-act"><button type="button" class="btn" value="cancel" data-dlg="cancel">${esc(cancel)}</button><button type="submit" class="btn ${danger ? "danger" : "pri"}" value="ok" data-dlg="ok" ${typed ? 'aria-disabled="true" disabled' : ""}>${esc(ok)}</button></div></form>`;
    d.setAttribute("aria-labelledby", "dlg-title");
    const finish = (v) => {
      d.close();
      d.onclose = null;
      dialogOpener?.focus?.();
      resolve(v);
    };
    d.querySelector('[data-dlg="cancel"]').onclick = () => finish(false);
    const typedEl = d.querySelector("#typed");
    if (typedEl)
      typedEl.oninput = () => {
        const okBtn = d.querySelector('[data-dlg="ok"]');
        const match = typedEl.value === typed;
        okBtn.disabled = !match;
        okBtn.setAttribute("aria-disabled", String(!match));
      };
    d.querySelector("form").onsubmit = (e) => {
      e.preventDefault();
      const choice = d.querySelector('input[name="choice"]:checked')?.value;
      const extra = d.querySelector("[data-input]");
      finish(choices ? choice : extra ? (extra.type === "file" ? extra.files : extra.value) : true);
    };
    d.oncancel = (e) => {
      e.preventDefault();
      finish(false);
    };
    d.showModal();
    (danger ? d.querySelector('[data-dlg="cancel"]') : typedEl || d.querySelector("[data-input]") || d.querySelector('[data-dlg="ok"]'))?.focus();
  });
}

// ---------- menus ----------
let menuOpener = null;
function openMenu(anchor, items) {
  closeMenu();
  const m = $("#menu");
  menuOpener = anchor;
  m.innerHTML = items.map((it) => (it === "-" ? '<hr class="hair">' : `<button type="button" role="menuitem" class="mi ${it.danger ? "danger" : ""} ${it.checked ? "checked" : ""}" data-action="${it.action}" ${Object.entries(it.data || {}).map(([k, v]) => `data-${k}="${esc(v)}"`).join(" ")} ${it.disabled ? "disabled" : ""}>${esc(it.label)}</button>`)).join("");
  m.hidden = false;
  const r = anchor.getBoundingClientRect();
  const w = m.offsetWidth;
  m.style.left = Math.max(8, Math.min(innerWidth - w - 8, r.right - w)) + "px";
  m.style.top = r.bottom + 6 + scrollY + "px";
  anchor.setAttribute("aria-expanded", "true");
  m.querySelector("[role=menuitem]:not([disabled])")?.focus();
}
function closeMenu() {
  const m = $("#menu");
  if (!m || m.hidden) return;
  m.hidden = true;
  menuOpener?.setAttribute("aria-expanded", "false");
}

// ---------- actions ----------
const actions = {
  refresh: async () => {
    setState(await api("/state"));
    ctx.connected = true;
    evCache.clear();
    render();
  },
  "lang-menu": (b) =>
    openMenu(b, [["zh-TW", "繁體中文"], ["zh-CN", "简体中文"], ["en", "English"]].map(([v, l]) => ({ label: (v === ctx.lang ? "✓ " : "　") + l, action: "set", data: { name: "language", value: v } }))),
  set: async (b) => {
    const name = b.dataset.name;
    let value = b.dataset.value;
    if (name === "motion") value = value === "on";
    const rank = (x) => ["read-only", "workspace-write", "workspace-network", "agent-config"].indexOf(x);
    if (name === "maxSandbox" && rank(value) > rank(ctx.state.settings.maxSandbox)) {
      const body = {
        "workspace-write": t("Loops on this device may then let agents change files inside their working directory. Each Loop must still ask for it explicitly."),
        "workspace-network": t("Agents may then also use the network: push to Git remotes, deploy, reach databases and APIs. Each Loop must still ask for it explicitly."),
        "agent-config": t("Loopbee will no longer add sandbox flags for Loops that ask for it; whatever the agent's own configuration allows will apply, possibly full access to this computer."),
      }[value];
      const ok = await confirmDialog({ title: t("Raise the permission limit to “{level}”?", { level: sandboxLabel(value) }), body, ok: t("Allow"), typed: value, danger: value === "agent-config" });
      if (!ok) return;
    }
    await saveSettings({ [name]: value });
    closeMenu();
  },
  "set-toggle": (b) => saveSettings({ [b.dataset.name]: b.getAttribute("aria-checked") !== "true" }),
  "pause-all": async () => {
    const paused = !ctx.state.settings.paused;
    await mutate("/scheduler/pause", { paused });
    toast(t(paused ? "All schedules paused" : "Schedules resumed"));
  },
  start: async (b) => {
    const id = b.dataset.id;
    await mutate(`/loops/${id}/run`);
    const r = V.runsSorted().find((x) => x.loopId === id);
    toast(t("Run started"));
    if (r) go("#/run/" + r.id);
  },
  rerun: async (b) => {
    await mutate(`/runs/${b.dataset.id}/rerun`);
    const r = V.runsSorted().find((x) => x.rerunOf === b.dataset.id);
    toast(t("New run started"));
    if (r) go("#/run/" + r.id);
  },
  ack: async (b) => {
    await mutate(`/runs/${b.dataset.id}/ack`);
    toast(t("Marked as reviewed"));
  },
  "cancel-run": async (b) => {
    const ok = await confirmDialog({ title: t("Cancel this run?"), body: t("Loopbee stops the whole process tree. Files the agent already changed stay as they are; evidence collected so far is kept."), ok: t("Cancel run"), cancel: t("Keep running"), danger: true });
    if (ok) {
      await mutate(`/runs/${b.dataset.id}/cancel`);
      toast(t("Run cancelled"));
    }
  },
  "toggle-loop": async (b) => {
    const l = structuredClone(V.loopById(b.dataset.id));
    if (!l.cron) return toast(t("Add a schedule in the editor first"), "err");
    l.enabled = !l.enabled;
    await mutate("/loops", l);
    toast(t(l.enabled ? "Schedule enabled" : "Schedule paused"));
  },
  "loop-menu": (b) => {
    const l = V.loopById(b.dataset.id);
    const busy = ctx.state.runs.some((r) => r.loopId === l.id && active(r));
    const ro = V.viewer();
    openMenu(b, ro
      ? [{ label: t("View definition"), action: "show-definition", data: { id: l.id } }]
      : [
          { label: t("Run now"), action: "start", data: { id: l.id }, disabled: busy },
          ...(l.cron ? [{ label: t(l.enabled ? "Pause schedule" : "Resume schedule"), action: "toggle-loop", data: { id: l.id } }] : []),
          { label: t("Edit definition"), action: "nav", data: { href: `#/loop/${l.id}/edit` } },
          { label: t("Duplicate as new Loop"), action: "duplicate", data: { id: l.id } },
          { label: t("View definition"), action: "show-definition", data: { id: l.id } },
          "-",
          { label: t("Delete Loop…"), action: "delete-loop", data: { id: l.id }, danger: true, disabled: busy },
        ]);
  },
  nav: (b) => {
    closeMenu();
    go(b.dataset.href);
  },
  duplicate: (b) => {
    const l = structuredClone(V.loopById(b.dataset.id));
    ctx.importDraft = { ...l, name: l.name + " " + t("(copy)"), enabled: false, private: true };
    closeMenu();
    draftKey = "";
    go("#/new?dup=" + Date.now());
  },
  "show-definition": (b) => {
    closeMenu();
    const l = V.loopById(b.dataset.id);
    confirmDialog({ title: t("Definition"), body: `<pre class="defpre">${esc(JSON.stringify(definitionOnly(l), null, 2))}</pre>`, ok: t("Copy JSON") }).then((ok) => ok && copy(JSON.stringify(definitionOnly(l), null, 2)));
  },
  "show-snapshot": (b) => {
    const r = ctx.state.runs.find((x) => x.id === b.dataset.id);
    confirmDialog({ title: t("Definition v{n} (snapshot)", { n: r.definition.version }), body: `<pre class="defpre">${esc(JSON.stringify(definitionOnly(r.definition), null, 2))}</pre>`, ok: t("Copy JSON") }).then((ok) => ok && copy(JSON.stringify(definitionOnly(r.definition), null, 2)));
  },
  "delete-loop": async (b) => {
    closeMenu();
    const l = V.loopById(b.dataset.id);
    const n = ctx.state.runs.filter((r) => r.loopId === l.id).length;
    const ok = await confirmDialog({ title: t("Delete “{name}”?", { name: l.name }), body: t("The definition is removed. Its {n} runs and their evidence stay in history; data already published to the team repository is not affected.", { n }), ok: t("Delete"), danger: true, typed: l.name });
    if (!ok) return;
    await mutate("/loops/" + l.id, undefined, "DELETE");
    toast(t("Loop deleted"));
    go("#/loops");
  },
  retry: async (b) => {
    const d = ctx.state.deliveries.find((x) => x.id === b.dataset.id);
    const ok = await confirmDialog({ title: t("Resend this notification?"), body: d?.status === "unknown" ? t("The previous attempt may already have arrived, so this can create a duplicate. Only the notification is resent; the Loop does not run again.") : t("Only the notification is resent; the Loop does not run again."), ok: t("Resend") });
    if (ok) {
      await mutate("/deliveries/" + b.dataset.id + "/retry");
      toast(t("Resend queued"));
    }
  },
  "check-delivery": async (b) => {
    const d = ctx.state.deliveries.find((x) => x.id === b.dataset.id);
    const v = await confirmDialog({ title: t("Check this notification"), body: t("{channel} · {when}. The request timed out, so Loopbee cannot tell whether it arrived. Please look in {channel}.", { channel: d.channel, when: new Date(d.updated).toLocaleString(ctx.lang) }), choices: [["received", t("It arrived"), t("Leave it as is")], ["resend", t("It did not arrive — resend"), t("May create a duplicate; only the notification is resent")]], ok: t("Confirm") });
    if (v === "resend") {
      await mutate("/deliveries/" + d.id + "/retry");
      toast(t("Resend queued"));
    }
  },
  prune: async () => {
    const ok = await confirmDialog({ title: t("Prune expired runs?"), body: t("Finished runs older than {n} days, their events and evidence files are permanently removed. Running and waiting runs are kept.", { n: ctx.state.settings.retentionDays }), ok: t("Prune"), danger: true });
    if (ok) {
      await mutate("/prune");
      toast(t("Expired runs pruned"));
    }
  },
  import: async () => {
    const src = await confirmDialog({ title: t("Import a Loop definition"), body: t("Paste JSON or YAML. The content is treated as data, never executed; it opens in the editor for checks before saving."), input: `<textarea class="inp mono definition-json" data-input rows="12" aria-label="JSON / YAML"></textarea>`, ok: t("Open in editor") });
    if (!src) return;
    const l = await api("/definition/parse", { source: src });
    ctx.importDraft = l;
    draftKey = "";
    go("#/new?import=" + Date.now());
  },
  "import-archive": async () => {
    const files = await confirmDialog({ title: t("Import archive"), body: t("Imported Loops get new IDs and arrive private and disabled. Existing data is never overwritten."), input: `<input class="inp" type="file" accept=".zip" data-input aria-label="ZIP">`, ok: t("Import") });
    const file = files?.[0];
    if (!file) return;
    const res = await fetch("/api/archive/import", { method: "POST", headers: { "Content-Type": "application/zip" }, body: file });
    if (!res.ok) throw new Error((await res.json()).error);
    setState(await res.json());
    toast(t("Archive imported"));
    go("#/loops");
  },
  "claude-import": async () => {
    const list = await api("/import/claude");
    confirmDialog({ title: t("Claude scheduled tasks (read only)"), body: list.length ? list.map((c) => `<details><summary>${esc(c.name)}</summary><pre class="defpre">${esc(c.raw)}</pre></details>`).join("") : `<p>${T("No scheduled tasks found in ~/.claude/scheduled-tasks.")}</p>`, ok: t("Close") });
  },
  detect: async (b) => {
    b.disabled = true;
    await mutate("/agents/detect");
    toast(t("Agent detection finished"));
  },
  sync: async (b) => {
    b.disabled = true;
    await mutate("/sync");
    toast(t("Team sync finished"));
  },
  "test-notify": async (b) => {
    await mutate("/notify/" + b.dataset.kind + "/test");
    toast(t("Test notification queued"));
  },
  "channel-edit": (b) => {
    ui.channel = ui.channel === b.dataset.kind ? "" : b.dataset.kind;
    render();
  },
  "channel-toggle": async (b) => {
    const kind = b.dataset.kind;
    const channels = structuredClone(ctx.state.settings.channels);
    let ch = channels.find((c) => c.kind === kind);
    if (!ch) channels.push((ch = { kind, enabled: false, endpoint: kind === "bark" ? "https://api.day.app" : "", secret: "", target: "" }));
    ch.enabled = !ch.enabled;
    try {
      await saveSettings({ channels });
    } catch (e) {
      ui.channel = kind;
      render();
      throw e;
    }
  },
  "team-role": (b) => {
    $('#team-form [name="role"]').value = b.dataset.value;
    for (const x of $$('[data-action="team-role"]')) {
      x.classList.toggle("on", x === b);
      x.setAttribute("aria-checked", String(x === b));
    }
  },
  "copy-digest": () => copy($("#digest-text").textContent),
  "copy-artifact": () => copy($("#ev-preview").textContent),
  "copy-diagnostics": async () => copy(JSON.stringify(await api("/diagnostics"), null, 2)),
  "select-node": (b) => {
    const i = Number(b.dataset.index);
    if (route.path[0] === "run") {
      const r = ctx.state.runs.find((x) => x.id === route.path[1]);
      const idx = (r.trace || []).map((x, j) => [x, j]).filter(([x]) => x.step === i).at(-1)?.[1];
      if (idx != null) {
        ui.node = idx;
        render();
      }
    } else if (isEditor(route)) {
      ui.step = i;
      render();
    }
  },
  "select-trace": (b) => {
    ui.node = Number(b.dataset.index);
    render();
  },
  "toggle-events": () => {
    ui.events = !ui.events;
    render();
  },
  "gate-preview": (b) => {
    ctx.gateSel = b.dataset.name;
    render();
  },
  "loops-filter": (b) => setQuery({ filter: b.dataset.value === "all" ? "" : b.dataset.value }),
  "runs-q": (b) => setQuery({ [b.dataset.name]: b.dataset.value === "all" && b.dataset.name === "status" ? "" : b.dataset.value, limit: "" }),
  "art-q": (b) => setQuery({ [b.dataset.name]: b.dataset.value === "all" ? "" : b.dataset.value }),
  "runs-outcome": (b) => setQuery({ outcome: route.q.outcome === b.dataset.value ? "" : b.dataset.value }),
  "clear-filter": () => {
    filters[route.path[0]] = "";
    go("#/" + route.path[0]);
  },
  "show-disabled": () => setQuery({ showDisabled: route.q.showDisabled ? "" : "1" }),
  "more-runs": () => setQuery({ limit: String((Number(route.q.limit) || 50) + 50) }),
  "more-arts": () => setQuery({ limit: String((Number(route.q.limit) || 60) + 60) }),
  // editor
  "editor-mode": (b) => {
    if (b.dataset.value === ui.mode || (b.dataset.value === "form" && ui.mode !== "json")) return;
    if (ui.mode === "json" && !syncJson()) return;
    ui.mode = b.dataset.value;
    render();
  },
  "ed-jump": (b) => $("#" + b.dataset.target)?.scrollIntoView({ behavior: "smooth", block: "start" }),
  "ed-toggle": (b) => {
    const f = b.dataset.f;
    draft[f] = !draft[f];
    b.setAttribute("aria-checked", String(draft[f]));
    refreshGaps();
  },
  "ed-intake": (b) => {
    if (b.dataset.value === "none") delete draft.intake;
    else {
      draft.intake = { kind: "github-issues", ...(draft.intake || {}) };
      draft.workspace = "worktree";
      draft.requireClean = false;
      if (!draft.branch || !draft.branch.includes("{issue}")) draft.branch = "loopbee/issue-{issue}";
    }
    if (b.dataset.value === "none" && draft.branch?.includes("{issue}")) draft.branch = draft.branch.replace("{issue}", "{run}");
    markDirty();
    render();
  },
  "ed-pr": (b) => {
    if (b.dataset.value === "none") delete draft.pullRequest;
    else {
      draft.pullRequest = { ...(draft.pullRequest || {}), mode: b.dataset.value };
      draft.workspace = "worktree";
      draft.requireClean = false;
      draft.branch ||= "loopbee/change-{date}-{run}";
    }
    markDirty();
    render();
  },
  "step-continue": (b) => {
    const s = draft.steps[Number(b.dataset.i)];
    s.continueOnError = !s.continueOnError;
    if (!s.continueOnError) delete s.continueOnError;
    b.setAttribute("aria-checked", String(!!s.continueOnError));
    markDirty();
  },
  "ed-seg": (b) => {
    const name = b.dataset.name;
    draft[name] = name === "catchUp" ? b.dataset.value === "1" : b.dataset.value;
    if (name === "workspace" && draft.workspace === "worktree") draft.requireClean = false;
    if (name === "workspace" && draft.workspace === "direct") delete draft.branch;
    if (name === "workspace" || name === "sandbox") {
      markDirty();
      return render();
    }
    for (const x of b.parentElement.children) {
      x.classList.toggle("on", x === b);
      x.setAttribute("aria-checked", String(x === b));
    }
    refreshGaps();
  },
  "select-step": (b) => {
    ui.step = Number(b.dataset.i);
    render();
  },
  "step-add-menu": (b) =>
    openMenu(b, STEP_KINDS.map((k) => ({ label: kindLabel(k) + `  (${k})`, action: "step-add", data: { kind: k } }))),
  "step-add": (b) => {
    closeMenu();
    const k = b.dataset.kind;
    // One source for step labels (kindLabel) so a new kind cannot be missing here and in the menu.
    const step = { kind: k, name: kindLabel(k) };
    if (k === "verify" || k === "run") step.argv = [];
    else if (k !== "gate") step.prompt = "";
    draft.steps.splice(ui.step + 1, 0, step);
    ui.step = ui.step + 1;
    markDirty();
    render();
    $(`#step-${ui.step}`)?.focus();
  },
  "step-up": (b) => moveStep(Number(b.dataset.i), -1),
  "step-down": (b) => moveStep(Number(b.dataset.i), 1),
  "step-remove": (b) => {
    draft.steps.splice(Number(b.dataset.i), 1);
    ui.step = Math.max(0, Number(b.dataset.i) - 1);
    markDirty();
    render();
  },
  "chat-example": (b) => {
    const input = $("#chat-input");
    input.value = t(EXAMPLES[Number(b.dataset.i)]);
    input.focus();
  },
  "chat-quick": (b) => chatSend(b.dataset.text),
  "chat-agent": (b) => {
    chat.agent = b.dataset.value;
    render();
  },
  "chat-cancel": () => chatAbort?.abort(),
  "chat-form": () => {
    ctx.importDraft = { ...chat.draft, directory: chat.draft.directory || chat.directory, conversation: chat.messages.map(({ role, text, at }) => ({ role, text, at })) };
    ui.dirty = false;
    draftKey = "";
    go(chat.loop ? `#/new?form=1&from=chat&dup=${Date.now()}` : `#/new?form=1&import=${Date.now()}`);
  },
  "chat-save": async (b) => {
    b.disabled = true;
    const d = { ...chat.draft, directory: chat.draft.directory || chat.directory, conversation: chat.messages.map(({ role, text, at }) => ({ role, text, at })) };
    const before = new Set(ctx.state.loops.map((l) => l.id));
    const s = await api("/loops", definitionOnly(d));
    setState(s);
    ui.dirty = false;
    const saved = d.id || s.loops.find((l) => !before.has(l.id))?.id;
    chat = null;
    toast(t(d.id ? "Saved" : "Draft saved with the schedule off"));
    go(saved ? "#/loop/" + saved : "#/loops");
  },
  "copy-links": (b) => copy((ctx.state.runs.find((r) => r.id === b.dataset.id)?.result?.links || []).join("\n")),
  "env-add": () => {
    draft.env = [...(draft.env || []), { name: "", value: "", secret: false }];
    markDirty();
    render();
    $(`[data-env="name"][data-i="${draft.env.length - 1}"]`)?.focus();
  },
  "env-remove": (b) => {
    draft.env.splice(Number(b.dataset.i), 1);
    markDirty();
    render();
  },
  "ed-notify": (b) => {
    draft.notify ||= {};
    const group = b.dataset.group;
    const v = b.dataset.value;
    if (group === "channels") {
      if (!v) draft.notify.channels = [];
      else {
        const set = new Set(draft.notify.channels || []);
        set.has(v) ? set.delete(v) : set.add(v);
        draft.notify.channels = [...set];
      }
    } else {
      const set = new Set(draft.notify.events?.length ? draft.notify.events : ["done", "attention", "failed"]);
      set.has(v) ? set.delete(v) : set.add(v);
      draft.notify.events = [...set];
    }
    markDirty();
    render();
  },
  "ed-notify-report": (b) => {
    draft.notify ||= {};
    draft.notify.includeReport = !draft.notify.includeReport;
    b.setAttribute("aria-checked", String(draft.notify.includeReport));
    markDirty();
  },
  "focus-field": (b) => {
    const f = b.dataset.field;
    if (isChat(route)) return actions["chat-form"]();
    if (f.startsWith("step-")) {
      ui.step = Number(f.slice(5));
      render();
    }
    const el = $(f.startsWith("step-") ? "#" + f : "#f-" + f) || $(`[data-f="${f}"]`);
    el?.scrollIntoView({ behavior: "smooth", block: "center" });
    el?.focus?.();
  },
  "save-loop": async (b) => {
    if (ui.mode === "json" && !syncJson()) return;
    b.disabled = true;
    const before = new Set(ctx.state.loops.map((l) => l.id));
    const s = await api("/loops", definitionOnly(draft));
    setState(s);
    ui.dirty = false;
    const saved = draft.id || s.loops.find((l) => !before.has(l.id))?.id;
    draftKey = "";
    toast(t("Saved"));
    go(saved ? "#/loop/" + saved : "#/loops");
  },
  "finish-onboarding": async (b) => {
    await mutate("/onboarding/done");
    go(b.dataset.href || "#/today");
  },
};
function moveStep(i, dir) {
  const j = i + dir;
  if (j < 0 || j >= draft.steps.length) return;
  [draft.steps[i], draft.steps[j]] = [draft.steps[j], draft.steps[i]];
  ui.step = j;
  markDirty();
  render();
  $(`[data-action="select-step"][data-i="${j}"]`)?.focus();
}
async function copy(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast(t("Copied"));
  } catch {
    toast(t("Copy failed — select the text and copy manually"), "err");
  }
}

// ---------- events ----------
document.addEventListener("click", async (e) => {
  const finishLink = e.target.closest('a[data-action="finish-onboarding"]');
  if (finishLink) e.preventDefault();
  const b = e.target.closest("[data-action]");
  if (!b || b.disabled || b.getAttribute("aria-disabled") === "true") {
    if (!e.target.closest("#menu")) closeMenu();
    const row = e.target.closest(".row[data-href]");
    if (row && !e.target.closest("a,button")) go(row.dataset.href);
    return;
  }
  if (b.tagName === "A" && b.dataset.action !== "ed-jump" && !finishLink) return;
  e.preventDefault();
  const fn = actions[b.dataset.action];
  if (!fn) return;
  if (!b.closest("#menu") && !["lang-menu", "loop-menu", "step-add-menu"].includes(b.dataset.action)) closeMenu();
  try {
    await fn(b);
  } catch (err) {
    toast(friendly(err.message), "err");
    if (document.body.contains(b)) b.disabled = false;
  }
});
function friendly(msg) {
  const map = {
    "definition changed; reload before saving": "This Loop was changed elsewhere. Reload it before saving.",
    "loop or resource lock is already active": "This Loop or another Loop with the same lock is already running.",
    "viewer is read-only": "Viewer mode is read only.",
    "review note required": "A review note is required.",
    "working directory does not exist": "The working directory does not exist on this device.",
    "verification executable must be explicitly allowed": "The verify command is not on the allowed tools list.",
    "workspace write exceeds this device's permission limit": "Workspace write exceeds this device’s permission limit.",
    "agent version unsupported; run detection first": "The agent was not detected. Run detection in Settings › Agents.",
  };
  return map[msg] ? t(map[msg]) : msg;
}
document.addEventListener("input", (e) => {
  const el = e.target;
  if (el.id === "search") {
    const page = route.path[0] === "artifact" ? "artifacts" : route.path[0];
    filters[page] = el.value;
    const pos = el.selectionStart;
    render();
    const s = $("#search");
    s.focus();
    s.setSelectionRange(pos, pos);
    return;
  }
  if (el.id === "chat-dir" && chat) {
    chat.directory = el.value.trim();
    if (chat.draft) chat.draft.directory = chat.directory;
    return;
  }
  if (!draft || !isEditor(route)) return;
  if (el.dataset.intake && draft.intake) {
    const f = el.dataset.intake;
    if (f === "trustedUsers") draft.intake.trustedUsers = el.value.split(/[\s,]+/).filter(Boolean);
    else if (el.value.trim()) draft.intake[f] = el.value.trim();
    else delete draft.intake[f];
    refreshGaps();
    return;
  }
  if (el.dataset.pr && draft.pullRequest) {
    if (el.value.trim()) draft.pullRequest.base = el.value.trim();
    else delete draft.pullRequest.base;
    refreshGaps();
    return;
  }
  if (el.dataset.env) {
    const e = draft.env[Number(el.dataset.i)];
    if (el.dataset.env === "name") e.name = el.value.trim().toUpperCase();
    else if (el.dataset.env === "value") e.value = el.value;
    else {
      e.secret = el.checked;
      e.value = "";
      markDirty();
      render();
      return;
    }
    refreshGaps();
    return;
  }
  if (el.dataset.f) {
    const f = el.dataset.f;
    draft[f] = el.type === "number" ? Number(el.value) : f === "allowedTools" ? el.value.split(/\s+/).filter(Boolean) : el.value;
    refreshGaps();
  } else if (el.dataset.sf) {
    const s = draft.steps[Number(el.dataset.i)];
    const f = el.dataset.sf;
    if (f === "argv") {
      try {
        const v = JSON.parse(el.value || "[]");
        if (!Array.isArray(v) || v.some((x) => typeof x !== "string")) throw 0;
        s.argv = v;
        el.removeAttribute("aria-invalid");
      } catch {
        el.setAttribute("aria-invalid", "true");
      }
    } else if (f !== "kind") s[f] = el.value;
    refreshGaps();
  } else if (el.id === "def-json") markDirty();
});
document.addEventListener("change", async (e) => {
  const el = e.target;
  try {
    if (el.dataset.env === "secret") return;
    if (el.dataset.sf === "kind") {
      const s = draft.steps[Number(el.dataset.i)];
      s.kind = el.value;
      if (el.value === "verify" || el.value === "run") s.argv ||= [];
      if (el.value !== "run") delete s.continueOnError;
      markDirty();
      render();
    } else if (el.dataset.f === "agent") {
      draft.agent = el.value;
      refreshGaps();
    } else if (el.id === "runs-loop") setQuery({ loop: el.value });
    else if (el.id === "runs-source") setQuery({ source: el.value });
    else if (el.id === "art-loop") setQuery({ loop: el.value });
  } catch (err) {
    toast(err.message, "err");
  }
});
document.addEventListener("submit", async (e) => {
  e.preventDefault();
  const form = e.target;
  if (form.closest("dialog")) return;
  const d = new FormData(form);
  const submit = e.submitter;
  if (submit) submit.disabled = true;
  try {
    if (form.id === "gate-form") {
      const approve = submit?.value === "approve";
      const note = String(d.get("note") || "").trim();
      if (!note) {
        $('#gate-form [name="note"]').focus();
        throw new Error(t("A review note is required."));
      }
      if (!approve) {
        const ok = await confirmDialog({ title: t("Send this run back?"), body: t("The run ends as “Sent back”. Nothing is reverted; start a new run after making changes."), ok: t("Send back"), danger: true });
        if (!ok) {
          submit.disabled = false;
          return;
        }
      }
      await mutate(`/runs/${route.path[1]}/gate`, { approve, note });
      toast(t(approve ? "Approved — the run continues" : "Sent back"));
      go(approve ? "#/run/" + route.path[1] : "#/today");
    } else if (form.id === "chat-form") {
      const input = $("#chat-input");
      const text = input.value;
      input.value = "";
      if (submit) submit.disabled = false;
      chatSend(text);
    } else if (form.id === "loop-form") {
      $('[data-action="save-loop"]')?.click();
    } else if (form.id === "digest-form") {
      await saveSettings({ digestCron: d.get("digestCron"), digestTimezone: d.get("digestTimezone") });
      toast(t("Saved"));
    } else if (form.id === "retention-form") {
      await saveSettings({ retentionDays: Number(d.get("retentionDays")) });
      toast(t("Saved"));
    } else if (form.id === "team-form") {
      await saveSettings({ role: d.get("role"), teamRemote: d.get("teamRemote"), teamBranch: d.get("teamBranch") });
      toast(t("Saved"));
    } else if (form.id === "channel-form") {
      const kind = form.dataset.kind;
      const channels = structuredClone(ctx.state.settings.channels);
      let ch = channels.find((c) => c.kind === kind);
      if (!ch) channels.push((ch = { kind, enabled: true, endpoint: "", secret: "", target: "" }));
      for (const k of ["endpoint", "secret", "target"]) if (d.has(k)) ch[k] = String(d.get(k));
      await saveSettings({ channels });
      ui.channel = "";
      render();
      toast(t("Saved"));
    }
  } catch (err) {
    toast(friendly(err.message), "err");
    if (submit && document.body.contains(submit)) submit.disabled = false;
  }
});
document.addEventListener("keydown", (e) => {
  const mod = e.metaKey || e.ctrlKey;
  const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement?.tagName);
  const menu = $("#menu");
  if (!menu.hidden) {
    const items = $$("[role=menuitem]:not([disabled])", menu);
    const i = items.indexOf(document.activeElement);
    if (e.key === "Escape") {
      closeMenu();
      menuOpener?.focus();
      e.preventDefault();
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      items[(i + (e.key === "ArrowDown" ? 1 : -1) + items.length) % items.length]?.focus();
      e.preventDefault();
    } else if (e.key === "Home" || e.key === "End") {
      items[e.key === "Home" ? 0 : items.length - 1]?.focus();
      e.preventDefault();
    }
    return;
  }
  if (mod && e.key === "Enter" && document.activeElement?.id === "chat-input") {
    e.preventDefault();
    $("#chat-form").requestSubmit();
    return;
  }
  if (mod && /^[1-5]$/.test(e.key)) {
    e.preventDefault();
    go("#/" + ["today", "loops", "runs", "artifacts", "digest"][Number(e.key) - 1]);
  } else if (mod && e.key === ",") {
    e.preventDefault();
    go("#/settings/general");
  } else if (mod && e.key.toLowerCase() === "n" && !V.viewer()) {
    e.preventDefault();
    go("#/new");
  } else if (mod && e.key.toLowerCase() === "r") {
    e.preventDefault();
    actions.refresh().catch(() => {});
  } else if (e.key === "/" && !typing && $("#search")) {
    e.preventDefault();
    $("#search").focus();
  } else if (e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown") && isEditor(route) && ui.mode === "form") {
    e.preventDefault();
    moveStep(ui.step, e.key === "ArrowUp" ? -1 : 1);
  } else if ((e.key === "ArrowLeft" || e.key === "ArrowRight") && document.activeElement?.closest?.(".seg")) {
    const items = $$("button", document.activeElement.closest(".seg"));
    const i = items.indexOf(document.activeElement);
    items[(i + (e.key === "ArrowRight" ? 1 : -1) + items.length) % items.length]?.focus();
    e.preventDefault();
  }
});
window.addEventListener("beforeunload", (e) => {
  if (ui.dirty) {
    e.preventDefault();
    e.returnValue = "";
  }
});
window.addEventListener("hashchange", onHashChange);
document.addEventListener("visibilitychange", () => document.body.classList.toggle("paused", document.hidden));
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", render);

// ---------- boot & polling ----------
function busy() {
  if ($("#dialog").open || !$("#menu").hidden || ui.channel) return true;
  if (isEditor(route) || isChat(route)) return true;
  const a = document.activeElement;
  return a && a.id !== "search" && /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName) && !!a.closest("main");
}
async function poll() {
  if (document.hidden) return;
  try {
    const s = await api("/state");
    const was = ctx.connected;
    ctx.connected = true;
    if (!was) toast(t("Reconnected"));
    const runningBefore = new Map((ctx.state?.runs || []).map((r) => [r.id, r.status]));
    setState(s);
    const watching = route.path[0] === "run" && s.runs.find((r) => r.id === route.path[1]);
    if (watching && runningBefore.get(watching.id) && runningBefore.get(watching.id) !== watching.status) {
      $("#live").textContent = `${watching.definition.name}: ${t(watching.status)}`;
    }
    if (!busy()) {
      const y = scrollY;
      render();
      scrollTo(0, y);
    }
  } catch (e) {
    if (e.status === 401) location.reload();
    if (ctx.connected) {
      ctx.connected = false;
      if (!busy()) render();
    }
  }
}
async function boot() {
  try {
    ctx.lang = localStorage.getItem("language") || ctx.lang;
  } catch {}
  try {
    const token = new URLSearchParams(location.hash.replace(/^#\/?/, "")).get("token");
    if (token) {
      await api("/session", { token });
      history.replaceState(null, "", location.pathname + "#/today");
    }
    setState(await api("/state"));
    const s = ctx.state;
    if (!s.settings.onboarded && !s.loops.length && s.settings.role === "executor" && (!location.hash || location.hash === "#/today")) {
      history.replaceState(null, "", location.pathname + "#/onboarding/0");
    }
    route = parseHash();
    lastHash = location.hash;
    render();
    setInterval(poll, 3000);
    setInterval(() => {
      if (!busy() && ctx.state.runs.some(active)) render();
    }, 60000);
  } catch {
    $("#app").innerHTML = `<div class="card mt14"><div class="empty"><span class="mark xl" aria-hidden="true"></span><div class="nm">${T("Loopbee is not connected")}</div><div class="sub">${T("Open Loopbee using launch-url.txt in the application data directory.")}</div></div></div>`;
  }
}
boot();
