const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

(async () => {
  const browser = await chromium.launch({
    headless: true,
    executablePath: process.env.OMB_CHROMIUM_EXECUTABLE
  });

  const page = await browser.newPage({ deviceScaleFactor: 2, viewport: { width: 1280, height: 900 } });
  const css = fs.readFileSync(path.join(__dirname, '../internal/control/templates/style.css'), 'utf8');
  const shaChipJS = fs.readFileSync(path.join(__dirname, '../internal/control/sha_chip.js'), 'utf8');
  const answersJS = fs.readFileSync(path.join(__dirname, '../internal/control/answers.js'), 'utf8');

  const testSha = '5eecca2780bd49c900565e124516a1bd666ec5e012825f34991b6ba1ef2fa6cf';
  const shortSha = '5eec…a6cf';

  const html = `<!doctype html>
<html lang="ru" data-theme="dark">
<head>
  <meta charset="utf-8">
  <style>
    ${css}
    body { padding: 20px; background: var(--bg); color: var(--text); font-family: var(--sans); }
    .test-box { margin-bottom: 24px; }
  </style>
</head>
<body>
  <div class="toolbar">
    <form class="search answers-search" method="get" action="/answers" data-answers-search>
      <label for="q" class="sr-only">Поиск</label>
      <input id="q" type="search" name="q" placeholder="Название, образ, автор или SHA-256" autocomplete="off">
    </form>
  </div>

  <div class="table-wrap">
    <table class="answers-table" style="width:100%">
      <thead>
        <tr>
          <th>Название</th>
          <th>Добавил</th>
          <th>SHA-256</th>
        </tr>
      </thead>
      <tbody>
        <tr id="row-1">
          <td><span class="answer-name">Windows 11 Lab Unattend</span></td>
          <td><span class="mono">vlad</span></td>
          <td>
            <span class="sha-chip" id="chip-1" data-sha-chip data-short="${shortSha}" data-full="${testSha}" role="button" tabindex="0" aria-expanded="false" title="${testSha}">
              <span class="sha-chip-text">${shortSha}</span>
              <button type="button" class="sha-chip-copy" data-sha-copy aria-label="Скопировать SHA-256" title="Скопировать SHA-256">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>
              </button>
            </span>
          </td>
        </tr>
        <tr id="row-2">
          <td><span class="answer-name">Ubuntu 24.04 Server</span></td>
          <td><span class="mono">admin</span></td>
          <td>
            <span class="sha-chip" id="chip-2" data-sha-chip data-short="9d21…14bb" data-full="9d21af870233ea8536128fa80295da9f7765ec01924bce63029141f2214414bb" role="button" tabindex="0" aria-expanded="false">
              <span class="sha-chip-text">9d21…14bb</span>
              <button type="button" class="sha-chip-copy" data-sha-copy aria-label="Скопировать SHA-256" title="Скопировать SHA-256">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>
              </button>
            </span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>

  <script>${shaChipJS}</script>
  <script>${answersJS}</script>
</body>
</html>`;

  await page.setContent(html);

  // 1. Initial verification of SHA chip
  const chip = page.locator('#chip-1');
  const chipText = page.locator('#chip-1 .sha-chip-text');
  const copyBtn = page.locator('#chip-1 .sha-chip-copy');

  assert.equal(await chipText.textContent(), shortSha, 'initial text must be shortSha');
  assert.equal(await copyBtn.isVisible(), false, 'copy button must be hidden initially');

  // 2. Click to expand
  await chip.click();
  assert.equal(await chip.getAttribute('class'), 'sha-chip is-expanded', 'chip must gain is-expanded class');
  assert.equal(await chip.getAttribute('aria-expanded'), 'true', 'aria-expanded must be true');
  assert.equal(await chipText.textContent(), testSha, 'text must be full SHA');
  assert.equal(await copyBtn.isVisible(), true, 'copy button must be visible when expanded');

  // 3. Click copy button
  await copyBtn.click();
  // class is-copied should be applied
  assert.equal(await copyBtn.getAttribute('class'), 'sha-chip-copy is-copied', 'copy button must have is-copied class');

  // 4. Click chip to collapse back
  await chip.click();
  assert.equal(await chip.getAttribute('class'), 'sha-chip', 'chip must collapse');
  assert.equal(await chip.getAttribute('aria-expanded'), 'false', 'aria-expanded must be false');
  assert.equal(await chipText.textContent(), shortSha, 'text must return to shortSha');

  // 5. Live search verification on answers table
  const searchInput = page.locator('input#q');
  const row1 = page.locator('#row-1');
  const row2 = page.locator('#row-2');

  // Search by author "vlad"
  await searchInput.fill('vlad');
  assert.equal(await row1.isVisible(), true, 'row-1 must be visible for query "vlad"');
  assert.equal(await row2.isVisible(), false, 'row-2 must be hidden for query "vlad"');

  // Search by SHA prefix "9d21"
  await searchInput.fill('9d21');
  assert.equal(await row1.isVisible(), false, 'row-1 must be hidden for query "9d21"');
  assert.equal(await row2.isVisible(), true, 'row-2 must be visible for query "9d21"');

  // Clear search
  await searchInput.fill('');
  assert.equal(await row1.isVisible(), true, 'row-1 must be visible after clear');
  assert.equal(await row2.isVisible(), true, 'row-2 must be visible after clear');

  // Expand chip again for visual screenshot
  await chip.click();

  // Screenshot in Dark Theme (1280px)
  await page.screenshot({ path: 'scripts/sha-expand-dark-1280.png', fullPage: true });

  // Light Theme
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
  await page.screenshot({ path: 'scripts/sha-expand-light-1280.png', fullPage: true });

  // Mobile viewport (390px)
  await page.setViewportSize({ width: 390, height: 800 });
  await page.screenshot({ path: 'scripts/sha-expand-light-390.png', fullPage: true });

  await browser.close();
  console.log('ALL PLAYWRIGHT TESTS PASSED SUCCESSFULLY!');
})();
