(() => {
  const english = {
    "Выберите пакет драйвера.": "Choose a driver package.",
    "Не удалось загрузить пакет.": "Could not upload the package.",
    "Соединение прервано.": "Connection lost.",
    "Загрузка отменена.": "Upload cancelled.",
    "Передаю пакет…": "Uploading package…",
    "Передано %s%. Сервер проверит состав пакета.": "Transferred %s%. The server will check the package contents.",
    "Файл передан — сервер проверяет состав пакета…": "File transferred — the server is verifying the package contents…",
    "Файл передан · сервер проверяет пакет…": "File transferred · server verifying package…",
    "%s из %s · передача": "%s of %s · uploading",
    "Пакет добавлен · список обновлён": "Package added · list updated",
    "Пакет драйвера": "Driver package"
  };
  const tr = (text, ...args) => {
    let result = document.documentElement.lang === 'en' ? (english[text] || text) : text;
    for (const arg of args) result = result.replace('%s', arg);
    return result;
  };
  const forms = document.querySelectorAll('[data-driver-upload]');
  const uploadDialog = document.getElementById('driver-upload-dialog');
  const minimizeBtn = document.getElementById('driver-upload-minimize');
  const uploadTitleInput = document.getElementById('driver-upload-title');
  const uploadDescInput = document.getElementById('driver-upload-desc');
  let uploadTitleManual = false;
  let activeRequest = null;

  const formatBytes = bytes => {
    if (typeof window !== 'undefined' && window.OMB && typeof window.OMB.formatUploadBytes === 'function') {
      return window.OMB.formatUploadBytes(bytes);
    }
    return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  };

  const openUploadDialog = () => {
    if (!uploadDialog) return;
    resetUploadForms();
    if (!uploadDialog.open && typeof uploadDialog.showModal === 'function') {
      uploadDialog.showModal();
    }
    if (dock) dock.setMinimized(false);
  };

  const dock = (typeof window !== 'undefined' && window.OMB && typeof window.OMB.createUploadDock === 'function')
    ? window.OMB.createUploadDock({
        onExpand: openUploadDialog,
        onCancel: () => { if (activeRequest) activeRequest.abort(); }
      })
    : null;

  if (uploadDialog && typeof uploadDialog.addEventListener === 'function') {
    uploadDialog.addEventListener('click', event => {
      if (event.target !== uploadDialog) return;
      const rect = uploadDialog.getBoundingClientRect();
      const inside = event.clientX >= rect.left && event.clientX <= rect.right &&
        event.clientY >= rect.top && event.clientY <= rect.bottom;
      if (!inside) uploadDialog.close();
    });
    uploadDialog.addEventListener('close', () => {
      if (activeRequest && dock) dock.setMinimized(true);
    });
  }

  if (uploadTitleInput) {
    uploadTitleInput.addEventListener('input', () => {
      uploadTitleManual = uploadTitleInput.value.trim() !== '';
    });
  }
  const suggestTitleFromFiles = (kind, files) => {
    if (!uploadTitleInput || uploadTitleManual || !files.length) return;
    let name = '';
    if (kind === 'folder') {
      const rel = files[0].webkitRelativePath || '';
      const slash = rel.indexOf('/');
      name = slash > 0 ? rel.slice(0, slash) : '';
    } else {
      name = (files[0].name || '').replace(/\.zip$/i, '');
    }
    name = name.replace(/[_]+/g, ' ').trim();
    if (name) {
      uploadTitleInput.value = name.slice(0, 100);
    }
  };
  const resetUploadForms = () => {
    const anyUploading = Boolean(activeRequest) || Array.from(forms).some(f => Boolean(f.dataset.uploading));
    if (!anyUploading) {
      if (uploadTitleInput) uploadTitleInput.value = '';
      if (uploadDescInput) uploadDescInput.value = '';
      uploadTitleManual = false;
    }
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

  const refreshPackagesSection = () => {
    if (typeof window === 'undefined' || !window.fetch || !window.DOMParser) return;
    const current = document.getElementById('driver-packages-section');
    if (!current) return;
    window.fetch(window.location.href, {
      credentials: 'same-origin',
      headers: { Accept: 'text/html' }
    })
      .then(res => (res.ok && !res.redirected ? res.text() : null))
      .then(html => {
        if (!html) return;
        const parsed = new window.DOMParser().parseFromString(html, 'text/html');
        const fresh = parsed.getElementById('driver-packages-section');
        if (!fresh) return;
        if (typeof current.replaceChildren === 'function') {
          current.replaceChildren(...Array.from(fresh.childNodes));
        } else {
          current.innerHTML = fresh.innerHTML;
        }
        bindPackageForms(current);
        document.dispatchEvent(new Event('omb:bulk-sync'));
      })
      .catch(() => {});
  };

  document.addEventListener('click', event => {
    const uploadButton = event.target.closest('[data-driver-upload-open]');
    const packageButton = event.target.closest('[data-driver-package-open]');
    const configButton = event.target.closest('[data-driver-config-open]');
    const deleteButton = event.target.closest('[data-driver-delete-open]');
    const packageDeleteButton = event.target.closest('[data-driver-package-delete-open]');
    if ((deleteButton && deleteButton.disabled) || (packageDeleteButton && packageDeleteButton.disabled)) return;
    if (uploadButton) {
      openUploadDialog();
      return;
    }
    if (deleteButton) {
      const parentDialog = deleteButton.closest('dialog');
      if (parentDialog && parentDialog.open) parentDialog.close();
    }
    const dialogID = packageButton ? 'driver-package-dialog-' + packageButton.dataset.driverPackageOpen
      : configButton ? 'driver-config-dialog-' + configButton.dataset.driverConfigOpen
      : packageDeleteButton ? 'driver-package-delete-dialog-' + packageDeleteButton.dataset.driverPackageDeleteOpen
      : deleteButton ? 'driver-delete-dialog-' + deleteButton.dataset.driverDeleteOpen
      : null;
    if (dialogID) {
      const dialog = document.getElementById(dialogID);
      if (dialog && typeof dialog.showModal === 'function') {
        const cfgForm = dialog.querySelector('[data-driver-config-form]');
        if (cfgForm) {
          const titleInput = cfgForm.querySelector('input[name="title"]');
          const submitBtn = cfgForm.querySelector('button[type="submit"]');
          if (titleInput) titleInput.value = titleInput.defaultValue;
          if (submitBtn) submitBtn.disabled = true;
        }
        const pkgForm = dialog.querySelector('[data-driver-package-form]');
        if (pkgForm) {
          const titleInput = pkgForm.querySelector('input[name="title"]');
          const descInput = pkgForm.querySelector('textarea[name="description"]');
          const submitBtn = pkgForm.querySelector('button[type="submit"]');
          if (titleInput) titleInput.value = titleInput.defaultValue;
          if (descInput) descInput.value = descInput.defaultValue;
          if (submitBtn) submitBtn.disabled = true;
        }
        dialog.showModal();
        if (cfgForm) {
          const titleInput = cfgForm.querySelector('input[name="title"]');
          if (titleInput) titleInput.focus();
        } else if (pkgForm) {
          const titleInput = pkgForm.querySelector('input[name="title"]');
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
  const bindPackageForms = root => {
    root.querySelectorAll('[data-driver-package-form]').forEach(form => {
      const titleInput = form.querySelector('input[name="title"]');
      const descInput = form.querySelector('textarea[name="description"]');
      const submitBtn = form.querySelector('button[type="submit"]');
      if (!titleInput || !descInput || !submitBtn) return;
      const normDesc = value => value.replace(/\r\n?/g, '\n').trim();
      const sync = () => {
        const titleChanged = titleInput.value.trim() !== titleInput.defaultValue.trim();
        const descChanged = normDesc(descInput.value) !== normDesc(descInput.defaultValue);
        submitBtn.disabled = (!titleChanged && !descChanged) || !form.checkValidity();
      };
      titleInput.addEventListener('input', sync);
      descInput.addEventListener('input', sync);
      form.addEventListener('submit', event => {
        sync();
        if (submitBtn.disabled) event.preventDefault();
      });
      sync();
    });
  };
  bindPackageForms(document);
  const setOtherFormsDisabled = (currentForm, disabled) => {
    forms.forEach(other => {
      if (other === currentForm) return;
      const otherFile = other.querySelector('input[type="file"]');
      const otherSubmit = other.querySelector('[type="submit"]');
      if (otherFile) otherFile.disabled = disabled;
      if (otherSubmit) {
        if (disabled) otherSubmit.disabled = true;
        else {
          const files = Array.from(otherFile && otherFile.files ? otherFile.files : []);
          otherSubmit.disabled = files.length === 0;
        }
      }
    });
  };
  forms.forEach(form => {
    const status = form.querySelector('[role="status"]');
    const progress = form.querySelector('progress');
    const submit = form.querySelector('[type="submit"]');
    const cancel = form.querySelector('[data-cancel]');
    const fileInput = form.querySelector('input[type="file"]');
    let request = null;

    const syncSubmit = () => {
      if (request || activeRequest) return;
      const files = Array.from(fileInput && fileInput.files ? fileInput.files : []);
      if (submit) submit.disabled = files.length === 0;
    };

    if (fileInput) {
      const onFileChange = () => {
        syncSubmit();
        const files = Array.from(fileInput.files || []);
        suggestTitleFromFiles(form.dataset.driverUpload, files);
      };
      fileInput.addEventListener('change', onFileChange);
      fileInput.addEventListener('input', onFileChange);
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
        if (uploadTitleInput) uploadTitleInput.disabled = true;
        if (uploadDescInput) uploadDescInput.disabled = true;
        if (minimizeBtn) minimizeBtn.hidden = false;
        setOtherFormsDisabled(form, true);
      } else {
        delete form.dataset.uploading;
        if (cancel) cancel.hidden = true;
        if (progress) progress.parentElement.hidden = true;
        if (fileInput) fileInput.disabled = false;
        if (uploadTitleInput) uploadTitleInput.disabled = false;
        if (uploadDescInput) uploadDescInput.disabled = false;
        if (minimizeBtn) minimizeBtn.hidden = true;
        setOtherFormsDisabled(form, false);
        syncSubmit();
      }
    };
    if (cancel) cancel.addEventListener('click', () => { if (request) request.abort(); });
    form.addEventListener('submit', event => {
      event.preventDefault();
      syncSubmit();
      if ((submit && submit.disabled) || request || activeRequest) return;
      const files = Array.from(fileInput && fileInput.files ? fileInput.files : []);
      if (!files.length) { if (status) status.textContent = tr('Выберите пакет драйвера.'); return; }
      const data = new FormData();
      data.append('csrf', form.dataset.csrf);
      const customTitle = uploadTitleInput ? uploadTitleInput.value.trim() : '';
      if (uploadTitleInput) data.append('title', customTitle);
      if (uploadDescInput) data.append('description', uploadDescInput.value.trim());
      let dockLabel = customTitle;
      if (form.dataset.driverUpload === 'folder') {
        files.forEach(file => {
          data.append('files[]', file, file.name);
          data.append('paths[]', file.webkitRelativePath || file.name);
        });
        if (!dockLabel) {
          const rel = files[0].webkitRelativePath || '';
          const slash = rel.indexOf('/');
          dockLabel = slash > 0 ? rel.slice(0, slash) : (files[0].name || tr('Пакет драйвера'));
        }
      } else {
        data.append('zip', files[0]);
        if (!dockLabel) dockLabel = files[0].name || tr('Пакет драйвера');
      }
      const xhr = new XMLHttpRequest();
      request = xhr;
      activeRequest = xhr;
      xhr.open('POST', '/drivers/import');
      xhr.upload.onprogress = ev => {
        if (ev.lengthComputable && ev.total > 0) {
          const pct = Math.min(100, Math.round(ev.loaded * 100 / ev.total));
          progress.value = pct;
          if (pct >= 100) {
            status.textContent = tr('Файл передан — сервер проверяет состав пакета…');
            if (dock) dock.progress(100, tr('Файл передан · сервер проверяет пакет…'));
          } else {
            status.textContent = tr('Передано %s%. Сервер проверит состав пакета.', pct);
            if (dock) dock.progress(pct, tr('%s из %s · передача', formatBytes(ev.loaded), formatBytes(ev.total)));
          }
        }
      };
      xhr.onload = () => {
        const wasOpen = Boolean(uploadDialog && uploadDialog.open);
        const ok = xhr.status === 303 || (xhr.status === 200 && xhr.responseURL.includes('/drivers?'));
        const respURL = xhr.responseURL || '/drivers';
        const errText = xhr.responseText ? xhr.responseText.trim() : '';
        request = null;
        if (activeRequest === xhr) activeRequest = null;
        busy(false);
        if (ok) {
          if (wasOpen) {
            if (dock) dock.dismiss();
            // Follow the server's result page: it says whether the package is new or already in the catalog.
            const target = new URL(respURL, window.location.href);
            if (target.origin === window.location.origin && target.pathname === '/drivers') window.location.assign(target.pathname + target.search);
            else window.location.assign('/drivers?message=' + encodeURIComponent('Пакет драйвера добавлен'));
          } else {
            resetUploadForms();
            if (dock) dock.finish('done', tr('Пакет добавлен · список обновлён'));
            refreshPackagesSection();
          }
        } else {
          const failMsg = errText || tr('Не удалось загрузить пакет.');
          status.textContent = failMsg;
          if (dock) {
            if (wasOpen) dock.dismiss();
            else dock.finish('error', failMsg);
          }
        }
      };
      xhr.onerror = () => {
        const wasOpen = Boolean(uploadDialog && uploadDialog.open);
        request = null;
        if (activeRequest === xhr) activeRequest = null;
        busy(false);
        status.textContent = tr('Соединение прервано.');
        if (dock) {
          if (wasOpen) dock.dismiss();
          else dock.finish('error', tr('Соединение прервано.'));
        }
      };
      xhr.onabort = () => {
        request = null;
        if (activeRequest === xhr) activeRequest = null;
        busy(false);
        status.textContent = tr('Загрузка отменена.');
        if (dock) dock.finish('cancelled');
      };
      busy(true);
      progress.value = 0;
      status.textContent = tr('Передаю пакет…');
      if (dock) dock.start(dockLabel, tr('Передаю пакет…'));
      xhr.send(data);
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
