(function () {
  'use strict';

  var uploadDialog = document.getElementById('answer-upload-dialog');
  var winUploadTitle = document.getElementById('answer-upload-title-input');
  var winUploadFile = document.getElementById('answer-upload-file-input');
  var winAutoTitle = '';
  var ubuntuUploadDialog = document.getElementById('ubuntu-upload-dialog');
  var ubuntuUploadTitle = document.getElementById('ubuntu-upload-title-input');
  var ubuntuUploadFile = document.getElementById('ubuntu-upload-file-input');
  var ubuntuAutoTitle = '';
  var renameDialog = document.getElementById('answer-rename-dialog');
  var renameForm = renameDialog ? renameDialog.querySelector('form') : null;
  var renameInput = document.getElementById('answer-rename-input');
  var renameSave = renameDialog ? renameDialog.querySelector('[data-answer-rename-save]') : null;
  var renameKind = document.getElementById('answer-rename-kind');
  var renameID = document.getElementById('answer-rename-id');
  var renameSHA = document.getElementById('answer-rename-sha');
  var renameInitial = '';

  function syncRenameSubmit() {
    if (!renameInput || !renameSave) return;
    var val = renameInput.value.trim();
    var len = Array.from(val).length;
    renameSave.disabled = !val || len > 80 || val === renameInitial;
  }

  if (renameInput) {
    renameInput.addEventListener('input', syncRenameSubmit);
  }

  var searchForm = document.querySelector('[data-answers-search]');
  if (searchForm) {
    var searchInput = searchForm.querySelector('input[name="q"]');
    if (searchInput) {
      searchInput.addEventListener('input', function () {
        var query = searchInput.value.trim().toLowerCase();
        var rows = document.querySelectorAll('.answers-table tbody tr');
        rows.forEach(function (row) {
          var text = (row.textContent || '').toLowerCase();
          var shaChip = row.querySelector('[data-sha-chip]');
          var fullSha = shaChip ? (shaChip.getAttribute('data-full') || '').toLowerCase() : '';
          var matches = !query || text.indexOf(query) !== -1 || (fullSha && fullSha.indexOf(query) !== -1);
          row.style.display = matches ? '' : 'none';
        });
      });
    }
  }

  if (winUploadFile && winUploadTitle) {
    winUploadFile.addEventListener('change', function () {
      var file = winUploadFile.files && winUploadFile.files[0];
      if (!file) return;
      var current = winUploadTitle.value.trim();
      if (current && current !== winAutoTitle) return;
      var base = file.name.replace(/\.xml$/i, '').trim();
      if (!base || /^(auto)?unattend$/i.test(base)) return;
      if (base.length > 80) base = base.slice(0, 80);
      winAutoTitle = base;
      winUploadTitle.value = base;
    });
  }

  if (ubuntuUploadFile && ubuntuUploadTitle) {
    ubuntuUploadFile.addEventListener('change', function () {
      var file = ubuntuUploadFile.files && ubuntuUploadFile.files[0];
      if (!file) return;
      var current = ubuntuUploadTitle.value.trim();
      if (current && current !== ubuntuAutoTitle) return;
      var base = file.name.replace(/\.(ya?ml|txt)$/i, '').trim();
      if (!base || base.toLowerCase() === 'user-data') return;
      if (base.length > 80) base = base.slice(0, 80);
      ubuntuAutoTitle = base;
      ubuntuUploadTitle.value = base;
    });
  }

  document.addEventListener('click', function (event) {
    var copyYamlBtn = event.target.closest('[data-copy-yaml]');
    if (copyYamlBtn) {
      var pre = document.getElementById('autoinstall-yaml');
      if (pre) {
        var text = pre.textContent || '';
        var original = copyYamlBtn.getAttribute('data-orig') || copyYamlBtn.textContent;
        copyYamlBtn.setAttribute('data-orig', original);
        var done = function () {
          copyYamlBtn.textContent = copyYamlBtn.getAttribute('data-copied') || 'Copied';
          setTimeout(function () { copyYamlBtn.textContent = original; }, 1600);
        };
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(done, function () {});
        }
      }
      return;
    }

    var ubuntuUploadBtn = event.target.closest('[data-ubuntu-upload-open]');
    if (ubuntuUploadBtn && ubuntuUploadDialog && typeof ubuntuUploadDialog.showModal === 'function') {
      event.preventDefault();
      var uForm = ubuntuUploadDialog.querySelector('form');
      if (uForm) uForm.reset();
      ubuntuAutoTitle = '';
      var uPopover = ubuntuUploadBtn.closest('[popover]');
      if (uPopover && typeof uPopover.hidePopover === 'function') uPopover.hidePopover();
      ubuntuUploadDialog.showModal();
      if (ubuntuUploadTitle) ubuntuUploadTitle.focus();
      return;
    }
    var renameBtn = event.target.closest('[data-answer-rename-open]');
    if (renameBtn && renameDialog && renameForm && renameInput && typeof renameDialog.showModal === 'function') {
      var renameMenu = renameBtn.closest('[popover]');
      if (renameMenu && typeof renameMenu.hidePopover === 'function') renameMenu.hidePopover();
      var kind = renameBtn.getAttribute('data-rename-kind') || 'windows';
      var id = renameBtn.getAttribute('data-rename-id') || '0';
      var title = (renameBtn.getAttribute('data-rename-title') || '').trim();
      var sha = (renameBtn.getAttribute('data-rename-sha') || '').trim();
      renameForm.action = '/answers/' + encodeURIComponent(kind) + '/' + encodeURIComponent(id) + '/rename';
      if (renameKind) renameKind.textContent = kind === 'ubuntu' ? 'Ubuntu' : 'Windows';
      if (renameID) renameID.textContent = '№ ' + id;
      if (renameSHA) renameSHA.textContent = sha.slice(0, 8);
      renameInitial = title;
      renameInput.value = title;
      syncRenameSubmit();
      renameDialog.showModal();
      renameInput.focus();
      renameInput.select();
      return;
    }

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
      winAutoTitle = '';
      var popover = uploadBtn.closest('[popover]');
      if (popover && typeof popover.hidePopover === 'function') {
        popover.hidePopover();
      }
      uploadDialog.showModal();
      var firstInput = uploadDialog.querySelector('input[name="title"]');
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
  });
})();

(function () {
  'use strict';
  var dialog = document.getElementById('answer-check-dialog');
  if (!dialog) return;
  var form = document.getElementById('answer-check-form');
  var error = dialog.querySelector('[data-check-error]');
  var list = dialog.querySelector('[data-check-history-list]');
  var more = dialog.querySelector('[data-check-more]');
  var submit = dialog.querySelector('[data-check-submit]');
  var viewClose = dialog.querySelector('[data-check-view-close]');
  var profileWrap = dialog.querySelector('[data-check-profile-wrap]');
  var profileSelect = form ? form.querySelector('select[name="windows_profile_id"]') : null;
  var endpoint = '', digest = '', next = 0, serial = 0, saving = false;
  var historyRequest;

  function showError(message) {
    error.textContent = message;
    error.hidden = !message;
  }

  function appendCheck(check) {
    var entry = document.createElement('article');
    entry.className = 'answer-check-entry';
    var result = document.createElement('span');
    result.textContent = check.result === 'success' ? dialog.dataset.success : dialog.dataset.failure;
    if (check.result === 'failure') result.className = 'check-failure';
    entry.appendChild(result);
    if (check.profile_title) {
      var profile = document.createElement('small');
      profile.className = 'answer-check-image';
      profile.textContent = check.profile_title;
      entry.appendChild(profile);
    }
    var meta = document.createElement('small');
    var date = new Date(check.created_at);
    meta.textContent = check.author + ' · ' + date.toLocaleString(document.documentElement.lang || 'ru');
    entry.appendChild(meta);
    if (check.sha256 !== digest) {
      var old = document.createElement('small');
      old.textContent = dialog.dataset.old;
      entry.appendChild(old);
    }
    [check.machine, check.comment].forEach(function (text) {
      if (!text) return;
      var p = document.createElement('p');
      p.textContent = text;
      entry.appendChild(p);
    });
    list.appendChild(entry);
  }

  async function readHistory(append, editing) {
    var current = serial;
    if (historyRequest) historyRequest.abort();
    historyRequest = new AbortController();
    more.disabled = true;
    if (!append) list.textContent = dialog.dataset.loading;
    try {
      var response = await fetch(endpoint + (append ? '?before=' + next : ''), {
        credentials: 'same-origin', signal: historyRequest.signal, headers: {'Accept': 'application/json'}
      });
      if (!response.ok) throw new Error(await response.text());
      var data = await response.json();
      if (current !== serial || !dialog.open) return;
      if (!append) list.textContent = '';
      (data.checks || []).forEach(appendCheck);
      if (!list.childNodes.length) list.textContent = dialog.dataset.empty;
      next = data.next || 0;
      more.hidden = !next;
      if (!append) {
        form.hidden = !(editing && data.can_record);
        form.elements.sha256.value = digest;
        viewClose.hidden = !form.hidden;
        if (!form.hidden) form.querySelector('input[type=radio]').focus();
      }
    } catch (failure) {
      if (failure.name === 'AbortError' || current !== serial) return;
      if (!append) list.textContent = '';
      showError(dialog.dataset.loadError);
    } finally {
      if (current === serial) more.disabled = false;
    }
  }

  document.addEventListener('click', function (event) {
    var trigger = event.target.closest('[data-answer-check-open], [data-answer-history]');
    if (!trigger || saving) return;
    var menu = trigger.closest('[popover]');
    if (menu && typeof menu.hidePopover === 'function') menu.hidePopover();
    serial++;
    endpoint = '/answers/' + encodeURIComponent(trigger.dataset.checkKind) + '/' + encodeURIComponent(trigger.dataset.checkId) + '/checks';
    digest = trigger.dataset.checkSha;
    next = 0;
    var isWindows = trigger.dataset.checkKind === 'windows';
    if (profileWrap) profileWrap.hidden = !isWindows;
    if (profileSelect) profileSelect.disabled = !isWindows;
    form.reset();
    form.hidden = true;
    viewClose.hidden = false;
    more.hidden = true;
    showError('');
    dialog.querySelector('.answer-check-title').textContent = trigger.dataset.checkTitle;
    dialog.showModal();
    readHistory(false, trigger.hasAttribute('data-answer-check-open'));
  });

  more.addEventListener('click', function () { showError(''); readHistory(true, !form.hidden); });
  dialog.addEventListener('close', function () { serial++; if (historyRequest) historyRequest.abort(); });
  form.addEventListener('submit', async function (event) {
    event.preventDefault();
    if (saving || !form.reportValidity()) return;
    saving = true;
    submit.disabled = true;
    showError('');
    try {
      var response = await fetch(endpoint, {method: 'POST', credentials: 'same-origin', headers: {'Accept':'application/json'}, body: new URLSearchParams(new FormData(form))});
      if (!response.ok) {
        var rejected = new Error();
        rejected.userMessage = (await response.text()).trim() || dialog.dataset.saveError;
        throw rejected;
      }
      // Require the JSON acknowledgement, including when a login redirect returned HTML.
      var data = await response.json();
      if (!data.ok) throw new Error(dialog.dataset.saveError);
      window.location.reload();
    } catch (failure) {
      showError(failure.userMessage || dialog.dataset.saveError);
    } finally {
      saving = false;
      submit.disabled = false;
    }
  });
})();
