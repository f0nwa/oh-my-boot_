(function () {
  'use strict';

  var searchInput = document.getElementById('user-search');
  var countEl = document.getElementById('user-count');
  var pwDialog = document.getElementById('user-password-dialog');
  var createDialog = document.getElementById('user-create-dialog');
  var deleteDialog = document.getElementById('user-delete-dialog');

  var createForm = createDialog ? createDialog.querySelector('form') : null;
  var createSubmitBtn = createDialog ? createDialog.querySelector('button.btn-primary') : null;
  var createUserInput = createDialog ? createDialog.querySelector('input[name="username"]') : null;
  var createPwInput = createDialog ? createDialog.querySelector('input[name="password"]') : null;

  function updateCreateSubmit() {
    if (!createSubmitBtn || !createUserInput || !createPwInput) return;
    var hasUser = createUserInput.value.trim().length > 0;
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
  [pwDialog, createDialog, deleteDialog].forEach(function (dialog) {
    if (!dialog) return;
    dialog.addEventListener('click', function (event) {
      if (event.target === dialog && typeof dialog.close === 'function') {
        dialog.close();
      }
    });
  });
})();
