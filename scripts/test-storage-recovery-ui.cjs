// Uses the existing Playwright installation with real rendered Control pages and scripts.
const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');

(async () => {
  const directory = process.argv[2];
  const posts = [];
  const server = http.createServer((req, res) => {
    if (req.method === 'POST') {
      let body = '';
      req.on('data', chunk => { body += chunk; });
      req.on('end', () => {
        posts.push({ path: req.url, body: new URLSearchParams(body) });
        res.writeHead(303, { Location: '/admin-ru.html' });
        res.end();
      });
      return;
    }
    const file = path.join(directory, new URL(req.url, 'http://localhost').pathname);
    if (!fs.existsSync(file) || !fs.statSync(file).isFile()) { res.writeHead(404); res.end(); return; }
    const type = file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html';
    res.writeHead(200, { 'Content-Type': type + '; charset=utf-8' });
    res.end(fs.readFileSync(file));
  });
  const browser = await chromium.launch({ headless: true, executablePath: process.env.OMB_CHROMIUM_EXECUTABLE });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    const context = await browser.newContext();
    const page = await context.newPage();
    page.setDefaultTimeout(2500);
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const base = 'http://127.0.0.1:' + server.address().port;
    for (const theme of ['light', 'dark']) {
      for (const width of [1280, 390]) {
        for (const lang of ['ru', 'en']) {
          await page.setViewportSize({ width, height: 900 });
          await page.goto(base + '/admin-' + lang + '.html');
          await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme);
          const open = page.locator('[data-storage-recovery-open]');
          const dialog = page.locator('#storage-recovery-dialog');
          assert.equal(await dialog.isVisible(), false, 'dialog initially closed');
          assert.equal((await page.locator('[data-smb-add]').innerText()).includes('+'), false);
          await open.click();
          assert.equal(await dialog.isVisible(), true, 'button opens dialog');
          const save = dialog.locator('[data-storage-recovery-save]');
          const interval = dialog.locator('[name=interval_minutes]');
          const enabled = dialog.locator('[name=enabled]');
          assert.equal(await save.isDisabled(), true, 'initial save disabled');
          await interval.fill('5');
          assert.equal(await save.isEnabled(), true, 'interval change enables save');
          await interval.fill('2');
          assert.equal(await save.isDisabled(), true, 'restoring interval disables save');
          await enabled.uncheck();
          assert.equal(await save.isEnabled(), true, 'checkbox change enables save');
          await enabled.check();
          assert.equal(await save.isDisabled(), true, 'restoring checkbox disables save');
          await interval.fill('');
          assert.equal(await save.isDisabled(), true, 'empty interval disables save');
          await interval.fill('61');
          assert.equal(await save.isDisabled(), true, 'out-of-range interval disables save');
          await interval.fill('2.5');
          assert.equal(await save.isDisabled(), true, 'fractional interval disables save');
          await interval.fill('5');
          await dialog.locator('[data-close-dialog]').first().click();
          await open.click();
          assert.equal(await interval.inputValue(), '2', 'cancel discards unsaved interval');
          assert.equal(await save.isDisabled(), true, 'reopened save disabled');
          await page.keyboard.press('Escape');
          await open.click();
          const bounds = await dialog.boundingBox();
          assert(bounds.x >= 0 && bounds.x + bounds.width <= width, 'dialog fits viewport');
          assert(bounds.y >= 0 && bounds.y + bounds.height <= 900, 'dialog height fits viewport');
          assert.equal(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true, 'no horizontal overflow');
          if (lang === 'ru' && process.env.OMB_UI_SCREENSHOT_DIR) {
            fs.mkdirSync(process.env.OMB_UI_SCREENSHOT_DIR, { recursive: true });
            await page.screenshot({ path: path.join(process.env.OMB_UI_SCREENSHOT_DIR, theme + '-' + width + '.png') });
          }
          await interval.fill('5');
          const request = page.waitForRequest(req => req.method() === 'POST');
          await save.click();
          const sent = await request;
          assert.equal(new URL(sent.url()).pathname, '/images/storage-recovery');
          const body = new URLSearchParams(sent.postData());
          assert.equal(body.get('interval_minutes'), '5');
          assert.equal(body.get('enabled'), 'on');
          assert(body.get('csrf'), 'CSRF preserved');
          await page.waitForLoadState();
        }
      }
    }
    await page.goto(base + '/admin-ru.html');
    await page.locator('[data-storage-recovery-open]').click();
    await page.locator('#storage-recovery-dialog [name=interval_minutes]').fill('5');
    const run = page.waitForRequest(req => req.method() === 'POST');
    await page.locator('form[action="/images/storage-recovery/run"] button').click();
    const runRequest = await run;
    assert.equal(new URL(runRequest.url()).pathname, '/images/storage-recovery/run');
    assert.equal(new URLSearchParams(runRequest.postData()).has('interval_minutes'), false, 'manual check does not save edits');
    await page.waitForLoadState();
    for (const role of ['operator', 'observer', 'offline']) {
      await page.goto(base + '/' + role + '-ru.html');
      await page.locator('[data-storage-recovery-open]').click();
      const dialog = page.locator('#storage-recovery-dialog');
      assert.equal(await dialog.isVisible(), true);
      assert.equal(await dialog.locator('form').count(), 0, role + ' has no recovery forms');
    }
    assert.deepEqual(errors, [], 'no browser errors');
    assert.equal(posts.length, 9, 'eight saves and one independent manual check');
    console.log('PASS: RU/EN, light/dark, 1280/390px; dirty state, validation, cancel/reopen, POST/CSRF, independent manual check, roles and unavailable Agent.');
  } finally {
    await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
