(function () {
  'use strict';

  var searchInput = document.getElementById('user-search');
  var countEl = document.getElementById('user-count');
  var pwDialog = document.getElementById('user-password-dialog');
  var createDialog = document.getElementById('user-create-dialog');
  var deleteDialog = document.getElementById('user-delete-dialog');
  var policyDialog = document.getElementById('password-policy-dialog');
  var expiryDialog = document.getElementById('user-expiry-dialog');
  var userFsDialog = document.getElementById('user-fs-access-dialog');
  var userFsForm = userFsDialog ? userFsDialog.querySelector('form') : null;
  var userFsSave = userFsDialog ? userFsDialog.querySelector('[data-user-fs-save]') : null;
  var userFsIdInput = document.getElementById('user-fs-modal-id');
  var userFsNameSpan = document.getElementById('user-fs-modal-name');
  var userFsPwInput = document.getElementById('user-fs-pw-input');
  var userFsPwBox = document.getElementById('user-fs-pw-box');
  var userFsPwLabel = document.getElementById('user-fs-pw-label');

  var userFsInitial = {
    id: '',
    name: '',
    mode: 'none',
    hasPw: false
  };

  function updateUserFsSave() {
    if (!userFsDialog || !userFsSave) return;
    var selectedRadio = userFsDialog.querySelector('input[name="mode"]:checked');
    var curMode = selectedRadio ? selectedRadio.value : 'none';
    var curPw = userFsPwInput ? userFsPwInput.value : '';

    userFsDialog.querySelectorAll('.radio-card[data-fs-card]').forEach(function (card) {
      var radio = card.querySelector('input[type="radio"]');
      card.classList.toggle('selected', !!(radio && radio.checked));
      card.classList.toggle('is-selected', !!(radio && radio.checked));
    });

    if (userFsPwBox && userFsPwBox.style) {
      userFsPwBox.style.opacity = (curMode === 'none') ? '0.4' : '1';
      userFsPwBox.style.pointerEvents = (curMode === 'none') ? 'none' : 'auto';
    }

    var modeChanged = curMode !== userFsInitial.mode;
    var isDirty = false;
    var isValid = false;

    if (curMode === 'none') {
      isDirty = modeChanged;
      isValid = true;
    } else {
      if (!userFsInitial.hasPw) {
        isValid = curPw.length >= 12;
        isDirty = isValid;
      } else {
        var pwValid = (curPw.length === 0) || (curPw.length >= 12);
        isValid = pwValid;
        isDirty = modeChanged || (curPw.length >= 12);
      }
    }

    userFsSave.disabled = !isDirty || !isValid;
  }

  if (userFsForm) {
    userFsForm.addEventListener('input', updateUserFsSave);
    userFsForm.addEventListener('change', updateUserFsSave);
    userFsForm.addEventListener('submit', function (event) {
      updateUserFsSave();
      if (userFsSave && userFsSave.disabled) {
        event.preventDefault();
      }
    });
  }
  if (userFsPwInput) {
    userFsPwInput.addEventListener('omb:password-sync', updateUserFsSave);
  }

  var createForm = createDialog ? createDialog.querySelector('form') : null;
  var createSubmitBtn = createDialog ? createDialog.querySelector('button.btn-primary') : null;
  var createUserInput = createDialog ? createDialog.querySelector('input[name="username"]') : null;
  var createPwInput = createDialog ? createDialog.querySelector('input[name="password"]') : null;
  var createModeSelect = createDialog ? createDialog.querySelector('[data-create-policy-mode]') : null;
  var createCustomWrap = createDialog ? createDialog.querySelector('[data-create-custom-wrap]') : null;
  var createCustomInput = createCustomWrap ? createCustomWrap.querySelector('input[name="password_max_age_days"]') : null;
  var createMustChange = createDialog ? createDialog.querySelector('input[name="must_change_password"]') : null;

  var USERNAME_PATTERN = /^[A-Za-z0-9_.-]{3,32}$/;

  function updateCreateSubmit() {
    if (!createSubmitBtn || !createUserInput || !createPwInput) return;
    var hasUser = USERNAME_PATTERN.test(createUserInput.value.trim());
    var hasPw = createPwInput.value.length >= 12;
    createSubmitBtn.disabled = !(hasUser && hasPw);
  }

  var pwForm = pwDialog ? pwDialog.querySelector('form') : null;
  var pwSubmitBtn = pwDialog ? pwDialog.querySelector('button.btn-primary') : null;
  var pwInput = pwDialog ? pwDialog.querySelector('input[name="password"]') : null;

  function updatePwSubmit() {
    if (!pwSubmitBtn || !pwInput) return;
    var hasPw = pwInput.value.length >= 12;
    pwSubmitBtn.disabled = !hasPw;
  }

  if (createForm) {
    createForm.addEventListener('input', updateCreateSubmit);
    createForm.addEventListener('change', updateCreateSubmit);
  }
  if (createPwInput) {
    createPwInput.addEventListener('omb:password-sync', updateCreateSubmit);
  }

  if (pwForm) {
    pwForm.addEventListener('input', updatePwSubmit);
    pwForm.addEventListener('change', updatePwSubmit);
  }
  if (pwInput) {
    pwInput.addEventListener('omb:password-sync', updatePwSubmit);
  }

  updateCreateSubmit();
  updatePwSubmit();

  function syncCreatePolicyMode() {
    if (!createModeSelect || !createCustomWrap) return;
    var isCustom = createModeSelect.value === 'custom';
    createCustomWrap.hidden = !isCustom;
    if (createCustomInput) createCustomInput.required = isCustom;
  }

  function parseStrictInt(str) {
    var s = String(str == null ? '' : str).trim();
    if (!/^\d+$/.test(s)) return NaN;
    return parseInt(s, 10);
  }

  var policyForm = policyDialog ? policyDialog.querySelector('form') : null;
  var policySubmitBtn = policyDialog ? policyDialog.querySelector('button.btn-primary') : null;
  var policyMaxInput = policyDialog ? policyDialog.querySelector('input[name="max_age_days"]') : null;
  var policyWarnInput = policyDialog ? policyDialog.querySelector('input[name="warn_days"]') : null;
  var policyWarnWrap = policyDialog ? policyDialog.querySelector('[data-policy-warn-wrap]') : null;
  var policyInitial = {
    max: policyForm && policyForm.getAttribute('data-initial-max') !== null
      ? parseStrictInt(policyForm.getAttribute('data-initial-max'))
      : (policyMaxInput ? parseStrictInt(policyMaxInput.value) : 0),
    warn: policyForm && policyForm.getAttribute('data-initial-warn') !== null
      ? parseStrictInt(policyForm.getAttribute('data-initial-warn'))
      : (policyWarnInput ? parseStrictInt(policyWarnInput.value) : 7)
  };

  function syncPolicyDialog() {
    if (!policyDialog) return;
    var maxVal = policyMaxInput ? parseStrictInt(policyMaxInput.value) : NaN;
    var warnVal = policyWarnInput ? parseStrictInt(policyWarnInput.value) : NaN;
    var validMax = !isNaN(maxVal) && maxVal >= 0 && maxVal <= 3650;
    var disabled = validMax && maxVal === 0;
    policyDialog.querySelectorAll('[data-policy-preset]').forEach(function (btn) {
      btn.classList.toggle('is-on', validMax && Number(btn.getAttribute('data-policy-preset')) === maxVal);
    });
    if (policyWarnInput) {
      policyWarnInput.readOnly = disabled;
      policyWarnInput.disabled = false;
    }
    if (policyWarnWrap && policyWarnWrap.style) policyWarnWrap.style.opacity = disabled ? '0.45' : '1';
    if (policySubmitBtn) {
      var validWarn = disabled || (!isNaN(warnVal) && warnVal >= 0 && warnVal <= 90);
      var dirty = validMax && validWarn && (maxVal !== policyInitial.max || (!disabled && warnVal !== policyInitial.warn));
      policySubmitBtn.disabled = !dirty;
    }
  }

  if (policyForm) {
    policyForm.addEventListener('input', syncPolicyDialog);
    policyForm.addEventListener('change', syncPolicyDialog);
  } else if (policyMaxInput) {
    policyMaxInput.addEventListener('input', syncPolicyDialog);
  }
  if (policyDialog) {
    syncPolicyDialog();
  }

  var expiryForm = expiryDialog ? expiryDialog.querySelector('form') : null;
  var expirySubmitBtn = expiryDialog ? expiryDialog.querySelector('button.btn-primary') : null;
  var expiryInitial = { mode: 'policy', days: 30, mustChange: false, self: false };

  function currentExpiryValues() {
    var mode = 'policy';
    if (expiryDialog) {
      expiryDialog.querySelectorAll('input[name="password_policy_mode"]').forEach(function (radio) {
        if (radio.checked) mode = radio.value;
      });
    }
    var daysInput = expiryDialog ? expiryDialog.querySelector('input[name="password_max_age_days"]') : null;
    var mustCheck = expiryDialog ? expiryDialog.querySelector('input[name="must_change_password"]') : null;
    return {
      mode: mode,
      days: daysInput ? parseStrictInt(daysInput.value) : NaN,
      mustChange: !!(mustCheck && mustCheck.checked)
    };
  }

  function syncExpiryDialog() {
    if (!expiryDialog) return;
    var vals = currentExpiryValues();
    expiryDialog.querySelectorAll('input[name="password_policy_mode"]').forEach(function (radio) {
      var card = radio.closest ? radio.closest('.radio-card') : null;
      if (card) card.classList.toggle('is-selected', radio.checked);
    });
    var customWrap = expiryDialog.querySelector('[data-expiry-custom-wrap]');
    var daysInput = expiryDialog.querySelector('input[name="password_max_age_days"]');
    var isCustom = vals.mode === 'custom';
    if (customWrap) customWrap.hidden = !isCustom;
    if (daysInput) daysInput.required = isCustom;
    if (expirySubmitBtn) {
      var validDays = !isCustom || (!isNaN(vals.days) && vals.days >= 1 && vals.days <= 3650);
      var dirty = vals.mode !== expiryInitial.mode ||
        (isCustom && vals.days !== expiryInitial.days) ||
        (!expiryInitial.self && vals.mustChange !== expiryInitial.mustChange);
      expirySubmitBtn.disabled = !(validDays && dirty);
    }
  }

  if (expiryForm) {
    expiryForm.addEventListener('input', syncExpiryDialog);
    expiryForm.addEventListener('change', syncExpiryDialog);
  }

  function filterUsers() {
    var query = (searchInput ? searchInput.value : '').toLowerCase().trim();
    var visible = 0;
    var active = 0;

    var rows = document.querySelectorAll('tr[data-user-row]');
    rows.forEach(function (row) {
      var login = (row.getAttribute('data-user-login') || '').toLowerCase();
      var id = (row.getAttribute('data-user-id') || '').toLowerCase();
      var role = (row.getAttribute('data-user-role') || '').toLowerCase();
      var text = (row.textContent || '').toLowerCase();
      var combined = login + ' ' + id + ' #' + id + ' ' + role + ' ' + text;

      var match = !query || query.split(/\s+/).every(function (token) {
        return combined.indexOf(token) !== -1;
      });

      if (match) {
        row.hidden = false;
        visible++;
        var isEnabled = row.getAttribute('data-user-enabled') === 'true' ||
                        row.getAttribute('data-user-enabled') === '1' ||
                        (row.querySelector('input[name="enabled"]') && row.querySelector('input[name="enabled"]').checked);
        if (isEnabled) {
          active++;
        }
      } else {
        row.hidden = true;
      }
    });

    if (countEl) {
      var en = document.documentElement.lang === 'en';
      var totalLabel = countEl.getAttribute('data-label-total') || (en ? 'Total' : 'Всего');
      var activeLabel = countEl.getAttribute('data-label-active') || (en ? 'active' : 'активных');
      countEl.textContent = totalLabel + ': ' + visible + ' · ' + activeLabel + ': ' + active;
    }

    var emptyRow = document.getElementById('users-empty-row');
    if (emptyRow) {
      emptyRow.hidden = visible > 0;
    }
  }

  if (searchInput) {
    searchInput.addEventListener('input', filterUsers);
  }

  // Initial count update on load
  filterUsers();

  // Track initial values of each row to highlight and enable Save button when modified
  var initialStates = {};
  function recordInitialStates() {
    document.querySelectorAll('tr[data-user-row]').forEach(function (row) {
      var id = row.getAttribute('data-user-id');
      if (!id) return;
      var roleSelect = document.querySelector('select[name="role"][form="user-change-' + id + '"]');
      var enabledInput = document.querySelector('input[name="enabled"][form="user-change-' + id + '"]');
      var saveBtn = document.querySelector('button[data-user-save="' + id + '"]') || document.querySelector('button[form="user-change-' + id + '"]');
      if (roleSelect && enabledInput && !roleSelect.disabled && !enabledInput.disabled) {
        initialStates[id] = {
          role: roleSelect.value,
          enabled: enabledInput.checked
        };
        if (saveBtn) {
          saveBtn.disabled = true;
          saveBtn.classList.remove('btn-primary');
        }
      }
    });
  }
  recordInitialStates();

  function updateRowDirty(id) {
    var init = initialStates[id];
    if (!init) return;
    var roleSelect = document.querySelector('select[name="role"][form="user-change-' + id + '"]');
    var enabledInput = document.querySelector('input[name="enabled"][form="user-change-' + id + '"]');
    var saveBtn = document.querySelector('button[data-user-save="' + id + '"]') || document.querySelector('button[form="user-change-' + id + '"]');
    if (!roleSelect || !enabledInput || !saveBtn) return;

    var isDirty = (roleSelect.value !== init.role) || (enabledInput.checked !== init.enabled);
    saveBtn.disabled = !isDirty;
    if (isDirty) {
      saveBtn.classList.add('btn-primary');
    } else {
      saveBtn.classList.remove('btn-primary');
    }
  }

  document.addEventListener('submit', function (event) {
    var form = event.target;
    if (!form) return;
    if (form === createForm) {
      updateCreateSubmit();
      if (createSubmitBtn && createSubmitBtn.disabled) {
        event.preventDefault();
      }
      return;
    }
    if (form === pwForm) {
      updatePwSubmit();
      if (pwSubmitBtn && pwSubmitBtn.disabled) {
        event.preventDefault();
      }
      return;
    }
    if (form === policyForm) {
      syncPolicyDialog();
      if (policySubmitBtn && policySubmitBtn.disabled) {
        event.preventDefault();
      }
      return;
    }
    if (form === expiryForm) {
      syncExpiryDialog();
      if (expirySubmitBtn && expirySubmitBtn.disabled) {
        event.preventDefault();
      }
      return;
    }
    if (!form.id) return;
    var match = form.id.match(/^user-change-(\d+)$/);
    if (match) {
      var id = match[1];
      updateRowDirty(id);
      var saveBtn = document.querySelector('button[data-user-save="' + id + '"]') || document.querySelector('button[form="user-change-' + id + '"]');
      if (saveBtn && saveBtn.disabled) {
        event.preventDefault();
      }
    }
  });

  document.addEventListener('change', function (event) {
    var target = event.target;
    if (!target) return;
    if (createModeSelect && target === createModeSelect) {
      syncCreatePolicyMode();
      return;
    }
    if (target.name === 'password_policy_mode' && expiryDialog) {
      syncExpiryDialog();
      return;
    }
    if (target.matches('select[name="role"]') || target.matches('input[name="enabled"]')) {
      var formAttr = target.getAttribute('form') || '';
      var match = formAttr.match(/^user-change-(\d+)$/);
      if (match) {
        var id = match[1];
        var row = document.querySelector('tr[data-user-id="' + id + '"]');
        if (target.matches('input[name="enabled"]')) {
          var labelSpan = target.closest('label') ? target.closest('label').querySelector('[data-enabled-label], span') : null;
          if (labelSpan) {
            var activeTxt = target.getAttribute('data-label-active') || 'Активен';
            var disabledTxt = target.getAttribute('data-label-disabled') || 'Отключён';
            labelSpan.textContent = target.checked ? activeTxt : disabledTxt;
          }
          if (row) {
            row.setAttribute('data-user-enabled', target.checked ? 'true' : 'false');
          }
        }
        if (target.matches('select[name="role"]') && row) {
          row.setAttribute('data-user-role', target.value);
        }
        updateRowDirty(id);
        filterUsers();
      }
    }
  });

  document.addEventListener('click', function (event) {
    // Password modal trigger
    var pwBtn = event.target.closest('[data-user-pw-id]');
    if (pwBtn && pwDialog && typeof pwDialog.showModal === 'function') {
      event.preventDefault();
      var userId = pwBtn.getAttribute('data-user-pw-id') || '';
      var userName = pwBtn.getAttribute('data-user-pw-name') || '';
      var isSelf = pwBtn.getAttribute('data-user-self') === 'true';

      var idInput = pwDialog.querySelector('input[name="id"]');
      if (idInput) {
        idInput.value = userId;
      }

      var userSpan = pwDialog.querySelector('#pw-modal-user');
      var idSpan = pwDialog.querySelector('#pw-modal-id');
      if (userSpan) {
        if (idSpan && idSpan.tagName !== 'INPUT') {
          userSpan.textContent = userName;
          idSpan.textContent = userId;
        } else {
          userSpan.textContent = userName ? (userName + ' (#' + userId + ')') : ('#' + userId);
        }
      }

      var pwMustWrap = pwDialog.querySelector('[data-pw-must-change-wrap]');
      var pwMustCheck = pwDialog.querySelector('input[name="must_change_password"]');
      if (pwMustWrap) pwMustWrap.hidden = isSelf;
      if (pwMustCheck) pwMustCheck.checked = !isSelf;

      var pwInput = pwDialog.querySelector('input[name="password"]');
      if (pwInput) {
        pwInput.value = '';
        pwInput.type = 'password';
        pwInput.dispatchEvent(new Event('omb:password-sync'));
      }
      updatePwSubmit();

      // Reset any generated copy button or notes from password.js
      pwDialog.querySelectorAll('small[role="status"]').forEach(function (el) { el.textContent = ''; });
      pwDialog.querySelectorAll('button').forEach(function (btn) {
        if (btn.textContent === 'Скопировать' || btn.textContent === 'Copy') {
          btn.hidden = true;
        }
      });

      pwDialog.showModal();
      return;
    }

    // Global password policy modal trigger
    var polOpenBtn = event.target.closest('[data-password-policy-open]');
    if (polOpenBtn && policyDialog && typeof policyDialog.showModal === 'function') {
      event.preventDefault();
      if (policyMaxInput) policyMaxInput.value = String(isNaN(policyInitial.max) ? 0 : policyInitial.max);
      if (policyWarnInput) policyWarnInput.value = String(isNaN(policyInitial.warn) ? 7 : policyInitial.warn);
      syncPolicyDialog();
      policyDialog.showModal();
      return;
    }

    // Preset buttons inside policy dialog
    var presetBtn = event.target.closest('[data-policy-preset]');
    if (presetBtn && policyDialog) {
      event.preventDefault();
      if (policyMaxInput) policyMaxInput.value = presetBtn.getAttribute('data-policy-preset') || '0';
      syncPolicyDialog();
      return;
    }

    // Per-user password expiry modal trigger
    var expBtn = event.target.closest('[data-user-expiry-id]');
    if (expBtn && expiryDialog && typeof expiryDialog.showModal === 'function') {
      event.preventDefault();
      var expId = expBtn.getAttribute('data-user-expiry-id') || '';
      var expName = expBtn.getAttribute('data-user-expiry-name') || '';
      var expMode = expBtn.getAttribute('data-user-expiry-mode') || 'policy';
      var expDays = expBtn.getAttribute('data-user-expiry-days') || '30';
      var expMust = expBtn.getAttribute('data-user-expiry-must-change') === 'true';
      var expChanged = expBtn.getAttribute('data-user-expiry-changed') || '—';
      var expSelf = expBtn.getAttribute('data-user-self') === 'true';

      var expIdInput = expiryDialog.querySelector('input[name="id"]');
      if (expIdInput) expIdInput.value = expId;
      var expUserSpan = expiryDialog.querySelector('#expiry-modal-user');
      if (expUserSpan) expUserSpan.textContent = expName ? (expName + ' (#' + expId + ')') : ('#' + expId);
      var expChangedSpan = expiryDialog.querySelector('#expiry-modal-changed');
      if (expChangedSpan) expChangedSpan.textContent = expChanged;

      expiryDialog.querySelectorAll('input[name="password_policy_mode"]').forEach(function (radio) {
        radio.checked = radio.value === expMode;
      });
      var expDaysInput = expiryDialog.querySelector('input[name="password_max_age_days"]');
      if (expDaysInput) expDaysInput.value = expDays;

      var expMustWrap = expiryDialog.querySelector('[data-expiry-must-change-wrap]');
      var expMustCheck = expiryDialog.querySelector('input[name="must_change_password"]');
      if (expMustWrap) expMustWrap.hidden = expSelf;
      if (expMustCheck) expMustCheck.checked = !expSelf && expMust;

      expiryInitial = {
        mode: expMode,
        days: parseStrictInt(expDays),
        mustChange: !expSelf && expMust,
        self: expSelf
      };

      syncExpiryDialog();
      expiryDialog.showModal();
      return;
    }

    // Create user modal trigger
    var addBtn = event.target.closest('[data-user-add]');
    if (addBtn && createDialog && typeof createDialog.showModal === 'function') {
      event.preventDefault();
      var newUserInput = createDialog.querySelector('input[name="username"]');
      if (newUserInput) {
        newUserInput.value = '';
      }
      var newPwInput = createDialog.querySelector('input[name="password"]');
      if (newPwInput) {
        newPwInput.value = '';
        newPwInput.type = 'password';
        newPwInput.dispatchEvent(new Event('omb:password-sync'));
      }
      if (createModeSelect) createModeSelect.value = 'policy';
      if (createCustomInput) createCustomInput.value = '30';
      if (createMustChange) createMustChange.checked = true;
      syncCreatePolicyMode();
      updateCreateSubmit();
      createDialog.querySelectorAll('small[role="status"]').forEach(function (el) { el.textContent = ''; });
      createDialog.querySelectorAll('button').forEach(function (btn) {
        if (btn.textContent === 'Скопировать' || btn.textContent === 'Copy') {
          btn.hidden = true;
        }
      });
      createDialog.showModal();
      return;
    }

    // User File Server Access modal trigger
    var userFsBtn = event.target.closest('[data-user-fs-id]');
    if (userFsBtn && userFsDialog && typeof userFsDialog.showModal === 'function') {
      event.preventDefault();
      var userId = userFsBtn.getAttribute('data-user-fs-id') || '';
      var userName = userFsBtn.getAttribute('data-user-fs-name') || '';
      var userMode = userFsBtn.getAttribute('data-user-fs-mode') || 'none';
      var userHasPw = userFsBtn.getAttribute('data-user-fs-has-pw') === 'true';

      userFsInitial = {
        id: userId,
        name: userName,
        mode: userMode,
        hasPw: userHasPw
      };

      if (userFsIdInput) userFsIdInput.value = userId;
      if (userFsNameSpan) userFsNameSpan.textContent = userName;

      userFsDialog.querySelectorAll('input[name="mode"]').forEach(function (radio) {
        radio.checked = radio.value === userMode;
      });

      if (userFsPwInput) {
        userFsPwInput.value = '';
        userFsPwInput.type = 'password';
        userFsPwInput.dispatchEvent(new Event('omb:password-sync'));
      }

      // Reset any generated copy button or notes from password.js
      userFsDialog.querySelectorAll('small[role="status"]').forEach(function (el) { el.textContent = ''; });
      userFsDialog.querySelectorAll('.pw-tools button').forEach(function (btn) {
        if (btn.textContent === 'Скопировать' || btn.textContent === 'Copy') {
          btn.hidden = true;
        }
      });

      if (userFsPwLabel) {
        var isEn = document.documentElement && document.documentElement.lang === 'en';
        if (!userHasPw) {
          userFsPwLabel.textContent = isEn
            ? 'Dedicated SMB / SFTP password (at least 12 characters; required for first access)'
            : 'Отдельный пароль SMB / SFTP (не менее 12 символов; обязателен при первом включении)';
        } else {
          userFsPwLabel.textContent = isEn
            ? 'Dedicated SMB / SFTP password (at least 12 characters; leave blank to keep current)'
            : 'Отдельный пароль SMB / SFTP (не менее 12 символов; оставьте пустым, чтобы не менять текущий)';
        }
      }

      updateUserFsSave();
      userFsDialog.showModal();
      return;
    }

    // Delete user modal trigger
    var delBtn = event.target.closest('[data-user-delete-id]');
    if (delBtn && deleteDialog && typeof deleteDialog.showModal === 'function') {
      event.preventDefault();
      var delId = delBtn.getAttribute('data-user-delete-id') || '';
      var delName = delBtn.getAttribute('data-user-delete-name') || '';
      var delIdInput = deleteDialog.querySelector('input[name="id"]');
      if (delIdInput) {
        delIdInput.value = delId;
      }
      var delUserSpan = deleteDialog.querySelector('#delete-modal-user');
      if (delUserSpan) {
        delUserSpan.textContent = delName ? (delName + ' (#' + delId + ')') : ('#' + delId);
      }
      deleteDialog.showModal();
      return;
    }

    // Close buttons [data-close-dialog]
    var closeBtn = event.target.closest('[data-close-dialog]');
    if (closeBtn) {
      event.preventDefault();
      var dialog = closeBtn.closest('dialog');
      if (dialog && typeof dialog.close === 'function') {
        dialog.close();
      }
      return;
    }

    // Backdrop click: clicking directly on the dialog background
    if (event.target && event.target.nodeName === 'DIALOG' && typeof event.target.close === 'function') {
      event.target.close();
    }
  });

  // Direct click listener for dialog backdrop close
  [pwDialog, createDialog, deleteDialog, policyDialog, expiryDialog, userFsDialog].forEach(function (dialog) {
    if (!dialog) return;
    dialog.addEventListener('click', function (event) {
      if (event.target === dialog && typeof dialog.close === 'function') {
        dialog.close();
      }
    });
  });
})();
