(() => {
  let lastClickedIdx = null;

  function currentReturnTo() {
    const url = new URL(window.location.href);
    url.searchParams.delete('message');
    const qs = url.searchParams.toString();
    return qs ? `${url.pathname}?${qs}` : url.pathname;
  }

  function formatTemplate(tpl, ...args) {
    let i = 0;
    return (tpl || '').replace(/%d/g, () => String(args[i++] ?? ''));
  }

  function rowCheckboxes() {
    return Array.from(document.querySelectorAll('#image-results [data-bulk-select-row]'));
  }

  function enabledCheckboxes() {
    return rowCheckboxes().filter((cb) => !cb.disabled);
  }

  function checkedCheckboxes() {
    return enabledCheckboxes().filter((cb) => cb.checked);
  }

  function syncBulkUI() {
    const bar = document.getElementById('image-bulk-bar');
    const selectAll = document.getElementById('image-select-all');
    const countEl = document.getElementById('image-bulk-count');
    const delBtn = document.getElementById('image-bulk-delete-open');
    const all = rowCheckboxes();
    const enabled = all.filter((cb) => !cb.disabled);
    const checked = enabled.filter((cb) => cb.checked);

    for (const cb of all) {
      const tr = cb.closest('tr');
      if (tr) tr.classList.toggle('is-selected', !cb.disabled && cb.checked);
    }

    if (selectAll) {
      selectAll.disabled = enabled.length === 0;
      selectAll.checked = enabled.length > 0 && checked.length === enabled.length;
      selectAll.indeterminate = checked.length > 0 && checked.length < enabled.length;
    }

    if (!bar) return;
    if (checked.length === 0) {
      bar.hidden = true;
      return;
    }

    const absentOnly = checked.every((cb) => cb.dataset.present === '0');
    if (countEl) {
      const label = countEl.dataset.label || 'Выбрано';
      countEl.textContent = `${label}: ${checked.length}`;
    }
    if (delBtn) {
      const base = absentOnly
        ? (delBtn.dataset.labelLibrary || 'Удалить из библиотеки')
        : (delBtn.dataset.labelDelete || 'Удалить');
      delBtn.textContent = `${base} (${checked.length})`;
    }
    bar.hidden = false;
  }

  function openBulkDeleteDialog() {
    const dialog = document.getElementById('image-bulk-delete-dialog');
    const titleEl = document.getElementById('image-bulk-delete-title');
    const summaryEl = document.getElementById('image-bulk-delete-summary');
    const listEl = document.getElementById('image-bulk-delete-list');
    const inputsEl = document.getElementById('image-bulk-delete-inputs');
    const confirmBtn = document.getElementById('image-bulk-delete-confirm');
    const form = document.getElementById('image-bulk-delete-form');
    if (!dialog || !titleEl || !summaryEl || !listEl || !inputsEl || !confirmBtn || !form) return;

    const checked = checkedCheckboxes();
    if (checked.length === 0) return;

    const absent = checked.filter((cb) => cb.dataset.present === '0');
    const onDisk = checked.filter((cb) => cb.dataset.present === '1');

    summaryEl.textContent = '';
    const box = document.createElement('p');
    if (onDisk.length === 0) {
      titleEl.textContent = formatTemplate(dialog.dataset.titleAbsent, absent.length);
      box.className = 'bulk-dlg-info';
      box.textContent = formatTemplate(dialog.dataset.summaryAbsent, absent.length);
      confirmBtn.textContent = formatTemplate(dialog.dataset.btnAbsent, absent.length);
    } else if (absent.length === 0) {
      titleEl.textContent = formatTemplate(dialog.dataset.titleDisk, onDisk.length);
      box.className = 'bulk-dlg-warn';
      box.textContent = formatTemplate(dialog.dataset.summaryDisk, onDisk.length);
      confirmBtn.textContent = formatTemplate(dialog.dataset.btnDisk, onDisk.length);
    } else {
      titleEl.textContent = formatTemplate(dialog.dataset.titleMixed, checked.length);
      box.className = 'bulk-dlg-warn';
      box.textContent = formatTemplate(dialog.dataset.summaryMixed, onDisk.length, absent.length);
      confirmBtn.textContent = formatTemplate(dialog.dataset.btnMixed, checked.length);
    }
    summaryEl.appendChild(box);

    listEl.textContent = '';
    inputsEl.textContent = '';
    const tagAbsent = dialog.dataset.tagAbsent || 'из библиотеки';
    const tagDisk = dialog.dataset.tagDisk || 'с диска';

    for (const cb of checked) {
      const li = document.createElement('li');
      const pathSpan = document.createElement('span');
      pathSpan.className = 'mono bulk-dlg-path';
      pathSpan.textContent = cb.dataset.label || cb.dataset.path || '';
      const tagSpan = document.createElement('span');
      const isPresent = cb.dataset.present === '1';
      tagSpan.className = isPresent ? 'bulk-dlg-tag bulk-dlg-tag-disk' : 'bulk-dlg-tag bulk-dlg-tag-absent';
      tagSpan.textContent = isPresent ? tagDisk : tagAbsent;
      li.appendChild(pathSpan);
      li.appendChild(tagSpan);
      listEl.appendChild(li);

      const input = document.createElement('input');
      input.type = 'hidden';
      input.name = 'item';
      input.value = `${cb.dataset.storageId}:${cb.dataset.path}`;
      inputsEl.appendChild(input);
    }

    const ret = form.querySelector('input[name="return_to"]');
    if (ret) ret.value = currentReturnTo();

    dialog.showModal();
  }

  document.addEventListener('click', (event) => {
    const button = event.target.closest('[data-image-delete-open]');
    if (button) {
      if (button.disabled) return;
      const dialog = document.getElementById(button.dataset.imageDeleteOpen);
      if (!dialog) return;
      const ret = dialog.querySelector('input[name="return_to"]');
      if (ret) ret.value = currentReturnTo();
      const menu = button.closest('[popover]');
      if (menu && menu.matches(':popover-open')) menu.hidePopover();
      dialog.showModal();
      return;
    }

    const close = event.target.closest('[data-image-delete-close]');
    if (close) {
      const dlg = close.closest('dialog');
      if (dlg) dlg.close();
      return;
    }

    const rowCb = event.target.closest('#image-results [data-bulk-select-row]');
    if (rowCb && !rowCb.disabled) {
      const all = rowCheckboxes();
      const idx = all.indexOf(rowCb);
      if (event.shiftKey && lastClickedIdx !== null && idx >= 0 && lastClickedIdx < all.length) {
        const from = Math.min(lastClickedIdx, idx);
        const to = Math.max(lastClickedIdx, idx);
        for (let i = from; i <= to; i++) {
          if (!all[i].disabled) all[i].checked = rowCb.checked;
        }
      }
      if (idx >= 0) lastClickedIdx = idx;
      syncBulkUI();
      return;
    }

    if (event.target.closest('#image-bulk-clear')) {
      for (const cb of rowCheckboxes()) cb.checked = false;
      lastClickedIdx = null;
      syncBulkUI();
      return;
    }

    if (event.target.closest('#image-bulk-delete-open')) {
      openBulkDeleteDialog();
    }
  });

  document.addEventListener('change', (event) => {
    if (event.target && event.target.id === 'image-select-all') {
      const checked = event.target.checked;
      for (const cb of enabledCheckboxes()) cb.checked = checked;
      lastClickedIdx = null;
      syncBulkUI();
    }
  });

  document.addEventListener('submit', (event) => {
    const form = event.target;
    if (!form || form.tagName !== 'FORM') return;
    const ret = form.querySelector('input[name="return_to"]');
    if (ret && window.location.pathname === '/images') {
      ret.value = currentReturnTo();
    }
  });

  document.addEventListener('omb:images-updated', () => {
    lastClickedIdx = null;
    syncBulkUI();
  });

  syncBulkUI();
})();
