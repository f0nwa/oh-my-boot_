// Interactive SHA-256 chips: click to expand full 64-char hash, copy to clipboard, click to collapse.
(function () {
  'use strict';

  function copyText(text, btn) {
    if (!navigator.clipboard || !navigator.clipboard.writeText) {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand('copy'); } catch (e) {}
      document.body.removeChild(ta);
      showCopied(btn);
      return;
    }
    navigator.clipboard.writeText(text).then(function () {
      showCopied(btn);
    }).catch(function () {});
  }

  function showCopied(btn) {
    if (!btn) return;
    btn.classList.add('is-copied');
    var svg = btn.querySelector('svg');
    if (svg) {
      svg.innerHTML = '<polyline points="20 6 9 17 4 12" stroke="currentColor" stroke-width="2.5" fill="none"></polyline>';
    }
    var origTitle = btn.getAttribute('title') || '';
    btn.setAttribute('title', 'Скопировано!');
    setTimeout(function () {
      btn.classList.remove('is-copied');
      btn.setAttribute('title', origTitle);
      if (svg) {
        svg.innerHTML = '<rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>';
      }
    }, 1500);
  }

  function toggleChip(chip) {
    var isExpanded = chip.classList.contains('is-expanded');
    var shortSha = chip.getAttribute('data-short') || '';
    var fullSha = chip.getAttribute('data-full') || '';
    var textEl = chip.querySelector('.sha-chip-text');

    if (!isExpanded) {
      chip.classList.add('is-expanded');
      chip.setAttribute('aria-expanded', 'true');
      if (textEl && fullSha) {
        textEl.textContent = fullSha;
      }
    } else {
      chip.classList.remove('is-expanded');
      chip.setAttribute('aria-expanded', 'false');
      if (textEl && shortSha) {
        textEl.textContent = shortSha;
      }
    }
  }

  document.addEventListener('click', function (e) {
    var copyBtn = e.target.closest('[data-sha-copy]');
    if (copyBtn) {
      e.preventDefault();
      e.stopPropagation();
      var chip = copyBtn.closest('[data-sha-chip]');
      var full = chip ? chip.getAttribute('data-full') : '';
      if (full) {
        copyText(full, copyBtn);
      }
      return;
    }

    var chip = e.target.closest('[data-sha-chip]');
    if (!chip) return;

    toggleChip(chip);
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' || e.key === ' ') {
      var chip = e.target.closest('[data-sha-chip]');
      if (chip && e.target === chip) {
        e.preventDefault();
        toggleChip(chip);
      }
    }
  });
})();
