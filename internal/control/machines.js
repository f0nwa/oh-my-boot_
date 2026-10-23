// Saves a machine row edited in the table without reloading the page. The
// form posts as it would without JavaScript; the new row and counters are
// taken from the page the server answers with.
(function () {
  if (!window.fetch || !window.DOMParser || !window.FormData || !window.URLSearchParams) return;
  document.addEventListener('submit', function (ev) {
    var form = ev.target;
    if (!form.matches || !form.matches('form[data-row-save]')) return;
    var row = form.closest('tr');
    if (!row || !row.id) return;
    ev.preventDefault();
    var button = form.querySelector('button');
    var status = form.querySelector('[data-row-status]');
    function fail(text) {
      if (status) { status.textContent = text || status.getAttribute('data-failed'); status.hidden = false; }
      if (button) button.disabled = false;
    }
    if (button) button.disabled = true;
    // The server reads ordinary forms as urlencoded, not multipart.
    fetch(form.action, { method: 'POST', body: new URLSearchParams(new FormData(form)), credentials: 'same-origin', headers: { 'Accept': 'text/html' } })
      .then(function (res) {
        return res.text().then(function (html) {
          var doc = new DOMParser().parseFromString(html, 'text/html');
          if (!res.ok) {
            var err = doc.querySelector('.notice.error');
            return fail(err ? err.textContent : '');
          }
          var next = doc.getElementById(row.id);
          if (!next) { location.href = res.url; return; } // no longer matches the search
          row.replaceWith(next);
          var saved = next.querySelector('[data-row-saved]');
          if (saved) saved.hidden = false;
          var counts = doc.querySelector('.machine-counts'), here = document.querySelector('.machine-counts');
          if (counts && here) here.innerHTML = counts.innerHTML;
          var link = next.querySelector('a');
          if (link) link.focus();
        });
      })
      .catch(function () { fail(''); });
  });
})();
