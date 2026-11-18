(() => {
  const english = {
    "Образ перемещается в другое хранилище": "Image is being moved to another storage",
    "Перемещается…": "Moving…",
    "Переключение…": "Switching…",
    "Не удалось запустить перемещение образа": "Could not start moving image",
    "Ошибка сети при запуске перемещения": "Network error while starting migration"
  };
  const tr = (text) => {
    return document.documentElement.lang === 'en' ? (english[text] || text) : text;
  };

  function findRow(storageID, path) {
    const cell = Array.from(document.querySelectorAll('.path-cell')).find(c =>
      c.dataset.storageId === String(storageID) && c.dataset.path === path
    );
    return cell ? cell.closest('tr') : null;
  }

  function setRowMigrating(row, percent, state, migrationID) {
    if (!row) return;
    if (migrationID) row.dataset.migrationId = String(migrationID);
    row.classList.add('is-migrating');
    const libStatus = row.querySelector('.image-library-status');
    if (libStatus) libStatus.hidden = true;
    const migStatus = row.querySelector('.image-migrating-status');
    if (migStatus) migStatus.hidden = false;
    const labelEl = row.querySelector('.migrating-label');
    if (labelEl) {
      const isSwitching = state === 'ready_to_rebind' || state === 'rebinding';
      labelEl.textContent = tr(isSwitching ? 'Переключение…' : 'Перемещается…');
    }
    const pct = Number(percent) || 0;
    const pctEl = row.querySelector('.migrating-pct');
    if (pctEl) pctEl.textContent = `${pct}%`;
    const bar = row.querySelector('.progress-bar-fill');
    if (bar) bar.style.width = `${pct}%`;
    const menuBtn = row.querySelector('.row-menu-btn');
    if (menuBtn) {
      menuBtn.disabled = true;
      menuBtn.title = tr('Образ перемещается в другое хранилище');
    }
  }

  function updateMigrateSubmit(form) {
    if (!form) return;
    const select = form.querySelector('[name="target_storage_id"]');
    const submitBtn = form.querySelector('button[type="submit"]');
    if (!select || !submitBtn) return;
    submitBtn.disabled = !select.value;
  }

  document.addEventListener('click', (event) => {
    const button = event.target.closest('[data-image-migrate-open]');
    if (button) {
      if (button.disabled) return;
      const dialog = document.getElementById(button.dataset.imageMigrateOpen);
      if (!dialog) return;
      const menu = button.closest('[popover]');
      if (menu && menu.matches(':popover-open')) menu.hidePopover();
      const form = dialog.querySelector('form');
      if (form) {
        form.reset();
        updateMigrateSubmit(form);
      }
      dialog.showModal();
      return;
    }
    const close = event.target.closest('[data-image-migrate-close]');
    if (close) {
      const dialog = close.closest('dialog');
      if (dialog) dialog.close();
      return;
    }
  });

  document.addEventListener('change', (event) => {
    const form = event.target.closest('[data-image-migrate-form]');
    if (form && event.target && event.target.name === 'target_storage_id') {
      updateMigrateSubmit(form);
    }
  });

  document.addEventListener('input', (event) => {
    const form = event.target.closest('[data-image-migrate-form]');
    if (form && event.target && event.target.name === 'target_storage_id') {
      updateMigrateSubmit(form);
    }
  });

  document.addEventListener('submit', async (event) => {
    const form = event.target.closest('[data-image-migrate-form]');
    if (!form) return;
    event.preventDefault();
    const dialog = form.closest('dialog');
    const targetSelect = form.querySelector('[name="target_storage_id"]');
    if (!targetSelect || !targetSelect.value) {
      updateMigrateSubmit(form);
      return;
    }

    const sourceStorageID = form.querySelector('[name="source_storage_id"]').value;
    const path = form.querySelector('[name="path"]').value;
    const csrf = form.querySelector('[name="csrf"]')?.value || '';

    const submitBtn = form.querySelector('button[type="submit"]');
    if (submitBtn) submitBtn.disabled = true;

    try {
      const resp = await fetch('/images/migrate', {
        method: 'POST',
        body: new URLSearchParams(new FormData(form)),
        credentials: 'same-origin',
        headers: {
          'Accept': 'application/json',
          'X-Requested-With': 'XMLHttpRequest',
          'X-CSRF-Token': csrf,
        },
      });

      if (!resp.ok) {
        let errorMsg = '';
        try {
          const contentType = resp.headers.get('content-type') || '';
          if (contentType.includes('application/json')) {
            const data = await resp.json();
            errorMsg = data.error;
          } else {
            errorMsg = (await resp.text()).trim();
          }
        } catch (_) {}
        alert(errorMsg || tr('Не удалось запустить перемещение образа'));
        if (submitBtn) updateMigrateSubmit(form);
        return;
      }

      if (dialog) dialog.close();

      let initialJob = null;
      try {
        initialJob = await resp.json();
      } catch (_) {}

      const row = findRow(sourceStorageID, path);
      setRowMigrating(row, initialJob?.percent || 0, initialJob?.state || 'copying', initialJob?.id);

      startPolling();
    } catch (err) {
      alert(tr('Ошибка сети при запуске перемещения'));
      if (submitBtn) updateMigrateSubmit(form);
    }
  });

  let pollTimer = null;
  let isPolling = false;
  let reloading = false;

  function stopPolling() {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  function scheduleNextPoll() {
    if (reloading || pollTimer) return;
    pollTimer = setTimeout(() => {
      pollTimer = null;
      poll();
    }, 1500);
  }

  function startPolling() {
    if (reloading) return;
    stopPolling();
    if (!isPolling) poll();
  }

  async function poll() {
    if (reloading || isPolling) return;
    isPolling = true;
    let activeCount = 0;
    try {
      const resp = await fetch('/api/images/migrations', {
        credentials: 'same-origin',
        headers: { 'Accept': 'application/json' },
      });
      if (!resp.ok) return;
      const jobs = await resp.json();
      if (!Array.isArray(jobs)) return;

      for (const job of jobs) {
        const row = findRow(job.source_storage_id, job.path);
        if (!row) continue;

        if (job.state === 'copying' || job.state === 'ready_to_rebind' || job.state === 'rebinding') {
          activeCount++;
          setRowMigrating(row, job.percent, job.state, job.id);
        } else if (row.dataset.migrationId && row.dataset.migrationId === String(job.id)) {
          if (job.state === 'completed') {
            reloading = true;
            stopPolling();
            window.location.reload();
            return;
          }
          if (job.state === 'failed' || job.state === 'canceled') {
            reloading = true;
            stopPolling();
            if (job.error) alert(job.error);
            window.location.reload();
            return;
          }
        }
      }
    } catch (e) {
      // Ignore transient network errors during poll
    } finally {
      isPolling = false;
      if (!reloading && (activeCount > 0 || document.querySelectorAll('.is-migrating').length > 0)) {
        scheduleNextPoll();
      }
    }
  }

  if (document.querySelector('.is-migrating')) {
    startPolling();
  }
})();
