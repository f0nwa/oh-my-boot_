(function () {
  'use strict';

  var dialog = document.getElementById('account-password-dialog');
  var fsPwDialog = document.getElementById('account-fs-password-dialog');

  function validateConfirm() {
    if (!dialog) return;
    var next = dialog.querySelector('input[name="new_password"]');
    var confirm = dialog.querySelector('input[name="confirm_password"]');
    if (!next || !confirm || typeof confirm.setCustomValidity !== 'function') return;
    if (confirm.value && next.value !== confirm.value) {
      confirm.setCustomValidity(document.documentElement.lang === 'en' ? 'Passwords do not match' : 'Пароли не совпадают');
    } else {
      confirm.setCustomValidity('');
    }
  }

  function validateFsConfirm() {
    if (!fsPwDialog) return;
    var next = fsPwDialog.querySelector('input[name="password"]');
    var confirm = fsPwDialog.querySelector('input[name="confirm_password"]');
    if (!next || !confirm || typeof confirm.setCustomValidity !== 'function') return;
    if (confirm.value && next.value !== confirm.value) {
      confirm.setCustomValidity(document.documentElement.lang === 'en' ? 'Passwords do not match' : 'Пароли не совпадают');
    } else {
      confirm.setCustomValidity('');
    }
  }

  function resetDialogForm() {
    if (!dialog) return;
    var cur = dialog.querySelector('input[name="current_password"]');
    var next = dialog.querySelector('input[name="new_password"]');
    var confirm = dialog.querySelector('input[name="confirm_password"]');
    if (cur) {
      cur.value = '';
      cur.type = 'password';
      cur.dispatchEvent(new Event('omb:password-sync'));
    }
    if (next) {
      next.value = '';
      next.type = 'password';
      next.dispatchEvent(new Event('omb:password-sync'));
    }
    if (confirm) {
      confirm.value = '';
      confirm.type = 'password';
      confirm.dispatchEvent(new Event('omb:password-sync'));
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

  function resetFsDialogForm() {
    if (!fsPwDialog) return;
    var next = fsPwDialog.querySelector('input[name="password"]');
    var confirm = fsPwDialog.querySelector('input[name="confirm_password"]');
    if (next) {
      next.value = '';
      next.type = 'password';
      next.dispatchEvent(new Event('omb:password-sync'));
    }
    if (confirm) {
      confirm.value = '';
      confirm.type = 'password';
      confirm.dispatchEvent(new Event('omb:password-sync'));
      if (typeof confirm.setCustomValidity === 'function') {
        confirm.setCustomValidity('');
      }
    }
    fsPwDialog.querySelectorAll('small[role="status"]').forEach(function (el) {
      el.textContent = '';
    });
    fsPwDialog.querySelectorAll('.pw-tools button').forEach(function (btn) {
      if (btn.textContent === 'Скопировать' || btn.textContent === 'Copy') {
        btn.hidden = true;
      }
    });
  }

  if (dialog) {
    dialog.addEventListener('input', function (event) {
      if (event.target && (event.target.name === 'new_password' || event.target.name === 'confirm_password')) {
        validateConfirm();
      }
    });
  }

  if (fsPwDialog) {
    fsPwDialog.addEventListener('input', function (event) {
      if (event.target && (event.target.name === 'password' || event.target.name === 'confirm_password')) {
        validateFsConfirm();
      }
    });
  }

  function openDialog(reset) {
    if (!dialog || typeof dialog.showModal !== 'function') return;
    if (reset) {
      resetDialogForm();
    }
    dialog.showModal();
    var cur = dialog.querySelector('input[name="current_password"]');
    if (cur) {
      cur.focus();
    }
  }

  function openFsDialog(reset) {
    if (!fsPwDialog || typeof fsPwDialog.showModal !== 'function') return;
    if (reset) {
      resetFsDialogForm();
    }
    fsPwDialog.showModal();
    var next = fsPwDialog.querySelector('input[name="password"]');
    if (next) {
      next.focus();
    }
  }

  function copyText(btn, text) {
    var origText = btn.textContent;
    var copiedText = (document.documentElement && document.documentElement.lang === 'en') ? 'Copied' : 'Скопировано';
    function showSuccess() {
      btn.textContent = copiedText;
      setTimeout(function () {
        btn.textContent = origText;
      }, 1200);
    }
    if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
      navigator.clipboard.writeText(text).then(showSuccess, function () {
        fallbackCopy(text, showSuccess);
      });
    } else {
      fallbackCopy(text, showSuccess);
    }
  }

  function fallbackCopy(text, cb) {
    try {
      var tmp = document.createElement('textarea');
      tmp.value = text;
      tmp.setAttribute('readonly', '');
      tmp.style.position = 'fixed';
      tmp.style.left = '-9999px';
      document.body.appendChild(tmp);
      tmp.select();
      if (document.execCommand('copy')) {
        cb();
      }
      document.body.removeChild(tmp);
    } catch (_) {
      // ignore
    }
  }

  document.addEventListener('click', function (event) {
    var copyBtn = event.target.closest('[data-copy]');
    if (copyBtn) {
      event.preventDefault();
      var text = copyBtn.getAttribute('data-copy');
      if (text) {
        copyText(copyBtn, text);
      }
      return;
    }

    var openBtn = event.target.closest('[data-account-password-open]');
    if (openBtn) {
      event.preventDefault();
      openDialog(true);
      return;
    }

    var openFsBtn = event.target.closest('[data-account-fs-password-open]');
    if (openFsBtn) {
      event.preventDefault();
      openFsDialog(true);
      return;
    }

    var closeBtn = event.target.closest('[data-close-dialog]');
    if (closeBtn) {
      var d = closeBtn.closest('dialog');
      if (d && typeof d.close === 'function') {
        event.preventDefault();
        d.close();
      }
      return;
    }
  });

  [dialog, fsPwDialog].forEach(function (d) {
    if (!d) return;
    d.addEventListener('click', function (event) {
      if (event.target === d && typeof d.close === 'function') {
        d.close();
      }
    });
  });

  if (dialog && dialog.getAttribute('data-auto-open') === 'true') {
    openDialog(false);
  }
})();
