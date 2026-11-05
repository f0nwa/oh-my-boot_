// ISO inspection can take several minutes on network storage.
(function () {
  document.querySelectorAll('form[data-ubuntu-inspect]').forEach(function (form) {
    form.addEventListener('submit', function (event) {
      var button = event.submitter;
      if (!button) return;
      button.disabled = true;
      button.setAttribute('aria-live', 'polite');
      button.textContent = form.dataset.checking;
    });
  });
})();
