(() => {
  const english = {
    "Сгенерировать": "Generate",
    "Скопировать": "Copy",
    "Скопировано": "Copied",
    "Сохраните пароль: после отправки формы он больше не будет показан.": "Save the password: it will not be shown again after the form is sent.",
  };
  const tr = text => document.documentElement.lang === 'en' ? (english[text] || text) : text;
  // Letters and digits without easily confused characters (0/O, 1/l/I).
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789';
  const length = 20;

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
    input.insertAdjacentElement('afterend', tools);

    make.addEventListener('click', () => {
      input.type = 'text';
      input.value = generate();
      input.dispatchEvent(new Event('input', { bubbles: true }));
      copy.hidden = false;
      note.textContent = tr('Сохраните пароль: после отправки формы он больше не будет показан.');
    });
    copy.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(input.value);
        note.textContent = tr('Скопировано');
      } catch (_) {
        input.select();
      }
    });
  });
})();
