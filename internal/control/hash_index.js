(function () {
  var isEn = document.documentElement && document.documentElement.lang === 'en';

  function formatSpeed(bps) {
    if (!bps || bps <= 0) return '';
    var mb = bps / (1024 * 1024);
    var unit = isEn ? 'MB/s' : 'МБ/с';
    if (mb >= 10) return Math.round(mb) + ' ' + unit;
    return mb.toFixed(1) + ' ' + unit;
  }

  function copyWithFallback(text, done) {
    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      navigator.clipboard.writeText(text).then(done, function () {
        fallbackCopy(text, done);
      });
      return;
    }
    fallbackCopy(text, done);
  }

  function fallbackCopy(text, done) {
    try {
      var tmp = document.createElement('textarea');
      tmp.value = text;
      tmp.setAttribute('readonly', '');
      tmp.style.position = 'fixed';
      tmp.style.left = '-9999px';
      document.body.appendChild(tmp);
      tmp.select();
      if (document.execCommand('copy') && done) done();
      document.body.removeChild(tmp);
    } catch (_) {
      // ignore
    }
  }

  document.addEventListener('click', function (event) {
    var btn = event.target.closest && event.target.closest('[data-sha-copy-btn]');
    if (!btn) return;
    var val = btn.getAttribute('data-sha-copy-btn');
    if (!val) return;
    var orig = btn.getAttribute('data-orig-label') || btn.textContent;
    btn.setAttribute('data-orig-label', orig);
    var copied = btn.getAttribute('data-copied') || (isEn ? 'Copied' : 'Скопировано');
    copyWithFallback(val, function () {
      btn.textContent = copied;
      clearTimeout(btn._copyTimer);
      btn._copyTimer = setTimeout(function () {
        btn.textContent = orig;
      }, 1400);
    });
  });

  var bar = document.getElementById('hash-index-bar');
  if (!bar) return;

  var countEl = document.getElementById('hash-index-count');
  var fileEl = document.getElementById('hash-index-file');
  var sepEl = document.getElementById('hash-index-sep');
  var progWrap = document.getElementById('hash-index-prog-wrap');
  var trackEl = document.getElementById('hash-index-track');
  var fillEl = document.getElementById('hash-index-fill');
  var pctEl = document.getElementById('hash-index-pct');
  var speedEl = document.getElementById('hash-index-speed');
  var pausedLabel = bar.getAttribute('data-paused-label') || (isEn ? 'Paused during active transfer' : 'Пауза на время загрузки или сканирования');

  var timer = null;
  var wasActive = false;
  var lastIndexed = -1;

  function schedule(ms) {
    clearTimeout(timer);
    if (document.hidden) return;
    timer = setTimeout(poll, ms);
  }

  function render(st) {
    if (!st || !st.enabled) {
      bar.hidden = true;
      wasActive = false;
      return false;
    }
    var running = st.state === 'running' && st.current;
    var paused = st.state === 'paused' && st.pending_count > 0;
    var active = running || paused;

    if (lastIndexed >= 0 && st.indexed_count > lastIndexed) {
      document.dispatchEvent(new CustomEvent('omb:refresh'));
    }
    lastIndexed = typeof st.indexed_count === 'number' ? st.indexed_count : lastIndexed;

    if (!active) {
      bar.hidden = true;
      if (wasActive) {
        wasActive = false;
        document.dispatchEvent(new CustomEvent('omb:refresh'));
      }
      return false;
    }

    wasActive = true;
    bar.hidden = false;
    if (countEl) {
      countEl.textContent = st.indexed_count + ' / ' + st.total_count;
    }
    if (running) {
      if (sepEl) sepEl.hidden = false;
      if (fileEl) {
        fileEl.textContent = st.current.path || '';
        fileEl.title = (st.current.storage_name ? st.current.storage_name + ' · ' : '') + (st.current.path || '');
      }
      if (progWrap) progWrap.hidden = false;
      if (trackEl) trackEl.hidden = false;
      var pct = Math.max(0, Math.min(100, st.current.percent || 0));
      if (fillEl) fillEl.style.width = pct + '%';
      if (pctEl) pctEl.textContent = pct + '%';
      if (speedEl) {
        var spd = formatSpeed(st.current.speed_bps);
        speedEl.textContent = spd;
        speedEl.hidden = !spd;
      }
    } else {
      if (sepEl) sepEl.hidden = false;
      if (fileEl) {
        fileEl.textContent = pausedLabel;
        fileEl.title = pausedLabel;
      }
      if (progWrap) progWrap.hidden = true;
      if (trackEl) trackEl.hidden = true;
    }
    return true;
  }

  function poll() {
    if (document.hidden) return;
    fetch('/images/hash-index/status', { credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) throw new Error('status');
        return r.json();
      })
      .then(function (data) {
        var active = render(data);
        schedule(active ? 2000 : 15000);
      })
      .catch(function () {
        schedule(15000);
      });
  }

  document.addEventListener('visibilitychange', function () {
    if (!document.hidden) poll();
    else clearTimeout(timer);
  });

  poll();
})();
