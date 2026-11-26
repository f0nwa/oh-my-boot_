// live_refresh.js — universal in-place background table updater for OMB.
// Polls the current URL at a set interval (default 15s) and seamlessly replaces
// target containers via DOMParser and replaceChildren. Pauses when tabs are hidden,
// modal dialogs are open, popovers are visible, or inputs have active focus.
(function (win) {
  'use strict';

  var doc = win.document;
  if (!doc || !win.fetch || !win.DOMParser) return;

  var refreshNodes = doc.querySelectorAll('[data-live-refresh]');
  if (!refreshNodes.length) return;

  var minInterval = 15;
  for (var i = 0; i < refreshNodes.length; i++) {
    var sec = parseInt(refreshNodes[i].getAttribute('data-live-refresh'), 10);
    if (sec > 0 && sec < minInterval) minInterval = sec;
  }

  function hasOpenOverlay() {
    if (doc.querySelector('dialog[open]')) return true;
    try {
      if (doc.querySelector('.row-menu-popover:popover-open')) return true;
    } catch (_) {}
    return false;
  }

  function hasActiveInput() {
    var el = doc.activeElement;
    if (!el) return false;
    var tag = el.tagName;
    return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';
  }

  function hasActiveSelection() {
    return !!doc.querySelector('[data-bulk-select-row]:checked');
  }

  var isFetching = false;
  var currentController = null;

  function refreshNow() {
    if (doc.hidden || hasOpenOverlay() || hasActiveInput() || hasActiveSelection() || isFetching) return;
    if (currentController && typeof currentController.abort === 'function') {
      currentController.abort();
    }
    currentController = typeof win.AbortController === 'function' ? new win.AbortController() : null;
    isFetching = true;

    var url = win.location.href;
    var fetchOpts = {
      credentials: 'same-origin',
      headers: { 'Accept': 'text/html' }
    };
    if (currentController) fetchOpts.signal = currentController.signal;

    win.fetch(url, fetchOpts)
      .then(function (res) {
        if (!res.ok || res.redirected) throw new Error('refresh unavailable');
        return res.text();
      })
      .then(function (html) {
        isFetching = false;
        currentController = null;
        if (!html || hasOpenOverlay() || hasActiveInput() || hasActiveSelection()) return;

        var parsed = new win.DOMParser().parseFromString(html, 'text/html');
        var nodes = doc.querySelectorAll('[data-live-refresh]');
        function syncTarget(sel) {
          var currentEl = doc.querySelector(sel);
          var freshEl = parsed.querySelector(sel);
          if (currentEl && freshEl) {
            if (typeof currentEl.replaceChildren === 'function') {
              currentEl.replaceChildren.apply(currentEl, Array.from(freshEl.childNodes));
            } else {
              currentEl.innerHTML = freshEl.innerHTML;
            }
          }
        }

        for (var n = 0; n < nodes.length; n++) {
          var node = nodes[n];
          var targetsAttr = node.getAttribute('data-live-targets');
          var targets = targetsAttr ? targetsAttr.split(',').map(function (s) { return s.trim(); }).filter(Boolean) : [];
          if (!targets.length) {
            if (node.id) targets = ['#' + node.id];
            else continue;
          }

          for (var t = 0; t < targets.length; t++) {
            syncTarget(targets[t]);
          }
        }
        syncTarget('#topnav');
        syncTarget('#subnav');
        doc.dispatchEvent(new win.Event('omb:bulk-sync'));
      })
      .catch(function () {
        isFetching = false;
        currentController = null;
      });
  }

  var timer = win.setInterval(function () {
    if (!doc.hidden && !hasOpenOverlay() && !hasActiveInput() && !hasActiveSelection()) {
      refreshNow();
    }
  }, minInterval * 1000);

  doc.addEventListener('visibilitychange', function () {
    if (!doc.hidden) {
      refreshNow();
    }
  });

  doc.addEventListener('omb:refresh', function () {
    refreshNow();
  });

  win.OMB = win.OMB || {};
  win.OMB.refresh = refreshNow;
})(typeof window !== 'undefined' ? window : this);
