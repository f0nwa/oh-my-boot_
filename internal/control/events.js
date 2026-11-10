// Oh My Boot — журнал сетевой загрузки (/events)
(() => {
  function initJournal() {
    // 1. Управление модальным окном подтверждения очистки журнала
    const dialog = document.getElementById('journal-clear-dialog');
    if (dialog && !dialog.dataset.initialized) {
      dialog.dataset.initialized = "true";
      document.querySelectorAll('[data-journal-clear-open]').forEach((btn) => {
        btn.addEventListener('click', () => {
          if (typeof dialog.showModal === 'function') {
            dialog.showModal();
          } else {
            dialog.setAttribute('open', '');
          }
        });
      });

      dialog.querySelectorAll('[data-close-dialog]').forEach((btn) => {
        btn.addEventListener('click', () => {
          if (typeof dialog.close === 'function') {
            dialog.close();
          } else {
            dialog.removeAttribute('open');
          }
        });
      });

      dialog.addEventListener('click', (e) => {
        if (e.target === dialog && typeof dialog.close === 'function') {
          dialog.close();
        }
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
      const retry = event.target.closest('[data-events-retry]');
      if (!retry) return;
      const area = retry.closest('[data-events-url]');
      if (area) loadBlock(area);
      else location.reload();
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
