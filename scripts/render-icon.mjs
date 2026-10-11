import { chromium } from "playwright";
import { readFile } from "node:fs/promises";
const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage({
    viewport: { width: 512, height: 512 },
    deviceScaleFactor: 1,
  });
  const svg = await readFile("internal/ui/web/bee.svg", "utf8");
  await page.setContent(
    `<style>body{margin:0;background:transparent;display:grid;place-items:center;height:512px}svg{width:440px;height:440px}</style>${svg}`,
  );
  await page.screenshot({
    path: "internal/ui/web/bee.png",
    omitBackground: true,
  });
} finally {
  await browser.close();
}
