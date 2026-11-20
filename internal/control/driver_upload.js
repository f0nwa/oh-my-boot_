(() => {
  const english = {
    "Выберите пакет драйвера.": "Choose a driver package.",
    "Не удалось загрузить пакет.": "Could not upload the package.",
    "Соединение прервано.": "Connection lost.",
    "Загрузка отменена.": "Upload cancelled.",
    "Передаю пакет…": "Uploading package…",
    "Передано %s%. Сервер проверит состав пакета.": "Transferred %s%. The server will check the package contents."
  };
  const tr = (text, ...args) => {
    let result = document.documentElement.lang === 'en' ? (english[text] || text) : text;
    for (const arg of args) result = result.replace('%s', arg);
    return result;
  };
  const forms = document.querySelectorAll('[data-driver-upload]');
  const resetUploadForms = () => {
    forms.forEach(form => {
      if (!form.dataset.uploading) {
        form.reset();
        const submit = form.querySelector('[type="submit"]');
        if (submit) submit.disabled = true;
        const status = form.querySelector('[role="status"]');
        if (status) status.textContent = '';
      }
    });
  };

  document.addEventListener('click', event => {
    const uploadButton = event.target.closest('[data-driver-upload-open]');
    const packageButton = event.target.closest('[data-driver-package-open]');
    const configButton = event.target.closest('[data-driver-config-open]');
    const deleteButton = event.target.closest('[data-driver-delete-open]');
    const packageDeleteButton = event.target.closest('[data-driver-package-delete-open]');
    if ((deleteButton && deleteButton.disabled) || (packageDeleteButton && packageDeleteButton.disabled)) return;
    if (deleteButton) {
      const parentDialog = deleteButton.closest('dialog');
      if (parentDialog && parentDialog.open) parentDialog.close();
    }
    const dialogID = uploadButton ? 'driver-upload-dialog'
      : packageButton ? 'driver-package-dialog-' + packageButton.dataset.driverPackageOpen
      : configButton ? 'driver-config-dialog-' + configButton.dataset.driverConfigOpen
      : packageDeleteButton ? 'driver-package-delete-dialog-' + packageDeleteButton.dataset.driverPackageDeleteOpen
      : deleteButton ? 'driver-delete-dialog-' + deleteButton.dataset.driverDeleteOpen
      : null;
    if (dialogID) {
      const dialog = document.getElementById(dialogID);
      if (dialog && typeof dialog.showModal === 'function') {
        if (dialogID === 'driver-upload-dialog') {
          resetUploadForms();
        }
        const cfgForm = dialog.querySelector('[data-driver-config-form]');
        if (cfgForm) {
          const titleInput = cfgForm.querySelector('input[name="title"]');
          const submitBtn = cfgForm.querySelector('button[type="submit"]');
          if (titleInput) titleInput.value = titleInput.defaultValue;
          if (submitBtn) submitBtn.disabled = true;
        }
        dialog.showModal();
        if (cfgForm) {
          const titleInput = cfgForm.querySelector('input[name="title"]');
          if (titleInput) titleInput.focus();
        }
      }
      return;
    }
    const closeButton = event.target.closest('[data-close-dialog]');
    if (closeButton) {
      const dialog = closeButton.closest('dialog');
      if (dialog) dialog.close();
      return;
    }
    if (event.target.tagName === 'DIALOG') {
      const rect = event.target.getBoundingClientRect();
      if (event.clientX < rect.left || event.clientX > rect.right ||
          event.clientY < rect.top || event.clientY > rect.bottom) event.target.close();
    }
  });
  document.querySelectorAll('[data-driver-config-form]').forEach(form => {
    const titleInput = form.querySelector('input[name="title"]');
    const submitBtn = form.querySelector('button[type="submit"]');
    if (!titleInput || !submitBtn) return;
    const sync = () => {
      const current = titleInput.value.trim();
      const initial = titleInput.defaultValue.trim();
      submitBtn.disabled = !current || current === initial || !form.checkValidity();
    };
    titleInput.addEventListener('input', sync);
    form.addEventListener('submit', event => {
      sync();
      if (submitBtn.disabled) event.preventDefault();
    });
    sync();
  });
  forms.forEach(form => {
    const status = form.querySelector('[role="status"]');
    const progress = form.querySelector('progress');
    const submit = form.querySelector('[type="submit"]');
    const cancel = form.querySelector('[data-cancel]');
    const fileInput = form.querySelector('input[type="file"]');
    let request = null;

    const syncSubmit = () => {
      if (request) return;
      const files = Array.from(fileInput && fileInput.files ? fileInput.files : []);
      if (submit) submit.disabled = files.length === 0;
    };

    if (fileInput) {
      fileInput.addEventListener('change', syncSubmit);
      fileInput.addEventListener('input', syncSubmit);
    }
    syncSubmit();

    // Cancel and the progress bar only make sense while a transfer runs.
    const busy = on => {
      if (on) {
        form.dataset.uploading = 'true';
        if (submit) submit.disabled = true;
        if (cancel) cancel.hidden = false;
        if (progress) progress.parentElement.hidden = false;
        if (fileInput) fileInput.disabled = true;
      } else {
        delete form.dataset.uploading;
        if (cancel) cancel.hidden = true;
        if (progress) progress.parentElement.hidden = true;
        if (fileInput) fileInput.disabled = false;
        syncSubmit();
      }
    };
    if (cancel) cancel.addEventListener('click', () => { if (request) request.abort(); });
    form.addEventListener('submit', event => {
      event.preventDefault();
      syncSubmit();
      if ((submit && submit.disabled) || request) return;
      const files = Array.from(fileInput && fileInput.files ? fileInput.files : []);
      if (!files.length) { if (status) status.textContent = tr('Выберите пакет драйвера.'); return; }
      const data = new FormData();
      data.append('csrf', form.dataset.csrf);
      if (form.dataset.driverUpload === 'folder') {
        files.forEach(file => {
          data.append('files[]', file, file.name);
          data.append('paths[]', file.webkitRelativePath || file.name);
        });
      } else data.append('zip', files[0]);
      request = new XMLHttpRequest();
      request.open('POST', '/drivers/import');
      request.upload.onprogress = ev => {
        if (ev.lengthComputable) progress.value = Math.round(ev.loaded * 100 / ev.total);
        status.textContent = tr('Передано %s%. Сервер проверит состав пакета.', progress.value);
      };
      request.onload = () => {
        if (request.status === 303 || (request.status === 200 && request.responseURL.includes('/drivers?'))) {
          // Follow the server's result page: it says whether the package is new or already in the catalog.
          const target = new URL(request.responseURL || '/drivers', window.location.href);
          if (target.origin === window.location.origin && target.pathname === '/drivers') window.location.assign(target.pathname + target.search);
          else window.location.assign('/drivers?message=' + encodeURIComponent('Пакет драйвера добавлен'));
        } else status.textContent = request.responseText.trim() || tr('Не удалось загрузить пакет.');
        busy(false);
        request = null;
      };
      request.onerror = () => { status.textContent = tr('Соединение прервано.'); busy(false); request = null; };
      request.onabort = () => { status.textContent = tr('Загрузка отменена.'); busy(false); request = null; };
      busy(true);
      progress.value = 0;
      status.textContent = tr('Передаю пакет…');
      request.send(data);
    });
  });

  const searchInput = document.getElementById('driver-q');
  if (searchInput) {
    searchInput.addEventListener('input', () => {
      const q = searchInput.value.trim().toLowerCase();
      const rows = document.querySelectorAll('.driver-config-table tr[id^="driver-config-"]');
      rows.forEach(row => {
        const text = (row.textContent || '').toLowerCase();
        row.style.display = (!q || text.includes(q)) ? '' : 'none';
      });
    });
  }
})();
