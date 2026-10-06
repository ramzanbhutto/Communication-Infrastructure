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
async function post(path, data) {
  const response = await page.request.post(`${origin.origin}/api/v1${path}`, {
    data,
    headers: { Origin: origin.origin },
  });
  if (!response.ok())
    throw new Error(`${path}: ${response.status()} ${await response.text()}`);
  return response.json();
}
try {
  await post("/session", { userId: "demo-operator" });
  // Optional setup adds two synthetic jobs. It never resets existing history.
  if (process.argv.includes("--add-synthetic-jobs")) {
    await post("/infrastructure/jobs", {
      channel: "email",
      contactId: "c102",
      assetId: "email-north",
      subject: "Requested property details",
      body: "Here are the synthetic property details you requested.",
      requestKey: "screenshots-email-v1",
      scenario: "success",
    });
    await post("/infrastructure/dns/email-north", { selector: "mail" });
    await post("/infrastructure/jobs", {
      channel: "call",
      contactId: "c101",
      assetId: "line-cedar",
      requestKey: "screenshots-call-v1",
      scenario: "success",
    });
  }
  await page.goto(`${origin.origin}/#infrastructure`);
  await page
    .getByRole("heading", { name: "Recorded jobs", exact: true })
    .waitFor();
  await page.evaluate(() => document.fonts.ready);
  if (process.argv.includes("--add-synthetic-jobs")) {
    await page.getByText("Delivered", { exact: true }).waitFor();
    await page.getByText("Completed", { exact: true }).waitFor();
  }
  await page.screenshot({
    path: `${folder}/infrastructure.png`,
    fullPage: true,
    animations: "disabled",
  });
  // Save a literal, self-contained design reference without scripts or secrets.
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
      new URL("infrastructure.html", target),
      `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Communication infrastructure</title><style>${style}</style></head><body><div>${html.replace(/<a /g, '<a id="workspace-skip" ')}</div></body></html>`,
    );
  }
  await page
    .getByRole("button", { name: "Domains & resources", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Probe SIP peer", exact: true })
    .click();
  await page.getByText(/200 OK.*ms over UDP loopback/).waitFor();
  await page.screenshot({
    path: `${folder}/infrastructure-domains.png`,
    fullPage: true,
    animations: "disabled",
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page
    .getByRole("button", { name: "Delivery pipeline", exact: true })
    .click();
  if (
    await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
  )
    throw new Error("Document overflow at mobile width");
  await page.screenshot({
    path: `${folder}/infrastructure-mobile.png`,
    fullPage: true,
    animations: "disabled",
  });
  if (errors.length) throw new Error(errors.join("\n"));
  console.log(
    "Captured infrastructure, domains and mobile screenshots. No browser errors.",
  );
} finally {
  await browser.close();
}
