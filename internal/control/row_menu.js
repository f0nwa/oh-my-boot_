(function () {
  'use strict';

  var gap = 6;
  var margin = 8;
  var active = null;

  function place() {
    if (!active || !active.panel.matches(':popover-open')) return;
    var rect = active.button.getBoundingClientRect();
    var panel = active.panel;
    var left = Math.max(margin, Math.min(rect.right - panel.offsetWidth, window.innerWidth - panel.offsetWidth - margin));
    var below = rect.bottom + gap;
    var top = below + panel.offsetHeight <= window.innerHeight - margin
      ? below
      : Math.max(margin, rect.top - panel.offsetHeight - gap);
    panel.style.left = left + 'px';
    panel.style.top = top + 'px';
  }

  // Capture toggle events so menus added by live search work without rebinding.
  document.addEventListener('toggle', function (event) {
    var panel = event.target;
    if (!panel.matches || !panel.matches('.row-menu-popover[popover]')) return;
    var button = document.querySelector('button[data-row-menu][popovertarget="' + CSS.escape(panel.id) + '"]');
    if (!button) return;
    var open = event.newState === 'open';
    button.setAttribute('aria-expanded', open ? 'true' : 'false');
    if (open) {
      if (active && active.panel !== panel && active.panel.matches(':popover-open') && typeof active.panel.hidePopover === 'function') {
        active.panel.hidePopover();
      }
      active = { button: button, panel: panel };
      place();
    } else if (active && active.panel === panel) {
      active = null;
    }
  }, true);

  document.addEventListener('click', function (event) {
    var item = event.target.closest('[data-close-popover]');
    if (!item) return;
    var panel = item.closest('[popover]');
    if (panel && panel.matches(':popover-open') && typeof panel.hidePopover === 'function') {
      panel.hidePopover();
    }
  });

  window.addEventListener('resize', place);
  window.addEventListener('scroll', place, true);
})();
