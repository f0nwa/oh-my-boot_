(() => {
  const forms = document.querySelectorAll('[data-driver-upload]');
  forms.forEach(form => {
    const status = form.querySelector('[role="status"]');
    const progress = form.querySelector('progress');
    const submit = form.querySelector('[type="submit"]');
    const cancel = form.querySelector('[data-cancel]');
    let request = null;
    cancel.addEventListener('click', () => { if (request) request.abort(); });
    form.addEventListener('submit', event => {
      event.preventDefault();
      if (request) return;
      const input = form.querySelector('[type="file"]');
      const files = Array.from(input.files || []);
      if (!files.length) { status.textContent = 'Выберите пакет драйвера.'; return; }
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
        status.textContent = `Передано ${progress.value}%. Сервер проверит состав пакета.`;
      };
      request.onload = () => {
        if (request.status === 303 || (request.status === 200 && request.responseURL.includes('/drivers?'))) {
          window.location.assign('/drivers?message=' + encodeURIComponent('Пакет драйвера добавлен'));
        } else status.textContent = request.responseText.trim() || 'Не удалось загрузить пакет.';
        submit.disabled = false;
        request = null;
      };
      request.onerror = () => { status.textContent = 'Соединение прервано.'; submit.disabled = false; request = null; };
      request.onabort = () => { status.textContent = 'Загрузка отменена.'; submit.disabled = false; request = null; };
      submit.disabled = true;
      progress.value = 0;
      status.textContent = 'Передаю пакет…';
      request.send(data);
    });
  });
})();
