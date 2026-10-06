import { mkdir } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const require = createRequire(new URL('../web/package.json', import.meta.url));
const { chromium } = require('@playwright/test');
const origin = new URL(process.env.OPS_DEMO_URL || 'http://127.0.0.1:2061');
if (!['127.0.0.1', 'localhost'].includes(origin.hostname)) {
  throw new Error('Screenshots are restricted to the local synthetic application.');
}
const directory = fileURLToPath(new URL('../docs/screenshots/', import.meta.url));
await mkdir(directory, { recursive: true });
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const errors = [];
page.on('pageerror', (error) => errors.push(error.message));
page.on('console', (entry) => { if (entry.type() === 'error') errors.push(entry.text()); });
const api = async (path, data) => {
  const response = await page.request.post(`${origin.origin}/api/v1${path}`, { data, headers: { Origin: origin.origin } });
  if (!response.ok()) throw new Error(`Screenshot setup failed: ${response.status()}`);
};
try {
  await api('/session', { userId: 'demo-operator' });
  if (process.argv.includes('--reset-demo')) await api('/demo/reset', { confirmation: 'RESET DEMO' });
  // Wait for actual initialization rather than capturing an intermediate cold-start warning.
  for (let attempt = 0; attempt < 20; attempt++) {
    const response = await page.request.get(`${origin.origin}/api/v1/conversations`);
    if (!response.ok()) throw new Error(`Queue check failed: ${response.status()}`);
    const { queue } = await response.json();
    if (queue.state === 'unavailable') throw new Error(queue.error);
    if (queue.pending !== null && queue.unpublished === 0) break;
    if (attempt === 19) throw new Error('The demo queue did not finish initialization.');
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  await page.goto(origin.origin);
  await page.getByRole('heading', { name: 'Attention queue' }).waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: `${directory}/desk.png`, fullPage: true, animations: 'disabled' });
  await page.getByRole('button', { name: 'Open investigation' }).click();
  await page.getByRole('heading', { name: 'Delta 02', exact: true }).waitFor();
  await page.getByRole('dialog').screenshot({ path: `${directory}/investigation.png`, animations: 'disabled' });
  await page.getByRole('button', { name: 'Close investigation' }).click();
  await page.getByRole('button', { name: 'Inspect decision', exact: true }).click();
  await page.getByRole('heading', { name: 'Recorded facts', exact: true }).waitFor();
  await page.getByRole('dialog').screenshot({ path: `${directory}/decision.png`, animations: 'disabled' });
  await page.getByRole('button', { name: 'Close investigation' }).click();
  await page.getByRole('button', { name: 'Sending assets', exact: true }).click();
  await page.getByLabel('Search assets').waitFor();
  await page.getByRole('heading', { name: 'Sending lines' }).waitFor();
  await page.screenshot({ path: `${directory}/assets.png`, fullPage: true, animations: 'disabled' });
  await page.getByRole('button', { name: 'Decisions', exact: true }).click();
  await page.getByLabel('Search decisions').waitFor();
  await page.getByRole('heading', { name: 'Recorded decisions' }).waitFor();
  await page.screenshot({ path: `${directory}/decisions.png`, fullPage: true, animations: 'disabled' });
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  await page.getByLabel('Message', { exact: true }).waitFor();
  await page.screenshot({ path: `${directory}/conversations.png`, fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Desk', exact: true }).click();
  await page.getByRole('heading', { name: 'Attention queue' }).waitFor();
  await page.screenshot({ path: `${directory}/mobile.png`, animations: 'disabled' });
  if (errors.length) throw new Error(`Browser errors: ${errors.join('\n')}`);
  console.log(`Captured seven application screenshots in ${directory}`);
} finally {
  await browser.close();
}
