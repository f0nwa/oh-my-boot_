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
    "Свернуть": "Minimize",
    "Загрузить": "Upload",
    "Скачать": "Download",
    "Проверить URL": "Query URL",
    "Проверяю…": "Checking…",
    "Не используется": "Not used",
    "Введите ожидаемый hex-хеш…": "Enter expected hex hash…",
    "Найден файл: %s · HTTP %s": "File found: %s · HTTP %s",
    "Размер не указан сервером · HTTP %s": "Size not reported by server · HTTP %s",
    "Недостаточно места в выбранном хранилище (свободно %s)": "Not enough free space in selected storage (%s free)",
    "Укажите корректный URL с http:// или https://": "Enter a valid http:// or https:// URL",
    "Имя файла должно оканчиваться на .iso": "File name must end with .iso",
    "Неверный формат контрольной суммы (ожидается %s hex-символов)": "Invalid checksum format (%s hex characters expected)",
    "Не удалось проверить URL": "Could not query URL",
    "Не удалось запустить загрузку по URL": "Could not start URL download",
    "Запускаю загрузку…": "Starting download…",
    "Отменить": "Cancel",
    "Скрыть": "Dismiss",
    "Скачано на %s (%s)": "Downloaded to %s (%s)",
    "подтверждён": "verified",
    "Загрузка отменена": "Download cancelled",
    "Ошибка загрузки": "Download error",
    "/с": "/s"
  };
  const tr = (text, ...args) => {
    let result = document.documentElement.lang === 'en' ? (english[text] || text) : text;
    for (const arg of args) result = result.replace('%s', arg);
    return result;
  };
  const formatBytes = bytes => {
    if (typeof window !== 'undefined' && window.OMB && typeof window.OMB.formatUploadBytes === 'function') {
      return window.OMB.formatUploadBytes(bytes);
    }
    return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
  };

  // --- Server-side URL downloads status panel (Progress A) ---
  const urlPanel = document.getElementById('url-downloads-panel');
  let pollTimer = null;
  let knownStates = new Map();

  const stopPolling = () => {
    if (pollTimer) {
      window.clearTimeout(pollTimer);
      pollTimer = null;
    }
  };

  const schedulePoll = () => {
    stopPolling();
    if (document.hidden) return;
    pollTimer = window.setTimeout(fetchURLDownloads, 1500);
  };

  const postURLJobAction = async (id, action) => {
    if (!urlPanel) return;
    try {
      await fetch(`/images/url/downloads/${encodeURIComponent(id)}/${action}`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          'Accept': 'application/json',
          'X-CSRF-Token': urlPanel.dataset.csrf || ''
        }
      });
    } catch (_) {
      // ignore transient errors; next poll updates state
    }
    fetchURLDownloads();
  };

  const renderURLJobs = jobs => {
    if (!urlPanel) return;
    if (!Array.isArray(jobs) || !jobs.length) {
      urlPanel.hidden = true;
      urlPanel.replaceChildren();
      stopPolling();
      return;
    }
    const canManage = urlPanel.dataset.canManage === '1';
    let hasRunning = false;
    let newlyDone = false;
    const nextStates = new Map();
    const frag = document.createDocumentFragment();

    jobs.forEach(job => {
      nextStates.set(job.id, job.state);
      if (job.state === 'done' && knownStates.get(job.id) && knownStates.get(job.id) !== 'done') {
        newlyDone = true;
      }
      if (job.state === 'running') hasRunning = true;

      const card = document.createElement('div');
      card.className = 'url-job';
      if (job.state === 'done') card.classList.add('is-done');
      else if (job.state === 'error' || job.state === 'canceled') card.classList.add('is-error');

      const top = document.createElement('div');
      top.className = 'url-job-top';

      const dot = document.createElement('span');
      dot.className = job.state === 'running'
        ? 'dot dot-accent dot-pulse'
        : (job.state === 'done' ? 'dot dot-ok' : 'dot dot-error');

      const name = document.createElement('span');
      name.className = 'url-job-name mono';
      name.textContent = job.filename || job.path;

      const sub = document.createElement('span');
      sub.className = 'url-job-sub';
      const target = job.folder ? `${job.storage_name} / ${job.folder}` : job.storage_name;
      if (job.state === 'running') {
        sub.textContent = `→ ${target}${job.host ? ' · ' + job.host : ''}`;
      } else if (job.state === 'done') {
        let msg = tr('Скачано на %s (%s)', target, formatBytes(job.bytes_done || job.bytes_total || 0));
        if (job.checksum_verified && job.checksum_algo) {
          msg += ` · ${job.checksum_algo.toUpperCase()} ${tr('подтверждён')}`;
        }
        sub.textContent = msg;
      } else {
        sub.textContent = job.error || (job.state === 'canceled' ? tr('Загрузка отменена') : tr('Ошибка загрузки'));
      }

      const right = document.createElement('div');
      right.className = 'url-job-right';

      if (job.state === 'running') {
        const meta = document.createElement('span');
        meta.className = 'url-job-meta mono';
        const parts = [];
        if (job.bytes_total > 0) {
          parts.push(`${formatBytes(job.bytes_done || 0)} / ${formatBytes(job.bytes_total)}`);
        } else if (job.bytes_done > 0) {
          parts.push(formatBytes(job.bytes_done));
        }
        if (job.speed_bps > 0) {
          parts.push(`${formatBytes(job.speed_bps)}${tr('/с')}`);
        }
        if (job.percent >= 0) {
          parts.push(`${job.percent}%`);
        }
        meta.textContent = parts.join(' · ');
        right.append(meta);
      }

      if (canManage) {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = job.state === 'running' ? 'btn btn-ghost url-job-btn url-job-cancel' : 'btn btn-ghost url-job-btn';
        btn.textContent = job.state === 'running' ? tr('Отменить') : tr('Скрыть');
        btn.addEventListener('click', () => {
          btn.disabled = true;
          postURLJobAction(job.id, job.state === 'running' ? 'cancel' : 'dismiss');
        });
        right.append(btn);
      }

      top.append(dot, name, sub, right);
      card.append(top);

      if (job.state === 'running') {
        const track = document.createElement('div');
        track.className = 'url-job-track';
        const fill = document.createElement('div');
        fill.className = job.percent >= 0 ? 'url-job-fill' : 'url-job-fill is-indeterminate';
        if (job.percent >= 0) fill.style.width = `${Math.min(100, Math.max(0, job.percent))}%`;
        track.append(fill);
        card.append(track);
      }

      frag.append(card);
    });

    knownStates = nextStates;
    urlPanel.replaceChildren(frag);
    urlPanel.hidden = false;
    if (newlyDone) {
      document.dispatchEvent(new Event('omb:refresh'));
    }
    if (hasRunning) schedulePoll();
    else stopPolling();
  };

  async function fetchURLDownloads() {
    if (!urlPanel) return;
    try {
      const res = await fetch('/images/url/downloads', {
        credentials: 'same-origin',
        headers: {'Accept': 'application/json'}
      });
      if (!res.ok) return;
      const data = await res.json();
      renderURLJobs(data.jobs || []);
    } catch (_) {
      // ignore transient network errors
    }
  }

  if (urlPanel) {
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden && !urlPanel.hidden) fetchURLDownloads();
      else stopPolling();
    });
    fetchURLDownloads();
  }

  // --- Upload dialog (file XHR + URL mode on ISO tab) ---
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
  const modeButtons = dialog ? Array.from(dialog.querySelectorAll('[data-upload-mode]')) : [];
  const filePane = document.getElementById('iso-mode-file');
  const urlPane = document.getElementById('iso-mode-url');
  const urlInput = document.getElementById('iso-url-input');
  const urlProbeBtn = document.getElementById('iso-url-probe');
  const urlProbeResult = document.getElementById('iso-url-probe-result');
  const urlFilename = document.getElementById('iso-url-filename');
  const urlAlgo = document.getElementById('iso-url-algo');
  const urlChecksum = document.getElementById('iso-url-checksum');
  const urlInsecure = document.getElementById('iso-url-insecure');

  let mode = 'file'; // file | url
  let request = null;
  let foldersRequest = null;
  let foldersReady = false;
  let urlSubmitting = false;
  let filenameManuallyEdited = false;
  const folderField = document.getElementById('iso-folder-field');

  const checksumExpectedLen = algo => {
    if (algo === 'sha256') return 64;
    if (algo === 'sha512') return 128;
    if (algo === 'md5') return 32;
    return 0;
  };

  const isValidHTTPURL = raw => {
    try {
      const u = new URL(raw.trim());
      return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.hostname;
    } catch (_) {
      return false;
    }
  };

  const extractURLFilename = raw => {
    try {
      const u = new URL(raw.trim());
      const seg = u.pathname.split('/').filter(Boolean).pop() || '';
      const decoded = decodeURIComponent(seg);
      if (/\.iso$/i.test(decoded) && !/[/\\:*?"<>|]/.test(decoded)) return decoded;
    } catch (_) {}
    return '';
  };

  const isURLFormReady = () => {
    if (!urlInput || !urlFilename) return false;
    if (!foldersReady || urlSubmitting) return false;
    const selectedOpt = storage.selectedOptions[0];
    if (!storage.value || (selectedOpt && selectedOpt.disabled)) return false;
    if (!isValidHTTPURL(urlInput.value)) return false;
    const fname = urlFilename.value.trim();
    if (!/\.iso$/i.test(fname) || /[/\\:*?"<>|]/.test(fname)) return false;
    const algo = urlAlgo ? urlAlgo.value : '';
    if (algo) {
      const want = checksumExpectedLen(algo);
      const val = (urlChecksum ? urlChecksum.value : '').trim();
      if (val.length !== want || !/^[0-9a-fA-F]+$/.test(val)) return false;
    }
    return true;
  };

  const syncSubmitState = () => {
    if (request || urlSubmitting) {
      submit.disabled = true;
      return;
    }
    if (mode === 'url') {
      submit.disabled = !isURLFormReady();
    } else {
      submit.disabled = !foldersReady;
    }
  };

  const setMode = nextMode => {
    if (request) return;
    mode = nextMode === 'url' && urlPane ? 'url' : 'file';
    modeButtons.forEach(btn => {
      const active = btn.dataset.uploadMode === mode;
      btn.classList.toggle('active', active);
      btn.setAttribute('aria-selected', active ? 'true' : 'false');
    });
    if (filePane) filePane.hidden = mode !== 'file';
    if (urlPane) urlPane.hidden = mode !== 'url';
    if (file) file.required = mode === 'file';
    submit.textContent = mode === 'url'
      ? (submit.dataset.labelUrl || tr('Скачать'))
      : (submit.dataset.labelFile || tr('Загрузить'));
    status.textContent = '';
    syncSubmitState();
  };

  modeButtons.forEach(btn => {
    btn.addEventListener('click', () => setMode(btn.dataset.uploadMode));
  });

  if (urlInput && urlFilename) {
    urlInput.addEventListener('input', () => {
      if (!filenameManuallyEdited) {
        const candidate = extractURLFilename(urlInput.value);
        if (candidate) urlFilename.value = candidate;
      }
      if (urlProbeResult) urlProbeResult.hidden = true;
      syncSubmitState();
    });
    urlFilename.addEventListener('input', () => {
      filenameManuallyEdited = urlFilename.value.trim() !== '';
      syncSubmitState();
    });
  }

  if (urlAlgo && urlChecksum) {
    urlAlgo.addEventListener('change', () => {
      const active = urlAlgo.value !== '';
      urlChecksum.disabled = !active;
      urlChecksum.placeholder = active ? tr('Введите ожидаемый hex-хеш…') : tr('Не используется');
      if (!active) urlChecksum.value = '';
      syncSubmitState();
    });
    urlChecksum.addEventListener('input', syncSubmitState);
  }

  if (urlProbeBtn && urlInput && urlProbeResult) {
    urlProbeBtn.addEventListener('click', async () => {
      const raw = urlInput.value.trim();
      if (!isValidHTTPURL(raw)) {
        urlProbeResult.className = 'url-probe-pill is-error';
        urlProbeResult.textContent = tr('Укажите корректный URL с http:// или https://');
        urlProbeResult.hidden = false;
        return;
      }
      urlProbeBtn.disabled = true;
      urlProbeBtn.textContent = tr('Проверяю…');
      urlProbeResult.hidden = true;
      try {
        const res = await fetch('/images/url/probe', {
          method: 'POST',
          credentials: 'same-origin',
          headers: {
            'Content-Type': 'application/json',
            'Accept': 'application/json',
            'X-CSRF-Token': form.dataset.csrf || ''
          },
          body: JSON.stringify({
            url: raw,
            storage_id: Number(storage.value || 0),
            insecure_tls: !!(urlInsecure && urlInsecure.checked)
          })
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok || !data.ok) {
          urlProbeResult.className = 'url-probe-pill is-error';
          urlProbeResult.textContent = data.error || tr('Не удалось проверить URL');
          urlProbeResult.hidden = false;
          return;
        }
        if (data.filename && (!filenameManuallyEdited || !urlFilename.value.trim())) {
          urlFilename.value = data.filename;
        }
        if (data.fits_storage === false && data.free_bytes > 0) {
          urlProbeResult.className = 'url-probe-pill is-error';
          urlProbeResult.textContent = tr('Недостаточно места в выбранном хранилище (свободно %s)', formatBytes(data.free_bytes));
        } else {
          urlProbeResult.className = 'url-probe-pill is-ok';
          urlProbeResult.textContent = data.size > 0
            ? tr('Найден файл: %s · HTTP %s', formatBytes(data.size), String(data.status_code || 200))
            : tr('Размер не указан сервером · HTTP %s', String(data.status_code || 200));
        }
        urlProbeResult.hidden = false;
        syncSubmitState();
      } catch (_) {
        urlProbeResult.className = 'url-probe-pill is-error';
        urlProbeResult.textContent = tr('Не удалось проверить URL');
        urlProbeResult.hidden = false;
      } finally {
        urlProbeBtn.disabled = false;
        urlProbeBtn.textContent = tr('Проверить URL');
      }
    });
  }

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
    modeButtons.forEach(btn => { btn.disabled = on; });
    storage.disabled = on;
    folder.disabled = on;
    file.disabled = on;
    cancel.hidden = !on;
    progress.parentElement.hidden = !on;
    syncSubmitState();
  };

  const resetDropzone = () => {
    file.value = '';
    if (dropzoneTitle) dropzoneTitle.textContent = initialDropzoneTitle;
    if (dropzone) dropzone.classList.remove('has-file');
  };

  const resetURLForm = () => {
    if (urlInput) urlInput.value = '';
    if (urlFilename) urlFilename.value = '';
    if (urlAlgo) urlAlgo.value = '';
    if (urlChecksum) {
      urlChecksum.value = '';
      urlChecksum.disabled = true;
      urlChecksum.placeholder = tr('Не используется');
    }
    if (urlInsecure) urlInsecure.checked = false;
    if (urlProbeResult) urlProbeResult.hidden = true;
    filenameManuallyEdited = false;
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
      syncSubmitState();
      status.textContent = '';
    } catch (error) { if (foldersRequest === controller && error.name !== 'AbortError') status.textContent = error.message; }
  }

  storage.addEventListener('change', loadFolders);
  cancel.addEventListener('click', () => { if (request) request.abort(); });

  const submitURLDownload = async () => {
    if (!isURLFormReady()) {
      const raw = (urlInput ? urlInput.value : '').trim();
      const fname = (urlFilename ? urlFilename.value : '').trim();
      if (!isValidHTTPURL(raw)) {
        status.textContent = tr('Укажите корректный URL с http:// или https://');
      } else if (!/\.iso$/i.test(fname)) {
        status.textContent = tr('Имя файла должно оканчиваться на .iso');
      } else if (urlAlgo && urlAlgo.value) {
        status.textContent = tr('Неверный формат контрольной суммы (ожидается %s hex-символов)', String(checksumExpectedLen(urlAlgo.value)));
      }
      return;
    }
    urlSubmitting = true;
    syncSubmitState();
    status.textContent = tr('Запускаю загрузку…');
    try {
      const res = await fetch('/images/url/download', {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          'Content-Type': 'application/json',
          'Accept': 'application/json',
          'X-CSRF-Token': form.dataset.csrf || ''
        },
        body: JSON.stringify({
          url: urlInput.value.trim(),
          filename: urlFilename.value.trim(),
          storage_id: Number(storage.value || 0),
          folder: folder.value || '',
          checksum_algo: urlAlgo ? urlAlgo.value : '',
          checksum_value: urlChecksum ? urlChecksum.value.trim() : '',
          insecure_tls: !!(urlInsecure && urlInsecure.checked)
        })
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data.ok) {
        status.textContent = data.error || tr('Не удалось запустить загрузку по URL');
        return;
      }
      status.textContent = '';
      resetURLForm();
      dialog.close();
      fetchURLDownloads();
    } catch (_) {
      status.textContent = tr('Не удалось запустить загрузку по URL');
    } finally {
      urlSubmitting = false;
      syncSubmitState();
    }
  };

  form.addEventListener('submit', event => {
    event.preventDefault();
    if (mode === 'url') {
      submitURLDownload();
      return;
    }
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
