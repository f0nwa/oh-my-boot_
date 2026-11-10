(function () {
  'use strict';

  var dialog = document.getElementById('account-password-dialog');
  if (!dialog) return;

  function validateConfirm() {
    var next = dialog.querySelector('input[name="new_password"]');
    var confirm = dialog.querySelector('input[name="confirm_password"]');
    if (!next || !confirm || typeof confirm.setCustomValidity !== 'function') return;
    if (confirm.value && next.value !== confirm.value) {
      confirm.setCustomValidity(document.documentElement.lang === 'en' ? 'Passwords do not match' : 'Пароли не совпадают');
    } else {
      confirm.setCustomValidity('');
    }
  }

  function resetDialogForm() {
    var cur = dialog.querySelector('input[name="current_password"]');
    var next = dialog.querySelector('input[name="new_password"]');
    var confirm = dialog.querySelector('input[name="confirm_password"]');
    if (cur) {
      cur.value = '';
    }
    if (next) {
      next.value = '';
      next.type = 'password';
    }
    if (confirm) {
      confirm.value = '';
      confirm.type = 'password';
      if (typeof confirm.setCustomValidity === 'function') {
        confirm.setCustomValidity('');
      }
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

  dialog.addEventListener('input', function (event) {
    if (event.target && (event.target.name === 'new_password' || event.target.name === 'confirm_password')) {
      validateConfirm();
    }
  });

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
