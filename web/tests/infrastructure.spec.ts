import { test, expect, type Page } from '@playwright/test';
const headers = { Origin: 'http://localhost:5174' };
async function start(page: Page) {
  expect(
    (
      await page.request.post('/api/v1/session', { data: { userId: 'demo-operator' }, headers })
    ).status()
  ).toBe(200);
  const reset = await page.request.post('/api/v1/demo/reset', {
    data: { confirmation: 'RESET DEMO' },
    headers
  });
  expect(reset.status()).toBe(200);
  await page.goto('/#infrastructure');
  await expect(page.getByRole('heading', { name: 'Recorded jobs', exact: true })).toBeVisible();
}
test.beforeEach(async ({ page }) => {
  await start(page);
});
test('email delivery evidence, retrieved reply and STOP suppression', async ({ page }) => {
  await page.getByRole('button', { name: 'Queue lab outreach', exact: true }).click();
  const row = page.locator('tbody tr').first();
  await expect(row.getByText('Delivered', { exact: true })).toBeVisible();
  await row.getByRole('button', { name: /^Inspect job/ }).click();
  const drawer = page.getByRole('dialog');
  await expect(drawer.getByText('signed_local_provider', { exact: true })).toBeVisible();
  await expect(drawer.getByText('delivered', { exact: true })).toBeVisible();
  await drawer.getByRole('button', { name: 'Close investigation' }).click();
  await page.getByRole('button', { name: /^Provider inbox/ }).click();
  await page.getByRole('button', { name: 'Simulate email reply', exact: true }).click();
  await page.locator('.infra-inbox-row').first().click();
  await expect(
    drawer.getByText('Please send the property details.', { exact: true })
  ).toBeVisible();
  await drawer.getByRole('button', { name: 'Mark resolved', exact: true }).click();
  await expect(drawer.getByRole('button', { name: 'Resolved', exact: true })).toBeDisabled();
  await drawer.getByRole('button', { name: 'Close investigation' }).click();
  await page.getByRole('button', { name: 'Simulate SMS STOP', exact: true }).click();
  await page.getByRole('button', { name: 'Delivery pipeline', exact: true }).click();
  await page.getByLabel('Channel or resource').selectOption('sms');
  await page.getByRole('button', { name: 'Queue lab outreach', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('opted out');
  const data = await (await page.request.get('/api/v1/infrastructure')).json();
  expect(data.jobs).toHaveLength(1);
  expect(data.inbox).toHaveLength(2);
});
test('ambiguous call requires explicit verification without resubmission', async ({ page }) => {
  await page.getByLabel('Channel or resource').selectOption('call');
  await page.getByLabel('Provider scenario').selectOption('ambiguous');
  await page.getByRole('button', { name: 'Queue lab outreach', exact: true }).click();
  const row = page.locator('tbody tr').first();
  await expect(row.getByText('Unknown', { exact: true })).toBeVisible();
  await row.getByRole('button', { name: /^Inspect job/ }).click();
  const drawer = page.getByRole('dialog');
  await expect(drawer.getByText('PROVIDER_UNCONFIRMED', { exact: true })).toBeVisible();
  await drawer.getByRole('button', { name: 'Verify existing lab record', exact: true }).click();
  await expect(drawer.getByText('Completed', { exact: true })).toBeVisible();
  await expect(drawer.getByText('signed_local_provider', { exact: true })).toBeVisible();
  const data = await (await page.request.get('/api/v1/infrastructure')).json();
  expect(data.jobs).toHaveLength(1);
  expect(data.jobs[0].attempts).toBe(1);
  await drawer.getByRole('button', { name: 'Close investigation' }).click();
});
test('DNS gate and volume ramp schedule a deduplicated email', async ({ page }) => {
  await page.getByRole('button', { name: 'Domains & resources', exact: true }).click();
  const domain = page
    .locator('.panel')
    .filter({ has: page.getByRole('heading', { name: 'North sending domain', exact: true }) });
  await domain.getByRole('button', { name: 'Enable ramp', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Inspect DNS first');
  await domain.getByRole('button', { name: 'Inspect DNS', exact: true }).click();
  await expect(domain.getByText('Present', { exact: true })).toHaveCount(3);
  await domain.getByRole('button', { name: 'Enable ramp', exact: true }).click();
  await expect(domain.getByRole('button', { name: 'Pause ramp', exact: true })).toBeVisible();
  await expect(domain.getByText('1 queued today', { exact: false })).toBeVisible();
  await domain.getByRole('button', { name: 'Pause ramp', exact: true }).click();
  await page.getByRole('button', { name: 'Delivery pipeline', exact: true }).click();
  await expect(page.locator('tbody tr').getByText('Delivered', { exact: true })).toBeVisible();
  const data = await (await page.request.get('/api/v1/infrastructure')).json();
  expect(data.jobs).toHaveLength(1);
  expect(data.jobs[0].purpose).toBe('ramp');
});
test('mobile layout and reviewer cannot mutate infrastructure', async ({ page }) => {
  await page.getByRole('button', { name: 'Queue lab outreach', exact: true }).click();
  await expect(page.locator('tbody tr').getByText('Delivered', { exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByLabel('Search delivery jobs')).toBeVisible();
  const skip = page.getByRole('link', { name: 'Skip to workspace', includeHidden: true });
  await expect(skip).toHaveCSS('opacity', '0');
  await skip.focus();
  await expect(skip).toHaveCSS('opacity', '1');
  await skip.press('Enter');
  await expect(skip).toHaveCSS('opacity', '0');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true
  );
  await page.getByLabel('Demo account').selectOption('demo-viewer');
  await expect(
    page.getByRole('button', { name: 'Queue lab outreach', exact: true })
  ).toBeDisabled();
  const response = await page.request.post('/api/v1/infrastructure/jobs', {
    headers,
    data: { channel: 'email', requestKey: 'forged-viewer-job' }
  });
  expect(response.status()).toBe(403);
});

test('SIP probe records a matched signaling response', async ({ page }) => {
  await page.getByRole('button', { name: 'Domains & resources', exact: true }).click();
  await page.getByRole('button', { name: 'Probe SIP peer', exact: true }).click();
  await expect(page.getByText(/200 OK.*ms over UDP loopback/)).toBeVisible();
  const data = await (await page.request.get('/api/v1/infrastructure')).json();
  expect(data.sipProbe.state).toBe('responsive');
  expect(data.sipProbe.source).toBe('Actual local SIP OPTIONS exchange');
});

test('unknown email verifies its existing provider resource and links to consent evidence', async ({
  page
}) => {
  await page.getByLabel('Provider scenario').selectOption('ambiguous');
  await page.getByRole('button', { name: 'Queue lab outreach', exact: true }).click();
  const row = page.locator('tbody tr').first();
  await expect(row.getByText('Unknown', { exact: true })).toBeVisible();
  await row.getByRole('button', { name: /^Inspect job/ }).click();
  const drawer = page.getByRole('dialog');
  await drawer.getByRole('button', { name: 'Verify existing lab record', exact: true }).click();
  await expect(drawer.getByText('Delivered', { exact: true })).toBeVisible();
  await drawer.getByRole('button', { name: 'Inspect eligibility decision', exact: true }).click();
  await expect(
    drawer.getByRole('heading', { name: 'Decision inspector', exact: true })
  ).toBeVisible();
  await expect(drawer.getByText('Explicit email permission', { exact: true })).toBeVisible();
  await expect(
    drawer.getByRole('definition').filter({ hasText: 'Explicit synthetic lab opt-in' })
  ).toBeVisible();
  const data = await (await page.request.get('/api/v1/infrastructure')).json();
  expect(data.jobs).toHaveLength(1);
  expect(data.jobs[0].attempts).toBe(1);
});

test('discarding unresolved local jobs requires an explicit reset choice', async ({ page }) => {
  await page.getByLabel('Channel or resource').selectOption('call');
  await page.getByLabel('Provider scenario').selectOption('ambiguous');
  await page.getByRole('button', { name: 'Queue lab outreach', exact: true }).click();
  await expect(page.locator('tbody tr').getByText('Unknown', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Reset synthetic scenarios', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Type RESET DEMO', { exact: true }).fill('RESET DEMO');
  await dialog.getByRole('button', { name: 'Reset demo', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText('Resolve pending or unconfirmed');
  await dialog.getByRole('checkbox').check();
  await dialog.getByRole('button', { name: 'Reset demo', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  const data = await (await page.request.get('/api/v1/infrastructure')).json();
  expect(data.jobs).toHaveLength(0);
});
