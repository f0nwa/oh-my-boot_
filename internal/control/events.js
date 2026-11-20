// Oh My Boot — журнал сетевой загрузки (/events)
(() => {
  function setupModalBackdrop(dlg) {
    if (!dlg || dlg.dataset.backdropInitialized) return;
    dlg.dataset.backdropInitialized = "true";

    dlg.querySelectorAll('[data-close-dialog]').forEach((btn) => {
      btn.addEventListener('click', () => {
        if (typeof dlg.close === 'function') {
          dlg.close();
        } else {
          dlg.removeAttribute('open');
        }
      });
    });

    dlg.addEventListener('click', (e) => {
      if (e.target !== dlg) return;
      const rect = dlg.getBoundingClientRect();
      const isInDialog = (
        rect.top <= e.clientY && e.clientY <= rect.top + rect.height &&
        rect.left <= e.clientX && e.clientX <= rect.left + rect.width
      );
      if (!isInDialog && typeof dlg.close === 'function') {
        dlg.close();
      }
    });
  }

  function initJournal() {
    // 1. Управление модальными окнами настроек и подтверждения очистки
    const settingsDialog = document.getElementById('journal-settings-dialog');
    const confirmDialog = document.getElementById('journal-clear-confirm-dialog') || document.getElementById('journal-clear-dialog');

    if (settingsDialog && !settingsDialog.dataset.initialized) {
      settingsDialog.dataset.initialized = "true";
      setupModalBackdrop(settingsDialog);

      document.querySelectorAll('[data-journal-settings-open]').forEach((btn) => {
        btn.addEventListener('click', () => {
          if (typeof settingsDialog.showModal === 'function') {
            settingsDialog.showModal();
          } else {
            settingsDialog.setAttribute('open', '');
          }
        });
      });
    }

    if (confirmDialog && !confirmDialog.dataset.initialized) {
      confirmDialog.dataset.initialized = "true";
      setupModalBackdrop(confirmDialog);

      document.querySelectorAll('[data-journal-clear-open]').forEach((btn) => {
        btn.addEventListener('click', () => {
          if (typeof confirmDialog.showModal === 'function') {
            confirmDialog.showModal();
          } else {
            confirmDialog.setAttribute('open', '');
          }
        });
      });
    }

    // 2. Реактивная активация кнопки «Сохранить» при изменении схемы автоочистки
    const retentionSelect = document.getElementById('journal-days');
    const retentionForm = retentionSelect ? retentionSelect.closest('form') : null;
    const retentionBtn = retentionForm ? retentionForm.querySelector('button') : null;

    if (retentionSelect && retentionBtn && !retentionSelect.dataset.initialized) {
      retentionSelect.dataset.initialized = "true";
      const initialDays = retentionSelect.value;
      retentionBtn.disabled = true;
      retentionBtn.classList.remove('btn-primary');

      retentionSelect.addEventListener('change', () => {
        const isChanged = retentionSelect.value !== initialDays;
        retentionBtn.disabled = !isChanged;
        if (isChanged) {
          retentionBtn.classList.add('btn-primary');
        } else {
          retentionBtn.classList.remove('btn-primary');
        }
      });
    }
  }

  const flights = new Map();

  function loadBlock(area) {
    if (flights.has(area)) return;
    const controller = new AbortController();
    flights.set(area, controller);
    area.setAttribute('aria-busy', 'true');
    const timer = setTimeout(() => controller.abort(), 15000);
    fetch(area.dataset.eventsUrl, {
      credentials: 'same-origin',
      headers: { Accept: 'text/html' },
      signal: controller.signal
    }).then((res) => {
      if (!res.ok || res.redirected) throw new Error('request failed');
      return res.text();
    }).then((html) => {
      if (!area.isConnected || controller.signal.aborted) return;
      area.innerHTML = html;
      initJournal();
      if (typeof window.initInfiniteScroll === 'function') window.initInfiniteScroll();
    }).catch(() => {
      if (!area.isConnected) return;
      const labels = document.getElementById('events-content');
      const message = document.createElement('p');
      message.className = 'error';
      message.setAttribute('role', 'alert');
      message.textContent = labels.dataset.eventsError;
      const retry = document.createElement('button');
      retry.type = 'button';
      retry.className = 'btn';
      retry.dataset.eventsRetry = '';
      retry.textContent = labels.dataset.eventsRetryLabel;
      if (area.id === 'events-heading') {
        const oldError = area.querySelector('[data-events-load-error]');
        if (oldError) oldError.remove();
        const error = document.createElement('div');
        error.dataset.eventsLoadError = '';
        error.append(message, retry);
        area.append(error);
      } else {
        area.replaceChildren(message, retry);
      }
    }).finally(() => {
      clearTimeout(timer);
      flights.delete(area);
      area.setAttribute('aria-busy', 'false');
    });
  }

  function init() {
    initJournal();
    document.querySelectorAll('[data-events-url]').forEach(loadBlock);
    document.addEventListener('click', (event) => {
      const openBtn = event.target.closest('[data-system-events-clear-open]');
      if (openBtn) {
        const dlg = document.getElementById('system-events-clear-dialog');
        if (dlg) {
          if (typeof dlg.showModal === 'function') dlg.showModal();
          else dlg.setAttribute('open', '');
        }
        return;
      }
      const closeBtn = event.target.closest('#system-events-clear-dialog [data-close-dialog]');
      if (closeBtn) {
        const dlg = document.getElementById('system-events-clear-dialog');
        if (dlg) {
          if (typeof dlg.close === 'function') dlg.close();
          else dlg.removeAttribute('open');
        }
        return;
      }
      const dlg = document.getElementById('system-events-clear-dialog');
      if (dlg && event.target === dlg && typeof dlg.close === 'function') {
        dlg.close();
        return;
      }

      const retry = event.target.closest('[data-events-retry]');
      if (!retry) return;
      const area = retry.closest('[data-events-url]');
      if (area) loadBlock(area);
      else location.reload();
    });
    document.addEventListener('change', (event) => {
      const sel = event.target.closest('.system-events-filter select');
      if (sel && sel.form) sel.form.submit();
    });
    window.addEventListener('pagehide', () => {
      flights.forEach((controller) => controller.abort());
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
