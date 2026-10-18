(() => {
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
    status.textContent = 'Читаю папки…';
    if (!storage.value) { status.textContent = ''; return; }
    try {
      const response = await fetch(`/images/storages/${storage.value}/folders`, {credentials: 'same-origin'});
      if (!response.ok) throw new Error('Не удалось прочитать папки хранилища');
      const folders = await response.json();
      folders.forEach(path => {
        const option = document.createElement('option');
        option.value = path;
        option.textContent = path || '(корневая папка)';
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
      status.textContent = 'Выберите ISO размером до 32 ГБ.';
      return;
    }
    request = new XMLHttpRequest();
    const url = `/images/upload/${storage.value}?name=${encodeURIComponent(iso.name)}&folder=${encodeURIComponent(folder.value)}`;
    request.open('POST', url);
    request.setRequestHeader('X-CSRF-Token', form.dataset.csrf);
    request.upload.onprogress = event => {
      if (event.lengthComputable) progress.value = Math.round(event.loaded * 100 / event.total);
      status.textContent = `Передано ${progress.value}% — после передачи сервер проверит файл.`;
    };
    request.onload = () => {
      if (request.status === 201) {
        status.textContent = 'ISO загружен и добавлен в каталог.';
        progress.value = 100;
        window.location.assign('/images?message=' + encodeURIComponent('ISO загружен и добавлен в каталог.'));
      } else status.textContent = request.responseText.trim() || 'Не удалось загрузить ISO.';
      submit.disabled = false;
      request = null;
    };
    request.onerror = () => { status.textContent = 'Соединение прервано.'; submit.disabled = false; request = null; };
    request.onabort = () => { status.textContent = 'Загрузка отменена.'; submit.disabled = false; request = null; };
    submit.disabled = true;
    progress.value = 0;
    status.textContent = 'Начинаю загрузку…';
    request.send(iso);
  });
  loadFolders();
})();
