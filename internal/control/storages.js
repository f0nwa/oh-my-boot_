(function () {
  'use strict';

  var smbDialog = document.getElementById('smb-create-dialog');

  document.addEventListener('click', function (event) {
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
