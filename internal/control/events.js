// Oh My Boot — журнал сетевой загрузки (/events)
(() => {
  function initJournal() {
    // 1. Управление модальным окном подтверждения очистки журнала
    const dialog = document.getElementById('journal-clear-dialog');
    if (dialog) {
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

    if (retentionSelect && retentionBtn) {
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

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initJournal);
  } else {
    initJournal();
  }
})();
