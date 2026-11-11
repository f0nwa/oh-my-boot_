(function() {
  function setupDialog(triggerSelector, dialogId) {
    var btn = document.querySelector(triggerSelector);
    var dlg = document.getElementById(dialogId);
    if (!btn || !dlg) return;
    btn.addEventListener('click', function() {
      if (typeof dlg.showModal === 'function') dlg.showModal();
      else dlg.setAttribute('open', '');
    });
    dlg.querySelectorAll('[data-close-dialog]').forEach(function(c) {
      c.addEventListener('click', function() {
        if (typeof dlg.close === 'function') dlg.close();
        else dlg.removeAttribute('open');
      });
    });
    dlg.addEventListener('click', function(e) {
      if (e.target === dlg && typeof dlg.close === 'function') dlg.close();
    });
  }
  setupDialog('[data-machine-edit-open]', 'machine-edit-dialog');
  setupDialog('[data-machine-delete-open]', 'machine-delete-dialog');
  var retry = document.querySelector('[data-events-retry]');
  if (retry) {
    retry.addEventListener('click', function() {
      window.location.reload();
    });
  }
})();
