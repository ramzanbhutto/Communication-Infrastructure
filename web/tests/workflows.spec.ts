import { test, expect, type Page } from '@playwright/test';

const origin = { Origin: 'http://localhost:5174' };
async function enter(page: Page, account = 'demo-operator') {
  const response = await page.request.post('/api/v1/session', {
    data: { userId: account },
    headers: origin
  });
  expect(response.status()).toBe(200);
}
test.beforeEach(async ({ page }) => {
  await enter(page);
  const reset = await page.request.post('/api/v1/demo/reset', {
    data: { confirmation: 'RESET DEMO' },
    headers: origin
  });
  expect(reset.status()).toBe(200);
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Attention queue' })).toBeVisible();
});

test('line investigation quarantine rejected restore recovery and verified audit', async ({
  page
}) => {
  const trigger = page.getByRole('button', { name: 'Inspect asset', exact: true }).first();
  await trigger.click();
  const drawer = page.getByRole('dialog');
  await expect(drawer.getByRole('heading', { name: 'Delta 02', exact: true })).toBeVisible();
  await expect(
    drawer
      .locator('.detail-block')
      .filter({ hasText: 'Latest measured filter rate' })
      .getByText('11.2%', { exact: true })
  ).toBeVisible();
  await drawer
    .getByLabel('Explanation')
    .fill('Provider filter rate is deteriorating. Hold new outreach.');
  await drawer.getByRole('button', { name: 'Quarantine line', exact: true }).click();
  await expect(
    drawer.getByText('Delta 02 is now quarantined. The audit entry is committed.')
  ).toBeVisible();
  await drawer.getByLabel('Explanation').fill('Verify the server restoration gate.');
  await drawer.getByRole('button', { name: 'Restore line', exact: true }).click();
  await expect(
    drawer.getByText('The latest observation does not meet restoration rules.')
  ).toBeVisible();
  await expect(drawer.getByText('asset · restore denied', { exact: true })).toBeVisible();
  await drawer.getByRole('button', { name: 'Record clean demo sample' }).click();
  await expect(
    drawer.getByText('Clean observation recorded. Review the requirements before restoring.')
  ).toBeVisible();
  await drawer.getByLabel('Explanation').fill('A clean post-quarantine observation is verified.');
  await drawer.getByRole('button', { name: 'Restore line', exact: true }).click();
  await expect(
    drawer.getByText('Delta 02 is now active. The audit entry is committed.')
  ).toBeVisible();
  await expect(drawer.getByText('asset · restore', { exact: true })).toBeVisible();
  await drawer.getByRole('button', { name: 'Close investigation' }).click();
  await expect(page.getByRole('heading', { name: 'Attention queue' })).toBeVisible();
});

test('SMS email-open rejection leads to immutable recorded evidence', async ({ page }) => {
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  await expect(page.getByText('NO_SMS_CONSENT', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Check and simulate SMS' }).click();
  await page.getByRole('button', { name: 'Inspect the recorded rejection' }).click();
  const drawer = page.getByRole('dialog');
  await expect(drawer.getByText('Explicit SMS consent', { exact: true })).toBeVisible();
  await expect(drawer.getByText('Does not grant SMS permission', { exact: true })).toBeVisible();
  await expect(drawer.getByText('No consent source recorded')).toBeVisible();
  const response = await page.request.get('/api/v1/desk');
  expect((await response.json()).metrics.delivered).toBe(0);
});

test('consented SMS persists and server suppresses DNC even when input is forged', async ({
  page
}) => {
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  await page.getByLabel('Contact', { exact: true }).selectOption('c102');
  await page.getByRole('button', { name: 'Check and simulate SMS' }).click();
  await expect(
    page.getByText('The simulated message receipt is stored. No real SMS was sent.')
  ).toBeVisible();
  const response = await page.request.post('/api/v1/dial', {
    data: { contactId: 'c103', lineId: 'line-cedar', message: '', requestKey: 'forged-dnc-test' },
    headers: origin
  });
  expect(response.status()).toBe(422);
  expect((await response.json()).code).toBe('DNC_SUPPRESSED');
});

test('single call displays busy state and a confirmed final outcome', async ({ page }) => {
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  await page.getByRole('button', { name: 'Assisted call', exact: true }).click();
  await page.getByRole('button', { name: 'Start simulated call' }).click();
  await expect(page.getByText('Call in progress', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Start simulated call' })).toBeDisabled();
  await expect(page.getByText('answered simulated', { exact: false })).toBeVisible({
    timeout: 15_000
  });
  await expect(page.getByRole('button', { name: 'Start simulated call' })).toBeEnabled();
});

test('resuming the actual Redis consumer persists replies and applies STOP', async ({ page }) => {
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  await expect(page.getByText(/Consumer paused · 3 transport entries/)).toBeVisible();
  await page.getByRole('button', { name: 'Resume consumer' }).click();
  await expect(page.getByText('Thursday afternoon works. Please send the details.')).toBeVisible();
  await expect(page.getByText('STOP', { exact: true })).toBeVisible();
  await expect(page.getByText(/Consumer enabled · 0 transport entries/)).toBeVisible();
  const response = await page.request.get('/api/v1/contacts/c105');
  expect((await response.json()).contact.optedOutAt).not.toBeNull();
});

test('filter and masked export use the same complete scope', async ({ page }) => {
  await page.getByRole('button', { name: 'Decisions', exact: true }).click();
  await page.getByLabel('Filter decision channel').selectOption('sms');
  await expect(page.getByText('2 matching', { exact: true })).toBeVisible();
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Export filtered' }).click();
  expect((await download).suggestedFilename()).toBe('decisions-last-24h-masked.csv');
  const response = await page.request.get('/api/v1/decisions/export?channel=sms');
  const csv = await response.text();
  expect(csv).toContain('masked_phone');
  expect(csv).not.toContain('+12025550101');
  expect(csv).not.toContain('jordan@example.test');
});

test('reviewer cannot mutate through the interface or API', async ({ page }) => {
  await page.getByLabel('Demo account').selectOption('demo-viewer');
  await page.getByRole('button', { name: 'Inspect asset', exact: true }).first().click();
  await expect(
    page.getByText(
      'The reviewer can inspect this record. Switch to the demo operator to change line state.'
    )
  ).toBeVisible();
  await expect(page.getByRole('button', { name: 'Quarantine line' })).toHaveCount(0);
  const response = await page.request.post('/api/v1/assets/line-delta/quarantine', {
    data: { reason: 'Attempt to forge a mutation.', expectedVersion: 1 },
    headers: origin
  });
  expect(response.status()).toBe(403);
});

test('refresh failure preserves real data with a visible stale warning', async ({ page }) => {
  await page.route('**/api/v1/desk', (route) =>
    route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 'DEPENDENCY_UNAVAILABLE',
        message: 'The local database is unavailable.'
      })
    })
  );
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Showing the last successful snapshot.');
  await expect(page.getByRole('heading', { name: 'Attention queue' })).toBeVisible();
  await page.unroute('**/api/v1/desk');
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.getByRole('alert')).toHaveCount(0);
});

test('keyboard dialog containment escape and focus restoration', async ({ page }) => {
  const trigger = page.getByRole('button', { name: 'Inspect asset', exact: true }).first();
  await trigger.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  for (let i = 0; i < 8; i++) {
    await page.keyboard.press('Tab');
    expect(
      await page.evaluate(() => Boolean(document.activeElement?.closest('[role="dialog"]')))
    ).toBe(true);
  }
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(trigger).toBeFocused();
});

test('mobile and zoom keep overflow local and preserve workflows', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole('button', { name: 'Sending assets', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Sending lines' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByRole('button', { name: 'Inspect', exact: true }).first().click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button', { name: 'Close investigation' }).click();
  await page.setViewportSize({ width: 720, height: 500 }); // 1440px viewport at 200% browser zoom.
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  await expect(page.getByLabel('Message', { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('a late response cannot overwrite a newer recorded snapshot', async ({ page }) => {
  let release = () => {};
  let captured = () => {};
  let delivered = () => {};
  const capturedResponse = new Promise<void>((resolve) => {
    captured = resolve;
  });
  const deliveredResponse = new Promise<void>((resolve) => {
    delivered = resolve;
  });
  const heldResponse = new Promise<void>((resolve) => {
    release = resolve;
  });
  let first = true;
  await page.route('**/api/v1/desk', async (route) => {
    const response = await route.fetch();
    const held = first;
    if (held) {
      first = false;
      captured();
      await heldResponse;
    }
    await route.fulfill({ response }).catch(() => {}); // The old browser request may be aborted.
    if (held) delivered();
  });
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await capturedResponse;
  const sent = await page.request.post('/api/v1/sms', {
    data: {
      contactId: 'c102',
      lineId: 'line-cedar',
      message: 'A deliberate synthetic test message.',
      requestKey: 'fresh-snapshot-sms'
    },
    headers: origin
  });
  expect(sent.status()).toBe(201);
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.locator('.metric').first().locator('strong')).toHaveText('4');
  release();
  await deliveredResponse;
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
      )
  );
  await expect(page.locator('.metric').first().locator('strong')).toHaveText('4');
});

test('incomplete API versions produce a stale warning rather than a screen crash', async ({
  page
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.route('**/api/v1/desk', async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    delete data.featuredHistory;
    await route.fulfill({ status: 200, json: data });
  });
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('unreadable response');
  await expect(page.getByRole('heading', { name: 'Attention queue' })).toBeVisible();
  expect(errors).toEqual([]);
});

test('loading and unavailable queue states remain explicit', async ({ page }) => {
  let release = () => {};
  const ready = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/v1/conversations', async (route) => {
    const response = await route.fetch();
    await ready;
    const data = await response.json();
    data.queue = {
      ...data.queue,
      state: 'unavailable',
      pending: null,
      error: 'Redis queue data is unavailable.'
    };
    await route.fulfill({ status: 200, json: data });
  });
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  await expect(page.getByText('Loading recorded data…')).toBeVisible();
  release();
  await expect(page.getByText(/Backlog unavailable/)).toBeVisible();
  await expect(page.getByText('Could you send the property details?')).toBeVisible();
  await expect(page.getByText('Redis queue data is unavailable.', { exact: false })).toBeVisible();
});

test('long explanations stay within the mobile investigation and reduced motion is respected', async ({
  page
}) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Inspect asset', exact: true }).first().click();
  const drawer = page.getByRole('dialog');
  await drawer.getByLabel('Explanation').fill('Synthetic-provider-observation-'.repeat(8));
  await drawer.getByRole('button', { name: 'Quarantine line' }).click();
  await expect(
    drawer.getByText('Delta 02 is now quarantined. The audit entry is committed.')
  ).toBeVisible();
  expect(await drawer.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  expect(await drawer.evaluate((element) => getComputedStyle(element).animationName)).toBe('none');
});

test('an unconfirmed mutation is sent once and never retried automatically', async ({ page }) => {
  let attempts = 0;
  await page.route('**/api/v1/sms', async (route) => {
    attempts++;
    await route.abort('failed');
  });
  await page.getByRole('button', { name: 'Conversations', exact: true }).click();
  await page.getByLabel('Contact', { exact: true }).selectOption('c102');
  await page.getByRole('button', { name: 'Check and simulate SMS' }).click();
  await expect(page.getByRole('alert')).toContainText('The result is not confirmed.');
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('The result is not confirmed.');
  expect(attempts).toBe(1);
});

test('refreshes preserve focus and layout without replaying page motion', async ({ page }) => {
  await expect
    .poll(() =>
      page
        .locator('.page-view')
        .evaluate(
          (element) =>
            element.getAnimations().filter((motion) => motion.playState === 'running').length
        )
    )
    .toBe(0);
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate(() => {
    document.documentElement.dataset.pageEntrances = '0';
    document.addEventListener('animationstart', (event) => {
      if (event.target instanceof Element && event.target.matches('.page-view')) {
        const root = document.documentElement;
        root.dataset.pageEntrances = String(Number(root.dataset.pageEntrances) + 1);
      }
    });
  });
  const inspect = page.getByRole('button', { name: 'Inspect asset', exact: true }).first();
  await inspect.focus();
  const before = await inspect.boundingBox();
  await page.waitForResponse((response) => response.url().endsWith('/api/v1/desk'));
  await expect(inspect).toBeFocused();
  expect(await inspect.boundingBox()).toEqual(before);
  const refreshed = page.waitForResponse((response) => response.url().endsWith('/api/v1/desk'));
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await refreshed;
  expect(await page.evaluate(() => document.documentElement.dataset.pageEntrances)).toBe('0');
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.getByRole('button', { name: 'Decisions', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Recorded decisions' })).toBeVisible();
  expect(
    await page.locator('.page-view').evaluate((element) => getComputedStyle(element).animationName)
  ).toBe('none');
});

test('search fields keep icons separate and clear with a button or Escape', async ({ page }) => {
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await page.getByRole('button', { name: 'Sending assets', exact: true }).click();
    const assets = page.getByLabel('Search assets');
    await expect(assets).toBeVisible();
    const icon = await page.locator('.search-icon').boundingBox();
    const input = await assets.boundingBox();
    expect(input!.x).toBeGreaterThan(icon!.x + icon!.width + 7);
    await assets.fill('Delta');
    await expect(page.getByText('1 assets', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Clear search', exact: true }).click();
    await expect(assets).toHaveValue('');
    await expect(assets).toBeFocused();
    await expect(page.getByText('2 assets', { exact: true })).toBeVisible();

    await page.getByRole('button', { name: 'Decisions', exact: true }).click();
    const decisions = page.getByLabel('Search decisions');
    const decisionIcon = await page.locator('.search-icon').boundingBox();
    const decisionInput = await decisions.boundingBox();
    expect(decisionInput!.x).toBeGreaterThan(decisionIcon!.x + decisionIcon!.width + 7);
    await decisions.fill('NO_SMS_CONSENT');
    await expect(page.getByText('1 matching', { exact: true })).toBeVisible();
    await decisions.press('Escape');
    await expect(decisions).toHaveValue('');
    await expect(decisions).toBeFocused();
    await expect(page.getByText('3 matching', { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true
    );
  }
});

test('desk filters and metric shortcuts lead to the recorded scope', async ({ page }) => {
  const filters = page.getByRole('group', { name: 'Filter attention queue' });
  await filters.getByRole('button', { name: /^Assets/ }).click();
  await expect(page.locator('.issue')).toHaveCount(2);
  await filters.getByRole('button', { name: /^Policy/ }).click();
  await expect(page.locator('.issue')).toHaveCount(1);
  await expect(page.locator('.issue')).toContainText('SMS permission is missing');
  await filters.getByRole('button', { name: /^All issues/ }).click();
  await expect(page.locator('.issue')).toHaveCount(4);
  await page.getByRole('button', { name: 'View blocked outreach decisions' }).click();
  await expect(page.getByLabel('Filter decision outcome')).toHaveValue('blocked');
  await expect(page.getByText('3 matching', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Desk', exact: true }).click();
  await page.getByRole('button', { name: 'View recorded policy decisions' }).click();
  await expect(page.getByLabel('Filter decision outcome')).toHaveValue('');
});

test('chart observation selection reveals history without changing latest backend evidence', async ({
  page
}) => {
  const inspector = page.locator('.sample-inspector');
  const selector = page.getByRole('slider');
  await expect(inspector).toContainText('28 / 250 filtered');
  await selector.focus();
  await selector.press('Home');
  await expect(inspector).toContainText('3 / 250 filtered');
  await expect(inspector).toContainText('1.2%');
  await page.getByRole('button', { name: 'Refresh', exact: true }).click();
  await expect(inspector).toContainText('3 / 250 filtered');
  const response = await page.request.get('/api/v1/assets/line-delta');
  const { asset } = await response.json();
  expect(asset.sample.filtered).toBe(28);
  await selector.focus();
  await selector.press('End');
  await expect(inspector).toContainText('28 / 250 filtered');
});
