(() => {
  const form = document.getElementById('ubuntu-editor');
  if (!form) return;
  const save = document.getElementById('ai-save');
  const saveNew = document.getElementById('ai-save-new');
  const setSaveDisabled = (disabled) => {
    save.disabled = disabled;
    if (saveNew) saveNew.disabled = disabled;
  };
  const state = document.getElementById('ai-preview-state');
  const passwordState = document.getElementById('ai-password-state');
  const yaml = document.getElementById('autoinstall-yaml');
  let revision = 0;
  let pending;
  let timer;
  const tokenField = () => {
    let input = form.querySelector('[name="preview_token"]');
    if (!input) {
      input = document.createElement('input');
      input.type = 'hidden'; input.name = 'preview_token'; form.append(input);
    }
    return input;
  };
  function download(ready, token) {
    const container = document.getElementById('ai-download');
    if (!container) return;
    let target = container.querySelector('form');
    if (!target && ready) {
      target = document.createElement('form');
      target.method = 'post'; target.action = '/ubuntu-autoinstall/preview/download';
      for (const [name, value] of [['csrf', form.elements.csrf.value], ['preview_token', token]]) {
        const input = document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; target.append(input);
      }
      const button = document.createElement('button'); button.className = 'btn'; button.textContent = form.dataset.download;
      target.append(button); container.append(target);
    }
    if (target) { target.hidden = !ready; target.elements.preview_token.value = token || ''; }
  }
  function invalidatePreview() {
    revision++; pending?.abort(); clearTimeout(timer);
    setSaveDisabled(true); state.textContent = form.dataset.stale;
    download(false, '');
  }
  function selectSection(id) {
    form.elements.active_section.value = id;
    form.querySelectorAll('[data-ai-panel]').forEach(el => { el.hidden = el.dataset.aiPanel !== id; });
    form.querySelectorAll('[data-ai-section]').forEach(el => { el.classList.toggle('on', el.dataset.aiSection === id); });
  }
  function selectTab(id) {
    form.elements.active_tab.value = id;
    form.querySelectorAll('[data-ai-tab-panel]').forEach(el => { el.hidden = el.dataset.aiTabPanel !== id; });
    form.querySelectorAll('[data-ai-tab]').forEach(el => { el.setAttribute('aria-selected', String(el.dataset.aiTab === id)); });
  }
  form.addEventListener('click', event => {
    const button = event.target.closest('button');
    if (!button) return;
    if (button.dataset.aiSection) { event.preventDefault(); selectSection(button.dataset.aiSection); }
    if (button.dataset.aiTab) { event.preventDefault(); selectTab(button.dataset.aiTab); }
  });
  async function preview() {
    const current = revision;
    pending?.abort(); pending = new AbortController();
    try {
      const response = await fetch(form.action, {
        method: 'POST', body: new URLSearchParams(new FormData(form)),
        headers: { Accept: 'application/json' }, signal: pending.signal
      });
      if (!response.headers.get('Content-Type')?.includes('application/json')) throw new Error('unavailable');
      const result = await response.json();
      if (current !== revision) return;
      const summary = document.getElementById('ai-summary'); summary.replaceChildren();
      for (const row of result.summary || []) {
        const label = document.createElement('dt'); label.textContent = row.label;
        const value = document.createElement('dd'); value.textContent = row.value; summary.append(label, value);
      }
      const issues = document.getElementById('ai-issues'); issues.replaceChildren();
      for (const row of result.issues || []) {
        const item = document.createElement('li'); item.textContent = row.message + (row.field && row.severity !== 'error' ? ' ' + row.field : '');
        if (row.severity === 'error') item.className = 'danger-text'; issues.append(item);
      }
      yaml.textContent = result.yaml || '';
      if (result.token) tokenField().value = result.token;
      const disabled = !response.ok || !result.ready;
      setSaveDisabled(disabled);
      state.textContent = !disabled ? form.dataset.checked : form.dataset.invalid;
      if (!disabled) {
        form.elements.password.value = '';
        passwordState.textContent = form.dataset.passwordAccepted;
      }
      download(!disabled, result.token);
    } catch (error) {
      if (error.name === 'AbortError' || current !== revision) return;
      setSaveDisabled(true); state.textContent = form.dataset.expired; download(false, '');
    }
  }
  function updateDiskWarnings() {
    const mode = form.elements.disk_mode.value;
    document.getElementById('ai-default-disk-warning').hidden = mode !== 'default';
    document.getElementById('ai-serial-disk-warning').hidden = mode !== 'serial';
  }
  updateDiskWarnings();
  form.addEventListener('input', () => { updateDiskWarnings(); invalidatePreview(); timer = setTimeout(preview, 350); });
  form.addEventListener('change', () => { updateDiskWarnings(); invalidatePreview(); timer = setTimeout(preview, 350); });
  form.addEventListener('submit', event => {
    if (event.submitter?.id === 'ai-save' || event.submitter?.id === 'ai-save-new') {
      if (event.submitter.disabled) event.preventDefault();
      return;
    }
    event.preventDefault(); invalidatePreview(); preview();
  });
})();
