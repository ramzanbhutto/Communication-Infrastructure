import { test, expect } from '@playwright/test';
const headers = { Origin: 'http://localhost:5174' };
test.beforeEach(async ({ page }) => {
  expect(
    (
      await page.request.post('/api/v1/session', { data: { userId: 'demo-operator' }, headers })
    ).status()
  ).toBe(200);
  expect(
    (
      await page.request.post('/api/v1/demo/reset', {
        data: { confirmation: 'RESET DEMO' },
        headers
      })
    ).status()
  ).toBe(200);
  await page.goto('/#infrastructure');
  await page.getByLabel('Channel or resource').selectOption('imessage');
});
test('optional iMessage verifies compatibility, delivery, read receipts and STOP', async ({
  page
}) => {
  const send = page.getByRole('button', { name: 'Queue lab outreach', exact: true });
  await expect(send).toBeDisabled();
  await page.getByRole('button', { name: 'Check iMessage compatibility', exact: true }).click();
  await expect(page.getByText('Existing iMessage chat verified', { exact: true })).toBeVisible();
  await expect(send).toBeEnabled();
  await send.click();
  const row = page.locator('tbody tr').first();
  await expect(row.getByText('Delivered', { exact: true })).toBeVisible();
  await page.getByText('Local iMessage scenarios', { exact: true }).click();
  await page.getByRole('button', { name: 'Simulate read receipt', exact: true }).click();
  await row.getByRole('button', { name: /^Inspect job/ }).click();
  const drawer = page.getByRole('dialog');
  await expect(drawer.getByText('read', { exact: true })).toBeVisible();
  await expect(drawer.getByText('authenticated_local_bridge', { exact: true })).toHaveCount(2);
  await drawer.getByRole('button', { name: 'Close investigation' }).click();
  await page.getByRole('button', { name: 'Simulate iMessage reply', exact: true }).click();
  await page.getByRole('button', { name: /^Provider inbox/ }).click();
  await page.locator('.infra-inbox-row').first().click();
  await expect(
    drawer.getByText('Could you send the property details here?', { exact: true })
  ).toBeVisible();
  await drawer.getByRole('button', { name: 'Close investigation' }).click();
  await page.getByRole('button', { name: 'Delivery pipeline', exact: true }).click();
  await page.getByRole('button', { name: 'Check iMessage compatibility', exact: true }).click();
  await expect(send).toBeEnabled();
  await page.getByText('Local iMessage scenarios', { exact: true }).click();
  await page.getByRole('button', { name: 'Simulate iMessage STOP', exact: true }).click();
  await expect(send).toBeDisabled();
  const rejection = await page.request.post('/api/v1/infrastructure/jobs', {
    headers,
    data: {
      channel: 'imessage',
      contactId: 'c102',
      assetId: 'bridge-imessage',
      body: 'Forged send after STOP',
      requestKey: 'imessage-forged-stopped-1'
    }
  });
  expect(rejection.status()).toBe(422);
  expect((await rejection.json()).code).toBe('OPTED_OUT');
});
test('compatibility handles absent chats and is inspectable by reviewers', async ({ page }) => {
  await page.getByLabel('Contact', { exact: true }).selectOption('c101');
  await page.getByRole('button', { name: 'Check iMessage compatibility', exact: true }).click();
  await expect(page.getByText('iMessage chat unavailable', { exact: true })).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Queue lab outreach', exact: true })
  ).toBeDisabled();
  await page.getByLabel('Demo account').selectOption('demo-viewer');
  await page.getByLabel('Contact', { exact: true }).selectOption('c102');
  await page.getByRole('button', { name: 'Check iMessage compatibility', exact: true }).click();
  await expect(page.getByText('Existing iMessage chat verified', { exact: true })).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Queue lab outreach', exact: true })
  ).toBeDisabled();
  await expect(
    page.getByRole('button', { name: 'Sync bridge messages', exact: true })
  ).toBeDisabled();
  expect(
    (
      await page.request.post('/api/v1/imessage/sync', { data: { contactId: 'c102' }, headers })
    ).status()
  ).toBe(403);
});
test('disabled optional channel explains its setup and fits mobile layouts', async ({ page }) => {
  await page.route('**/api/v1/infrastructure', async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, json: { ...(await response.json()), imessageEnabled: false } });
  });
  await page.reload();
  await page.getByLabel('Channel or resource').selectOption('imessage');
  await expect(page.getByText(/The option is disabled/)).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Queue lab outreach', exact: true })
  ).toBeDisabled();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
