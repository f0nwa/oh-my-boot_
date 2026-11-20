(() => {
  const form = document.querySelector('.login form, form[action="/login"]');
  if (!form) return;

  const userInput = form.querySelector('input[name="username"]');
  const passInput = form.querySelector('input[name="password"]');
  const submitBtn = form.querySelector('button.btn-primary, button[type="submit"], button');
  if (!userInput || !passInput || !submitBtn) return;

  const authModeInput = form.querySelector('#login-auth-mode');
  const userLabel = form.querySelector('label[for="login-username"]');
  const modeButtons = form.querySelectorAll('[data-login-mode]');
  if (authModeInput && modeButtons.length > 0) {
    modeButtons.forEach(btn => {
      btn.addEventListener('click', () => {
        const mode = btn.getAttribute('data-login-mode') || 'local';
        authModeInput.value = mode;
        modeButtons.forEach(b => b.classList.toggle('is-active', b === btn));
        if (userLabel && btn.getAttribute('data-label-user')) {
          userLabel.textContent = btn.getAttribute('data-label-user');
        }
        if (btn.getAttribute('data-label-submit')) {
          submitBtn.textContent = btn.getAttribute('data-label-submit');
        }
      });
    });
  }

  const userWrapper = userInput.closest('.field-float');
  const passWrapper = passInput.closest('.field-float');

  function sync() {
    const userVal = userInput.value ? userInput.value.trim() : '';
    const passVal = passInput.value || '';

    if (userWrapper) {
      userWrapper.classList.toggle('is-floating', userInput.value.length > 0);
    }
    if (passWrapper) {
      passWrapper.classList.toggle('is-floating', passInput.value.length > 0);
    }

    const ready = userVal.length > 0 && passVal.length > 0;
    submitBtn.disabled = !ready;
  }

  form.addEventListener('input', sync);
  form.addEventListener('change', sync);

  sync();
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', sync);
  }
  window.addEventListener('load', sync);
  setTimeout(sync, 50);
  setTimeout(sync, 200);
  setTimeout(sync, 500);
  setTimeout(sync, 1000);

  form.addEventListener('submit', e => {
    sync();
    if (submitBtn.disabled) {
      e.preventDefault();
    }
  });

  const svg = document.querySelector('.login-net');
  if (!svg) return;

  const NS = 'http://www.w3.org/2000/svg';
  const server = { x: 960, y: 540 };
  const coords = [
    [280, 200], [250, 540], [290, 880],
    [580, 150], [580, 930], [1340, 150], [1340, 930],
    [1640, 200], [1670, 540], [1630, 880]
  ];

  function makeSequence(idx) {
    const loader = idx % 3 === 2 ? 'undionly.kpxe' : (idx % 3 === 1 ? 'snponly-shim.efi' : 'ipxe-shim.efi');
    return [
      { dir: 1, color: 'var(--faint)', label: 'DHCP DISCOVER' },
      { dir: -1, color: 'var(--accent)', label: 'OFFER' },
      { dir: -1, color: 'var(--accent)', label: loader },
      { dir: -1, color: 'var(--accent)', label: 'boot/menu.ipxe' },
      { dir: -1, color: 'var(--ok)', label: 'menu ready' }
    ];
  }

  function svgEl(parent, tag, attrs) {
    const el = document.createElementNS(NS, tag);
    for (const k in attrs) el.setAttribute(k, attrs[k]);
    parent.appendChild(el);
    return el;
  }

  const nodes = coords.map(([x, y], idx) => {
    const g = svgEl(svg, 'g', { class: 'login-net-node' });
    svgEl(g, 'line', { x1: x, y1: y, x2: server.x, y2: server.y });
    svgEl(g, 'rect', { class: 'node-box', x: x - 13, y: y - 10, width: 26, height: 18, rx: 3 });
    svgEl(g, 'line', { class: 'node-stand', x1: x - 6, y1: y + 12, x2: x + 6, y2: y + 12 });
    const lbl = svgEl(g, 'text', {
      class: 'node-lbl',
      x: x,
      y: y < server.y ? y - 20 : y + 32,
      'text-anchor': 'middle'
    });
    const pkt = svgEl(g, 'circle', { class: 'pkt', cx: 0, cy: 0, r: 4 });
    return { x, y, g, lbl, pkt, seq: makeSequence(idx), step: -1, timer: 0, moveTimer: 0 };
  });

  const narrowMQ = window.matchMedia('(max-width: 759px)');
  const reducedMQ = window.matchMedia('(prefers-reduced-motion: reduce)');
  let running = false;

  function clearNodeTimers(n) {
    if (n.timer) { clearTimeout(n.timer); n.timer = 0; }
    if (n.moveTimer) { clearTimeout(n.moveTimer); n.moveTimer = 0; }
  }

  function applyStaticFrame() {
    nodes.forEach((n, idx) => {
      clearNodeTimers(n);
      n.pkt.style.transition = 'none';
      n.pkt.style.opacity = '0';
      if (idx === 0 || idx === 4 || idx === 7) {
        n.g.setAttribute('class', 'login-net-node is-done');
        n.lbl.textContent = 'menu ready';
      } else if (idx === 2 || idx === 5 || idx === 8) {
        n.g.setAttribute('class', 'login-net-node is-work');
        n.lbl.textContent = n.seq[2].label;
      } else {
        n.g.setAttribute('class', 'login-net-node');
        n.lbl.textContent = '';
      }
    });
  }

  function stepNode(n) {
    if (!running) return;
    if (n.step < 0) {
      n.g.setAttribute('class', 'login-net-node is-work');
      n.step = 0;
    }
    if (n.step < n.seq.length) {
      const p = n.seq[n.step];
      const from = p.dir > 0 ? [n.x, n.y] : [server.x, server.y];
      const to = p.dir > 0 ? [server.x, server.y] : [n.x, n.y];
      n.pkt.style.transition = 'none';
      n.pkt.style.transform = 'translate(' + from[0] + 'px,' + from[1] + 'px)';
      n.pkt.setAttribute('fill', p.color);
      n.pkt.style.opacity = '1';
      n.lbl.textContent = p.label;
      n.step++;
      n.moveTimer = setTimeout(() => {
        if (!running) return;
        n.pkt.style.transition = 'transform 0.9s cubic-bezier(.4,0,.2,1), opacity 0.2s';
        n.pkt.style.transform = 'translate(' + to[0] + 'px,' + to[1] + 'px)';
      }, 25);
      n.timer = setTimeout(() => stepNode(n), 1150);
    } else {
      n.pkt.style.opacity = '0';
      n.g.setAttribute('class', 'login-net-node is-done');
      n.lbl.textContent = 'menu ready';
      n.timer = setTimeout(() => {
        if (!running) return;
        n.g.setAttribute('class', 'login-net-node');
        n.lbl.textContent = '';
        n.step = -1;
        n.timer = setTimeout(() => stepNode(n), 1500 + Math.random() * 3200);
      }, 2200);
    }
  }

  function syncNetAnimation() {
    const shouldRun = !reducedMQ.matches && !narrowMQ.matches && document.visibilityState !== 'hidden';
    if (!shouldRun) {
      running = false;
      applyStaticFrame();
      return;
    }
    if (running) return;
    running = true;
    nodes.forEach((n, idx) => {
      clearNodeTimers(n);
      n.step = -1;
      n.g.setAttribute('class', 'login-net-node');
      n.lbl.textContent = '';
      n.pkt.style.opacity = '0';
      n.timer = setTimeout(() => stepNode(n), idx < 2 ? 120 + idx * 260 : 400 + idx * 500 + Math.random() * 800);
    });
  }

  document.addEventListener('visibilitychange', syncNetAnimation);
  if (narrowMQ.addEventListener) narrowMQ.addEventListener('change', syncNetAnimation);
  if (reducedMQ.addEventListener) reducedMQ.addEventListener('change', syncNetAnimation);
  window.addEventListener('pagehide', () => {
    running = false;
    nodes.forEach(clearNodeTimers);
  });

  syncNetAnimation();
})();
