const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');

(async () => {
  const directory = process.argv[2];
  if (!directory) {
    console.error('Usage: node test-login-ui.cjs <html-dir>');
    process.exit(1);
  }

  const posts = [];
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://localhost');
    if (req.method === 'POST') {
      let body = '';
      req.on('data', chunk => { body += chunk; });
      req.on('end', () => {
        posts.push({ path: url.pathname, body: new URLSearchParams(body) });
        res.writeHead(303, { Location: '/' });
        res.end();
      });
      return;
    }

    let filePath;
    if (url.pathname.startsWith('/assets/')) {
      filePath = path.join(directory, url.pathname.replace('/assets/', ''));
    } else {
      filePath = path.join(directory, url.pathname === '/' ? 'login-ru.html' : url.pathname.replace(/^\//, ''));
    }

    if (!fs.existsSync(filePath) || !fs.statSync(filePath).isFile()) {
      res.writeHead(404);
      res.end('Not found: ' + filePath);
      return;
    }
    const type = filePath.endsWith('.js')
      ? 'text/javascript'
      : filePath.endsWith('.css')
      ? 'text/css'
      : 'text/html';
    res.writeHead(200, { 'Content-Type': type + '; charset=utf-8' });
    res.end(fs.readFileSync(filePath));
  });

  const chromiumPath = process.env.OMB_CHROMIUM_EXECUTABLE || '/Users/fonwa/Library/Caches/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-mac-arm64/chrome-headless-shell';
  const browser = await chromium.launch({
    headless: true,
    executablePath: chromiumPath
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const baseUrl = `http://127.0.0.1:${server.address().port}`;

  try {
    for (const theme of ['light', 'dark']) {
      for (const viewport of [{ width: 1280, height: 900 }, { width: 390, height: 844 }]) {
        const context = await browser.newContext({ viewport });
        const page = await context.newPage();
        await page.goto(`${baseUrl}/login-ru.html`);
        await page.evaluate(t => document.documentElement.setAttribute('data-theme', t), theme);

        // 1. Initial state: button disabled, labels inside
        const btn = page.locator('button.btn-primary');
        const userField = page.locator('input[name="username"]');
        const passField = page.locator('input[name="password"]');
        const userWrapper = page.locator('.field-float:has(input[name="username"])');
        const passWrapper = page.locator('.field-float:has(input[name="password"])');

        assert.equal(await btn.isDisabled(), true, 'submit button must start disabled');
        assert.equal(await userWrapper.evaluate(el => el.classList.contains('is-floating')), false, 'username label must start inside');
        assert.equal(await passWrapper.evaluate(el => el.classList.contains('is-floating')), false, 'password label must start inside');

        // 2. Focus username field
        await userField.focus();
        assert.equal(await btn.isDisabled(), true, 'button remains disabled when only focused');

        // 3. Fill username only
        await userField.fill('vlad');
        assert.equal(await userWrapper.evaluate(el => el.classList.contains('is-floating')), true, 'username label must float with text');
        assert.equal(await btn.isDisabled(), true, 'button must remain disabled with only username');

        // Blur username
        await passField.focus();
        assert.equal(await userWrapper.evaluate(el => el.classList.contains('is-floating')), true, 'username label must stay floating after blur');

        // 4. Fill password
        await passField.fill('SecretPass123!');
        assert.equal(await passWrapper.evaluate(el => el.classList.contains('is-floating')), true, 'password label must float with text');
        assert.equal(await btn.isDisabled(), false, 'button must be enabled when both fields are filled');

        // 5. Test password toggle eye button
        const eyeToggle = page.locator('.pw-toggle');
        assert.equal(await eyeToggle.count(), 1, 'password toggle button must be present');
        assert.equal(await passField.getAttribute('type'), 'password', 'password field starts with type=password');
        await eyeToggle.click();
        assert.equal(await passField.getAttribute('type'), 'text', 'password field toggles to type=text');
        await eyeToggle.click();
        assert.equal(await passField.getAttribute('type'), 'password', 'password field toggles back to type=password');

        // 6. Clear username -> button disables immediately
        await userField.fill('');
        assert.equal(await btn.isDisabled(), true, 'button must disable when username is cleared');
        assert.equal(await userWrapper.evaluate(el => el.classList.contains('is-floating')), false, 'empty username label returns inside');

        // 7. Whitespace-only username should NOT enable button
        await userField.fill('   ');
        assert.equal(await btn.isDisabled(), true, 'whitespace-only username must keep button disabled');

        // 8. Re-fill username -> button enables again
        await userField.fill('vlad');
        assert.equal(await btn.isDisabled(), false, 'button must re-enable when username is filled');

        // 9. Submit form
        posts.length = 0;
        await btn.click();
        assert.equal(posts.length, 1, 'form submit should produce 1 POST');
        assert.equal(posts[0].path, '/login', 'submitted path must be /login');
        assert.equal(posts[0].body.get('username'), 'vlad', 'submitted username matches');
        assert.equal(posts[0].body.get('password'), 'SecretPass123!', 'submitted password matches');

        await context.close();
      }
    }

    // 10. Check English version
    {
      const context = await browser.newContext();
      const page = await context.newPage();
      await page.goto(`${baseUrl}/login-en.html`);
      const userLabel = await page.locator('.field-float label[for="login-username"]').textContent();
      const passLabel = await page.locator('.field-float label[for="login-password"]').textContent();
      const btnText = await page.locator('button.btn-primary').textContent();
      assert.equal(userLabel, 'Username', 'English username label');
      assert.equal(passLabel, 'Password', 'English password label');
      assert.equal(btnText, 'Sign in', 'English button text');
      await context.close();
    }

    console.log('LOGIN_UI_BROWSER_TESTS_PASS');
  } finally {
    await browser.close();
    server.close();
  }
})();
