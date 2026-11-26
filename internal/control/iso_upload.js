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
    "Передано %s% — после передачи сервер проверит файл.": "Transferred %s% — the server will check the file after the transfer."
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
  open.addEventListener('click', () => dialog.showModal());
  close.addEventListener('click', () => dialog.close());
  dialog.addEventListener('cancel', event => { if (request) event.preventDefault(); });
  const storage = form.querySelector('[name="storage"]');
  const folder = form.querySelector('[name="folder"]');
  const file = form.querySelector('[name="file"]');
  const status = document.getElementById('iso-upload-status');
  const progress = document.getElementById('iso-upload-progress');
  const submit = form.querySelector('[type="submit"]');
  const cancel = document.getElementById('iso-upload-cancel');
  let request = null;
  let foldersRequest = null;
  let foldersReady = false;
  const folderField = document.getElementById('iso-folder-field');
  // Cancel and the progress bar only make sense while a transfer runs.
  const busy = on => { close.disabled = on; submit.disabled = on || !foldersReady; storage.disabled = on; folder.disabled = on; cancel.hidden = !on; progress.parentElement.hidden = !on; };

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
      submit.disabled = false;
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
    request = new XMLHttpRequest();
    const url = `/images/upload/${storage.value}?name=${encodeURIComponent(iso.name)}&folder=${encodeURIComponent(folder.value)}&expected_kind=${encodeURIComponent(expectedKind)}`;
    request.open('POST', url);
    request.setRequestHeader('X-CSRF-Token', form.dataset.csrf);
    request.upload.onprogress = event => {
      if (event.lengthComputable) progress.value = Math.round(event.loaded * 100 / event.total);
      status.textContent = tr('Передано %s% — после передачи сервер проверит файл.', progress.value);
    };
    request.onload = () => {
      if (request.status === 201) {
        const doneMsg = expectedKind === 'iso' ? 'ISO загружен и добавлен в каталог.' : 'Файл загружен и добавлен в каталог.';
        status.textContent = tr(doneMsg);
        progress.value = 100;
        const tabParam = tab && tab !== 'iso' ? `tab=${encodeURIComponent(tab)}&` : '';
        window.location.assign(`/images?${tabParam}message=` + encodeURIComponent(doneMsg));
      } else {
        const failMsg = expectedKind === 'iso' ? 'Не удалось загрузить ISO.' : 'Не удалось загрузить файл.';
        status.textContent = request.responseText.trim() || tr(failMsg);
      }
      busy(false);
      request = null;
    };
    request.onerror = () => { status.textContent = tr('Соединение прервано.'); busy(false); request = null; };
    request.onabort = () => { status.textContent = tr('Загрузка отменена.'); busy(false); request = null; };
    busy(true);
    progress.value = 0;
    status.textContent = tr('Начинаю загрузку…');
    request.send(iso);
  });
  loadFolders();
})();

// Show the chosen file name in the drop zone.
(() => {
  const input = document.querySelector('#iso-upload-form .dropzone input[type="file"]');
  if (!input) return;
  const zone = input.closest('.dropzone');
  const title = zone.querySelector('strong');
  const initial = title.textContent;
  input.addEventListener('change', () => {
    const file = input.files[0];
    title.textContent = file ? `${file.name} · ${(file.size / 1024 ** 3).toFixed(1)} GB` : initial;
    zone.classList.toggle('has-file', !!file);
  });
})();
