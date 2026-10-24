// Answer file editor: sections in place, variant blocks, list rows, tabs and
// a live summary/XML preview. The page works without this script.
(function () {
  'use strict';
  var form = document.getElementById('editor');
  if (!form) return;

  // Variant blocks: show the one whose radio is chosen.
  function syncVariants() {
    form.querySelectorAll('.variant[data-for]').forEach(function (block) {
      var checked = form.querySelector('input[type=radio][name="' + block.dataset.for + '"]:checked');
      block.hidden = !checked || checked.value !== block.dataset.value;
    });
  }

  // Sections.
  var order = Array.prototype.map.call(form.querySelectorAll('.ed-toc-item'), function (b) { return b.dataset.section; });
  var current = document.getElementById('ed-section');
  function show(id) {
    if (order.indexOf(id) < 0) return;
    current.value = id;
    form.querySelectorAll('.ed-section').forEach(function (s) { s.hidden = s.dataset.section !== id; });
    form.querySelectorAll('.ed-toc-item').forEach(function (b) {
      var on = b.dataset.section === id;
      b.classList.toggle('on', on);
      if (on) b.setAttribute('aria-current', 'true'); else b.removeAttribute('aria-current');
    });
    var main = form.querySelector('.ed-main');
    if (main && main.getBoundingClientRect().top < 0) main.scrollIntoView({ block: 'start' });
  }
  form.querySelectorAll('.ed-toc-item').forEach(function (b) {
    b.addEventListener('click', function (e) { e.preventDefault(); show(b.dataset.section); });
  });
  form.querySelectorAll('[data-step]').forEach(function (b) {
    b.addEventListener('click', function (e) {
      e.preventDefault();
      var i = order.indexOf(current.value) + Number(b.dataset.step);
      show(order[(i + order.length) % order.length]);
    });
  });

  // New list rows: copy the last row with the next number, emptied.
  form.querySelectorAll('[data-add]').forEach(function (b) {
    b.addEventListener('click', function (e) {
      var path = b.dataset.add;
      var table = form.querySelector('table[data-rows="' + path + '"]');
      if (!table) return;
      e.preventDefault();
      var rows = table.querySelectorAll('tr[data-row]');
      var last = rows[rows.length - 1];
      var prefix = path + '.';
      var max = -1;
      table.querySelectorAll('[name^="' + prefix + '"]').forEach(function (el) {
        var n = parseInt(el.name.slice(prefix.length), 10);
        if (n > max) max = n;
      });
      var row = last.cloneNode(true);
      row.querySelectorAll('[name]').forEach(function (el) {
        var rest = el.name.slice(prefix.length);
        el.name = prefix + (max + 1) + rest.slice(rest.indexOf('.'));
        if (el.type === 'checkbox') el.checked = false;
        else if (el.tagName === 'SELECT') el.selectedIndex = 0;
        else el.value = '';
      });
      last.parentNode.appendChild(row);
      var first = row.querySelector('input,textarea');
      if (first) first.focus();
    });
  });

  // Tabs of the side panel.
  var panel = form.querySelector('.ed-panel');
  function tab(name) {
    panel.querySelectorAll('[data-tab]').forEach(function (b) { b.classList.toggle('on', b.dataset.tab === name); b.setAttribute('aria-selected', b.dataset.tab === name); });
    panel.querySelectorAll('[data-panel]').forEach(function (p) { p.hidden = p.dataset.panel !== name; });
  }
  panel.querySelectorAll('[data-tab]').forEach(function (b) { b.addEventListener('click', function () { tab(b.dataset.tab); }); });
  tab('summary');

  // Live preview.
  var xml = document.getElementById('ed-xml');
  var lastLines = new Set(xml.textContent.split('\n'));
  var timer = null, seq = 0;
  function text(el, s) { el.textContent = s; }
  function li(label, value, danger) {
    var item = document.createElement('li');
    if (label !== null) {
      var k = document.createElement('span'); k.className = 'k'; k.textContent = label; item.appendChild(k);
    }
    var v = document.createElement('span'); v.textContent = value; if (danger) v.className = 'ed-danger'; item.appendChild(v);
    return item;
  }
  function render(data) {
    var error = document.getElementById('ed-error');
    error.hidden = !data.error; text(error, data.error || '');
    var summary = document.getElementById('ed-summary');
    summary.replaceChildren.apply(summary, (data.summary || []).map(function (l) { return li(l.Label, l.Text, l.Danger); }));
    var questions = document.getElementById('ed-questions');
    var warnings = data.warnings || [];
    if (warnings.length) questions.replaceChildren.apply(questions, warnings.map(function (w) { return li(null, w.Text, false); }));
    else { var none = document.createElement('li'); none.className = 'muted'; none.textContent = form.dataset.noquestions; questions.replaceChildren(none); }
    text(document.getElementById('ed-qcount'), String(warnings.length));
    document.getElementById('ed-wipes').hidden = !data.wipes;
    if (data.xml) {
      var lines = data.xml.split('\n');
      xml.replaceChildren.apply(xml, lines.map(function (line) {
        var s = document.createElement('span');
        s.textContent = line + '\n';
        if (!lastLines.has(line)) s.className = 'hl';
        return s;
      }));
      lastLines = new Set(lines);
      var first = xml.querySelector('.hl');
      if (first) xml.scrollTop = Math.max(0, first.offsetTop - xml.offsetTop - 40);
      text(document.getElementById('ed-xmlsize'), Math.max(1, Math.round(data.size / 1024)) + ' KB');
    }
    Object.keys(data.marks || {}).forEach(function (id) {
      var m = data.marks[id], el = form.querySelector('[data-mark="' + id + '"]');
      if (!el) return;
      el.classList.toggle('bad', m.questions > 0);
      el.classList.toggle('chg', !m.questions && !!m.mark);
      el.textContent = m.questions > 0 ? m.questions + ' ' + form.dataset.questions : (m.mark === 'changed' ? form.dataset.changed : (m.mark || ''));
    });
  }
  function preview() {
    var body = new URLSearchParams(new FormData(form));
    body.set('do', 'preview');
    var mine = ++seq;
    fetch(form.dataset.preview, { method: 'POST', body: body, credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (data) { if (data && mine === seq) render(data); })
      .catch(function () {});
  }
  function schedule() { clearTimeout(timer); timer = setTimeout(preview, 400); }
  form.addEventListener('input', schedule);
  form.addEventListener('change', function (e) { if (e.target.matches('[data-variant]')) syncVariants(); schedule(); });
  form.querySelectorAll('[data-nojs]').forEach(function (el) { el.hidden = true; });
  syncVariants();
})();
