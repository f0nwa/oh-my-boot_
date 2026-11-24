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

  // --- 2FA (TOTP) setup, recovery code regeneration, and disable ---
  var setup2FADialog = document.getElementById('account-2fa-setup-dialog');
  var regen2FADialog = document.getElementById('account-2fa-regen-dialog');
  var disable2FADialog = document.getElementById('account-2fa-disable-dialog');
  var inline2FA = document.querySelector('[data-setup-2fa-inline]');
  var activeRecoveryCodes = [];

  function show2FAError(container, msg) {
    if (!container) return;
    var errBox = container.querySelector('[data-2fa-error]');
    if (!errBox) return;
    errBox.textContent = msg || '';
    errBox.hidden = !msg;
  }

  function renderCodes(container, codes) {
    activeRecoveryCodes = Array.isArray(codes) ? codes.slice() : [];
    var grid = container.querySelector('[data-2fa-codes]');
    if (!grid) return;
    grid.textContent = '';
    activeRecoveryCodes.forEach(function (code) {
      var pill = document.createElement('div');
      pill.className = 'recovery-code-pill mono';
      pill.textContent = code;
      grid.appendChild(pill);
    });
  }

  function sync2FAEnableSubmit(container) {
    if (!container) return;
    var form = container.querySelector('[data-2fa-enable-form]');
    if (!form) return;
    var submit = form.querySelector('button[type="submit"]');
    if (!submit) return;
    var codeIn = form.querySelector('input[name="code"]');
    var pend = form.querySelector('[data-2fa-pending]');
    var digits = codeIn ? (codeIn.value || '').replace(/\s+/g, '') : '';
    var hasPending = !pend || pend.value.trim() !== '';
    submit.disabled = !(hasPending && /^\d{6}$/.test(digits));
  }

  function begin2FA(container) {
    if (!container) return;
    var csrf = container.getAttribute('data-csrf') || '';
    var step1 = container.querySelector('[data-2fa-step1]');
    var step2 = container.querySelector('[data-2fa-step2]');
    if (step1) step1.hidden = false;
    if (step2) step2.hidden = true;
    show2FAError(container, '');
    var pendInit = container.querySelector('[data-2fa-pending]');
    if (pendInit) pendInit.value = '';
    var codeInit = container.querySelector('input[name="code"]');
    if (codeInit) codeInit.value = '';
    sync2FAEnableSubmit(container);
    var body = new URLSearchParams();
    body.set('csrf', csrf);
    fetch('/account/2fa/begin', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-Requested-With': 'XMLHttpRequest' },
      body: body.toString()
    }).then(function (r) {
      return r.json().then(function (d) { return { ok: r.ok, data: d }; });
    }).then(function (res) {
      if (!res.ok) {
        show2FAError(container, res.data.error || 'Error');
        return;
      }
      var qrBox = container.querySelector('[data-2fa-qr]');
      if (qrBox) {
        qrBox.innerHTML = res.data.qr_svg || '';
      }
      var secEl = container.querySelector('[data-2fa-secret]');
      if (secEl) secEl.textContent = res.data.secret_formatted || res.data.secret || '';
      var copySec = container.querySelector('[data-2fa-copy-secret]');
      if (copySec) copySec.setAttribute('data-copy', res.data.secret || '');
      var pend = container.querySelector('[data-2fa-pending]');
      if (pend) pend.value = res.data.pending_token || '';
      var codeIn = container.querySelector('input[name="code"]');
      if (codeIn) {
        codeIn.value = '';
        codeIn.focus();
      }
      sync2FAEnableSubmit(container);
    }).catch(function () {
      show2FAError(container, document.documentElement.lang === 'en' ? 'Failed to start 2FA setup' : 'Не удалось начать настройку 2FA');
    });
  }

  function wire2FAEnableForm(container) {
    if (!container) return;
    var form = container.querySelector('[data-2fa-enable-form]');
    if (!form) return;
    var codeIn = form.querySelector('input[name="code"]');
    var submit = form.querySelector('button[type="submit"]');
    if (codeIn) {
      codeIn.addEventListener('input', function () { sync2FAEnableSubmit(container); });
      codeIn.addEventListener('change', function () { sync2FAEnableSubmit(container); });
    }
    sync2FAEnableSubmit(container);
    form.addEventListener('submit', function (event) {
      event.preventDefault();
      sync2FAEnableSubmit(container);
      if (submit && submit.disabled) return;
      show2FAError(container, '');
      if (submit) submit.disabled = true;
      var fd = new URLSearchParams(new FormData(form));
      fetch('/account/2fa/enable', {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-Requested-With': 'XMLHttpRequest' },
        body: fd.toString()
      }).then(function (r) {
        return r.json().then(function (d) { return { ok: r.ok, data: d }; });
      }).then(function (res) {
        if (!res.ok) {
          show2FAError(container, res.data.error || 'Error');
          sync2FAEnableSubmit(container);
          return;
        }
        var step1 = container.querySelector('[data-2fa-step1]');
        var step2 = container.querySelector('[data-2fa-step2]');
        if (step1) step1.hidden = true;
        if (step2) step2.hidden = false;
        renderCodes(container, res.data.codes || []);
      }).catch(function () {
        show2FAError(container, document.documentElement.lang === 'en' ? 'Failed to enable 2FA' : 'Не удалось включить 2FA');
        sync2FAEnableSubmit(container);
      });
    });
  }

  wire2FAEnableForm(setup2FADialog);
  wire2FAEnableForm(inline2FA);
  if (inline2FA) {
    begin2FA(inline2FA);
  }

  if (regen2FADialog) {
    var regenForm = regen2FADialog.querySelector('[data-2fa-regen-form]');
    if (regenForm) {
      regenForm.addEventListener('submit', function (event) {
        event.preventDefault();
        show2FAError(regen2FADialog, '');
        var fd = new URLSearchParams(new FormData(regenForm));
        fetch('/account/2fa/recovery-codes', {
          method: 'POST',
          headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-Requested-With': 'XMLHttpRequest' },
          body: fd.toString()
        }).then(function (r) {
          return r.json().then(function (d) { return { ok: r.ok, data: d }; });
        }).then(function (res) {
          if (!res.ok) {
            show2FAError(regen2FADialog, res.data.error || 'Error');
            return;
          }
          var s1 = regen2FADialog.querySelector('[data-2fa-regen-step1]');
          var s2 = regen2FADialog.querySelector('[data-2fa-step2]');
          if (s1) s1.hidden = true;
          if (s2) s2.hidden = false;
          renderCodes(regen2FADialog, res.data.codes || []);
        }).catch(function () {
          show2FAError(regen2FADialog, document.documentElement.lang === 'en' ? 'Failed to regenerate codes' : 'Не удалось перевыпустить коды');
        });
      });
    }
  }

  document.addEventListener('click', function (event) {
    if (event.target.closest('[data-2fa-setup-open]') && setup2FADialog && typeof setup2FADialog.showModal === 'function') {
      event.preventDefault();
      setup2FADialog.showModal();
      begin2FA(setup2FADialog);
      return;
    }
    if (event.target.closest('[data-2fa-regen-open]') && regen2FADialog && typeof regen2FADialog.showModal === 'function') {
      event.preventDefault();
      var s1 = regen2FADialog.querySelector('[data-2fa-regen-step1]');
      var s2 = regen2FADialog.querySelector('[data-2fa-step2]');
      if (s1) s1.hidden = false;
      if (s2) s2.hidden = true;
      show2FAError(regen2FADialog, '');
      var pw = regen2FADialog.querySelector('input[name="current_password"]');
      if (pw) pw.value = '';
      regen2FADialog.showModal();
      if (pw) pw.focus();
      return;
    }
    if (event.target.closest('[data-2fa-disable-open]') && disable2FADialog && typeof disable2FADialog.showModal === 'function') {
      event.preventDefault();
      var dpw = disable2FADialog.querySelector('input[name="current_password"]');
      if (dpw) dpw.value = '';
      disable2FADialog.querySelectorAll('[data-dialog-error]').forEach(function (el) { el.hidden = true; });
      disable2FADialog.showModal();
      if (dpw) dpw.focus();
      return;
    }
    var copyCodesBtn = event.target.closest('[data-2fa-copy-codes]');
    if (copyCodesBtn && activeRecoveryCodes.length > 0) {
      event.preventDefault();
      copyText(copyCodesBtn, activeRecoveryCodes.join('\n'));
      return;
    }
    var dlCodesBtn = event.target.closest('[data-2fa-download-codes]');
    if (dlCodesBtn && activeRecoveryCodes.length > 0) {
      event.preventDefault();
      var host = dlCodesBtn.closest('[data-username]');
      var uname = (host && host.getAttribute('data-username')) || 'user';
      var content = 'oh my boot_ 2FA recovery codes (' + uname + ')\n\n' + activeRecoveryCodes.join('\n') + '\n';
      var blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
      var url = URL.createObjectURL(blob);
      var a = document.createElement('a');
      a.href = url;
      a.download = 'omb-2fa-recovery-codes-' + uname + '.txt';
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
      return;
    }
    if (event.target.closest('[data-2fa-done]')) {
      event.preventDefault();
      window.location.reload();
      return;
    }
  });

  [setup2FADialog, regen2FADialog, disable2FADialog].forEach(function (d) {
    if (!d) return;
    d.addEventListener('click', function (event) {
      if (event.target === d && typeof d.close === 'function') {
        d.close();
      }
    });
  });

  if (disable2FADialog && disable2FADialog.getAttribute('data-auto-open') === 'true' && typeof disable2FADialog.showModal === 'function') {
    disable2FADialog.showModal();
  }
})();
