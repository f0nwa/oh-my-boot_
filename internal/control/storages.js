(function () {
  'use strict';

  var smbDialog = document.getElementById('smb-create-dialog');
  var nfsDialog = document.getElementById('nfs-create-dialog');
  var retryDialog = document.getElementById('smb-retry-dialog');
  var nfsRetryDialog = document.getElementById('nfs-retry-dialog');
  var renameDialog = document.getElementById('storage-rename-dialog');
  var renameForm = renameDialog && renameDialog.querySelector('form');
  var renameNameInput = document.getElementById('storage-rename-name');
  var renameSave = renameDialog && renameDialog.querySelector('[data-storage-rename-save]');
  var deleteDialog = document.getElementById('storage-delete-dialog') || document.getElementById('smb-delete-dialog');
  var recoveryDialog = document.getElementById('storage-recovery-dialog');
  var recoveryForm = document.getElementById('storage-recovery-settings');
  var recoverySave = recoveryDialog && recoveryDialog.querySelector('[data-storage-recovery-save]');
  var fsDialog = document.getElementById('file-server-dialog');
  var fsForm = document.getElementById('file-server-form');
  var fsSave = fsDialog && fsDialog.querySelector('[data-file-server-save]');
  var smbCheck = document.getElementById('fs-smb-enabled');
  var sftpCheck = document.getElementById('fs-sftp-enabled');
  var sftpPortInput = document.getElementById('fs-sftp-port');
  var smbCard = document.getElementById('proto-card-smb');
  var sftpCard = document.getElementById('proto-card-sftp');
  var smbChip = document.getElementById('fs-smb-chip');
  var sftpChip = document.getElementById('fs-sftp-chip');
  var sftpAddr = document.getElementById('fs-sftp-addr');

  var smbForm = smbDialog && smbDialog.querySelector('form');
  var smbPathInput = smbDialog && smbDialog.querySelector('input[name="path"]');
  var smbUserInput = smbDialog && smbDialog.querySelector('input[name="username"]');
  var smbPwInput = smbDialog && smbDialog.querySelector('input[name="password"]');
  var smbSubmitBtn = smbDialog && (smbDialog.querySelector('[data-smb-submit]') || smbDialog.querySelector('button.btn-primary'));

  var nfsForm = nfsDialog && nfsDialog.querySelector('form');
  var nfsServerInput = nfsDialog && nfsDialog.querySelector('input[name="server"]');
  var nfsExportPathInput = nfsDialog && nfsDialog.querySelector('input[name="export_path"]');
  var nfsSubmitBtn = nfsDialog && (nfsDialog.querySelector('[data-nfs-submit]') || nfsDialog.querySelector('button.btn-primary'));

  var retryForm = retryDialog && retryDialog.querySelector('form');
  var retryUserInput = retryDialog && retryDialog.querySelector('input[name="username"]');
  var retryPwInput = retryDialog && retryDialog.querySelector('input[name="password"]');
  var retrySubmitBtn = retryDialog && (retryDialog.querySelector('[data-smb-retry-submit]') || retryDialog.querySelector('button.btn-primary'));

  function updateNfsSubmit() {
    if (!nfsSubmitBtn || !nfsServerInput || !nfsExportPathInput) return;
    var serverVal = (nfsServerInput.value || '').trim();
    var exportPathVal = (nfsExportPathInput.value || '').trim();
    var isValid = serverVal.length > 0 && exportPathVal.length > 0;
    if (nfsServerInput.validity && !nfsServerInput.validity.valid) isValid = false;
    if (nfsExportPathInput.validity && !nfsExportPathInput.validity.valid) isValid = false;
    nfsSubmitBtn.disabled = !isValid;
  }

  function updateSmbSubmit() {
    if (!smbSubmitBtn || !smbPathInput || !smbUserInput || !smbPwInput) return;
    var pathVal = (smbPathInput.value || '').trim();
    var userVal = (smbUserInput.value || '').trim();
    var pwVal = smbPwInput.value || '';
    var isValid = pathVal.length > 0 && userVal.length > 0 && pwVal.length > 0;
    if (smbPathInput.validity && !smbPathInput.validity.valid) isValid = false;
    if (smbUserInput.validity && !smbUserInput.validity.valid) isValid = false;
    if (smbPwInput.validity && !smbPwInput.validity.valid) isValid = false;
    smbSubmitBtn.disabled = !isValid;
  }

  function updateSmbRetrySubmit() {
    if (!retrySubmitBtn || !retryUserInput || !retryPwInput) return;
    var userVal = (retryUserInput.value || '').trim();
    var pwVal = retryPwInput.value || '';
    var isValid = userVal.length > 0 && pwVal.length > 0;
    if (retryUserInput.validity && !retryUserInput.validity.valid) isValid = false;
    if (retryPwInput.validity && !retryPwInput.validity.valid) isValid = false;
    retrySubmitBtn.disabled = !isValid;
  }

  function updateFileServerSave() {
    if (!fsForm || !fsSave) return;
    var smbInit = smbCheck ? smbCheck.defaultChecked : false;
    var sftpInit = sftpCheck ? sftpCheck.defaultChecked : false;
    var portInit = sftpPortInput ? Number(sftpPortInput.defaultValue) : 2022;

    var smbCur = smbCheck ? smbCheck.checked : false;
    var sftpCur = sftpCheck ? sftpCheck.checked : false;
    var portCur = sftpPortInput ? sftpPortInput.valueAsNumber : NaN;

    var isDirty = (smbCur !== smbInit) || (sftpCur !== sftpInit) || (portCur !== portInit);
    var isValid = !isNaN(portCur) && portCur >= 1024 && portCur <= 65535;

    fsSave.disabled = !isDirty || !isValid;

    if (smbCard) smbCard.classList.toggle('is-on', smbCur);
    if (sftpCard) sftpCard.classList.toggle('is-on', sftpCur);
    var isEn = document.documentElement && document.documentElement.lang === 'en';
    if (smbChip) {
      smbChip.className = 'proto-status ' + (smbCur ? 'is-on' : 'is-off');
      smbChip.innerHTML = '<span class="dot ' + (smbCur ? 'dot-ok' : 'dot-off') + '"></span>' + (smbCur ? (isEn ? 'Active' : 'Активен') : (isEn ? 'Disabled' : 'Выключен'));
    }
    if (sftpChip) {
      sftpChip.className = 'proto-status ' + (sftpCur ? 'is-on' : 'is-off');
      sftpChip.innerHTML = '<span class="dot ' + (sftpCur ? 'dot-ok' : 'dot-off') + '"></span>' + (sftpCur ? (isEn ? 'Active' : 'Активен') : (isEn ? 'Disabled' : 'Выключен'));
    }
    if (sftpAddr && isValid && sftpAddr.dataset && sftpAddr.dataset.baseHost) {
      sftpAddr.textContent = 'sftp://' + sftpAddr.dataset.baseHost + ':' + portCur + '/';
    }
  }

  if (fsForm) {
    fsForm.addEventListener('input', updateFileServerSave);
    fsForm.addEventListener('change', updateFileServerSave);
    fsForm.addEventListener('submit', function (event) {
      updateFileServerSave();
      if (fsSave && fsSave.disabled) {
        event.preventDefault();
      }
    });
    updateFileServerSave();
  }

  function updateRenameSave() {
    if (!renameNameInput || !renameSave) {
      return;
    }
    var current = renameNameInput.value.trim();
    var initial = (renameNameInput.defaultValue || '').trim();
    renameSave.disabled = !current || current === initial || !renameNameInput.validity.valid;
  }

  if (renameForm) {
    renameForm.addEventListener('input', updateRenameSave);
    renameForm.addEventListener('change', updateRenameSave);
    renameForm.addEventListener('submit', function (event) {
      updateRenameSave();
      if (renameSave.disabled) {
        event.preventDefault();
      }
    });
  }

  function updateRecoverySave() {
    if (!recoveryForm || !recoverySave) {
      return;
    }
    var enabled = recoveryForm.elements.enabled;
    var interval = recoveryForm.elements.interval_minutes;
    var rescanEnabled = recoveryForm.elements.rescan_enabled;
    var rescanInterval = recoveryForm.elements.rescan_interval_minutes;

    var recDot = document.getElementById('storage-recovery-dot');
    if (recDot && enabled) {
      recDot.className = 'dot ' + (enabled.checked ? 'dot-ok' : 'dot-off');
    }
    var scanDot = document.getElementById('storage-rescan-dot');
    if (scanDot && rescanEnabled) {
      scanDot.className = 'dot ' + (rescanEnabled.checked ? 'dot-ok' : 'dot-off');
    }

    var changed = (enabled && enabled.checked !== enabled.defaultChecked) ||
      (interval && interval.valueAsNumber !== Number(interval.defaultValue)) ||
      (rescanEnabled && rescanEnabled.checked !== rescanEnabled.defaultChecked) ||
      (rescanInterval && rescanInterval.valueAsNumber !== Number(rescanInterval.defaultValue));

    var valid = (!interval || interval.validity.valid) &&
      (!rescanInterval || rescanInterval.validity.valid);

    recoverySave.disabled = !changed || !valid;
  }

  if (recoveryForm) {
    recoveryForm.addEventListener('input', updateRecoverySave);
    recoveryForm.addEventListener('change', updateRecoverySave);
    recoveryForm.addEventListener('submit', function (event) {
      updateRecoverySave();
      if (recoverySave.disabled) {
        event.preventDefault();
      }
    });
    updateRecoverySave();
  }

  if (nfsForm) {
    nfsForm.addEventListener('input', updateNfsSubmit);
    nfsForm.addEventListener('change', updateNfsSubmit);
    nfsForm.addEventListener('submit', function (event) {
      updateNfsSubmit();
      if (nfsSubmitBtn && nfsSubmitBtn.disabled) {
        event.preventDefault();
      }
    });
    updateNfsSubmit();
  }

  if (smbForm) {
    smbForm.addEventListener('input', updateSmbSubmit);
    smbForm.addEventListener('change', updateSmbSubmit);
    smbForm.addEventListener('submit', function (event) {
      updateSmbSubmit();
      if (smbSubmitBtn && smbSubmitBtn.disabled) {
        event.preventDefault();
      }
    });
    updateSmbSubmit();
  }

  if (retryForm) {
    retryForm.addEventListener('input', updateSmbRetrySubmit);
    retryForm.addEventListener('change', updateSmbRetrySubmit);
    retryForm.addEventListener('submit', function (event) {
      updateSmbRetrySubmit();
      if (retrySubmitBtn && retrySubmitBtn.disabled) {
        event.preventDefault();
      }
    });
    updateSmbRetrySubmit();
  }

  function hideClosestPopover(element) {
    if (!element) return;
    var popover = element.closest('[popover]');
    if (popover && typeof popover.hidePopover === 'function') {
      popover.hidePopover();
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
      // ignore copy failures
    }
  }

  document.addEventListener('click', function (event) {
    var copyBtn = event.target.closest('[data-copy], [data-copy-target]');
    if (copyBtn) {
      event.preventDefault();
      var text = copyBtn.getAttribute('data-copy');
      if (!text && copyBtn.getAttribute('data-copy-target')) {
        var targetEl = document.querySelector(copyBtn.getAttribute('data-copy-target'));
        if (targetEl) text = targetEl.textContent.trim();
      }
      if (text) {
        copyText(copyBtn, text);
      }
      return;
    }

    var fsOpenBtn = event.target.closest('[data-file-server-open]');
    if (fsOpenBtn && fsDialog && typeof fsDialog.showModal === 'function') {
      event.preventDefault();
      hideClosestPopover(fsOpenBtn);
      if (fsForm) {
        fsForm.reset();
        updateFileServerSave();
      }
      fsDialog.showModal();
      return;
    }

    var recoveryBtn = event.target.closest('[data-storage-recovery-open]');
    if (recoveryBtn && recoveryDialog && typeof recoveryDialog.showModal === 'function') {
      event.preventDefault();
      if (recoveryForm) {
        recoveryForm.reset();
        updateRecoverySave();
      }
      recoveryDialog.showModal();
      return;
    }

    var addBtn = event.target.closest('[data-smb-add]');
    if (addBtn && smbDialog && typeof smbDialog.showModal === 'function') {
      event.preventDefault();
      hideClosestPopover(addBtn);
      if (smbForm) {
        smbForm.reset();
      }
      updateSmbSubmit();
      smbDialog.showModal();
      if (smbPathInput) {
        smbPathInput.focus();
      }
      return;
    }

    var nfsAddBtn = event.target.closest('[data-nfs-add]');
    if (nfsAddBtn && nfsDialog && typeof nfsDialog.showModal === 'function') {
      event.preventDefault();
      hideClosestPopover(nfsAddBtn);
      if (nfsForm) {
        nfsForm.reset();
      }
      updateNfsSubmit();
      nfsDialog.showModal();
      if (nfsServerInput) {
        nfsServerInput.focus();
      }
      return;
    }

    var retryBtn = event.target.closest('[data-smb-retry-id]');
    if (retryBtn && retryDialog && typeof retryDialog.showModal === 'function') {
      event.preventDefault();
      hideClosestPopover(retryBtn);
      var id = retryBtn.getAttribute('data-smb-retry-id') || '';
      var name = retryBtn.getAttribute('data-smb-retry-name') || '';
      var path = retryBtn.getAttribute('data-smb-retry-path') || '';

      if (retryForm) {
        retryForm.reset();
        retryForm.action = '/images/smb/' + encodeURIComponent(id) + '/retry';
      }
      var nameEl = document.getElementById('smb-retry-name');
      if (nameEl) {
        nameEl.textContent = name;
      }
      var pathEl = document.getElementById('smb-retry-path');
      if (pathEl) {
        pathEl.textContent = path;
      }
      updateSmbRetrySubmit();
      retryDialog.showModal();
      if (retryUserInput) {
        retryUserInput.focus();
      }
      return;
    }

    var nfsRetryBtn = event.target.closest('[data-nfs-retry-id]');
    if (nfsRetryBtn && nfsRetryDialog && typeof nfsRetryDialog.showModal === 'function') {
      event.preventDefault();
      hideClosestPopover(nfsRetryBtn);
      var nfsRetryID = nfsRetryBtn.getAttribute('data-nfs-retry-id') || '';
      var nfsRetryName = nfsRetryBtn.getAttribute('data-nfs-retry-name') || '';
      var nfsRetryVersion = nfsRetryBtn.getAttribute('data-nfs-retry-version') || '4.1';
      var nfsRetryLocation = nfsRetryBtn.getAttribute('data-nfs-retry-location') || '';

      var nfsRetryForm = nfsRetryDialog.querySelector('form');
      if (nfsRetryForm) {
        nfsRetryForm.reset();
        nfsRetryForm.action = '/images/nfs/' + encodeURIComponent(nfsRetryID) + '/retry';
      }
      var nfsNameEl = document.getElementById('nfs-retry-name');
      if (nfsNameEl) {
        nfsNameEl.textContent = nfsRetryName;
      }
      var nfsPathEl = document.getElementById('nfs-retry-path');
      if (nfsPathEl) {
        nfsPathEl.textContent = nfsRetryLocation;
      }
      var nfsVersionEl = document.getElementById('nfs-retry-version');
      if (nfsVersionEl) {
        nfsVersionEl.value = nfsRetryVersion;
      }
      nfsRetryDialog.showModal();
      if (nfsVersionEl) {
        nfsVersionEl.focus();
      }
      return;
    }

    var renameBtn = event.target.closest('[data-storage-rename-id]');
    if (renameBtn && renameDialog && typeof renameDialog.showModal === 'function') {
      event.preventDefault();
      hideClosestPopover(renameBtn);
      var renameId = renameBtn.getAttribute('data-storage-rename-id') || '';
      var renameName = renameBtn.getAttribute('data-storage-rename-name') || '';
      var renamePath = renameBtn.getAttribute('data-storage-rename-path') || '';

      var renameForm = renameDialog.querySelector('form');
      if (renameForm) {
        renameForm.reset();
        renameForm.action = '/images/storages/' + encodeURIComponent(renameId) + '/rename';
      }
      var renameIdInput = document.getElementById('storage-rename-id');
      if (renameIdInput) {
        renameIdInput.value = renameId;
      }
      var renameNameInput = document.getElementById('storage-rename-name');
      if (renameNameInput) {
        renameNameInput.value = renameName;
        renameNameInput.defaultValue = renameName;
      }
      var renamePathEl = document.getElementById('storage-rename-path');
      if (renamePathEl) {
        renamePathEl.textContent = renamePath;
      }
      updateRenameSave();
      renameDialog.showModal();
      if (renameNameInput) {
        renameNameInput.focus();
        renameNameInput.select();
      }
      return;
    }

    var deleteBtn = event.target.closest('[data-storage-delete-id], [data-smb-delete-id]');
    if (deleteBtn && deleteDialog && typeof deleteDialog.showModal === 'function') {
      event.preventDefault();
      hideClosestPopover(deleteBtn);
      var deleteId = deleteBtn.getAttribute('data-storage-delete-id') || deleteBtn.getAttribute('data-smb-delete-id') || '';
      var deleteName = deleteBtn.getAttribute('data-storage-delete-name') || deleteBtn.getAttribute('data-smb-delete-name') || '';
      var deletePath = deleteBtn.getAttribute('data-storage-delete-path') || deleteBtn.getAttribute('data-smb-delete-path') || '';
      var deleteKind = deleteBtn.getAttribute('data-storage-delete-kind') || (deleteBtn.hasAttribute('data-nfs-retry-id') ? 'nfs' : 'smb');

      var deleteForm = deleteDialog.querySelector('form');
      if (deleteForm) {
        deleteForm.reset();
        var prefix = deleteKind === 'nfs' ? '/images/nfs/' : '/images/smb/';
        deleteForm.action = prefix + encodeURIComponent(deleteId) + '/remove';
      }
      var deleteNameEl = document.getElementById('storage-delete-name') || document.getElementById('smb-delete-name');
      if (deleteNameEl) {
        deleteNameEl.textContent = deleteName;
      }
      var deletePathEl = document.getElementById('storage-delete-path') || document.getElementById('smb-delete-path');
      if (deletePathEl) {
        deletePathEl.textContent = deletePath;
      }
      deleteDialog.showModal();
      return;
    }

    var closeBtn = event.target.closest('[data-close-dialog]');
    if (closeBtn) {
      event.preventDefault();
      var dialog = closeBtn.closest('dialog');
      if (dialog && typeof dialog.close === 'function') {
        dialog.close();
      }
      return;
    }
  });
})();
