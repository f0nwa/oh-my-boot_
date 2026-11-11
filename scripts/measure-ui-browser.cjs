// Use an existing Playwright installation and Chromium; do not install packages.
// node measure-ui-browser.cjs BASE_URL RAW_COOKIE_FILE OUTPUT CHROMIUM_EXECUTABLE [cycles-only|search-only]
const fs = require('node:fs');
const assert = require('node:assert/strict');
const { chromium } = require('playwright');

(async () => {
  const [base, cookieFile, output, executablePath, mode] = process.argv.slice(2);
  const result = { base, started: new Date().toISOString(), errors: [], networkErrors: [], samples: [], scroll: [] };
  const save = () => fs.writeFileSync(output, JSON.stringify(result, null, 2) + '\n');
  const browser = await chromium.launch({ executablePath, headless: true });
  try {
    result.browser = browser.version();
    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
    await context.addCookies([{ name: 'omb_session', value: fs.readFileSync(cookieFile, 'utf8').trim(), url: base, secure: true }]);
    const page = await context.newPage();
    page.on('pageerror', e => result.errors.push(e.message));
    page.on('requestfailed', r => { if (r.failure()?.errorText !== 'net::ERR_ABORTED') result.networkErrors.push({ url: r.url(), error: r.failure() }); });
    page.on('response', r => { if (r.status() >= 400) result.networkErrors.push({ url: r.url(), status: r.status() }); });
    const cdp = await context.newCDPSession(page);
    await cdp.send('Performance.enable');
    async function sample(label, collect = false) {
      if (collect) await cdp.send('HeapProfiler.collectGarbage');
      const heap = await cdp.send('Runtime.getHeapUsage');
      const dom = await cdp.send('Memory.getDOMCounters');
      const metrics = await cdp.send('Performance.getMetrics');
      const rows = await page.locator('tbody tr').count();
      result.samples.push({ label, unix: Date.now()/1000, heap, dom, rows,
        performance: Object.fromEntries(metrics.metrics.filter(m => ['JSHeapUsedSize','JSHeapTotalSize','LayoutCount','TaskDuration'].includes(m.name)).map(m => [m.name,m.value])) });
      save();
    }
    async function goto(path) {
      const response = await page.goto(base + path, { waitUntil: 'domcontentloaded' });
      assert.equal(response.status(), 200);
      assert(!page.url().includes('/login'), 'session expired');
    }
    async function scroll(path, expected, kind) {
      await goto(path);
      const table = page.locator(kind === 'machines' ? '#machines-table-area tbody tr' : '#events-content tbody tr');
      await table.first().waitFor();
      const initial = await table.count();
      assert.equal(initial, 50);
      await sample(`${kind}-first`);
      const start = Date.now();
      for (let step=0; step<Math.ceil(expected/50)+3; step++) {
        const sentinel = page.locator('.infinite-scroll-sentinel').last();
        if (await sentinel.getAttribute('data-has-more') !== 'true') break;
        const before = await table.count();
        await sentinel.scrollIntoViewIfNeeded();
        await page.waitForFunction(({ before, selector }) => {
          const s = document.querySelector('.infinite-scroll-sentinel');
          return document.querySelectorAll(selector).length > before || s?.dataset.hasMore === 'false' || document.querySelector('[data-infinite-retry]');
        }, { before, selector: kind === 'machines' ? '#machines-table-area tbody tr' : '#events-content tbody tr' }, { timeout: 20000 });
        assert.equal(await page.locator('[data-infinite-retry]').count(), 0, 'scroll request failed');
        if (step % 50 === 0) { console.log(kind, await table.count()); save(); }
      }
      const texts = await table.allTextContents();
      assert.equal(texts.length, expected, `${kind} row count`);
      const ids = texts.map(t => t.match(kind === 'machines' ? /02:7e:57:00:[0-9a-f]{2}:[0-9a-f]{2}/ : kind === 'requests' ? /\/load\/\d{6}/ : /load-\d{6}/)?.[0]);
      assert(ids.every(Boolean), 'missing fixture identifiers');
      assert.equal(new Set(ids).size, expected, `${kind} duplicates`);
      result.scroll.push({ kind, rows: texts.length, unique: new Set(ids).size, firstRows: initial, seconds: (Date.now()-start)/1000 });
      await sample(`${kind}-full`);
    }
    await goto('/menu');
    await sample('menu-baseline-gc', true);
    if (mode === 'search-only') {
      await goto('/machines');
      await sample('search-baseline-gc', true);
      for (let i=1; i<=40; i++) {
        for (const query of ['load-0001', '']) {
          await page.locator('#q').fill(query);
          await page.waitForFunction(({ query }) => {
            const rows = document.querySelectorAll('#machines-table-area tbody tr');
            return rows.length === (query ? 1 : 50) && document.querySelector('#q')?.getAttribute('aria-busy') === 'false';
          }, { query });
        }
        if (i%10 === 0) await sample(`search-cycle-${i}-gc`, true);
      }
      await goto('/menu');
      await sample('search-after-navigation-gc', true);
      result.ok = result.errors.length === 0 && result.networkErrors.length === 0;
      assert(result.ok);
      result.finished = new Date().toISOString();
      save();
      return;
    }
    if (mode !== 'cycles-only') {
      await scroll('/machines', 500, 'machines');
      await scroll('/events?tab=dhcp', 1000, 'dhcp');
      await scroll('/events?tab=requests', 20000, 'requests');
    }
    await goto('/menu');
    await sample('after-full-scroll-gc', true);
    for (let i=1; i<=20; i++) {
      await goto('/machines');
      const search = page.locator('#q');
      await search.fill('load-0001');
      await page.waitForFunction(() => document.querySelectorAll('#machines-table-area tbody tr').length === 1 && document.querySelector('#machines-table-area')?.textContent.includes('load-0001'));
      for (const tab of ['requests', 'dhcp', 'machines']) {
        if (tab === 'requests') await goto('/events?tab=requests');
        else {
          await page.locator('nav.tabs a[href="/events?tab=' + tab + '"]').click();
          await page.waitForURL('**/events?tab=' + tab);
        }
        await page.locator('#events-content tbody tr').first().waitFor();
      }
      await goto('/menu');
      await page.locator('[data-menu-versions-open]').click();
      await page.locator('#menu-versions-dialog table').waitFor();
      await page.keyboard.press('Escape');
      await sample(`cycle-${i}-menu-gc`, true);
      if (i%5 === 0) console.log('cycles', i);
    }
    await page.screenshot({ path: output.replace(/\.json$/, '.png'), fullPage: false });
    await sample('final-menu-gc', true);
    assert.equal(result.errors.length, 0, 'JS errors');
    assert.equal(result.networkErrors.length, 0, 'network errors');
    result.finished = new Date().toISOString();
    result.ok = true;
    save();
  } catch (e) {
    result.failure = e.stack;
    save();
    throw e;
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
