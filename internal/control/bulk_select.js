(() => {
  const lastClickedByGroup = Object.create(null);

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

  function groupRows(group) {
    return Array.from(document.querySelectorAll(`[data-bulk-select-row][data-bulk-group="${CSS.escape(group)}"]`));
  }

  function syncGroup(group) {
    if (!group) return;
    const rows = groupRows(group);
    const enabled = rows.filter((cb) => !cb.disabled);
    const checked = enabled.filter((cb) => cb.checked);

    for (const cb of rows) {
      const container = cb.closest('tr, .driver-package-card');
      if (container) container.classList.toggle('is-selected', !cb.disabled && cb.checked);
    }

    const selectAll = document.querySelector(`[data-bulk-select-all="${CSS.escape(group)}"]`);
    if (selectAll) {
      selectAll.disabled = enabled.length === 0;
      selectAll.checked = enabled.length > 0 && checked.length === enabled.length;
      selectAll.indeterminate = checked.length > 0 && checked.length < enabled.length;
    }

    const bar = document.querySelector(`[data-bulk-bar="${CSS.escape(group)}"]`);
    if (!bar) return;
    if (checked.length === 0) {
      bar.hidden = true;
      return;
    }

    const countEl = bar.querySelector(`[data-bulk-count="${CSS.escape(group)}"]`);
    if (countEl) {
      const label = countEl.dataset.label || 'Выбрано';
      countEl.textContent = `${label}: ${checked.length}`;
    }

    const countBtns = bar.querySelectorAll('[data-bulk-count-btn]');
    for (const btn of countBtns) {
      const base = btn.dataset.label || btn.textContent.replace(/\s*\(\d+\)$/, '').trim();
      btn.textContent = `${base} (${checked.length})`;
    }

    bar.hidden = false;
  }

  function syncAllGroups() {
    const seen = new Set();
    document.querySelectorAll('[data-bulk-bar]').forEach((bar) => {
      const g = bar.dataset.bulkBar;
      if (g && !seen.has(g)) {
        seen.add(g);
        syncGroup(g);
      }
    });
  }

  function openBulkDialog(dialogId, group) {
    const dialog = document.getElementById(dialogId);
    if (!dialog) return;
    const checked = groupRows(group).filter((cb) => !cb.disabled && cb.checked);
    if (checked.length === 0) return;

    const titleEl = dialog.querySelector('[data-bulk-dlg-title]');
    const summaryEl = dialog.querySelector('[data-bulk-dlg-summary]');
    const listEl = dialog.querySelector('[data-bulk-dlg-list]');
    const inputsEl = dialog.querySelector('[data-bulk-dlg-inputs]');
    const confirmBtn = dialog.querySelector('[data-bulk-dlg-confirm]');
    const form = dialog.querySelector('form');
    const inputName = dialog.dataset.inputName || 'item';
    const skipPublished = dialog.dataset.skipPublished === '1';

    let activeItems = checked;
    let skippedPublished = [];
    if (skipPublished) {
      skippedPublished = checked.filter((cb) => cb.dataset.bulkPublished === '1');
      activeItems = checked.filter((cb) => cb.dataset.bulkPublished !== '1');
    }

    if (titleEl) {
      titleEl.textContent = formatTemplate(dialog.dataset.title, checked.length);
    }

    if (summaryEl) {
      summaryEl.textContent = '';
      const box = document.createElement('p');
      if (skipPublished && skippedPublished.length > 0) {
        box.className = activeItems.length === 0 ? 'bulk-dlg-warn' : 'bulk-dlg-warn-accent';
        if (activeItems.length === 0) {
          box.textContent = formatTemplate(dialog.dataset.summaryAllPublished, skippedPublished.length);
        } else {
          box.textContent = formatTemplate(dialog.dataset.summaryMixedPublished, skippedPublished.length, checked.length);
        }
        summaryEl.appendChild(box);
      } else if (dialog.dataset.summary) {
        box.className = dialog.dataset.summaryKind === 'info' ? 'bulk-dlg-info' : 'bulk-dlg-warn';
        box.textContent = formatTemplate(dialog.dataset.summary, activeItems.length);
        summaryEl.appendChild(box);
      }
    }

    if (listEl) {
      listEl.textContent = '';
      const tagSkipped = dialog.dataset.tagSkipped || 'пропущена · в меню';
      const tagDelete = dialog.dataset.tagDelete || '';
      const tagDisk = dialog.dataset.tagDisk || 'с диска';
      for (const cb of checked) {
        const li = document.createElement('li');
        const labelSpan = document.createElement('span');
        labelSpan.className = 'bulk-dlg-path';
        if (cb.dataset.bulkMono === '1') labelSpan.classList.add('mono');
        labelSpan.textContent = cb.dataset.bulkLabel || cb.dataset.bulkValue || '';
        li.appendChild(labelSpan);

        let tagText = cb.dataset.bulkTag || '';
        let tagClass = 'bulk-dlg-tag bulk-dlg-tag-absent';
        if (skipPublished && cb.dataset.bulkPublished === '1') {
          tagText = tagSkipped;
          tagClass = 'bulk-dlg-tag bulk-dlg-tag-warn';
        } else if (cb.dataset.bulkDiskOnly === '1') {
          tagText = tagDisk;
          tagClass = 'bulk-dlg-tag bulk-dlg-tag-disk';
        } else if (skipPublished && tagDelete) {
          tagText = tagDelete;
        }
        if (tagText) {
          const tagSpan = document.createElement('span');
          tagSpan.className = tagClass;
          tagSpan.textContent = tagText;
          li.appendChild(tagSpan);
        }
        listEl.appendChild(li);
      }
    }

    if (inputsEl) {
      inputsEl.textContent = '';
      for (const cb of activeItems) {
        const input = document.createElement('input');
        input.type = 'hidden';
        input.name = inputName;
        input.value = cb.dataset.bulkValue || '';
        inputsEl.appendChild(input);
      }
    }

    const deleteFileWrap = dialog.querySelector('[data-bulk-delete-file-wrap]');
    if (deleteFileWrap) {
      const hasManagedPresent = activeItems.some((cb) => cb.dataset.bulkDiskOnly !== '1' && cb.dataset.bulkPresent === '1');
      deleteFileWrap.hidden = !hasManagedPresent;
      const dfCb = deleteFileWrap.querySelector('input[type="checkbox"]');
      if (dfCb) dfCb.checked = false;
    }

    if (confirmBtn) {
      confirmBtn.disabled = activeItems.length === 0;
      if (dialog.dataset.btn) {
        confirmBtn.textContent = formatTemplate(dialog.dataset.btn, activeItems.length);
      }
    }

    if (form) {
      const ret = form.querySelector('input[name="return_to"]');
      if (ret) ret.value = currentReturnTo();
    }

    dialog.showModal();
  }

  function submitBulkForm(btn) {
    const group = btn.dataset.bulkSubmit;
    const form = document.getElementById(btn.dataset.bulkForm);
    if (!group || !form) return;
    const checked = groupRows(group).filter((cb) => !cb.disabled && cb.checked);
    if (checked.length === 0) return;

    const inputsEl = form.querySelector('[data-bulk-form-inputs]');
    if (!inputsEl) return;
    inputsEl.textContent = '';
    const inputName = btn.dataset.bulkInputName || 'item';
    for (const cb of checked) {
      const input = document.createElement('input');
      input.type = 'hidden';
      input.name = inputName;
      input.value = cb.dataset.bulkValue || '';
      inputsEl.appendChild(input);
    }
    const actionInput = form.querySelector('input[name="action"]');
    if (actionInput && btn.dataset.bulkActionVal) {
      actionInput.value = btn.dataset.bulkActionVal;
    }
    const ret = form.querySelector('input[name="return_to"]');
    if (ret) ret.value = currentReturnTo();
    form.submit();
  }

  document.addEventListener('click', (event) => {
    const closeBtn = event.target.closest('[data-bulk-dlg-close]');
    if (closeBtn) {
      const dlg = closeBtn.closest('dialog');
      if (dlg) dlg.close();
      return;
    }

    const rowCb = event.target.closest('[data-bulk-select-row][data-bulk-group]');
    if (rowCb && !rowCb.disabled) {
      const group = rowCb.dataset.bulkGroup;
      const all = groupRows(group);
      const idx = all.indexOf(rowCb);
      const lastIdx = lastClickedByGroup[group];
      if (event.shiftKey && typeof lastIdx === 'number' && idx >= 0 && lastIdx < all.length) {
        const start = Math.min(lastIdx, idx);
        const end = Math.max(lastIdx, idx);
        for (let i = start; i <= end; i++) {
          if (!all[i].disabled) all[i].checked = rowCb.checked;
        }
      }
      lastClickedByGroup[group] = idx;
      syncGroup(group);
      return;
    }

    const clearBtn = event.target.closest('[data-bulk-clear]');
    if (clearBtn) {
      const group = clearBtn.dataset.bulkClear;
      for (const cb of groupRows(group)) cb.checked = false;
      lastClickedByGroup[group] = null;
      syncGroup(group);
      return;
    }

    const openBtn = event.target.closest('[data-bulk-dialog-open]');
    if (openBtn) {
      openBulkDialog(openBtn.dataset.bulkDialogOpen, openBtn.dataset.bulkGroup);
      return;
    }

    const submitBtn = event.target.closest('[data-bulk-submit]');
    if (submitBtn) {
      submitBulkForm(submitBtn);
    }
  });

  document.addEventListener('change', (event) => {
    const selectAll = event.target.closest('[data-bulk-select-all]');
    if (selectAll) {
      const group = selectAll.dataset.bulkSelectAll;
      for (const cb of groupRows(group)) {
        if (!cb.disabled) cb.checked = selectAll.checked;
      }
      lastClickedByGroup[group] = null;
      syncGroup(group);
      return;
    }
    const rowCb = event.target.closest('[data-bulk-select-row][data-bulk-group]');
    if (rowCb) {
      syncGroup(rowCb.dataset.bulkGroup);
    }
  });

  document.addEventListener('omb:bulk-sync', syncAllGroups);
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', syncAllGroups);
  } else {
    syncAllGroups();
  }
})();
