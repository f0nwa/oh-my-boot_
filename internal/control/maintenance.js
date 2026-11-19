(function () {
  'use strict';

  function showDialog(dlg) {
    if (!dlg) return;
    if (typeof dlg.showModal === 'function') dlg.showModal();
    else dlg.setAttribute('open', '');
  }

  function closeDialog(dlg) {
    if (!dlg) return;
    if (typeof dlg.close === 'function') dlg.close();
    else dlg.removeAttribute('open');
  }

  document.addEventListener('click', function (event) {
    var btn = event.target.closest('[data-maintenance-open]');
    if (btn) {
      var dlg = document.getElementById('maintenance-enable-dialog');
      if (!dlg) return;
      var form = dlg.querySelector('form');
      if (form) {
        form.querySelector('[name="scope"]').value = btn.dataset.scope || 'storage';
        form.querySelector('[name="storage_id"]').value = btn.dataset.storageId || '';
        form.querySelector('[name="path"]').value = btn.dataset.path || '';
        form.querySelector('[name="return_to"]').value = btn.dataset.returnTo || location.pathname;
        var reasonInput = form.querySelector('[name="reason"]');
        if (reasonInput) reasonInput.value = '';
      }
      var target = document.getElementById('maint-dlg-target');
      if (target) target.textContent = btn.dataset.label || '';
      var activeEl = document.getElementById('maint-dlg-active');
      if (activeEl) {
        var count = parseInt(btn.dataset.activeCount || '0', 10) || 0;
        activeEl.textContent = count > 0
          ? (activeEl.dataset.busyText || '').replace('%d', String(count))
          : (activeEl.dataset.idleText || '');
      }
      showDialog(dlg);
      return;
    }

    var closeBtn = event.target.closest('#maintenance-enable-dialog [data-close-dialog]');
    if (closeBtn) {
      closeDialog(closeBtn.closest('dialog'));
      return;
    }

    if (event.target && event.target.id === 'maintenance-enable-dialog') {
      closeDialog(event.target);
    }
  });
})();
