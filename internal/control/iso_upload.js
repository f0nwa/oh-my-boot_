(() => {
  const english = {
    "Читаю папки…": "Reading folders…",
    "Не удалось прочитать папки хранилища": "Could not read storage folders",
    "(корневая папка)": "(root folder)",
    "Выберите ISO размером до 32 ГБ.": "Choose an ISO up to 32 GB.",
    "Выберите файл .wim размером до 32 ГБ.": "Choose a .wim file up to 32 GB.",
    "Выберите файл .wim или .esd размером до 32 ГБ.": "Choose a .wim or .esd file up to 32 GB.",
    "ISO загружен и добавлен в каталог.": "ISO uploaded and added to the catalog.",
    "Файл загружен и добавлен в каталог.": "File uploaded and added to the catalog.",
    "Не удалось загрузить ISO.": "Could not upload the ISO.",
    "Не удалось загрузить файл.": "Could not upload the file.",
    "Соединение прервано.": "Connection lost.",
    "Загрузка отменена.": "Upload cancelled.",
    "Начинаю загрузку…": "Starting upload…",
    "Не хватит места: в хранилище свободно %s ГБ.": "Not enough space: the storage has %s GB free.",
    "Передано %s% — после передачи сервер проверит файл.": "Transferred %s% — the server will check the file after the transfer.",
    "Файл передан — сервер проверяет и добавляет в каталог…": "File transferred — the server is verifying and adding it to the catalog…",
    "Файл передан · сервер проверяет…": "File transferred · server verifying…",
    "%s из %s · передача": "%s of %s · uploading",
    "Добавлен в каталог · список обновлён": "Added to catalog · list updated",
    "Закрыть": "Close",
    "Свернуть": "Minimize"
  };
  const tr = (text, ...args) => {
    let result = document.documentElement.lang === 'en' ? (english[text] || text) : text;
    for (const arg of args) result = result.replace('%s', arg);
    return result;
  };
  const form = document.getElementById('iso-upload-form');
  if (!form) return;
  const dialog = document.getElementById('iso-upload-dialog');
  const open = document.getElementById('iso-upload-open');
  const close = document.getElementById('iso-upload-close');
  const storage = form.querySelector('[name="storage"]');
  const folder = form.querySelector('[name="folder"]');
  const file = form.querySelector('[name="file"]');
  const status = document.getElementById('iso-upload-status');
  const progress = document.getElementById('iso-upload-progress');
  const submit = form.querySelector('[type="submit"]');
  const cancel = document.getElementById('iso-upload-cancel');
  const dropzone = form.querySelector('.dropzone');
  const dropzoneTitle = dropzone ? dropzone.querySelector('strong') : null;
  const initialDropzoneTitle = dropzoneTitle ? dropzoneTitle.textContent : '';
  let request = null;
  let foldersRequest = null;
  let foldersReady = false;
  const folderField = document.getElementById('iso-folder-field');

  const formatBytes = bytes => {
    if (typeof window !== 'undefined' && window.OMB && typeof window.OMB.formatUploadBytes === 'function') {
      return window.OMB.formatUploadBytes(bytes);
    }
    return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
  };

  const openDialog = () => {
    if (!dialog.open) dialog.showModal();
    if (dock) dock.setMinimized(false);
  };

  const dock = (typeof window !== 'undefined' && window.OMB && typeof window.OMB.createUploadDock === 'function')
    ? window.OMB.createUploadDock({
        onExpand: openDialog,
        onCancel: () => { if (request) request.abort(); }
      })
    : null;

  open.addEventListener('click', openDialog);
  close.addEventListener('click', () => dialog.close());
  dialog.addEventListener('click', event => {
    if (event.target !== dialog) return;
    const rect = dialog.getBoundingClientRect();
    const inside = event.clientX >= rect.left && event.clientX <= rect.right &&
      event.clientY >= rect.top && event.clientY <= rect.bottom;
    if (!inside) dialog.close();
  });
  dialog.addEventListener('close', () => {
    if (request && dock) dock.setMinimized(true);
  });

  // Cancel and the progress bar only make sense while a transfer runs.
  // Keep the close button enabled so the user can minimize the dialog without aborting.
  const busy = on => {
    close.disabled = false;
    close.textContent = on ? tr('Свернуть') : tr('Закрыть');
    submit.disabled = on || !foldersReady;
    storage.disabled = on;
    folder.disabled = on;
    file.disabled = on;
    cancel.hidden = !on;
    progress.parentElement.hidden = !on;
  };

  const resetDropzone = () => {
    file.value = '';
    if (dropzoneTitle) dropzoneTitle.textContent = initialDropzoneTitle;
    if (dropzone) dropzone.classList.remove('has-file');
  };

  async function loadFolders() {
    if (foldersRequest) foldersRequest.abort();
    const controller = new AbortController();
    foldersRequest = controller;
    foldersReady = false;
    submit.disabled = true;
    folderField.hidden = true;
    folder.replaceChildren();
    status.textContent = tr('Читаю папки…');
    const selectedOpt = storage.selectedOptions[0];
    if (!storage.value || (selectedOpt && selectedOpt.disabled)) { status.textContent = ''; return; }
    try {
      const response = await fetch(`/images/storages/${storage.value}/folders`, {credentials: 'same-origin', signal: controller.signal});
      if (!response.ok) throw new Error(tr('Не удалось прочитать папки хранилища'));
      const folders = await response.json();
      if (foldersRequest !== controller) return;
      if (!Array.isArray(folders) || !folders.length) throw new Error(tr('Не удалось прочитать папки хранилища'));
      folders.forEach(path => {
        const option = document.createElement('option');
        option.value = path;
        option.textContent = path || tr('(корневая папка)');
        folder.append(option);
      });
      folderField.hidden = folders.length < 2;
      foldersReady = true;
      if (!request) submit.disabled = false;
      status.textContent = '';
    } catch (error) { if (foldersRequest === controller && error.name !== 'AbortError') status.textContent = error.message; }
  }

  storage.addEventListener('change', loadFolders);
  cancel.addEventListener('click', () => { if (request) request.abort(); });
  form.addEventListener('submit', event => {
    event.preventDefault();
    const iso = file.files[0];
    const selectedOpt = storage.selectedOptions[0];
    if (!iso || !storage.value || (selectedOpt && selectedOpt.disabled) || !foldersReady || !folder.options.length || request) return;
    const expectedKind = form.dataset.expectedKind || 'iso';
    const tab = form.dataset.tab || 'iso';
    const maxBytes = 32 * 1024 ** 3;
    let extOK = /\.iso$/i.test(iso.name);
    let invalidMsg = 'Выберите ISO размером до 32 ГБ.';
    if (expectedKind === 'wim') {
      extOK = /\.wim$/i.test(iso.name);
      invalidMsg = 'Выберите файл .wim размером до 32 ГБ.';
    } else if (expectedKind === 'wim_or_esd') {
      extOK = /\.(wim|esd)$/i.test(iso.name);
      invalidMsg = 'Выберите файл .wim или .esd размером до 32 ГБ.';
    }
    if (!extOK || iso.size <= 0 || iso.size > maxBytes) {
      status.textContent = tr(invalidMsg);
      return;
    }
    // The server checks again; this saves a long transfer that would fail.
    const free = Number(storage.selectedOptions[0]?.dataset.free || 0);
    if (free > 0 && iso.size > free) {
      status.textContent = tr('Не хватит места: в хранилище свободно %s ГБ.', (free / 1024 ** 3).toFixed(1));
      return;
    }
    const xhr = new XMLHttpRequest();
    request = xhr;
    const url = `/images/upload/${storage.value}?name=${encodeURIComponent(iso.name)}&folder=${encodeURIComponent(folder.value)}&expected_kind=${encodeURIComponent(expectedKind)}`;
    xhr.open('POST', url);
    xhr.setRequestHeader('X-CSRF-Token', form.dataset.csrf);
    xhr.upload.onprogress = event => {
      if (event.lengthComputable && event.total > 0) {
        const pct = Math.min(100, Math.round(event.loaded * 100 / event.total));
        progress.value = pct;
        if (pct >= 100) {
          status.textContent = tr('Файл передан — сервер проверяет и добавляет в каталог…');
          if (dock) dock.progress(100, tr('Файл передан · сервер проверяет…'));
        } else {
          status.textContent = tr('Передано %s% — после передачи сервер проверит файл.', pct);
          if (dock) dock.progress(pct, tr('%s из %s · передача', formatBytes(event.loaded), formatBytes(event.total)));
        }
      }
    };
    xhr.onload = () => {
      const wasOpen = dialog.open;
      const ok = xhr.status === 201;
      const errText = xhr.responseText ? xhr.responseText.trim() : '';
      request = null;
      busy(false);
      if (ok) {
        const doneMsg = expectedKind === 'iso' ? 'ISO загружен и добавлен в каталог.' : 'Файл загружен и добавлен в каталог.';
        status.textContent = tr(doneMsg);
        progress.value = 100;
        if (wasOpen) {
          if (dock) dock.dismiss();
          const tabParam = tab && tab !== 'iso' ? `tab=${encodeURIComponent(tab)}&` : '';
          window.location.assign(`/images?${tabParam}message=` + encodeURIComponent(doneMsg));
        } else {
          resetDropzone();
          if (dock) dock.finish('done', tr('Добавлен в каталог · список обновлён'));
          document.dispatchEvent(new Event('omb:refresh'));
        }
      } else {
        const failMsg = errText || tr(expectedKind === 'iso' ? 'Не удалось загрузить ISO.' : 'Не удалось загрузить файл.');
        status.textContent = failMsg;
        if (dock) {
          if (wasOpen) dock.dismiss();
          else dock.finish('error', failMsg);
        }
      }
    };
    xhr.onerror = () => {
      const wasOpen = dialog.open;
      request = null;
      busy(false);
      status.textContent = tr('Соединение прервано.');
      if (dock) {
        if (wasOpen) dock.dismiss();
        else dock.finish('error', tr('Соединение прервано.'));
      }
    };
    xhr.onabort = () => {
      request = null;
      busy(false);
      status.textContent = tr('Загрузка отменена.');
      if (dock) dock.finish('cancelled');
    };
    busy(true);
    progress.value = 0;
    status.textContent = tr('Начинаю загрузку…');
    if (dock) dock.start(iso.name, tr('Начинаю загрузку…'));
    xhr.send(iso);
  });

  // Show the chosen file name in the drop zone.
  if (file && dropzone && dropzoneTitle) {
    file.addEventListener('change', () => {
      const chosen = file.files[0];
      dropzoneTitle.textContent = chosen ? `${chosen.name} · ${(chosen.size / 1024 ** 3).toFixed(1)} GB` : initialDropzoneTitle;
      dropzone.classList.toggle('has-file', !!chosen);
    });
  }

  loadFolders();
})();
