(function () {
  'use strict';

  var uploadDialog = document.getElementById('answer-upload-dialog');

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
