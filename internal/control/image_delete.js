(() => {
  document.addEventListener('click', (event) => {
    const button = event.target.closest('[data-image-delete-open]');
    if (button) {
      if (button.disabled) return;
      const dialog = document.getElementById(button.dataset.imageDeleteOpen);
      if (!dialog) return;
      const menu = button.closest('[popover]');
      if (menu && menu.matches(':popover-open')) menu.hidePopover();
      dialog.showModal();
      return;
    }
    const close = event.target.closest('[data-image-delete-close]');
    if (close) close.closest('dialog').close();
  });
})();
