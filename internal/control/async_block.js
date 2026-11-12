// Oh My Boot — клиентский модуль асинхронных блоков
(function () {
  'use strict';

  var activeControllers = new Set();
  var blockControllers = new WeakMap();

  var english = {
    'Загрузка…': 'Loading…',
    'Не удалось загрузить данные': 'Failed to load data',
    'Повторить': 'Retry'
  };

  function tr(text) {
    return (document.documentElement && document.documentElement.lang === 'en')
      ? (english[text] || text)
      : text;
  }

  function loadAsyncBlock(el) {
    if (!el || !el.getAttribute) return;
    var url = el.getAttribute('data-async-url');
    if (!url) return;

    var prev = blockControllers.get(el);
    if (prev) {
      prev.abort();
      activeControllers.delete(prev);
      blockControllers.delete(el);
    }

    var controller = new AbortController();
    activeControllers.add(controller);
    blockControllers.set(el, controller);

    el.setAttribute('aria-busy', 'true');
    el.dataset.loading = 'true';
    el.innerHTML = '<div class="async-block-loading"><span class="search-spinner" aria-hidden="true"></span> ' +
      tr('Загрузка…') + '</div>';

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
        if (!el.isConnected) return;
        el.removeAttribute('aria-busy');
        el.removeAttribute('data-loading');
        el.dataset.loaded = 'true';
        el.innerHTML = html;
        if (typeof window.initInfiniteScroll === 'function') {
          window.initInfiniteScroll();
        }
      })
      .catch(function (err) {
        if (err && err.name === 'AbortError') return;
        if (!el.isConnected) return;
        el.removeAttribute('aria-busy');
        el.removeAttribute('data-loading');
        el.innerHTML = '<div class="async-block-error"><span class="error">' +
          tr('Не удалось загрузить данные') +
          '</span> <button type="button" class="btn" data-async-retry>↻ ' +
          tr('Повторить') +
          '</button></div>';
      })
      .finally(function () {
        activeControllers.delete(controller);
        if (blockControllers.get(el) === controller) {
          blockControllers.delete(el);
        }
      });
  }

  function cancelAsyncBlock(el) {
    if (!el) return;
    var controller = blockControllers.get(el);
    if (controller) {
      controller.abort();
      activeControllers.delete(controller);
      blockControllers.delete(el);
    }
    el.removeAttribute('aria-busy');
    el.removeAttribute('data-loading');
  }

  function isInsideClosedDialog(el) {
    var dlg = el.closest('dialog');
    return dlg !== null && !dlg.open;
  }

  function onDialogOpen(dlg) {
    if (!dlg) return;
    var blocks = dlg.querySelectorAll('[data-async-url]');
    blocks.forEach(function (el) {
      if (el.dataset.loaded !== 'true' && el.dataset.loading !== 'true') {
        loadAsyncBlock(el);
      }
    });
  }

  function onDialogClose(dlg) {
    if (!dlg) return;
    var blocks = dlg.querySelectorAll('[data-async-url]');
    blocks.forEach(function (el) {
      cancelAsyncBlock(el);
    });
  }

  function initAsyncBlocks(root) {
    var scope = root || document;
    var blocks = scope.querySelectorAll('[data-async-url]');
    blocks.forEach(function (el) {
      if (!isInsideClosedDialog(el) && el.dataset.loaded !== 'true' && el.dataset.loading !== 'true') {
        loadAsyncBlock(el);
      }
    });
  }

  // Делегированный обработчик клика на [data-async-retry]
  document.addEventListener('click', function (ev) {
    var retryBtn = ev.target.closest('[data-async-retry]');
    if (!retryBtn) return;
    var el = retryBtn.closest('[data-async-url]');
    if (!el) return;
    ev.preventDefault();
    loadAsyncBlock(el);
  });

  // Делегированный обработчик открытия диалога версий меню и закрытия диалогов
  document.addEventListener('click', function (ev) {
    var rollbackBtn = ev.target.closest('[data-menu-rollback-open]');
    if (rollbackBtn) {
      var rollbackDialog = document.getElementById('menu-rollback-dialog');
      if (rollbackDialog) {
        var version = rollbackBtn.getAttribute('data-menu-rollback-open');
        rollbackDialog.querySelector('[name="id"]').value = version;
        rollbackDialog.querySelector('[data-rollback-version]').textContent = '#' + version;
        if (!rollbackDialog.open) {
          if (typeof rollbackDialog.showModal === 'function') rollbackDialog.showModal();
          else rollbackDialog.setAttribute('open', '');
        }
      }
      return;
    }

    var menuVersionsBtn = ev.target.closest('[data-menu-versions-open]');
    if (menuVersionsBtn) {
      var dlg = document.getElementById('menu-versions-dialog');
      if (dlg) {
        if (!dlg.open) {
          if (typeof dlg.showModal === 'function') dlg.showModal();
          else dlg.setAttribute('open', '');
        }
        onDialogOpen(dlg);
      }
      return;
    }

    var closeBtn = ev.target.closest('[data-close-dialog]');
    if (closeBtn) {
      var d = closeBtn.closest('dialog');
      if (d) {
        if (typeof d.close === 'function') d.close();
        else d.removeAttribute('open');
        onDialogClose(d);
      }
      return;
    }

    if (ev.target && ev.target.tagName === 'DIALOG' && ev.target.open) {
      var rect = ev.target.getBoundingClientRect();
      var isInDialog = (
        rect.top <= ev.clientY &&
        ev.clientY <= rect.top + rect.height &&
        rect.left <= ev.clientX &&
        ev.clientX <= rect.left + rect.width
      );
      if (!isInDialog) {
        if (typeof ev.target.close === 'function') ev.target.close();
        else ev.target.removeAttribute('open');
        onDialogClose(ev.target);
      }
    }
  });

  // Перехват вызовов showModal и close у диалогов
  if (typeof HTMLDialogElement !== 'undefined') {
    var origShowModal = HTMLDialogElement.prototype.showModal;
    if (origShowModal) {
      HTMLDialogElement.prototype.showModal = function () {
        origShowModal.apply(this, arguments);
        onDialogOpen(this);
      };
    }
    var origClose = HTMLDialogElement.prototype.close;
    if (origClose) {
      HTMLDialogElement.prototype.close = function () {
        origClose.apply(this, arguments);
        onDialogClose(this);
      };
    }
  }

  // Событие close у нативных диалогов
  document.addEventListener('close', function (ev) {
    if (ev.target && ev.target.tagName === 'DIALOG') {
      onDialogClose(ev.target);
    }
  }, true);

  // MutationObserver для отслеживания открытия/закрытия диалогов
  if (typeof MutationObserver !== 'undefined') {
    var dialogObserver = new MutationObserver(function (mutations) {
      mutations.forEach(function (m) {
        if (m.type === 'attributes' && m.attributeName === 'open') {
          var target = m.target;
          if (target && target.tagName === 'DIALOG') {
            if (target.open) {
              onDialogOpen(target);
            } else {
              onDialogClose(target);
            }
          }
        }
      });
    });

    var startObserving = function () {
      if (document.body) {
        dialogObserver.observe(document.body, {
          attributes: true,
          subtree: true,
          attributeFilter: ['open']
        });
      }
    };

    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', startObserving);
    } else {
      startObserving();
    }
  }

  // Отмена активных запросов при уходе со страницы
  window.addEventListener('pagehide', function () {
    activeControllers.forEach(function (c) {
      c.abort();
    });
    activeControllers.clear();
  });

  // Экспорт функций в глобальную область
  window.loadAsyncBlock = loadAsyncBlock;
  window.cancelAsyncBlock = cancelAsyncBlock;
  window.initAsyncBlocks = initAsyncBlocks;

  // Инициализация при загрузке документа
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      initAsyncBlocks();
    });
  } else {
    initAsyncBlocks();
  }
})();
