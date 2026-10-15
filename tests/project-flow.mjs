// Projects on the boards: Loops in two repositories are grouped on the Loops page and filtered on Today and Runs,
// the editor shows the project loopbee derived from the origin remote, and a viewer filters the team board the same
// way. Called from tests/e2e.mjs after onboarding (language English).
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startCore, waitFor } from "./demo.mjs";

export async function projectFlow(page, core, env) {
  const node = process.execPath;
  const root = await mkdtemp(join(tmpdir(), "loopbee-projects-"));
  const repo = (name, remote) => {
    const dir = join(root, name);
    execFileSync("git", ["init", "-q", dir]);
    execFileSync("git", ["-C", dir, "remote", "add", "origin", remote]);
    return dir;
  };
  const loop = (name, directory) => ({
    id: "", version: 0, name, description: "", directory, agent: "codex", sandbox: "read-only", cron: "", timezone: "UTC",
    catchUp: false, enabled: false, private: false, requireClean: false, lock: "", doneWhen: "verify.passed", maxRounds: 1,
    wallMinutes: 5, stallRounds: 1, allowedTools: [node], steps: [{ kind: "verify", name: "Check", argv: [node, "-e", "1"] }],
  });
  const save = async (l) => (await core.api("/loops", l)).loops.find((x) => x.name === l.name);
  // The remote's user and token never reach the project name.
  const api = await save(loop("API nightly", repo("api", "https://bot:ghp_notarealtoken@github.com/acme/api.git")));
  const web = await save(loop("Web nightly", repo("web", "git@github.com:acme/web.git")));
  assert.equal(api.project, "acme/api");
  assert.equal(web.project, "acme/web");
  for (const l of [api, web]) {
    const started = await core.api(`/loops/${l.id}/run`, {});
    const run = started.runs.find((r) => r.loopId === l.id);
    await waitFor(core.api, (st) => st.runs.find((r) => r.id === run.id && r.status === "completed"));
  }
  const done = page.locator('[data-col="done"]');

  // Today, all projects: each card says its project. Picking one keeps only its Runs, also behind the KPI links.
  await page.goto(core.base + "/#/today");
  await done.getByText("API nightly").first().waitFor();
  await done.getByText("Web nightly").first().waitFor();
  await done.locator(".bcard", { hasText: "API nightly" }).first().getByText("acme/api").waitFor();
  await page.locator("#today-project").selectOption("acme/api");
  await page.waitForFunction(() => location.hash === "#/today?project=acme%2Fapi");
  await done.getByText("API nightly").first().waitFor();
  assert.equal(await done.getByText("Web nightly").count(), 0, "Today filtered to acme/api still shows acme/web");
  assert.equal(await done.locator(".bcard", { hasText: "API nightly" }).first().getByText("acme/api").count(), 0, "a filtered board repeats the project on every card");
  assert.match(await page.locator(".kpis a", { hasText: "Today’s runs" }).getAttribute("href"), /project=acme%2Fapi/);

  // Loops: one section per project; the picker narrows it to one.
  await page.goto(core.base + "/#/loops?showDisabled=1");
  await page.locator(".sec h2", { hasText: "acme/api" }).waitFor();
  await page.locator(".sec h2", { hasText: "acme/web" }).waitFor();
  await page.locator("#loops-project").selectOption("acme/web");
  await page.getByRole("link", { name: "Web nightly" }).waitFor();
  assert.equal(await page.getByRole("link", { name: "API nightly" }).count(), 0, "Loops filtered to acme/web still lists acme/api");

  // Runs: the project filter counts as a filter and Clear removes it.
  await page.goto(core.base + "/#/runs?range=today");
  await page.locator("#runs-project").selectOption("acme/api");
  await page.getByText("1 filters applied").waitFor();
  assert.equal(await page.locator(".grp a", { hasText: "Web nightly" }).count(), 0, "Runs filtered to acme/api still lists acme/web");
  await page.getByRole("button", { name: "Clear", exact: true }).click();
  await page.waitForFunction(() => location.hash === "#/runs");

  // The editor shows the derived project and can rename it.
  await page.goto(core.base + `/#/loop/${api.id}/edit`);
  assert.equal(await page.locator("#f-project").inputValue(), "acme/api");

  // A viewer filters the team board the same way: the project travels with the snapshot, the folder does not.
  const remote = join(root, "team.git");
  execFileSync("git", ["init", "-q", "--bare", remote]);
  let st = await core.api("/state");
  await core.api("/settings", { ...st.settings, teamRemote: remote });
  await core.api("/sync", {});
  const viewer = await startCore(undefined, env);
  try {
    st = await viewer.api("/state");
    await viewer.api("/settings", { ...st.settings, language: "en", role: "viewer", teamRemote: remote });
    await viewer.api("/sync", {});
    await page.goto(viewer.url);
    await page.goto(viewer.base + "/#/today");
    await page.locator("#today-project").selectOption("acme/web");
    await done.getByText("Web nightly").first().waitFor();
    assert.equal(await done.getByText("API nightly").count(), 0, "the viewer's board filtered to acme/web shows acme/api");
  } finally {
    await viewer.stop();
    await rm(viewer.data, { recursive: true, force: true });
    st = await core.api("/state");
    await core.api("/settings", { ...st.settings, teamRemote: "" });
    await page.goto(core.url);
  }
  return { root };
}
