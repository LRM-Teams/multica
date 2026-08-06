const puppeteer = require("/tmp/node_modules/puppeteer-core");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const exe =
  "/home/andong3/.cache/ms-playwright/chromium_headless_shell-1234/chrome-headless-shell-linux64/chrome-headless-shell";
const base = "http://127.0.0.1:5199/";

(async () => {
  const browser = await puppeteer.launch({
    executablePath: exe,
    args: ["--no-sandbox", "--disable-setuid-sandbox", "--force-device-scale-factor=1"],
  });
  const page = await browser.newPage();
  await page.setViewport({ width: 1600, height: 900, deviceScaleFactor: 1 });
  page.on("console", (m) => {
    if (m.type() === "error") console.log("CONSOLE ERROR:", m.text());
  });
  page.on("pageerror", (e) => console.log("PAGE ERROR:", e.message));

  await page.goto(base, { waitUntil: "networkidle0" });
  await page.waitForSelector('[data-testid="node-card"]', { timeout: 20000 });
  await sleep(900);
  await page.screenshot({ path: "shots-100-fullpage.png", fullPage: true });
  console.log("took shots-100-fullpage.png");

  for (const z of ["0.4", "1", "1.6"]) {
    await page.goto(base + "?zoom=" + z, { waitUntil: "networkidle0" });
    await page.waitForSelector('[data-testid="node-card"]', { timeout: 20000 });
    await sleep(700);
    const el = await page.$(`[data-zoom-section="${z}"]`);
    if (el) {
      await el.screenshot({ path: `shots-${z}-grid.png` });
      console.log(`took shots-${z}-grid.png`);
    } else {
      console.log("missing zoom section", z);
    }
  }

  await browser.close();
  console.log("done");
})();
