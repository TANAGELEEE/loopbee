import { chromium } from "playwright";
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm, mkdir } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import assert from "node:assert/strict";
import { uiFlow } from "./ui-flow.mjs";

assert.equal(
  process.platform,
  "win32",
  "desktop E2E requires Windows and WebView2",
);
const data = await mkdtemp(join(tmpdir(), "Loopbee desktop 中文 "));
const executable = resolve(
  process.env.LOOPBEE_DESKTOP_EXE || ".test-data/loopbee-desktop-test.exe",
);
const errors = [];
let child;
let browser;
let page;
const delay = (ms) => new Promise((done) => setTimeout(done, ms));

async function startDesktop() {
  const portServer = createServer();
  await new Promise((done, reject) => {
    portServer.once("error", reject);
    portServer.listen(0, "127.0.0.1", done);
  });
  const port = portServer.address().port;
  await new Promise((done) => portServer.close(done));
  let launchError;
  child = spawn(executable, ["-data", data], {
    windowsHide: true,
    stdio: "ignore",
    env: {
      ...process.env,
      LOOPBEE_TEST_CDP_PORT: String(port),
    },
  });
  child.on("error", (error) => {
    launchError = error;
  });
  const endpoint = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    if (launchError) throw launchError;
    assert.equal(
      child.exitCode,
      null,
      "Windows desktop exited before WebView2 started",
    );
    try {
      const response = await fetch(`${endpoint}/json/version`, {
        signal: AbortSignal.timeout(1000),
      });
      if (response.ok) break;
    } catch {}
    await delay(100);
  }
  browser = await chromium.connectOverCDP(endpoint, { timeout: 10000 });
  while (Date.now() < deadline) {
    page = browser
      .contexts()
      .flatMap((context) => context.pages())
      .find((candidate) =>
        /^http:\/\/127\.0\.0\.1:\d+\//.test(candidate.url()),
      );
    if (page) break;
    await delay(100);
  }
  assert.ok(page, "actual desktop WebView2 did not load the Loopbee UI");
  page.on("pageerror", (error) => errors.push(error.message));
  page.setDefaultTimeout(10000);
  await page.locator("#language").waitFor();
}

async function stopDesktop() {
  if (child?.pid && child.exitCode === null) {
    const stopped = new Promise((done) => child.once("exit", done));
    const killer = spawn(
      "taskkill.exe",
      ["/PID", String(child.pid), "/T", "/F"],
      {
        windowsHide: true,
        stdio: "ignore",
      },
    );
    await new Promise((done, reject) => {
      killer.once("error", reject);
      killer.once("exit", done);
    });
    await stopped;
  }
  await browser?.close();
  child = browser = page = undefined;
}

async function api(path, body) {
  return page.evaluate(
    async ({ path, body }) => {
      const response = await fetch(
        `/api${path}`,
        body === undefined
          ? {}
          : {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify(body),
            },
      );
      if (!response.ok)
        throw new Error(
          `API ${path}: ${response.status} ${await response.text()}`,
        );
      return response.json();
    },
    { path, body },
  );
}

async function waitForRun(id, status) {
  await page.waitForFunction(
    async ({ id, status }) => {
      const state = await (await fetch("/api/state")).json();
      return state.runs.some((run) => run.id === id && run.status === status);
    },
    { id, status },
  );
}

try {
  await startDesktop();
  const { gateLoop, verifyRun } = await uiFlow(page, { data, node: process.execPath, name: "Desktop <safe> 中文" });
  let state;

  state = await api(`/loops/${gateLoop.id}/run`, {});
  const cancelRun = state.runs.find(
    (run) => run.loopId === gateLoop.id && !run.finished,
  );
  await waitForRun(cancelRun.id, "waiting");
  await page.getByRole("link", { name: "Runs", exact: true }).first().click();
  await page.locator(`a[href="#/run/${cancelRun.id}"]`).click();
  await page.getByRole("button", { name: "Cancel this run" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Cancel run" }).click();
  await waitForRun(cancelRun.id, "cancelled");

  state = await api(`/loops/${gateLoop.id}/run`, {});
  const interruptedRun = state.runs.find(
    (run) => run.loopId === gateLoop.id && !run.finished,
  );
  await waitForRun(interruptedRun.id, "waiting");
  await page.getByRole("link", { name: "Today", exact: true }).click();
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await page.locator(".kpis").waitFor();
  await mkdir("docs/evidence", { recursive: true });
  await page.screenshot({
    path: "docs/evidence/windows-desktop.png",
    fullPage: true,
  });
  assert.deepEqual(errors, [], "desktop script errors before restart");
  await stopDesktop();
  await startDesktop();
  state = await api("/state");
  assert.equal(
    state.runs.find((run) => run.id === interruptedRun.id)?.status,
    "interrupted",
  );
  assert.equal(
    state.runs.find((run) => run.id === verifyRun.id)?.status,
    "completed",
  );
  assert.equal(state.settings.theme, "dark");
  assert.equal(state.settings.language, "en");
  assert.ok(
    await page
      .locator("body")
      .evaluate((body) => body.classList.contains("dark")),
  );
  assert.ok(
    state.loops.some(
      (loop) => loop.id === gateLoop.id && loop.name === "Desktop <safe> 中文",
    ),
  );
  assert.ok(
    await page
      .locator(".brand .mark")
      .evaluate((mark) => mark.getBoundingClientRect().width > 0),
  );
  assert.deepEqual(errors, [], "desktop script errors after restart");
  console.log(
    "PASS: Windows WebView2 desktop; Unicode path; create, run, approve, cancel; verify artifact download; three languages and theme; interrupted recovery and persistence; no script errors",
  );
} finally {
  await stopDesktop();
  await rm(data, {
    recursive: true,
    force: true,
    maxRetries: 20,
    retryDelay: 250,
  });
}
