(function () {
  'use strict';

  var modeInput = document.getElementById('login-2fa-mode');
  var codeInput = document.getElementById('login-2fa-code');
  var toggleBtn = document.getElementById('login-2fa-toggle');
  var labelEl = document.querySelector('label[for="login-2fa-code"]');
  if (!modeInput || !codeInput || !toggleBtn) return;

  function syncMode() {
    var isRecovery = modeInput.value === 'recovery';
    if (labelEl) {
      labelEl.textContent = toggleBtn.getAttribute(isRecovery ? 'data-title-recovery' : 'data-title-totp') || labelEl.textContent;
    }
    toggleBtn.textContent = toggleBtn.getAttribute(isRecovery ? 'data-label-totp' : 'data-label-recovery') || toggleBtn.textContent;
    codeInput.value = '';
    codeInput.placeholder = isRecovery ? 'XXXX-XXXX' : '000000';
    codeInput.setAttribute('inputmode', isRecovery ? 'text' : 'numeric');
    codeInput.setAttribute('autocomplete', isRecovery ? 'off' : 'one-time-code');
    codeInput.classList.toggle('input-otp-recovery', isRecovery);
    codeInput.focus();
  }

  toggleBtn.addEventListener('click', function (event) {
    event.preventDefault();
    modeInput.value = modeInput.value === 'recovery' ? 'totp' : 'recovery';
    syncMode();
  });
})();
