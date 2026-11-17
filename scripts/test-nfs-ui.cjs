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
    page.setDefaultTimeout(3000);
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const base = 'http://127.0.0.1:' + server.address().port;

    for (const theme of ['light', 'dark']) {
      for (const { width, height } of [{ width: 1280, height: 900 }, { width: 390, height: 844 }]) {
        for (const lang of ['ru', 'en']) {
          await page.setViewportSize({ width, height });
          await page.goto(base + '/admin-' + lang + '.html');
          await page.evaluate(t => document.documentElement.dataset.theme = t, theme);

          // 1. Verify no <details> or <summary> anywhere in main
          const detailsCount = await page.locator('main details, main summary').count();
          assert.equal(detailsCount, 0, 'No <details> or <summary> allowed in main');

          // 2. Verify Connect Storage dropdown button
          const connectBtn = page.locator('.storage-connect-wrap > .btn');
          assert.equal(await connectBtn.isVisible(), true, 'Connect storage button is visible');
          
          const popover = page.locator('#storage-connect-menu');
          assert.equal(await popover.isVisible(), false, 'Menu popover initially closed');

          // Open dropdown popover
          await connectBtn.click();
          assert.equal(await popover.isVisible(), true, 'Clicking button opens popover');

          const smbItem = popover.locator('[data-smb-add]');
          const nfsItem = popover.locator('[data-nfs-add]');
          assert.equal(await smbItem.isVisible(), true, 'SMB option visible');
          assert.equal(await nfsItem.isVisible(), true, 'NFS option visible');

          // Click NFS option -> opens #nfs-create-dialog
          await nfsItem.click();
          assert.equal(await popover.isVisible(), false, 'Popover closes when option clicked');
          
          const nfsDialog = page.locator('#nfs-create-dialog');
          assert.equal(await nfsDialog.isVisible(), true, 'NFS dialog opened');
          
          const versionSelect = nfsDialog.locator('select[name="version"]');
          assert.equal(await versionSelect.inputValue(), '4.1', 'NFSv4.1 selected by default');
          
          // Close NFS dialog
          await nfsDialog.locator('[data-close-dialog]').click();
          assert.equal(await nfsDialog.isVisible(), false, 'NFS dialog closed on cancel');

          // Open dropdown again and click SMB -> opens #smb-create-dialog
          await connectBtn.click();
          await smbItem.click();
          const smbDialog = page.locator('#smb-create-dialog');
          assert.equal(await smbDialog.isVisible(), true, 'SMB dialog opened');
          await smbDialog.locator('[data-close-dialog]').click();
          assert.equal(await smbDialog.isVisible(), false, 'SMB dialog closed on cancel');

          // 3. Verify Table rendering for NFS row if present
          const nfsRow = page.locator('tr:has-text("NFS")').first();
          if (await nfsRow.count() > 0) {
            const rowMenuBtn = nfsRow.locator('[data-row-menu]');
            assert.equal(await rowMenuBtn.isVisible(), true, 'NFS row has menu button');
            await rowMenuBtn.click();
            
            const rowMenu = page.locator('.row-menu-popover:visible');
            assert.equal(await rowMenu.count() > 0, true, 'Row menu opened');

            const retryBtn = rowMenu.locator('[data-nfs-retry-id]');
            if (await retryBtn.count() > 0) {
              await retryBtn.click();
              const retryDialog = page.locator('#nfs-retry-dialog');
              assert.equal(await retryDialog.isVisible(), true, 'NFS retry dialog opened');
              await retryDialog.locator('[data-close-dialog]').click();
            }

            const renameBtn = rowMenu.locator('[data-storage-rename-id]');
            if (await renameBtn.count() > 0) {
              await renameBtn.click();
              const renameDialog = page.locator('#storage-rename-dialog');
              assert.equal(await renameDialog.isVisible(), true, 'Storage rename dialog opened');
              assert.equal(await renameDialog.locator('#storage-rename-name').isVisible(), true, 'Rename name input visible');
              await renameDialog.locator('[data-close-dialog]').click();
              assert.equal(await renameDialog.isVisible(), false, 'Storage rename dialog closed');
            }
          }
        }
      }
    }

    assert.equal(errors.length, 0, 'No console errors: ' + errors.join('; '));
    console.log('NFS_UI_BROWSER_TESTS_PASS');
  } finally {
    await browser.close();
    server.close();
  }
})();
