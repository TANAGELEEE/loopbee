// The sidebar (magpie's): shown by default in a wide window, hidden and shown again from the header's button or
// ⌃⌘S / Ctrl+Shift+S, the choice kept, the switch animated as one view transition, never discarding what was typed,
// and no horizontal scrolling in either layout. Called from tests/e2e.mjs after onboarding.
import assert from "node:assert/strict";

export async function sidebarFlow(page, base, api) {
  const s = await api("/state");
  await api("/settings", { ...s.settings, language: "en" });
  const side = () => page.evaluate(() => document.body.classList.contains("side-nav"));
  const toggle = page.locator("#sideToggle");
  await page.setViewportSize({ width: 1280, height: 820 });
  await page.evaluate(() => localStorage.removeItem("loopbee.navLayout"));
  await page.goto(base + "/#/loops");
  await page.reload();
  await page.locator("#app > *").first().waitFor();

  // Wide window, nothing picked: the sidebar, the header saying where the person is, Settings only at its foot.
  assert.ok(await side(), "a wide window opens with the sidebar");
  assert.ok(await page.locator("#sidebar").isVisible());
  assert.equal(await page.locator(".top .nav").count(), 0, "no header tabs beside the sidebar");
  assert.equal(await page.locator(".top .crumb b").textContent(), "Loops");
  assert.equal(await page.getByRole("link", { name: "Settings" }).count(), 1, "one Settings link");
  assert.equal(await toggle.getAttribute("aria-pressed"), "true");
  assert.equal(await toggle.getAttribute("aria-label"), "Hide sidebar");
  await page.locator("#sidebar").getByRole("link", { name: "Runs", exact: true }).click();
  await page.waitForFunction(() => location.hash === "#/runs" && document.querySelector('#sidebar [aria-current="page"]')?.textContent === "Runs");

  // The button hides it as one motion (a view transition where the browser has them).
  const morphs = await page.evaluate(() => {
    const b = document.querySelector("#sideToggle");
    b.focus();
    b.click();
    return !!document.startViewTransition && document.documentElement.classList.contains("nav-morph");
  });
  assert.ok(morphs, "the layouts trade places in a view transition");
  await page.waitForFunction(() => !document.documentElement.classList.contains("nav-morph"));
  assert.ok(!(await side()));
  assert.ok(await page.locator("#sidebar").isHidden());
  assert.ok(await page.locator(".top .nav").isVisible(), "the pages are back in the header");
  assert.equal(await toggle.getAttribute("aria-label"), "Show sidebar");
  assert.equal(await page.evaluate(() => document.activeElement?.id), "sideToggle", "focus stays on the button");

  // The choice is kept, and the keyboard brings it back.
  await page.reload();
  await page.locator("#app > *").first().waitFor();
  assert.ok(!(await side()), "hidden stays hidden after a reload");
  await page.keyboard.press(process.platform === "darwin" ? "Control+Meta+KeyS" : "Control+Shift+KeyS");
  await page.waitForFunction(() => document.body.classList.contains("side-nav"));

  // Switching never redraws the page: what is typed stays.
  await page.goto(base + "/#/new?form=1");
  await page.locator("#f-name").fill("Typed before the switch");
  await toggle.click();
  await page.waitForFunction(() => !document.body.classList.contains("side-nav"));
  assert.equal(await page.locator("#f-name").inputValue(), "Typed before the switch");
  await toggle.click();
  await page.waitForFunction(() => document.body.classList.contains("side-nav"));

  // Either layout at the minimum window has no horizontal scrolling; a phone gets the tabs and no button.
  for (const pick of ["side", "top"]) {
    await page.evaluate((p) => localStorage.setItem("loopbee.navLayout", p), pick);
    for (const w of [1280, 1024, 960]) {
      await page.setViewportSize({ width: w, height: 700 });
      for (const hash of ["#/today", "#/loops", "#/runs", "#/artifacts", "#/new?form=1", "#/settings/general"]) {
        await page.goto(base + "/" + hash);
        await page.reload();
        await page.locator("#app > *").first().waitFor();
        await page.waitForTimeout(100);
        assert.equal(await side(), pick === "side", `${pick} at ${w}px`);
        const over = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
        assert.ok(over <= 0, `${hash} overflows by ${over}px at ${w}px with the ${pick} layout`);
      }
    }
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(base + "/#/today");
  await page.reload();
  await page.locator("#app > *").first().waitFor();
  assert.ok(!(await side()), "a phone has the header's tabs");
  assert.equal(await toggle.count(), 0, "and no sidebar button");
  await page.evaluate(() => localStorage.removeItem("loopbee.navLayout"));
  await page.setViewportSize({ width: 1280, height: 820 });
}
