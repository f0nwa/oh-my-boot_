(() => {
  const english = {
    "Читаю папки…": "Reading folders…",
    "Не удалось прочитать папки хранилища": "Could not read storage folders",
    "(корневая папка)": "(root folder)",
    "Выберите ISO размером до 32 ГБ.": "Choose an ISO up to 32 GB.",
    "ISO загружен и добавлен в каталог.": "ISO uploaded and added to the catalog.",
    "Не удалось загрузить ISO.": "Could not upload the ISO.",
    "Соединение прервано.": "Connection lost.",
    "Загрузка отменена.": "Upload cancelled.",
    "Начинаю загрузку…": "Starting upload…",
    "Передано %s% — после передачи сервер проверит файл.": "Transferred %s% — the server will check the file after the transfer."
  };
  const tr = (text, ...args) => {
    let result = document.documentElement.lang === 'en' ? (english[text] || text) : text;
    for (const arg of args) result = result.replace('%s', arg);
    return result;
  };
  const form = document.getElementById('iso-upload-form');
  if (!form) return;
  const storage = form.querySelector('[name="storage"]');
  const folder = form.querySelector('[name="folder"]');
  const file = form.querySelector('[name="file"]');
  const status = document.getElementById('iso-upload-status');
  const progress = document.getElementById('iso-upload-progress');
  const submit = form.querySelector('[type="submit"]');
  const cancel = document.getElementById('iso-upload-cancel');
  let request = null;

  async function loadFolders() {
    folder.replaceChildren();
    status.textContent = tr('Читаю папки…');
    if (!storage.value) { status.textContent = ''; return; }
    try {
      const response = await fetch(`/images/storages/${storage.value}/folders`, {credentials: 'same-origin'});
      if (!response.ok) throw new Error(tr('Не удалось прочитать папки хранилища'));
      const folders = await response.json();
      folders.forEach(path => {
        const option = document.createElement('option');
        option.value = path;
        option.textContent = path || tr('(корневая папка)');
        folder.append(option);
      });
      status.textContent = '';
    } catch (error) { status.textContent = error.message; }
  }

  storage.addEventListener('change', loadFolders);
  cancel.addEventListener('click', () => { if (request) request.abort(); });
  form.addEventListener('submit', event => {
    event.preventDefault();
    const iso = file.files[0];
    if (!iso || !storage.value || !folder.options.length || request) return;
    if (!/\.iso$/i.test(iso.name) || iso.size <= 0 || iso.size > 32 * 1024 ** 3) {
      status.textContent = tr('Выберите ISO размером до 32 ГБ.');
      return;
    }
    request = new XMLHttpRequest();
    const url = `/images/upload/${storage.value}?name=${encodeURIComponent(iso.name)}&folder=${encodeURIComponent(folder.value)}`;
    request.open('POST', url);
    request.setRequestHeader('X-CSRF-Token', form.dataset.csrf);
    request.upload.onprogress = event => {
      if (event.lengthComputable) progress.value = Math.round(event.loaded * 100 / event.total);
      status.textContent = tr('Передано %s% — после передачи сервер проверит файл.', progress.value);
    };
    request.onload = () => {
      if (request.status === 201) {
        status.textContent = tr('ISO загружен и добавлен в каталог.');
        progress.value = 100;
        window.location.assign('/images?message=' + encodeURIComponent('ISO загружен и добавлен в каталог.'));
      } else status.textContent = request.responseText.trim() || tr('Не удалось загрузить ISO.');
      submit.disabled = false;
      request = null;
    };
    request.onerror = () => { status.textContent = tr('Соединение прервано.'); submit.disabled = false; request = null; };
    request.onabort = () => { status.textContent = tr('Загрузка отменена.'); submit.disabled = false; request = null; };
    submit.disabled = true;
    progress.value = 0;
    status.textContent = tr('Начинаю загрузку…');
    request.send(iso);
  });
  loadFolders();
})();
