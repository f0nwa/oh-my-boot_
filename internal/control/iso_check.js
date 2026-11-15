// ISO inspection can take several minutes on network storage.
(function () {
  document.addEventListener('submit', function (event) {
    var form = event.target.closest && event.target.closest('form[data-ubuntu-inspect]');
    if (!form) return;
    var button = event.submitter;
    if (!button) return;
    button.disabled = true;
    button.setAttribute('aria-live', 'polite');
    button.textContent = form.dataset.checking;
  });
})();
