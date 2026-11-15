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

  // Load layout template snippet and build sample pages
  const html = `<!doctype html>
<html lang="ru" data-theme="dark">
<head>
  <meta charset="utf-8">
  <style>
    ${css}
    body { padding: 20px; background: var(--bg); color: var(--text); font-family: var(--font-sans); }
    .test-box { margin-bottom: 24px; }
  </style>
</head>
<body>
  <!-- 1. Answers Table -->
  <div class="test-box">
    <table class="answers-table" style="width:100%">
      <tr>
        <td class="answer-col-name">Windows 11 Pro</td>
        <td class="row-actions answer-actions">
          <button type="button" class="secondary row-menu-btn" id="btn-answers" aria-label="Действия">
            <svg class="row-menu-icon" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" aria-hidden="true">
              <line x1="2.5" y1="4" x2="13.5" y2="4"></line>
              <line x1="2.5" y1="8" x2="13.5" y2="8"></line>
              <line x1="2.5" y1="12" x2="13.5" y2="12"></line>
            </svg>
            <span class="sr-only">☰</span>
          </button>
        </td>
      </tr>
    </table>
  </div>

  <!-- 2. Images Table -->
  <div class="test-box">
    <table style="width:100%">
      <tr>
        <td>Win11.iso</td>
        <td class="row-actions iso-actions">
          <button type="button" class="secondary row-menu-btn" id="btn-images" aria-label="Действия">
            <svg class="row-menu-icon" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" aria-hidden="true">
              <line x1="2.5" y1="4" x2="13.5" y2="4"></line>
              <line x1="2.5" y1="8" x2="13.5" y2="8"></line>
              <line x1="2.5" y1="12" x2="13.5" y2="12"></line>
            </svg>
            <span class="sr-only">☰</span>
          </button>
        </td>
      </tr>
    </table>
  </div>

  <!-- 3. Installations Table -->
  <div class="test-box">
    <table style="width:100%">
      <tr>
        <td>Установка Windows 11</td>
        <td class="download-row-actions">
          <div class="download-actions">
            <a class="btn secondary" id="btn-inst-edit" href="#">Изменить</a>
            <button type="button" class="secondary row-menu-btn" id="btn-inst" aria-label="Действия">
              <svg class="row-menu-icon" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" aria-hidden="true">
                <line x1="2.5" y1="4" x2="13.5" y2="4"></line>
                <line x1="2.5" y1="8" x2="13.5" y2="8"></line>
                <line x1="2.5" y1="12" x2="13.5" y2="12"></line>
              </svg>
              <span class="sr-only">☰</span>
            </button>
          </div>
        </td>
      </tr>
    </table>
  </div>

  <!-- 4. Storages Table -->
  <div class="test-box">
    <table style="width:100%">
      <tr>
        <td>SMB Share</td>
        <td class="row-actions">
          <div class="row-actions-group">
            <button type="button" class="secondary row-menu-btn" id="btn-stor" aria-label="Дополнительные действия">
              <svg class="row-menu-icon" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" aria-hidden="true">
                <line x1="2.5" y1="4" x2="13.5" y2="4"></line>
                <line x1="2.5" y1="8" x2="13.5" y2="8"></line>
                <line x1="2.5" y1="12" x2="13.5" y2="12"></line>
              </svg>
              <span class="sr-only">☰</span>
            </button>
          </div>
        </td>
      </tr>
    </table>
  </div>
</body>
</html>`;

  for (const theme of ['dark', 'light']) {
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await page.setContent(html);
      await page.evaluate(t => document.documentElement.dataset.theme = t, theme);

      const buttons = ['btn-answers', 'btn-images', 'btn-inst', 'btn-stor'];
      for (const id of buttons) {
        const btn = page.locator('#' + id);
        const icon = btn.locator('svg.row-menu-icon');
        
        const bBox = await btn.boundingBox();
        const iBox = await icon.boundingBox();

        assert(Math.abs(bBox.width - 28) < 1, `${id} width should be 28px, got ${bBox.width}`);
        assert(Math.abs(bBox.height - 28) < 1, `${id} height should be 28px, got ${bBox.height}`);
        assert(Math.abs(iBox.width - 14) < 1, `${id} icon width should be 14px, got ${iBox.width}`);
        assert(Math.abs(iBox.height - 14) < 1, `${id} icon height should be 14px, got ${iBox.height}`);

        // Verify centering: icon center should match button center
        const bCenterX = bBox.x + bBox.width / 2;
        const bCenterY = bBox.y + bBox.height / 2;
        const iCenterX = iBox.x + iBox.width / 2;
        const iCenterY = iBox.y + iBox.height / 2;

        assert(Math.abs(bCenterX - iCenterX) < 0.5, `${id} icon horizontal center mismatch: b=${bCenterX}, i=${iCenterX}`);
        assert(Math.abs(bCenterY - iCenterY) < 0.5, `${id} icon vertical center mismatch: b=${bCenterY}, i=${iCenterY}`);
      }

      // Check installations alignment: edit button and sandwich button vertical center
      const editBox = await page.locator('#btn-inst-edit').boundingBox();
      const menuBox = await page.locator('#btn-inst').boundingBox();
      const editCenterY = editBox.y + editBox.height / 2;
      const menuCenterY = menuBox.y + menuBox.height / 2;
      assert(Math.abs(editCenterY - menuCenterY) < 1, `installations edit and menu vertical centers mismatch: ${editCenterY} vs ${menuCenterY}`);
    }
  }

  console.log('VERIFY_SANDWICH_BUTTONS_OK');
  await browser.close();
})();
