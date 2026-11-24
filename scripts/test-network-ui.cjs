const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');

(async () => {
  const directory = process.argv[2];
  if (!directory) {
    console.error('Usage: node test-network-ui.cjs <html-dir>');
    process.exit(1);
  }
  const posts = [];
  const server = http.createServer((req, res) => {
    if (req.method === 'POST') {
      let body = '';
      req.on('data', chunk => { body += chunk; });
      req.on('end', () => {
        posts.push({ path: req.url, body: new URLSearchParams(body) });
        res.writeHead(303, { Location: '/network-ru.html' });
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

  const chromiumPath = process.env.OMB_CHROMIUM_EXECUTABLE || undefined;
  const browser = await chromium.launch({
    headless: true,
    ...(chromiumPath ? { executablePath: chromiumPath } : {})
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));

  try {
    const context = await browser.newContext();
    const page = await context.newPage();
    page.setDefaultTimeout(3000);
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const base = 'http://127.0.0.1:' + server.address().port;

    for (const theme of ['light', 'dark']) {
      for (const width of [1280, 390]) {
        for (const lang of ['ru', 'en']) {
          posts.length = 0;
          await page.setViewportSize({ width, height: 900 });
          await page.goto(base + '/network-' + lang + '.html');
          await page.evaluate(t => document.documentElement.dataset.theme = t, theme);

          const form = page.locator('#network-edit-form');
          assert.equal(await form.isVisible(), true, 'form is visible');

          const save = form.locator('.form-actions button');
          assert.equal(await save.isVisible(), true, 'save button is visible');
          assert.equal(await save.isDisabled(), true, 'save button initially disabled');

          // Try submitting when disabled
          await save.click({ force: true });
          assert.equal(posts.length, 0, 'disabled save does not post');

          // 1. Text input: interface
          const ifaceInput = form.locator('input[name="interface"]');
          const origIface = await ifaceInput.inputValue();
          await ifaceInput.fill(origIface + '_test');
          assert.equal(await save.isEnabled(), true, 'changed interface enables save');
          await ifaceInput.fill(origIface);
          assert.equal(await save.isDisabled(), true, 'reverted interface disables save');

          // 2. Required text input: server_ip & invalid validity
          const ipInput = form.locator('input[name="server_ip"]');
          const origIP = await ipInput.inputValue();
          await ipInput.fill('192.168.10.222');
          assert.equal(await save.isEnabled(), true, 'changed IP enables save');
          await ipInput.fill('');
          assert.equal(await save.isDisabled(), true, 'empty required IP disables save');
          await ipInput.fill(origIP);
          assert.equal(await save.isDisabled(), true, 'reverted IP disables save');

          // 3. Select: mode
          const modeSelect = form.locator('select[name="mode"]');
          const origMode = await modeSelect.inputValue();
          const newMode = origMode === 'proxy' ? 'full' : 'proxy';
          await modeSelect.selectOption(newMode);
          assert.equal(await save.isEnabled(), true, 'changed mode enables save');
          await modeSelect.selectOption(origMode);
          assert.equal(await save.isDisabled(), true, 'reverted mode disables save');

          // 4. Select: default_driver
          const driverSelect = form.locator('select[name="default_driver"]');
          const origDriver = await driverSelect.inputValue();
          const newDriver = origDriver === 'snp' ? 'ipxe' : 'snp';
          await driverSelect.selectOption(newDriver);
          assert.equal(await save.isEnabled(), true, 'changed default_driver enables save');
          await driverSelect.selectOption(origDriver);
          assert.equal(await save.isDisabled(), true, 'reverted default_driver disables save');

          // 5. Checkbox: reuse_firmware_network
          const reuseCheck = form.locator('input[name="reuse_firmware_network"]');
          const origReuse = await reuseCheck.isChecked();
          if (origReuse) await reuseCheck.uncheck(); else await reuseCheck.check();
          assert.equal(await save.isEnabled(), true, 'toggled reuse_firmware_network enables save');
          if (origReuse) await reuseCheck.check(); else await reuseCheck.uncheck();
          assert.equal(await save.isDisabled(), true, 'reverted reuse_firmware_network disables save');

          // 6. Number input: winpe_timeout & min validation
          const timeoutInput = form.locator('input[name="winpe_timeout"]');
          const origTimeout = await timeoutInput.inputValue();
          await timeoutInput.fill('120');
          assert.equal(await save.isEnabled(), true, 'valid timeout enables save');
          await timeoutInput.fill('5'); // min is 10
          assert.equal(await save.isDisabled(), true, 'below-min timeout disables save');
          await timeoutInput.fill(origTimeout);
          assert.equal(await save.isDisabled(), true, 'reverted timeout disables save');

          // 7. Verify successful POST when enabled
          await ifaceInput.fill(origIface + '_new');
          assert.equal(await save.isEnabled(), true, 'save enabled before submit');
          const postPromise = page.waitForRequest(req => req.method() === 'POST');
          await save.click();
          const req = await postPromise;
          assert.equal(new URL(req.url()).pathname, '/network/preview');
          const postBody = new URLSearchParams(req.postData());
          assert.equal(postBody.get('interface'), origIface + '_new');
        }
      }
    }

    assert.equal(errors.length, 0, 'no javascript errors: ' + errors.join('; '));
    console.log('test-network-ui: OK');
  } finally {
    await browser.close();
    server.close();
  }
})();
