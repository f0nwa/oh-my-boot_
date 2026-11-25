(function () {
  var lastTrigger = null;

  document.addEventListener('click', function (event) {
    var trigger = event.target.closest && event.target.closest('[data-image-dialog]');
    if (trigger) {
      var dialog = document.getElementById(trigger.dataset.imageDialog);
      if (!dialog) return;
      lastTrigger = trigger;
      if (typeof dialog.showModal === 'function') dialog.showModal();
      else dialog.setAttribute('open', '');
      var name = dialog.querySelector('input[name="name"]');
      if (name) name.focus();
      return;
    }

    var closeButton = event.target.closest && event.target.closest('[data-image-close]');
    if (closeButton) {
      var openDialog = closeButton.closest('dialog');
      if (!openDialog) return;
      if (typeof openDialog.close === 'function') openDialog.close();
      else openDialog.removeAttribute('open');
      if (lastTrigger && document.contains(lastTrigger)) {
        lastTrigger.focus();
      } else if (openDialog.id) {
        var matchedTrigger = document.querySelector('[data-image-dialog="' + openDialog.id + '"]');
        if (matchedTrigger) matchedTrigger.focus();
      }
      lastTrigger = null;
    }
  });

  var form = document.querySelector('form[data-image-search]');
  var results = document.getElementById('image-results');
  var filters = document.getElementById('image-filters');
  var error = document.getElementById('image-search-error');
  if (!form || !results || !filters || !error || !form.elements.q) return;

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
          var nextResults = page.getElementById('image-results');
          var nextFilters = page.getElementById('image-filters');
          if (!nextResults || !nextFilters) throw new Error('invalid search response');
          results.replaceChildren.apply(results, Array.from(nextResults.childNodes));
          filters.replaceChildren.apply(filters, Array.from(nextFilters.childNodes));
          history.replaceState(null, '', url);
          document.dispatchEvent(new CustomEvent('omb:images-updated'));
        })
        .catch(function (err) {
          if (current === sequence && err.name !== 'AbortError') error.hidden = false;
        });
    }, 150);
  }

  input.addEventListener('input', update);
})();
