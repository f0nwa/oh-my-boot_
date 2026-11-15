(function() {
  var form = document.getElementById('network-edit-form');
  var card = document.getElementById('network-scenario-card');
  if (!form || !card) return;

  var selLoader = form.querySelector('select[name="uefi_loader"]');
  var selMode = form.querySelector('select[name="mode"]');
  var selDriver = form.querySelector('select[name="default_driver"]');
  var chkReuse = form.querySelector('input[name="reuse_firmware_network"]');
  var ipxeGroup = document.getElementById('ipxe-options');
  var grubNote = document.getElementById('grub-loader-note');
  var saveBtn = form.querySelector('.form-actions button');

  var dynFile = document.getElementById('dyn-file');
  var dynDriver = document.getElementById('dyn-driver');
  var dynMenu = document.getElementById('dyn-menu');
  var condMode = document.getElementById('dyn-cond-mode');
  var condLoader = document.getElementById('dyn-cond-loader');
  var condDriver = document.getElementById('dyn-cond-driver');
  var dynSummary = document.getElementById('dyn-summary');

  var initialValues = {};
  var trackedElements = form.querySelectorAll('input:not([type="hidden"]), select, textarea');
  for (var i = 0; i < trackedElements.length; i++) {
    var el = trackedElements[i];
    if (!el.name) continue;
    if (el.type === 'checkbox') {
      initialValues[el.name] = el.checked;
    } else {
      initialValues[el.name] = el.value;
    }
  }

  function isFormChanged() {
    for (var i = 0; i < trackedElements.length; i++) {
      var el = trackedElements[i];
      if (!el.name) continue;
      if (el.type === 'checkbox') {
        if (el.checked !== initialValues[el.name]) return true;
      } else {
        if (el.value !== initialValues[el.name]) return true;
      }
    }
    return false;
  }

  function updateSaveButton() {
    if (!saveBtn) return;
    var changed = isFormChanged();
    var valid = typeof form.checkValidity === 'function' ? form.checkValidity() : true;
    saveBtn.disabled = !changed || !valid;
  }

  function update() {
    var isGrub = selLoader.value === 'grub-test';
    var isFull = selMode.value === 'full';
    var isSnp = selDriver.value === 'snp';
    var isReuse = chkReuse.checked;

    if (isGrub) {
      ipxeGroup.style.opacity = '0.45';
      ipxeGroup.style.pointerEvents = 'none';
      ipxeGroup.style.userSelect = 'none';
      ipxeGroup.setAttribute('aria-disabled', 'true');
      if (grubNote) grubNote.style.display = '';
    } else {
      ipxeGroup.style.opacity = '1';
      ipxeGroup.style.pointerEvents = 'auto';
      ipxeGroup.style.userSelect = 'auto';
      ipxeGroup.setAttribute('aria-disabled', 'false');
      if (grubNote) grubNote.style.display = 'none';
    }

    if (isGrub) {
      dynFile.textContent = card.dataset.txtGrubFile;
      dynDriver.textContent = card.dataset.txtGrubDriver;
      dynMenu.textContent = card.dataset.txtGrubMenu;
    } else {
      dynFile.textContent = isSnp ? card.dataset.txtSnpFile : card.dataset.txtIpxeFile;
      dynDriver.textContent = isSnp ? card.dataset.txtSnpDriver : card.dataset.txtIpxeDriver;
      if (isFull && isSnp && isReuse) {
        dynMenu.textContent = card.dataset.txtFastMenu;
      } else {
        dynMenu.textContent = card.dataset.txtStdMenu;
      }
    }

    setCondition(condMode, isFull);
    setCondition(condLoader, !isGrub);
    setCondition(condDriver, isSnp);

    var allConditions = isFull && !isGrub && isSnp;
    if (allConditions && isReuse) {
      dynSummary.innerHTML = '<span class="dot dot-ok"></span><strong>' + card.dataset.txtActive + '</strong>';
    } else if (!isReuse) {
      dynSummary.innerHTML = '<span class="dot dot-muted"></span>' + card.dataset.txtDisabled;
    } else {
      dynSummary.innerHTML = '<span class="dot dot-accent"></span>' + card.dataset.txtInactive;
    }

    updateSaveButton();
  }

  function setCondition(li, ok) {
    if (!li) return;
    var dot = li.querySelector('.dot');
    if (dot) {
      dot.className = 'dot ' + (ok ? 'dot-ok' : 'dot-muted');
    }
    li.style.color = ok ? 'var(--text)' : 'var(--muted)';
  }

  form.addEventListener('change', update);
  form.addEventListener('input', update);
  form.addEventListener('submit', function (event) {
    updateSaveButton();
    if (saveBtn && saveBtn.disabled) {
      event.preventDefault();
    }
  });
  update();
})();
