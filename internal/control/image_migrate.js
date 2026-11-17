(() => {
  const english = {
    "Образ перемещается в другое хранилище": "Image is being moved to another storage",
    "Перемещается...": "Moving…",
    "Не удалось запустить перемещение образа": "Could not start moving image",
    "Ошибка сети при запуске перемещения": "Network error while starting migration"
  };
  const tr = (text) => {
    return document.documentElement.lang === 'en' ? (english[text] || text) : text;
  };

  document.addEventListener('click', (event) => {
    const button = event.target.closest('[data-image-migrate-open]');
    if (button) {
      if (button.disabled) return;
      const dialog = document.getElementById(button.dataset.imageMigrateOpen);
      if (!dialog) return;
      const menu = button.closest('[popover]');
      if (menu && menu.matches(':popover-open')) menu.hidePopover();
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

  document.addEventListener('submit', async (event) => {
    const form = event.target.closest('[data-image-migrate-form]');
    if (!form) return;
    event.preventDefault();
    const dialog = form.closest('dialog');
    const targetSelect = form.querySelector('[name="target_storage_id"]');
    if (!targetSelect || !targetSelect.value) return;

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
        if (submitBtn) submitBtn.disabled = false;
        return;
      }

      if (dialog) dialog.close();

      const cell = Array.from(document.querySelectorAll('.path-cell')).find(c =>
        c.dataset.storageId === String(sourceStorageID) && c.dataset.path === path
      );
      const row = cell ? cell.closest('tr') : null;
      if (row) {
        row.classList.add('is-migrating');
        const badge = row.querySelector('.badge-migrating');
        if (badge) badge.hidden = false;
        const progress = row.querySelector('.progress-bar-micro');
        if (progress) progress.hidden = false;
        const menuBtn = row.querySelector('.row-menu-btn');
        if (menuBtn) {
          menuBtn.disabled = true;
          menuBtn.title = tr('Образ перемещается в другое хранилище');
        }
      }

      startPolling();
    } catch (err) {
      alert(tr('Ошибка сети при запуске перемещения'));
      if (submitBtn) submitBtn.disabled = false;
    }
  });

  let pollTimer = null;

  function startPolling() {
    if (pollTimer) return;
    poll();
    pollTimer = setInterval(poll, 1500);
  }

  async function poll() {
    try {
      const resp = await fetch('/api/images/migrations', {
        credentials: 'same-origin',
        headers: { 'Accept': 'application/json' },
      });
      if (!resp.ok) return;
      const jobs = await resp.json();
      if (!Array.isArray(jobs)) return;

      let activeCount = 0;
      jobs.forEach(job => {
        const cell = Array.from(document.querySelectorAll('.path-cell')).find(c =>
          c.dataset.storageId === String(job.source_storage_id) && c.dataset.path === job.path
        );
        const row = cell ? cell.closest('tr') : null;
        if (!row) return;

        if (job.state === 'copying' || job.state === 'ready_to_rebind' || job.state === 'rebinding') {
          activeCount++;
          row.classList.add('is-migrating');
          const badge = row.querySelector('.badge-migrating');
          if (badge) badge.hidden = false;
          const pctEl = row.querySelector('.migrating-pct');
          if (pctEl) pctEl.textContent = `${job.percent || 0}%`;
          const bar = row.querySelector('.progress-bar-fill');
          if (bar) bar.style.width = `${job.percent || 0}%`;
          const menuBtn = row.querySelector('.row-menu-btn');
          if (menuBtn) menuBtn.disabled = true;
        } else if (job.state === 'completed') {
          window.location.reload();
        } else if (job.state === 'failed' || job.state === 'canceled') {
          if (job.error) alert(job.error);
          window.location.reload();
        }
      });

      if (activeCount === 0 && document.querySelectorAll('.is-migrating').length === 0) {
        clearInterval(pollTimer);
        pollTimer = null;
      }
    } catch (e) {
      // Ignore transient network errors during poll
    }
  }

  if (document.querySelector('.is-migrating')) {
    startPolling();
  }
})();
