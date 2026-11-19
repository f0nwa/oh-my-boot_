// Refreshes the «Сейчас» page in place while it is visible, pausing when a
// row menu popover or confirmation dialog is open, and wires session reset /
// DHCP ignore confirmation modals.
(function () {
  var live = document.getElementById('live');

  function hasOpenOverlay() {
    if (document.querySelector('dialog[open]')) return true;
    try {
      if (document.querySelector('.row-menu-popover:popover-open')) return true;
    } catch (_) {}
    return false;
  }

  function showDialog(dlg) {
    if (!dlg) return;
    if (typeof dlg.showModal === 'function') dlg.showModal();
    else dlg.setAttribute('open', '');
  }

  function closeDialog(dlg) {
    if (!dlg) return;
    if (typeof dlg.close === 'function') dlg.close();
    else dlg.removeAttribute('open');
  }

  if (live && window.fetch && window.DOMParser) {
    var seconds = parseInt(live.getAttribute('data-refresh'), 10) || 15;
    function refresh() {
      if (document.hidden || hasOpenOverlay()) return;
      fetch(location.href, { credentials: 'same-origin', headers: { 'Accept': 'text/html' } })
        .then(function (res) { return res.ok ? res.text() : null; })
        .then(function (html) {
          if (!html || hasOpenOverlay()) return;
          var next = new DOMParser().parseFromString(html, 'text/html').getElementById('live');
          if (next) live.innerHTML = next.innerHTML;
        })
        .catch(function () {});
    }
    setInterval(refresh, seconds * 1000);
    document.addEventListener('visibilitychange', function () { if (!document.hidden) refresh(); });
  }

  document.addEventListener('click', function (event) {
    var closeBtn = event.target.closest('dialog [data-close-dialog]');
    if (closeBtn) {
      closeDialog(closeBtn.closest('dialog'));
      return;
    }

    var smbBtn = event.target.closest('[data-smb-disconnect-open]');
    if (smbBtn) {
      var dlg = document.getElementById('now-smb-disconnect-dialog');
      if (!dlg) return;
      var form = dlg.querySelector('form');
      if (form) {
        form.querySelector('[name="mac"]').value = smbBtn.dataset.mac || '';
        form.querySelector('[name="session_id"]').value = smbBtn.dataset.sessionId || '';
        form.querySelector('[name="ip"]').value = smbBtn.dataset.ip || '';
        form.querySelector('[name="pid"]').value = smbBtn.dataset.pid || '';
      }
      var summary = document.getElementById('smb-dlg-summary');
      if (summary) {
        var parts = [];
        if (smbBtn.dataset.label) parts.push(smbBtn.dataset.label);
        if (smbBtn.dataset.ip) parts.push(smbBtn.dataset.ip);
        if (smbBtn.dataset.profile) parts.push('\\\\ombisos\\' + smbBtn.dataset.profile);
        if (smbBtn.dataset.pid) parts.push('PID ' + smbBtn.dataset.pid);
        summary.textContent = parts.join(' · ');
      }
      showDialog(dlg);
      return;
    }

    var httpBtn = event.target.closest('[data-http-cancel-open]');
    if (httpBtn) {
      var dlg = document.getElementById('now-http-cancel-dialog');
      if (!dlg) return;
      var form = dlg.querySelector('form');
      if (form) {
        form.querySelector('[name="mac"]').value = httpBtn.dataset.mac || '';
        form.querySelector('[name="transfer_id"]').value = httpBtn.dataset.transferId || '';
      }
      var summary = document.getElementById('http-dlg-summary');
      if (summary) {
        var parts = [];
        if (httpBtn.dataset.label) parts.push(httpBtn.dataset.label);
        if (httpBtn.dataset.path) parts.push(httpBtn.dataset.path);
        summary.textContent = parts.join(' · ');
      }
      showDialog(dlg);
      return;
    }

    var dhcpBtn = event.target.closest('[data-dhcp-ignore-open]');
    if (dhcpBtn) {
      var dlg = document.getElementById('now-dhcp-ignore-dialog');
      if (!dlg) return;
      var mac = dhcpBtn.dataset.mac || '';
      var ignore = dhcpBtn.dataset.ignored !== '0';
      var form = document.getElementById('dhcp-dlg-form');
      if (form) {
        form.action = '/events/machines/' + encodeURIComponent(mac) + '/dhcp-ignore';
        form.querySelector('[name="ignored"]').value = ignore ? '1' : '0';
      }
      var macEl = document.getElementById('dhcp-dlg-mac');
      if (macEl) macEl.textContent = mac;
      var desc = document.getElementById('dhcp-dlg-desc');
      if (desc) desc.textContent = ignore ? (desc.dataset.ignoreText || '') : (desc.dataset.allowText || '');
      var submit = document.getElementById('dhcp-dlg-submit');
      if (submit) {
        submit.textContent = ignore ? (submit.dataset.ignoreLabel || '') : (submit.dataset.allowLabel || '');
        submit.classList.toggle('btn-danger', ignore);
        submit.classList.toggle('btn-primary', !ignore);
      }
      showDialog(dlg);
      return;
    }

    if (event.target.tagName === 'DIALOG') {
      closeDialog(event.target);
    }
  });
})();
