// Refreshes the «Сейчас» page in place while it is visible, so the machine
// steps move without reloading the whole page or losing the scroll position.
(function () {
  var live = document.getElementById('live');
  if (!live || !window.fetch || !window.DOMParser) return;
  var seconds = parseInt(live.getAttribute('data-refresh'), 10) || 15;
  function refresh() {
    if (document.hidden) return;
    fetch(location.href, { credentials: 'same-origin', headers: { 'Accept': 'text/html' } })
      .then(function (res) { return res.ok ? res.text() : null; })
      .then(function (html) {
        if (!html) return;
        var next = new DOMParser().parseFromString(html, 'text/html').getElementById('live');
        if (next) live.innerHTML = next.innerHTML;
      })
      .catch(function () {});
  }
  setInterval(refresh, seconds * 1000);
  document.addEventListener('visibilitychange', function () { if (!document.hidden) refresh(); });
})();
