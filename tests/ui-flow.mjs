// End-to-end UI flow shared by the browser (nogui) and Windows WebView2 desktop tests.
// It drives the real interface: onboarding, editor, run, gate review, evidence, settings, languages.
import assert from "node:assert/strict";

const inPage = (page, path, body, method) =>
  page.evaluate(
    async ({ path, body, method }) => {
      const r = await fetch("/api" + path, {
        method: method || (body === undefined ? "GET" : "POST"),
        headers: body === undefined ? {} : { "Content-Type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (!r.ok) throw new Error(`API ${path}: ${r.status} ${await r.text()}`);
      return r.json();
    },
    { path, body, method },
  );

export async function setLanguage(page, lang) {
  await page.locator("#language").click();
  const label = { en: "English", "zh-TW": "繁體中文", "zh-CN": "简体中文" }[lang];
  await page.locator("#menu [role=menuitem]", { hasText: label }).click();
  await page.waitForFunction((l) => document.documentElement.lang === l, lang);
}

export async function uiFlow(page, { data, node, name = "Smoke <safe>" }) {
  // First launch shows onboarding; switch to English there and skip it.
  await page.locator(".ob-head").waitFor();
  await page.locator('[data-action="lang-menu"]').click();
  await page.locator("#menu [role=menuitem]", { hasText: "English" }).click();
  await page.getByRole("heading", { name: "Welcome to Loopbee" }).waitFor();
  await page.getByRole("button", { name: "Skip setup" }).click();
  await page.getByRole("heading", { name: "Create your first Loop" }).waitFor();

  // Create a Loop in the form editor (the chat is covered by agents-e2e.mjs).
  await page.getByRole("link", { name: "New Loop", exact: true }).first().click();
  await page.locator('a[href="#/new?form=1"]').first().click();
  await page.locator("#f-name").fill(name);
  await page.locator("#f-directory").fill(data);
  await page.locator('[data-f="requireClean"]').click();
  await page.locator('[data-action="select-step"][data-i="0"]').click();
  await page.locator('[name="kind-0"]').selectOption("gate");
  const save = page.getByRole("button", { name: "Save Loop", exact: true });
  await assert.doesNotReject(save.waitFor());
  assert.equal(await save.isDisabled(), false, "editor reports blocking gaps for a valid draft");
  await save.click();
  await page.locator(".hd h1", { hasText: name }).waitFor();

  // Run it, review at the gate and approve with a note.
  await page.getByRole("button", { name: "Run now", exact: true }).first().click();
  await page.getByRole("link", { name: "Go to review" }).click();
  await page.locator("#gate-form").waitFor();
  await page.getByRole("button", { name: "Approve and continue" }).click();
  await page.getByText("A review note is required.").waitFor(); // note is mandatory
  await page.locator('#gate-form [name="note"]').fill("E2E reviewed evidence");
  await page.getByRole("button", { name: "Approve and continue" }).click();
  await page.waitForFunction(() => document.querySelector(".hd .chip")?.textContent.includes("Done"));
  const gateLoop = (await inPage(page, "/state")).loops.find((l) => l.name === name);

  // Edit the definition and save a new version.
  await page.goto(page.url().split("#")[0] + "#/loop/" + gateLoop.id + "/edit");
  await page.locator('[data-f="description"]').fill("Updated definition");
  await page.getByRole("button", { name: "Save as v2" }).click();
  await page.locator(".hd .chip", { hasText: "Version v2" }).waitFor();

  // A verification Loop with real evidence, created through the API.
  let state = await inPage(page, "/loops", {
    ...gateLoop, id: "", version: 0, name: "Verification evidence", private: false, directory: data,
    steps: [{ kind: "verify", name: "Verify output", argv: [node, "-e", 'console.log("verified evidence")'] }], allowedTools: [node],
  });
  const verifyLoop = state.loops.find((l) => l.name === "Verification evidence");
  state = await inPage(page, `/loops/${verifyLoop.id}/run`, {});
  const verifyRun = state.runs.find((r) => r.loopId === verifyLoop.id);
  await page.waitForFunction(async (id) => (await (await fetch("/api/state")).json()).runs.some((r) => r.id === id && r.status === "completed"), verifyRun.id);

  // Run details show the round timeline and the evidence preview.
  await page.goto(page.url().split("#")[0] + "#/run/" + verifyRun.id);
  await page.locator(".round .step").first().waitFor();
  await page.waitForFunction(() => document.querySelector("#ev-preview")?.textContent.includes("verified evidence"));

  // Artifact library: open and download.
  await page.getByRole("link", { name: "Artifacts", exact: true }).click();
  await page.locator(".alist a.row").first().click();
  await page.waitForFunction(() => document.querySelector("#ev-preview")?.textContent.length > 0 && !document.querySelector("#ev-preview").textContent.includes("Loading"));
  const download = page.waitForEvent("download");
  await page.getByRole("link", { name: "Download", exact: true }).click();
  assert.equal(await (await download).failure(), null);

  // Settings apply immediately: theme and languages.
  await page.getByRole("link", { name: "Settings" }).click();
  await page.getByRole("radio", { name: "Dark" }).click();
  await page.waitForFunction(() => document.body.classList.contains("dark"));
  await setLanguage(page, "zh-CN");
  await page.getByRole("link", { name: "通用", exact: true }).waitFor();
  await setLanguage(page, "zh-TW");
  await page.getByRole("link", { name: "一般", exact: true }).waitFor();
  await setLanguage(page, "en");
  return { gateLoop, verifyLoop, verifyRun };
}
