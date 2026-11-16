(() => {
  const form = document.querySelector('.login form, form[action="/login"]');
  if (!form) return;

  const userInput = form.querySelector('input[name="username"]');
  const passInput = form.querySelector('input[name="password"]');
  const submitBtn = form.querySelector('button.btn-primary, button[type="submit"], button');
  if (!userInput || !passInput || !submitBtn) return;

  const userWrapper = userInput.closest('.field-float');
  const passWrapper = passInput.closest('.field-float');

  function sync() {
    const userVal = userInput.value ? userInput.value.trim() : '';
    const passVal = passInput.value || '';

    if (userWrapper) {
      userWrapper.classList.toggle('is-floating', userInput.value.length > 0);
    }
    if (passWrapper) {
      passWrapper.classList.toggle('is-floating', passInput.value.length > 0);
    }

    const ready = userVal.length > 0 && passVal.length > 0;
    submitBtn.disabled = !ready;
  }

  form.addEventListener('input', sync);
  form.addEventListener('change', sync);

  sync();
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', sync);
  }
  window.addEventListener('load', sync);
  setTimeout(sync, 50);
  setTimeout(sync, 200);
  setTimeout(sync, 500);
  setTimeout(sync, 1000);

  form.addEventListener('submit', e => {
    sync();
    if (submitBtn.disabled) {
      e.preventDefault();
    }
  });
})();
