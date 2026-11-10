(function () {
  'use strict';

  var createDialog = document.getElementById('machine-create-dialog');
  var importDialog = document.getElementById('machine-import-dialog');
  var editDialog = document.getElementById('machine-edit-dialog');
  var ruleCreateDialog = document.getElementById('rule-create-dialog');
  var ruleEditDialog = document.getElementById('rule-edit-dialog');

  // Auto-open dialog on validation error from server
  document.querySelectorAll('dialog[data-auto-open="true"]').forEach(function (dlg) {
    if (typeof dlg.showModal === 'function' && !dlg.open) {
      dlg.showModal();
    }
  });

  document.addEventListener('click', function (ev) {
    var addBtn = ev.target.closest('[data-machine-add-open]');
    if (addBtn && createDialog && typeof createDialog.showModal === 'function') {
      ev.preventDefault();
      createDialog.showModal();
      var macInput = createDialog.querySelector('input[name="mac"]');
      if (macInput) macInput.focus();
      return;
    }

    var importBtn = ev.target.closest('[data-machine-import-open]');
    if (importBtn && importDialog && typeof importDialog.showModal === 'function') {
      ev.preventDefault();
      importDialog.showModal();
      return;
    }

    var ruleAddBtn = ev.target.closest('[data-rule-add-open]');
    if (ruleAddBtn && ruleCreateDialog && typeof ruleCreateDialog.showModal === 'function') {
      ev.preventDefault();
      ruleCreateDialog.showModal();
      var prefixInput = ruleCreateDialog.querySelector('input[name="prefix"]');
      if (prefixInput) prefixInput.focus();
      return;
    }

    var editBtn = ev.target.closest('[data-machine-edit-btn]');
    if (editBtn && editDialog && typeof editDialog.showModal === 'function') {
      ev.preventDefault();
      var mac = editBtn.getAttribute('data-mac') || '';
      var name = editBtn.getAttribute('data-name') || '';
      var maker = editBtn.getAttribute('data-maker') || '';
      var makerOrig = editBtn.getAttribute('data-maker-orig') || '';
      var product = editBtn.getAttribute('data-product') || '';
      var productOrig = editBtn.getAttribute('data-product-orig') || '';
      var note = editBtn.getAttribute('data-note') || '';

      var macEl = document.getElementById('edit-machine-mac');
      if (macEl) macEl.textContent = mac;

      var editForm = document.getElementById('machine-edit-form');
      if (editForm) {
        editForm.action = '/machines/' + mac;
        editForm.setAttribute('data-target-row', 'm-' + mac);
        var status = editForm.querySelector('[data-row-status]');
        if (status) status.hidden = true;
      }

      var deleteForm = document.getElementById('machine-delete-form');
      if (deleteForm) {
        deleteForm.action = '/machines/' + mac + '/delete';
      }

      var nameInput = document.getElementById('edit-machine-name');
      if (nameInput) nameInput.value = name;

      var makerInput = document.getElementById('edit-machine-maker');
      if (makerInput) {
        makerInput.value = maker;
        makerInput.placeholder = makerOrig;
      }

      var productInput = document.getElementById('edit-machine-product');
      if (productInput) {
        productInput.value = product;
        productInput.placeholder = productOrig;
      }

      var noteInput = document.getElementById('edit-machine-note');
      if (noteInput) noteInput.value = note;

      editDialog.showModal();
      if (nameInput) nameInput.focus();
      return;
    }

    var ruleEditBtn = ev.target.closest('[data-rule-edit-btn]');
    if (ruleEditBtn && ruleEditDialog && typeof ruleEditDialog.showModal === 'function') {
      ev.preventDefault();
      var prefix = ruleEditBtn.getAttribute('data-prefix') || '';
      var title = ruleEditBtn.getAttribute('data-title') || '';
      var ruleMaker = ruleEditBtn.getAttribute('data-maker') || '';
      var driver = ruleEditBtn.getAttribute('data-driver') || '';
      var ruleNote = ruleEditBtn.getAttribute('data-note') || '';

      var prefixEl = document.getElementById('edit-rule-prefix');
      if (prefixEl) prefixEl.textContent = prefix;

      var ruleEditForm = document.getElementById('rule-edit-form');
      if (ruleEditForm) {
        ruleEditForm.action = '/machine-rules/' + prefix;
      }

      var ruleDeleteForm = document.getElementById('rule-delete-form');
      if (ruleDeleteForm) {
        ruleDeleteForm.action = '/machine-rules/' + prefix + '/delete';
      }

      var titleInput = document.getElementById('edit-rule-title');
      if (titleInput) titleInput.value = title;

      var ruleMakerInput = document.getElementById('edit-rule-maker');
      if (ruleMakerInput) ruleMakerInput.value = ruleMaker;

      var driverSelect = document.getElementById('edit-rule-driver');
      if (driverSelect) driverSelect.value = driver;

      var ruleNoteInput = document.getElementById('edit-rule-note');
      if (ruleNoteInput) ruleNoteInput.value = ruleNote;

      ruleEditDialog.showModal();
      if (titleInput) titleInput.focus();
      return;
    }

    var closeBtn = ev.target.closest('[data-close-dialog]');
    if (closeBtn) {
      ev.preventDefault();
      var dlg = closeBtn.closest('dialog');
      if (dlg && typeof dlg.close === 'function') {
        dlg.close();
      }
      return;
    }

    if (ev.target && ev.target.nodeName === 'DIALOG' && typeof ev.target.close === 'function') {
      ev.target.close();
    }
  });

  // Saves a machine row edited via dialog without reloading the page.
  if (!window.fetch || !window.DOMParser || !window.FormData || !window.URLSearchParams) return;
  document.addEventListener('submit', function (ev) {
    var form = ev.target;
    if (!form.matches || !form.matches('form[data-row-save]')) return;
    var rowId = form.getAttribute('data-target-row');
    var row = rowId ? document.getElementById(rowId) : form.closest('tr');
    if (!row || !row.id) return;
    ev.preventDefault();
    var dlg = form.closest('dialog');
    var button = (dlg ? dlg.querySelector('button[form="' + form.id + '"]') : null) || form.querySelector('button');
    var status = form.querySelector('[data-row-status]');
    function fail(text) {
      if (status) { status.textContent = text || status.getAttribute('data-failed'); status.hidden = false; }
      if (button) button.disabled = false;
    }
    if (button) button.disabled = true;
    fetch(form.action, { method: 'POST', body: new URLSearchParams(new FormData(form)), credentials: 'same-origin', headers: { 'Accept': 'text/html' } })
      .then(function (res) {
        return res.text().then(function (html) {
          var doc = new DOMParser().parseFromString(html, 'text/html');
          if (!res.ok) {
            var err = doc.querySelector('.notice.error');
            return fail(err ? err.textContent : '');
          }
          if (button) button.disabled = false;
          var next = doc.getElementById(row.id);
          if (!next) { location.href = res.url; return; }
          row.replaceWith(next);
          if (dlg && typeof dlg.close === 'function') dlg.close();
          var saved = next.querySelector('[data-row-saved]');
          if (saved) saved.hidden = false;
          var counts = doc.querySelector('.machine-counts'), here = document.querySelector('.machine-counts');
          if (counts && here) here.innerHTML = counts.innerHTML;
          var link = next.querySelector('a');
          if (link) link.focus();
        });
      })
      .catch(function () { fail(''); });
  });
})();
