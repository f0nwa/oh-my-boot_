// Keep the server's search and status filters authoritative while updating
// results without interrupting typing. The ordinary GET form works without JS.
(function () {
  var form = document.querySelector('[data-download-search]');
  var results = document.getElementById('download-results');
  var filters = document.getElementById('download-filters');
  var error = document.getElementById('download-search-error');
  if (!form || !results || !filters || !error) return;

  document.addEventListener('click', function (event) {
    var button = event.target.closest('[data-download-delete-open]');
    if (button) {
      if (button.disabled) return;
      var dialog = document.getElementById(button.getAttribute('data-download-delete-open'));
      if (!dialog) return;
      var menu = button.closest('[popover]');
      if (menu && menu.matches(':popover-open')) menu.hidePopover();
      dialog.showModal();
      return;
    }
    var close = event.target.closest('[data-download-delete-close]');
    if (close) close.closest('dialog').close();
  });

  var input = form.elements.q;
  var timer, pending, sequence = 0;

  function update() {
    var current = ++sequence;
    clearTimeout(timer);
    if (pending) pending.abort();
    error.hidden = true;
    timer = setTimeout(function () {
      var url = new URL(form.action, window.location.href);
      url.search = new URLSearchParams(new FormData(form)).toString();
      pending = new AbortController();
      fetch(url, { credentials: 'same-origin', signal: pending.signal })
        .then(function (response) {
          if (!response.ok || response.redirected) throw new Error('search unavailable');
          return response.text();
        })
        .then(function (html) {
          if (current !== sequence) return;
          var page = new DOMParser().parseFromString(html, 'text/html');
          var nextResults = page.getElementById('download-results');
          var nextFilters = page.getElementById('download-filters');
          if (!nextResults || !nextFilters) throw new Error('invalid search response');
          results.replaceChildren.apply(results, Array.from(nextResults.childNodes));
          filters.replaceChildren.apply(filters, Array.from(nextFilters.childNodes));
          history.replaceState(null, '', url);
        })
        .catch(function (err) {
          if (current === sequence && err.name !== 'AbortError') error.hidden = false;
        });
    }, 150);
  }

  input.addEventListener('input', update);
})();
