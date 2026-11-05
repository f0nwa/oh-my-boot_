(() => {
  const gap = 6;
  const margin = 8;
  let active;
  function place() {
    if (!active || !active.panel.matches(':popover-open')) return;
    const button = active.button.getBoundingClientRect();
    const panel = active.panel;
    const left = Math.max(margin, Math.min(button.right - panel.offsetWidth, innerWidth - panel.offsetWidth - margin));
    const below = button.bottom + gap;
    const top = below + panel.offsetHeight <= innerHeight - margin ? below : Math.max(margin, button.top - panel.offsetHeight - gap);
    panel.style.left = `${left}px`;
    panel.style.top = `${top}px`;
  }
  document.querySelectorAll('button[popovertarget^="image-delete-"]').forEach((button) => {
    const panel = document.getElementById(button.getAttribute('popovertarget'));
    if (!panel) return;
    panel.addEventListener('toggle', (event) => {
      if (event.newState === 'open') {
        active = {button, panel};
        place();
      } else if (active && active.panel === panel) {
        active = null;
      }
    });
  });
  window.addEventListener('resize', place);
  window.addEventListener('scroll', place, true);
})();
