// Paths are chosen, not typed: in the desktop app every path field has a button that opens the system's chooser.
// The headless core stands in for the desktop with LOOPBEE_PICK_STUB, which answers every chooser with `chosen`.
// Called from tests/e2e.mjs (language English); the chat's folder field is checked in tests/agents-e2e.mjs.
import assert from "node:assert/strict";

export async function pickFlow(page, base, chosen) {
  // Form: the working directory, a read path, and copied files, which must lie inside the working directory.
  await page.goto(base + "/#/new?form=1");
  await page.reload();
  const field = (id) => page.locator(`.pathpick:has(#${id})`).getByRole("button");
  await field("f-directory").click();
  await page.waitForFunction((p) => document.querySelector("#f-directory")?.value === p, chosen);
  await field("f-readPaths").click();
  await page.waitForFunction((p) => document.querySelector("#f-readPaths")?.value === p, chosen);
  await field("f-readPaths").click();
  assert.equal(await page.locator("#f-readPaths").inputValue(), chosen, "adding the same path twice repeats it");
  const worktree = page.getByRole("radio", { name: "Fresh git worktree" });
  if (await worktree.count()) await worktree.click();
  if (await page.locator("#f-copyFiles").count()) {
    await field("f-copyFiles").click();
    await page.getByText("Choose a file inside the working directory").waitFor();
    assert.equal(await page.locator("#f-copyFiles").inputValue(), "");
  }
  // Leave the form without saving: the editor asks first.
  await page.goto(base + "/#/today");
  await page.getByRole("dialog").getByRole("button", { name: "Discard changes" }).click();
  await page.waitForFunction(() => location.hash === "#/today");
}
