import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
const require = createRequire(new URL("../web/package.json", import.meta.url));
const { chromium, expect } = require("@playwright/test");
const origin = "http://127.0.0.1:2063";
if (process.env.OPS_SCREENSHOT_ISOLATED !== "true")
  throw new Error(
    "Set OPS_SCREENSHOT_ISOLATED=true only with the isolated test-database screenshot server on port 2063.",
  );
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
const folder = new URL("../docs/screenshots/", import.meta.url);
await mkdir(folder, { recursive: true });
async function post(path, data) {
  const r = await page.request.post(`${origin}/api/v1${path}`, {
    data,
    headers: { Origin: origin },
  });
  if (!r.ok()) throw new Error(`${path}: ${r.status()} ${await r.text()}`);
  return r.json();
}
async function capture(name) {
  await page.evaluate(() => {
    window.scrollTo(0, 0);
    return document.fonts.ready;
  });
  await page.screenshot({
    path: new URL(`${name}.png`, folder).pathname,
    fullPage: true,
    animations: "disabled",
  });
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth > innerWidth,
  );
  if (overflow) throw new Error(`${name}: document overflow`);
}
async function reference(name) {
  let css = await readFile(
    new URL("../web/src/styles.css", import.meta.url),
    "utf8",
  );
  const font = await readFile(
    new URL("../web/public/fonts/manrope.ttf", import.meta.url),
  );
  css = css.replace(
    "url('/fonts/manrope.ttf')",
    `url('data:font/ttf;base64,${font.toString("base64")}')`,
  );
  const html = await page.locator("#root").innerHTML();
  await mkdir(new URL("../.superdesign/tmp/", import.meta.url), {
    recursive: true,
  });
  await writeFile(
    new URL(`../.superdesign/tmp/${name}.html`, import.meta.url),
    `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Covent Ops ${name}</title><style>${css}</style></head><body><div id="root">${html}</div></body></html>`,
  );
}
try {
  await post("/session", { userId: "demo-operator" });
  await post("/demo/reset", {
    confirmation: "RESET DEMO",
    discardUnconfirmed: true,
  });
  await post("/email-diagnostics/email-north/check", {
    selector: "mail",
    mode: "fixture",
  });
  const { campaign } = await post("/campaigns", {
    name: "Requested property updates",
    steps: [
      {
        channel: "email",
        subject: "Your requested property details",
        body: "Hello {name}. Your synthetic property details are ready for review.",
        delaySeconds: 0,
      },
      {
        channel: "email",
        subject: "Any questions about the property?",
        body: "Hello {name}. Would you like additional synthetic property details?",
        delaySeconds: 600,
      },
    ],
    assetIds: ["email-north"],
    timezone: "UTC",
    startHour: 0,
    endHour: 24,
    weekdaysOnly: false,
  });
  await post(`/campaigns/${campaign.id}/activate`, {
    expectedVersion: campaign.version,
  });
  await post(`/campaigns/${campaign.id}/enroll`, {
    contactIds: ["c102"],
    timezone: "UTC",
  });
  await expect
    .poll(async () => {
      const r = await page.request.get(
        `${origin}/api/v1/campaigns/${campaign.id}`,
      );
      return (await r.json()).enrollments[0]?.step;
    })
    .toBe(1);
  await post("/infrastructure/scenarios", {
    channel: "email",
    contactId: "c102",
    assetId: "email-north",
    body: "Please send the property address.",
  });
  await page.goto(`${origin}/#campaigns`);
  await page
    .getByRole("button", { name: /Requested property updates/ })
    .click();
  await expect(page.locator(".campaign-table")).toContainText(
    "Reply received. Automation stopped",
  );
  await capture("campaigns");
  await reference("campaigns");
  await page.setViewportSize({ width: 390, height: 844 });
  await capture("campaigns-mobile");
  await page.setViewportSize({ width: 1440, height: 1100 });
  await page
    .getByRole("button", { name: "Review email configuration", exact: true })
    .click();
  await page.getByLabel("DNS scenario").selectOption("timeout");
  await page
    .getByRole("button", { name: "Apply fixture and inspect", exact: true })
    .click();
  await expect(page.locator(".email-check").first()).toContainText(
    "MX lookup failed",
  );
  await page.getByLabel("DNS scenario").selectOption("healthy");
  await page
    .getByRole("button", { name: "Apply fixture and inspect", exact: true })
    .click();
  await expect(page.locator(".email-check-grid")).toContainText(
    "Parsed RSA key",
  );
  await page
    .getByRole("button", { name: "Verify signed fixture", exact: true })
    .click();
  await expect(page.getByText("DKIM pass", { exact: true })).toBeVisible();
  await capture("email-diagnostics");
  await reference("email-diagnostics");
  await page.setViewportSize({ width: 390, height: 844 });
  await capture("email-diagnostics-mobile");
  if (errors.length) throw new Error(errors.join("\n"));
  console.log(
    "Captured desktop and mobile campaign and diagnostic screenshots. No page errors or document overflow.",
  );
} finally {
  await browser.close();
}
