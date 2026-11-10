// Oh My Boot — журнал сетевой загрузки (/events)
(() => {
  function initJournal() {
    const dialog = document.getElementById('journal-clear-dialog');
    if (!dialog) return;

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

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initJournal);
  } else {
    initJournal();
  }
})();
