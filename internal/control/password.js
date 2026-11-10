(() => {
  const english = {
    "Сгенерировать": "Generate",
    "Скопировать": "Copy",
    "Скопировано": "Copied",
    "Показать пароль": "Show password",
    "Скрыть пароль": "Hide password",
    "Сохраните пароль: после отправки формы он больше не будет показан.": "Save the password: it will not be shown again after the form is sent.",
  };
  const tr = text => document.documentElement.lang === 'en' ? (english[text] || text) : text;
  // Letters and digits without easily confused characters (0/O, 1/l/I).
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789';
  const length = 20;
  const eyeOpenSVG = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/></svg>';
  const eyeClosedSVG = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9.88 9.88a3 3 0 1 0 4.24 4.24"/><path d="M10.73 5.08A10.43 10.43 0 0 1 12 5c6.5 0 10 7 10 7a13.16 13.16 0 0 1-1.67 2.68"/><path d="M6.61 6.61A13.53 13.53 0 0 0 2 12s3.5 7 10 7a9.74 9.74 0 0 0 5.39-1.61"/><line x1="2" y1="2" x2="22" y2="22"/></svg>';

  function generate() {
    const limit = 256 - (256 % alphabet.length);
    let result = '';
    while (result.length < length) {
      const bytes = new Uint8Array(length * 2);
      crypto.getRandomValues(bytes);
      for (const byte of bytes) {
        if (byte < limit && result.length < length) result += alphabet[byte % alphabet.length];
      }
    }
    return result;
  }

  document.querySelectorAll('input[type="password"]').forEach(input => {
    if (input.dataset.pwReady === '1') return;
    input.dataset.pwReady = '1';

    const wrap = document.createElement('span');
    wrap.className = 'pw-wrap';
    input.parentNode.insertBefore(wrap, input);
    wrap.appendChild(input);

    const toggle = document.createElement('button');
    toggle.type = 'button';
    toggle.className = 'pw-toggle';
    wrap.appendChild(toggle);

    function syncToggle() {
      const visible = input.type !== 'password';
      const label = visible ? tr('Скрыть пароль') : tr('Показать пароль');
      toggle.innerHTML = visible ? eyeClosedSVG : eyeOpenSVG;
      toggle.setAttribute('aria-pressed', visible ? 'true' : 'false');
      toggle.setAttribute('aria-label', label);
      toggle.title = label;
    }

    toggle.addEventListener('click', () => {
      input.type = input.type === 'password' ? 'text' : 'password';
      syncToggle();
    });
    input.addEventListener('omb:password-sync', syncToggle);

    const dialog = input.closest('dialog');
    if (dialog) {
      dialog.addEventListener('close', () => {
        input.type = 'password';
        syncToggle();
      });
    }

    syncToggle();
  });

  document.querySelectorAll('input[data-password-generator]').forEach(input => {
    const tools = document.createElement('p');
    tools.className = 'pw-tools';
    const make = document.createElement('button');
    make.type = 'button';
    make.className = 'secondary';
    make.textContent = tr('Сгенерировать');
    const copy = document.createElement('button');
    copy.type = 'button';
    copy.className = 'secondary';
    copy.textContent = tr('Скопировать');
    copy.hidden = true;
    const note = document.createElement('small');
    note.setAttribute('role', 'status');
    tools.append(make, ' ', copy, ' ', note);
    const anchor = input.closest('.pw-wrap') || input;
    anchor.insertAdjacentElement('afterend', tools);

    make.addEventListener('click', () => {
      input.type = 'password';
      input.value = generate();
      input.dispatchEvent(new Event('omb:password-sync'));
      input.dispatchEvent(new Event('input', { bubbles: true }));
      const confirmSelector = input.getAttribute('data-password-confirm');
      if (confirmSelector) {
        const confirmInput = document.querySelector(confirmSelector);
        if (confirmInput) {
          confirmInput.type = 'password';
          confirmInput.value = input.value;
          confirmInput.dispatchEvent(new Event('omb:password-sync'));
          confirmInput.dispatchEvent(new Event('input', { bubbles: true }));
        }
      }
      copy.hidden = false;
      note.textContent = tr('Сохраните пароль: после отправки формы он больше не будет показан.');
    });
    copy.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(input.value);
        note.textContent = tr('Скопировано');
      } catch (_) {
        const tmp = document.createElement('textarea');
        tmp.value = input.value;
        tmp.setAttribute('readonly', '');
        tmp.style.position = 'fixed';
        tmp.style.left = '-9999px';
        document.body.appendChild(tmp);
        tmp.select();
        try {
          if (document.execCommand('copy')) {
            note.textContent = tr('Скопировано');
          }
        } catch (__) {
          // Ignore copy failure when clipboard API and execCommand are unavailable.
        }
        document.body.removeChild(tmp);
      }
    });
  });
})();
