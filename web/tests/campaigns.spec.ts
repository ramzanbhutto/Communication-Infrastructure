import { test, expect, type Page } from '@playwright/test';
const headers = { Origin: 'http://localhost:5174' };
async function start(page: Page) {
  expect(
    (
      await page.request.post('/api/v1/session', { data: { userId: 'demo-operator' }, headers })
    ).status()
  ).toBe(200);
  expect(
    (
      await page.request.post('/api/v1/demo/reset', {
        data: { confirmation: 'RESET DEMO', discardUnconfirmed: true },
        headers
      })
    ).status()
  ).toBe(200);
  expect(
    (
      await page.request.post('/api/v1/email-diagnostics/email-north/check', {
        data: { selector: 'mail', mode: 'fixture' },
        headers
      })
    ).status()
  ).toBe(200);
  await page.goto('/#campaigns');
}
test.beforeEach(async ({ page }) => {
  await start(page);
});
test('campaign draft to verified delivery to reply-driven stop', async ({ page }) => {
  await page.getByRole('button', { name: 'Create campaign', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByLabel('Campaign name')).toHaveValue('Requested property updates');
  await dialog.getByRole('button', { name: 'Save draft', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await page.getByRole('button', { name: 'Activate', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Pause campaign', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Enroll selected contacts', exact: true }).click();
  await expect(
    page.getByRole('row').filter({ has: page.getByText('c102', { exact: true }) })
  ).toContainText('Previous delivery confirmed');
  const row = page.getByRole('row').filter({ has: page.getByText('c102', { exact: true }) });
  await row.getByRole('button', { name: 'Inspect delivery', exact: true }).click();
  await expect(dialog.getByText('Delivered', { exact: true })).toBeVisible();
  await expect(dialog.getByText('signed_local_provider', { exact: false })).toBeVisible();
  await dialog.getByRole('button', { name: 'Close investigation' }).click();
  expect(
    (
      await page.request.post('/api/v1/infrastructure/scenarios', {
        data: {
          channel: 'email',
          contactId: 'c102',
          assetId: 'email-north',
          body: 'Please send the property address.'
        },
        headers
      })
    ).status()
  ).toBe(200);
  await expect(row).toContainText('Reply received. Automation stopped');
  const data = await (await page.request.get('/api/v1/infrastructure')).json();
  expect(data.jobs).toHaveLength(1);
  await page.getByRole('button', { name: 'Pause campaign', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Resume campaign', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Resume campaign', exact: true }).click();
  await page.getByRole('button', { name: 'Cancel campaign', exact: true }).click();
  await dialog.getByRole('button', { name: 'Confirm cancellation', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Enroll selected contacts', exact: true })
  ).toHaveCount(0);
});
test('DNS failures stay unavailable and history explains correction', async ({ page }) => {
  await page
    .getByRole('navigation')
    .getByRole('button', { name: 'Email diagnostics', exact: true })
    .click();
  await page.getByRole('button', { name: /East sending domain/ }).click();
  await page.getByLabel('DNS scenario').selectOption('timeout');
  await page.getByRole('button', { name: 'Apply fixture and inspect', exact: true }).click();
  await expect(page.locator('.email-check').first()).toContainText('MX lookup failed');
  await expect(
    page.locator('.email-check').first().getByText('Unavailable', { exact: true })
  ).toBeVisible();
  await page.getByLabel('DNS scenario').selectOption('healthy');
  await page.getByRole('button', { name: 'Apply fixture and inspect', exact: true }).click();
  await expect(page.locator('.email-check-grid')).toContainText('Parsed RSA key');
  await expect(page.locator('.evidence-change').first()).toContainText('unavailable');
  await expect(page.locator('.email-history button')).toHaveCount(2);
  await page.locator('.email-history button').last().click();
  await expect(
    page.getByText('Historical assessment. Campaign gating uses the latest current assessment.')
  ).toBeVisible();
});
test('reviewer controls, keyboard dismissal and mobile layout', async ({ page }) => {
  await page.getByRole('button', { name: 'Create campaign', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByLabel('Demo account').selectOption('demo-viewer');
  await expect(page.getByRole('button', { name: 'Create campaign', exact: true })).toBeDisabled();
  const response = await page.request.post('/api/v1/campaigns', { data: {}, headers });
  expect(response.status()).toBe(403);
  await page
    .getByRole('navigation')
    .getByRole('button', { name: 'Email diagnostics', exact: true })
    .click();
  await expect(page.getByRole('button', { name: 'Run diagnostics', exact: true })).toBeDisabled();
  expect(
    (
      await page.request.post('/api/v1/email-diagnostics/email-north/check', {
        data: { selector: 'mail', mode: 'fixture' },
        headers
      })
    ).status()
  ).toBe(403);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
test('deferred email campaign resumes after verified fixture correction', async ({ page }) => {
  await page.request.post('/api/v1/email-diagnostics/email-east/fixture', {
    data: { scenario: 'healthy' },
    headers
  });
  await page.request.post('/api/v1/email-diagnostics/email-east/check', {
    data: { selector: 'mail', mode: 'fixture' },
    headers
  });
  const created = await page.request.post('/api/v1/campaigns', {
    data: {
      name: 'East domain recovery',
      steps: [
        {
          channel: 'email',
          subject: 'Requested property update',
          body: 'Your requested synthetic property details.',
          delaySeconds: 0
        }
      ],
      assetIds: ['email-east'],
      timezone: 'UTC',
      startHour: 0,
      endHour: 24,
      weekdaysOnly: false
    },
    headers
  });
  expect(created.status()).toBe(201);
  const { campaign } = await created.json();
  expect(
    (
      await page.request.post(`/api/v1/campaigns/${campaign.id}/activate`, {
        data: { expectedVersion: campaign.version },
        headers
      })
    ).status()
  ).toBe(200);
  await page.request.post('/api/v1/email-diagnostics/email-east/fixture', {
    data: { scenario: 'timeout' },
    headers
  });
  expect(
    (
      await page.request.post(`/api/v1/campaigns/${campaign.id}/enroll`, {
        data: { contactIds: ['c102'], timezone: 'UTC' },
        headers
      })
    ).status()
  ).toBe(200);
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await page.getByRole('button', { name: /East domain recovery/ }).click();
  await expect(page.locator('.campaign-table')).toContainText('Email configuration needs review');
  expect((await (await page.request.get('/api/v1/infrastructure')).json()).jobs).toHaveLength(0);
  await page.getByRole('button', { name: 'Review email configuration', exact: true }).click();
  await page.getByRole('button', { name: /East sending domain/ }).click();
  await page.getByLabel('DNS scenario').selectOption('healthy');
  await page.getByRole('button', { name: 'Apply fixture and inspect', exact: true }).click();
  await expect
    .poll(async () => {
      const data = await (await page.request.get(`/api/v1/campaigns/${campaign.id}`)).json();
      return data.enrollments[0]?.state;
    })
    .toBe('completed');
});

test('signed fixtures distinguish cryptographic failure from alignment failure', async ({
  page
}) => {
  await page.goto('/#email');
  await page.getByRole('button', { name: 'Verify signed fixture', exact: true }).click();
  await expect(page.getByText('DKIM pass', { exact: true })).toBeVisible();
  await expect(page.getByText('DMARC pass', { exact: true })).toBeVisible();
  await page.getByLabel('Signed message scenario').selectOption('unaligned');
  await page.getByRole('button', { name: 'Verify signed fixture', exact: true }).click();
  await expect(page.getByText('DKIM pass', { exact: true })).toBeVisible();
  await expect(page.getByText('DMARC fail', { exact: true })).toBeVisible();
  await page.getByLabel('Signed message scenario').selectOption('tampered');
  await page.getByRole('button', { name: 'Verify signed fixture', exact: true }).click();
  await expect(page.getByText('DKIM fail', { exact: true })).toBeVisible();
});
