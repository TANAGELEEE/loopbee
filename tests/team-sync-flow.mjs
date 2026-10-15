// Team sync without a button: setting the repository publishes at once and Settings › Team says when; a failing sync
// shows a banner that leads to the setting; a viewer gets the board without pressing “Pull now”. Called from
// tests/e2e.mjs after onboarding (language English).
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startCore, waitFor } from "./demo.mjs";

export async function teamSyncFlow(page, core, env) {
  const root = await mkdtemp(join(tmpdir(), "loopbee-team-"));
  const remote = join(root, "team.git");
  execFileSync("git", ["init", "-q", "--bare", remote]);
  const setRemote = async (api, extra) => {
    const st = await api("/state");
    await api("/settings", { ...st.settings, ...extra });
  };
  try {
    // An unreachable repository: the sync fails at once and every page says so, with the way to the setting.
    await setRemote(core.api, { teamRemote: join(root, "missing.git") });
    await waitFor(core.api, (st) => st.team.error);
    await page.goto(core.base + "/#/today");
    await page.locator("#team-banner", { hasText: "Team sync failed" }).waitFor();
    await page.locator("#team-banner").getByRole("link", { name: "Team sync" }).click();
    await page.waitForFunction(() => location.hash === "#/settings/team");
    await page.locator("#team-error", { hasText: "Last sync failed" }).waitFor();

    // A working repository: published at once, no button pressed; the banner goes away.
    await setRemote(core.api, { teamRemote: remote });
    await waitFor(core.api, (st) => st.team.last && !st.team.error);
    const published = execFileSync("git", ["-C", remote, "ls-tree", "-r", "--name-only", "loopbee"], { encoding: "utf8" });
    assert.match(published, /^loopbee\/[0-9a-f]+\.json$/m, "setting the repository published the snapshot");
    await page.reload();
    await page.locator("#team-status", { hasText: "last published" }).waitFor();
    assert.equal(await page.locator("#team-banner").count(), 0, "the failure banner stays after a successful sync");

    // A viewer sees the executor's Loops without “Pull now”.
    const name = (await core.api("/state")).loops.find((l) => !l.private)?.name;
    assert.ok(name, "the executor has a published Loop");
    const viewer = await startCore(undefined, env);
    try {
      await setRemote(viewer.api, { language: "en", role: "viewer", teamRemote: remote });
      await waitFor(viewer.api, (st) => st.loops.some((l) => l.name === name));
      await page.goto(viewer.url);
      await page.goto(viewer.base + "/#/settings/team");
      await page.locator("#team-status", { hasText: "Pulls by itself" }).waitFor();
      await page.locator("#team-status", { hasText: "last pulled" }).waitFor();
    } finally {
      await viewer.stop();
      await rm(viewer.data, { recursive: true, force: true });
    }
  } finally {
    await setRemote(core.api, { teamRemote: "" });
    await page.goto(core.url);
    await rm(root, { recursive: true, force: true });
  }
}
