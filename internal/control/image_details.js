(function () {
  document.querySelectorAll('[data-image-dialog]').forEach(function (trigger) {
    var dialog = document.getElementById(trigger.dataset.imageDialog);
    if (!dialog) return;
    trigger.addEventListener('click', function () {
      if (typeof dialog.showModal === 'function') dialog.showModal();
      else dialog.setAttribute('open', '');
      var name = dialog.querySelector('input[name="name"]');
      if (name) name.focus();
    });
    dialog.querySelectorAll('[data-image-close]').forEach(function (button) {
      button.addEventListener('click', function () {
        if (typeof dialog.close === 'function') dialog.close();
        else dialog.removeAttribute('open');
        trigger.focus();
      });
    });
  });
})();
