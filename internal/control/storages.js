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
    var changed = enabled.checked !== enabled.defaultChecked ||
      interval.valueAsNumber !== Number(interval.defaultValue);
    recoverySave.disabled = !changed || !interval.validity.valid;
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

  function hideClosestPopover(element) {
    if (!element) return;
    var popover = element.closest('[popover]');
    if (popover && typeof popover.hidePopover === 'function') {
      popover.hidePopover();
    }
  }

  document.addEventListener('click', function (event) {
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
      var form = smbDialog.querySelector('form');
      if (form) {
        form.reset();
      }
      smbDialog.showModal();
      var pathInput = smbDialog.querySelector('input[name="path"]');
      if (pathInput) {
        pathInput.focus();
      }
      return;
    }

    var nfsAddBtn = event.target.closest('[data-nfs-add]');
    if (nfsAddBtn && nfsDialog && typeof nfsDialog.showModal === 'function') {
      event.preventDefault();
      hideClosestPopover(nfsAddBtn);
      var nfsForm = nfsDialog.querySelector('form');
      if (nfsForm) {
        nfsForm.reset();
      }
      nfsDialog.showModal();
      var serverInput = nfsDialog.querySelector('input[name="server"]');
      if (serverInput) {
        serverInput.focus();
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

      var retryForm = retryDialog.querySelector('form');
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
      retryDialog.showModal();
      var userInput = retryDialog.querySelector('input[name="username"]');
      if (userInput) {
        userInput.focus();
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

    if (event.target && event.target.nodeName === 'DIALOG' && typeof event.target.close === 'function') {
      event.target.close();
    }
  });
})();
