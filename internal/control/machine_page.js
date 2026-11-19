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
  setupDialog('[data-machine-smb-disconnect-open]', 'machine-smb-disconnect-dialog');
  setupDialog('[data-machine-http-cancel-open]', 'machine-http-cancel-dialog');
  setupDialog('[data-machine-dhcp-ignore-open]', 'machine-dhcp-ignore-dialog');
  var retry = document.querySelector('[data-events-retry]');
  if (retry) {
    retry.addEventListener('click', function() {
      window.location.reload();
    });
  }
})();
