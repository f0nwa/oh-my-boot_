// Keep the server's search and status filters authoritative while updating
// results without interrupting typing. The ordinary GET form works without JS.
(function () {
  var form = document.querySelector('[data-download-search]');
  var results = document.getElementById('download-results');
  var filters = document.getElementById('download-filters');
  var error = document.getElementById('download-search-error');
  if (!form || !results || !filters || !error) return;

  document.addEventListener('click', function (event) {
    document.querySelectorAll('.download-delete[open]').forEach(function (menu) {
      if (!menu.contains(event.target)) menu.open = false;
    });
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
