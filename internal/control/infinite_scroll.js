// Oh My Boot — клиентский модуль бесконечного скролла
(function () {
  'use strict';

  var activeControllers = new Set();
  var observer = null;

  var english = {
    'Не удалось загрузить данные': 'Failed to load data',
    'Повторить': 'Retry',
    'Загрузка следующих 50 записей…': 'Loading next 50 entries…'
  };

  function tr(text) {
    return document.documentElement.lang === 'en' ? (english[text] || text) : text;
  }

  function findTargetTable(sentinel) {
    var prev = sentinel.previousElementSibling;
    while (prev) {
      if (prev.tagName === 'TABLE') return prev;
      var t = prev.querySelector('table');
      if (t) return t;
      prev = prev.previousElementSibling;
    }
    if (sentinel.parentElement) {
      return sentinel.parentElement.querySelector('table');
    }
    return null;
  }

  function parsePartial(html) {
    var parser = new DOMParser();
    // Оборачивание в <table><tbody> гарантирует, что теги <tr> не отсекутся парсером HTML5
    var doc = parser.parseFromString('<table><tbody>' + html + '</tbody></table>', 'text/html');
    var rows = Array.from(doc.querySelectorAll('tbody tr'));
    if (rows.length === 0) {
      rows = Array.from(doc.querySelectorAll('tr'));
    }
    var sentinel = doc.querySelector('.infinite-scroll-sentinel');
    if (!sentinel) {
      var plainDoc = parser.parseFromString(html, 'text/html');
      sentinel = plainDoc.querySelector('.infinite-scroll-sentinel');
    }
    return { rows: rows, sentinel: sentinel };
  }

  function buildUrl(sentinel) {
    var rawUrl = sentinel.getAttribute('data-url');
    if (!rawUrl) return null;
    var u = new URL(rawUrl, window.location.origin);
    u.searchParams.set('partial', '1');
    if (sentinel.dataset.nextOffset !== undefined && sentinel.dataset.nextOffset !== '') {
      u.searchParams.set('offset', sentinel.dataset.nextOffset);
    }
    if (sentinel.dataset.beforeTime) {
      u.searchParams.set('before_time', sentinel.dataset.beforeTime);
    }
    if (sentinel.dataset.beforeId) {
      u.searchParams.set('before_id', sentinel.dataset.beforeId);
    }
    return u.toString();
  }

  function loadNext(sentinel) {
    if (sentinel.getAttribute('data-has-more') !== 'true') return;
    if (sentinel.dataset.loading === 'true') return;

    var url = buildUrl(sentinel);
    if (!url) return;

    sentinel.dataset.loading = 'true';
    if (!sentinel._originalHTML) {
      sentinel._originalHTML = sentinel.innerHTML;
    }

    var controller = new AbortController();
    activeControllers.add(controller);

    fetch(url, {
      credentials: 'same-origin',
      headers: { 'Accept': 'text/html' },
      signal: controller.signal
    })
      .then(function (res) {
        if (!res.ok) throw new Error('HTTP ' + res.status);
        return res.text();
      })
      .then(function (html) {
        if (!sentinel.isConnected) return;
        var parsed = parsePartial(html);
        var table = findTargetTable(sentinel);
        var tbody = table ? (table.querySelector('tbody') || table) : null;

        if (tbody && parsed.rows.length > 0) {
          parsed.rows.forEach(function (row) {
            tbody.appendChild(row);
          });
        }

        if (parsed.sentinel) {
          if (observer) observer.unobserve(sentinel);
          sentinel.replaceWith(parsed.sentinel);
          if (observer && parsed.sentinel.getAttribute('data-has-more') === 'true') {
            observer.observe(parsed.sentinel);
          }
        } else {
          sentinel.removeAttribute('data-loading');
          sentinel.setAttribute('data-has-more', 'false');
        }
      })
      .catch(function (err) {
        if (err && err.name === 'AbortError') return;
        if (!sentinel.isConnected) return;
        sentinel.removeAttribute('data-loading');
        sentinel.innerHTML = '<div class="infinite-scroll-error"><span class="error">' +
          tr('Не удалось загрузить данные') +
          '</span> <button type="button" class="btn" data-infinite-retry>' +
          tr('Повторить') +
          '</button></div>';
      })
      .finally(function () {
        activeControllers.delete(controller);
      });
  }

  function retryLoad(sentinel) {
    sentinel.innerHTML = sentinel._originalHTML || ('<div class="infinite-scroll-loading"><span class="search-spinner" aria-hidden="true"></span> ' + tr('Загрузка следующих 50 записей…') + '</div>');
    loadNext(sentinel);
  }

  function init() {
    if (!('IntersectionObserver' in window)) return;

    if (!observer) {
      observer = new IntersectionObserver(function (entries) {
        entries.forEach(function (entry) {
          if (entry.isIntersecting) {
            loadNext(entry.target);
          }
        });
      }, { rootMargin: '200px' });
    }

    document.querySelectorAll('.infinite-scroll-sentinel').forEach(function (el) {
      if (el.getAttribute('data-has-more') === 'true') {
        observer.observe(el);
      }
    });
  }

  document.addEventListener('click', function (ev) {
    var retryBtn = ev.target.closest('[data-infinite-retry]');
    if (!retryBtn) return;
    var sentinel = retryBtn.closest('.infinite-scroll-sentinel');
    if (!sentinel) return;
    ev.preventDefault();
    retryLoad(sentinel);
  });

  window.addEventListener('pagehide', function () {
    activeControllers.forEach(function (c) {
      c.abort();
    });
    activeControllers.clear();
  });

  window.initInfiniteScroll = init;

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
