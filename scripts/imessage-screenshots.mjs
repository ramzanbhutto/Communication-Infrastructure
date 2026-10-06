import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
const require = createRequire(new URL("../web/package.json", import.meta.url));
const { chromium } = require("@playwright/test");
const origin = new URL(process.env.OPS_DEMO_URL || "http://127.0.0.1:2061");
if (!["127.0.0.1", "localhost"].includes(origin.hostname))
  throw new Error("Only a local demo may be captured.");
const folder = fileURLToPath(new URL("../docs/screenshots/", import.meta.url));
await mkdir(folder, { recursive: true });
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1080 } });
const errors = [];
page.on("pageerror", (error) => errors.push(error.message));
page.on("console", (entry) => {
  if (entry.type() === "error") errors.push(entry.text());
});
try {
  const login = await page.request.post(`${origin.origin}/api/v1/session`, {
    data: { userId: "demo-operator" },
    headers: { Origin: origin.origin },
  });
  if (!login.ok()) throw new Error("Demo login failed.");
  await page.goto(`${origin.origin}/#infrastructure`);
  await page.getByLabel("Channel or resource").selectOption("imessage");
  await page
    .getByRole("button", { name: "Check iMessage compatibility", exact: true })
    .click();
  await page
    .getByText("Existing iMessage chat verified", { exact: true })
    .waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: `${folder}/imessage.png`,
    fullPage: true,
    animations: "disabled",
  });
  if (process.argv.includes("--design-reference")) {
    const css = await readFile(
      new URL("../web/src/styles.css", import.meta.url),
      "utf8",
    );
    const font = await readFile(
      new URL("../web/public/fonts/manrope.ttf", import.meta.url),
    );
    const html = await page.locator("#root").innerHTML();
    const style = css.replace(
      "url('/fonts/manrope.ttf')",
      `url('data:font/ttf;base64,${font.toString("base64")}')`,
    );
    const target = new URL("../.superdesign/tmp/", import.meta.url);
    await mkdir(target, { recursive: true });
    await writeFile(
      new URL("imessage.html", target),
      `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Optional iMessage</title><style>${style}</style></head><body><div>${html.replace(/<a /g, '<a id="workspace-skip" ')}</div></body></html>`,
    );
  }
  await page.setViewportSize({ width: 390, height: 844 });
  if (
    await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
  )
    throw new Error("Mobile document overflow.");
  await page.screenshot({
    path: `${folder}/imessage-mobile.png`,
    fullPage: true,
    animations: "disabled",
  });
  if (errors.length) throw new Error(errors.join("\n"));
  console.log(
    "Captured optional iMessage at desktop and mobile widths without browser errors. No jobs submitted.",
  );
} finally {
  await browser.close();
}
