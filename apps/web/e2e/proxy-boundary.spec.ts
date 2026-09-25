import { expect, test } from '@playwright/test';
import { ADMIN_TOKEN } from './fixture-settings.mjs';
import { account, fixture, openConsole, requests, web } from './console';

test('production proxy rejects unauthenticated, public and cross-origin calls and ignores forged browser authority', async ({ page, request }) => {
  expect((await page.request.get('/core/v1/admin/projects')).status()).toBe(401);
  await openConsole(page, request, 'projects');
  await expect(page.getByRole('heading', { name: 'Projects and keys', exact: true })).toBeVisible();
  const before = (await requests(request)).calls.length;
  expect((await page.request.get('/v1/agents', { headers: { Authorization: 'Bearer project-key' } })).status()).toBe(404);
  expect((await page.request.post('/core/v1/admin/projects', { headers: { Origin: 'https://foreign.invalid' }, data: { name: 'Denied' } })).status()).toBe(403);
  expect((await page.request.post('/core/v1/admin/projects/proj_7f3a91c2/agents', { headers: { Origin: web }, data: {} })).status()).toBe(404);
  expect((await requests(request)).calls.length).toBe(before);
  const response = await page.request.get('/core/v1/admin/projects', { headers: { Authorization: 'Bearer forged-token', 'X-Core-Console-Actor': 'forged-actor' } });
  expect(response.ok()).toBe(true);
  expect((await requests(request)).calls.at(-1)).toMatchObject({ authenticated: true, actor: account.username, cookie: null });
  expect(await response.text()).not.toContain(ADMIN_TOKEN);
  expect(await page.content()).not.toContain(ADMIN_TOKEN);
  const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage }, cookie: document.cookie }));
  expect(storage).not.toContain(ADMIN_TOKEN);
  expect(storage).not.toContain('core_console_session');
});

test('authentication read failure leaves private pages unmounted', async ({ page, request }) => {
  await request.post(`${fixture}/__fixture/reset`);
  await page.route('**/console/auth', route => route.abort('failed'));
  await page.goto('/');
  await expect(page.getByText('Could not connect to your console.', { exact: true })).toBeVisible();
  expect((await requests(request)).calls).toEqual([]);
  await expect(page.getByRole('navigation', { name: 'Console navigation' })).toHaveCount(0);
});
