(() => {
  const english = {
    "Развернуть": "Expand",
    "Отменить": "Cancel",
    "Скрыть": "Dismiss",
    "Готово": "Done",
    "Ошибка": "Error",
    "Проверка…": "Checking…",
    "Статус загрузки": "Upload status",
    "Б": "B",
    "КБ": "KB",
    "МБ": "MB",
    "ГБ": "GB"
  };

  const isEN = () => document.documentElement.lang === 'en';
  const tr = text => (isEN() ? (english[text] || text) : text);

  const formatBytes = bytes => {
    const n = Number(bytes) || 0;
    const units = ['Б', 'КБ', 'МБ', 'ГБ'];
    let val = n;
    let idx = 0;
    while (val >= 1024 && idx < units.length - 1) {
      val /= 1024;
      idx++;
    }
    const fixed = idx === 0 ? String(Math.round(val)) : val.toFixed(1);
    const formatted = isEN() ? fixed : fixed.replace('.', ',');
    return `${formatted} ${tr(units[idx])}`;
  };

  const activeDocks = new Set();
  const handleBeforeUnload = event => {
    if (activeDocks.size === 0) return;
    event.preventDefault();
    event.returnValue = '';
    return '';
  };
  window.addEventListener('beforeunload', handleBeforeUnload);

  const createUploadDock = (options = {}) => {
    const onExpand = typeof options.onExpand === 'function' ? options.onExpand : () => {};
    const onCancel = typeof options.onCancel === 'function' ? options.onCancel : () => {};

    let state = 'idle'; // idle | uploading | checking | done | error
    let minimized = false;
    let el = null;
    let dotEl = null;
    let nameEl = null;
    let pctEl = null;
    let barEl = null;
    let metaEl = null;
    let expandBtn = null;
    let cancelBtn = null;

    const ensureDOM = () => {
      if (el) return el;
      el = document.createElement('aside');
      el.className = 'upload-dock';
      el.setAttribute('role', 'region');
      el.setAttribute('aria-label', tr('Статус загрузки'));
      el.setAttribute('aria-live', 'polite');
      el.hidden = true;

      const top = document.createElement('div');
      top.className = 'upload-dock-top';

      dotEl = document.createElement('span');
      dotEl.className = 'dot dot-accent dot-pulse';

      nameEl = document.createElement('span');
      nameEl.className = 'upload-dock-name';

      pctEl = document.createElement('span');
      pctEl.className = 'upload-dock-pct mono';

      top.append(dotEl, nameEl, pctEl);

      const track = document.createElement('div');
      track.className = 'upload-dock-track';
      barEl = document.createElement('div');
      barEl.className = 'upload-dock-fill';
      track.append(barEl);

      const bottom = document.createElement('div');
      bottom.className = 'upload-dock-bottom';

      metaEl = document.createElement('span');
      metaEl.className = 'upload-dock-meta';

      const actions = document.createElement('div');
      actions.className = 'upload-dock-actions';

      expandBtn = document.createElement('button');
      expandBtn.type = 'button';
      expandBtn.className = 'btn upload-dock-btn';
      expandBtn.textContent = tr('Развернуть');
      expandBtn.addEventListener('click', () => {
        onExpand();
      });

      cancelBtn = document.createElement('button');
      cancelBtn.type = 'button';
      cancelBtn.className = 'btn btn-ghost upload-dock-btn upload-dock-cancel';
      cancelBtn.textContent = tr('Отменить');
      cancelBtn.addEventListener('click', () => {
        if (state === 'uploading' || state === 'checking') {
          onCancel();
        } else {
          dismiss();
        }
      });

      actions.append(expandBtn, cancelBtn);
      bottom.append(metaEl, actions);
      el.append(top, track, bottom);
      document.body.append(el);
      return el;
    };

    const syncVisibility = () => {
      if (!el) return;
      el.hidden = !minimized || state === 'idle';
    };

    const setActive = on => {
      if (on) activeDocks.add(api);
      else activeDocks.delete(api);
    };

    const applyVisualState = (pct, metaText) => {
      ensureDOM();
      el.classList.remove('is-done', 'is-error');
      barEl.classList.remove('is-ok', 'is-bad', 'is-indet');
      if (metaText !== undefined) {
        metaEl.textContent = metaText;
        metaEl.title = metaText;
      }

      if (state === 'uploading') {
        const clamped = Math.max(0, Math.min(100, Math.round(pct || 0)));
        dotEl.className = 'dot dot-accent dot-pulse';
        pctEl.textContent = `${clamped}%`;
        barEl.style.width = `${clamped}%`;
        expandBtn.hidden = false;
        cancelBtn.textContent = tr('Отменить');
        cancelBtn.classList.add('upload-dock-cancel');
      } else if (state === 'checking') {
        dotEl.className = 'dot dot-accent dot-pulse';
        pctEl.textContent = tr('Проверка…');
        barEl.style.width = '100%';
        barEl.classList.add('is-indet');
        expandBtn.hidden = false;
        cancelBtn.textContent = tr('Отменить');
        cancelBtn.classList.add('upload-dock-cancel');
      } else if (state === 'done') {
        el.classList.add('is-done');
        dotEl.className = 'dot dot-ok';
        pctEl.textContent = tr('Готово');
        barEl.style.width = '100%';
        barEl.classList.add('is-ok');
        expandBtn.hidden = true;
        cancelBtn.textContent = tr('Скрыть');
        cancelBtn.classList.remove('upload-dock-cancel');
      } else if (state === 'error') {
        el.classList.add('is-error');
        dotEl.className = 'dot dot-error';
        pctEl.textContent = tr('Ошибка');
        barEl.style.width = '100%';
        barEl.classList.add('is-bad');
        expandBtn.hidden = false;
        cancelBtn.textContent = tr('Скрыть');
        cancelBtn.classList.remove('upload-dock-cancel');
      }
      syncVisibility();
    };

    const start = (filename, metaText) => {
      ensureDOM();
      state = 'uploading';
      minimized = false;
      setActive(true);
      nameEl.textContent = filename || '';
      nameEl.title = filename || '';
      applyVisualState(0, metaText || '');
    };

    const progress = (pct, metaText) => {
      if (state !== 'uploading' && state !== 'checking') return;
      const clamped = Math.max(0, Math.min(100, Math.round(pct || 0)));
      state = clamped >= 100 ? 'checking' : 'uploading';
      applyVisualState(clamped, metaText);
    };

    const setMinimized = on => {
      minimized = Boolean(on);
      if (!minimized && (state === 'done' || state === 'error')) {
        state = 'idle';
      }
      syncVisibility();
    };

    const finish = (kind, metaText) => {
      setActive(false);
      if (kind === 'cancelled') {
        dismiss();
        return;
      }
      state = kind === 'done' ? 'done' : 'error';
      if (!minimized) {
        dismiss();
        return;
      }
      applyVisualState(100, metaText || '');
    };

    const dismiss = () => {
      setActive(false);
      state = 'idle';
      minimized = false;
      syncVisibility();
    };

    const isActive = () => state === 'uploading' || state === 'checking';

    const api = { start, progress, setMinimized, finish, dismiss, isActive };
    return api;
  };

  window.OMB = window.OMB || {};
  window.OMB.createUploadDock = createUploadDock;
  window.OMB.formatUploadBytes = formatBytes;
})();
