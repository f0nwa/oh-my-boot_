(function () {
  'use strict';

  var dialog = document.getElementById('account-password-dialog');
  if (!dialog) return;

  function resetDialogForm() {
    var cur = dialog.querySelector('input[name="current_password"]');
    var next = dialog.querySelector('input[name="new_password"]');
    if (cur) {
      cur.value = '';
    }
    if (next) {
      next.value = '';
      next.type = 'password';
    }
    dialog.querySelectorAll('small[role="status"]').forEach(function (el) {
      el.textContent = '';
    });
    dialog.querySelectorAll('.pw-tools button').forEach(function (btn) {
      if (btn.textContent === 'Скопировать' || btn.textContent === 'Copy') {
        btn.hidden = true;
      }
    });
    dialog.querySelectorAll('[data-dialog-error]').forEach(function (el) {
      el.hidden = true;
    });
  }

  function openDialog(reset) {
    if (typeof dialog.showModal !== 'function') return;
    if (reset) {
      resetDialogForm();
    }
    dialog.showModal();
    var cur = dialog.querySelector('input[name="current_password"]');
    if (cur) {
      cur.focus();
    }
  }

  document.addEventListener('click', function (event) {
    var openBtn = event.target.closest('[data-account-password-open]');
    if (openBtn) {
      event.preventDefault();
      openDialog(true);
      return;
    }

    var closeBtn = event.target.closest('[data-close-dialog]');
    if (closeBtn && closeBtn.closest('#account-password-dialog')) {
      event.preventDefault();
      if (typeof dialog.close === 'function') {
        dialog.close();
      }
    }
  });

  dialog.addEventListener('click', function (event) {
    if (event.target === dialog && typeof dialog.close === 'function') {
      dialog.close();
    }
  });

  if (dialog.getAttribute('data-auto-open') === 'true') {
    openDialog(false);
  }
})();
