(function () {
  'use strict';

  var smbDialog = document.getElementById('smb-create-dialog');
  var retryDialog = document.getElementById('smb-retry-dialog');
  var recoveryDialog = document.getElementById('storage-recovery-dialog');
  var recoveryForm = document.getElementById('storage-recovery-settings');
  var recoverySave = recoveryDialog && recoveryDialog.querySelector('[data-storage-recovery-save]');

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

    var retryBtn = event.target.closest('[data-smb-retry-id]');
    if (retryBtn && retryDialog && typeof retryDialog.showModal === 'function') {
      event.preventDefault();
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
