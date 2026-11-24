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

  var editDlg = document.getElementById('machine-edit-dialog');
  if (editDlg) {
    var editForm = editDlg.querySelector('form');
    var editSaveBtn = editDlg.querySelector('[data-machine-edit-save]') || editDlg.querySelector('button.btn-primary');
    var nameInput = document.getElementById('m-name');
    var makerInput = document.getElementById('m-maker');
    var modelInput = document.getElementById('m-model');
    var noteInput = document.getElementById('m-note');

    function getInitial() {
      return {
        name: nameInput ? nameInput.defaultValue : '',
        maker: makerInput ? makerInput.defaultValue : '',
        model: modelInput ? modelInput.defaultValue : '',
        note: noteInput ? noteInput.defaultValue : ''
      };
    }
    var initial = getInitial();

    function syncEditSave() {
      if (!editSaveBtn) return;
      var curName = (nameInput ? nameInput.value : '').trim();
      var curMaker = (makerInput ? makerInput.value : '').trim();
      var curModel = (modelInput ? modelInput.value : '').trim();
      var curNote = (noteInput ? noteInput.value : '').trim();

      var initName = (initial.name || '').trim();
      var initMaker = (initial.maker || '').trim();
      var initModel = (initial.model || '').trim();
      var initNote = (initial.note || '').trim();

      var isDirty = curName !== initName ||
                    curMaker !== initMaker ||
                    curModel !== initModel ||
                    curNote !== initNote;
      editSaveBtn.disabled = !isDirty;
    }

    if (editForm) {
      editForm.addEventListener('input', syncEditSave);
      editForm.addEventListener('change', syncEditSave);
      editForm.addEventListener('submit', function(ev) {
        syncEditSave();
        if (editSaveBtn && editSaveBtn.disabled) {
          ev.preventDefault();
        }
      });
    }

    var editOpenBtn = document.querySelector('[data-machine-edit-open]');
    if (editOpenBtn) {
      editOpenBtn.addEventListener('click', function() {
        if (editForm && typeof editForm.reset === 'function') {
          editForm.reset();
        }
        syncEditSave();
      });
    }

    syncEditSave();
  }
  var retry = document.querySelector('[data-events-retry]');
  if (retry) {
    retry.addEventListener('click', function() {
      window.location.reload();
    });
  }
})();
