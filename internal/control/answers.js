(function () {
  'use strict';

  var uploadDialog = document.getElementById('answer-upload-dialog');

  document.addEventListener('click', function (event) {
    var deleteBtn = event.target.closest('[data-answer-delete-open]');
    if (deleteBtn) {
      var deleteDialog = document.getElementById(deleteBtn.getAttribute('data-answer-delete-open'));
      var menu = deleteBtn.closest('[popover]');
      if (menu && typeof menu.hidePopover === 'function') menu.hidePopover();
      if (deleteDialog && typeof deleteDialog.showModal === 'function') deleteDialog.showModal();
      return;
    }

    var uploadBtn = event.target.closest('[data-answer-upload-open]');
    if (uploadBtn && uploadDialog && typeof uploadDialog.showModal === 'function') {
      event.preventDefault();
      var form = uploadDialog.querySelector('form');
      if (form) {
        form.reset();
      }
      var popover = uploadBtn.closest('[popover]');
      if (popover && typeof popover.hidePopover === 'function') {
        popover.hidePopover();
      }
      uploadDialog.showModal();
      var firstInput = uploadDialog.querySelector('select[name="profile_id"], input[name="title"]');
      if (firstInput) {
        firstInput.focus();
      }
      return;
    }

    var closeBtn = event.target.closest('[data-close-dialog]');
    if (closeBtn) {
      var dialog = closeBtn.closest('dialog');
      if (dialog && typeof dialog.close === 'function') {
        event.preventDefault();
        dialog.close();
      }
      return;
    }

    if (event.target && event.target.nodeName === 'DIALOG') {
      var rect = event.target.getBoundingClientRect();
      var isInDialog =
        rect.top <= event.clientY &&
        event.clientY <= rect.top + rect.height &&
        rect.left <= event.clientX &&
        event.clientX <= rect.left + rect.width;
      if (!isInDialog && typeof event.target.close === 'function') {
        event.target.close();
      }
    }
  });
})();
