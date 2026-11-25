(function() {
  var form = document.getElementById('network-edit-form');
  var card = document.getElementById('network-scenario-card');
  if (!form || !card) return;

  var selMode = form.querySelector('select[name="mode"]');
  var selDriver = form.querySelector('select[name="default_driver"]');
  var chkReuse = form.querySelector('input[name="reuse_firmware_network"]');
  var selClientAccess = form.querySelector('select[name="client_access"]');
  var inpServerIP = form.querySelector('input[name="server_ip"]');
  var inpSubnet = form.querySelector('input[name="subnet"]');
  var subnetsRow = form.querySelector('[data-client-subnets-row]');
  var subnetsInput = subnetsRow ? subnetsRow.querySelector('textarea') : null;
  var anyWarn = form.querySelector('[data-client-any-warn]');
  var poolRow = form.querySelector('[data-pool-row]');
  var poolInputs = poolRow ? poolRow.querySelectorAll('input') : [];
  var saveBtn = form.querySelector('.form-actions button');

  var dynFile = document.getElementById('dyn-file');
  var dynDriver = document.getElementById('dyn-driver');
  var dynMenu = document.getElementById('dyn-menu');
  var dynAccess = document.getElementById('dyn-access');
  var dynSMB = document.getElementById('dyn-smb');
  var condMode = document.getElementById('dyn-cond-mode');
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
    var isFull = selMode.value === 'full';
    var isSnp = selDriver.value === 'snp';
    var isReuse = chkReuse.checked;

    if (poolRow) {
      poolRow.classList.toggle('net-pool-disabled', !isFull);
      for (var i = 0; i < poolInputs.length; i++) {
        poolInputs[i].disabled = !isFull;
      }
    }

    if (selClientAccess) {
      var accessMode = selClientAccess.value;
      var isSubnets = accessMode === 'subnets';
      var isAny = accessMode === 'any';
      if (subnetsRow) {
        subnetsRow.classList.toggle('hidden', !isSubnets);
      }
      if (subnetsInput) {
        subnetsInput.disabled = !isSubnets;
        subnetsInput.required = isSubnets;
      }
      if (anyWarn) {
        anyWarn.classList.toggle('hidden', !isAny);
      }
      if (dynAccess) {
        var subnetVal = inpSubnet ? inpSubnet.value.trim() : '';
        if (isAny) {
          dynAccess.textContent = card.dataset.txtAccessAny || 'any';
          dynAccess.className = 'net-access-warn';
        } else if (isSubnets) {
          var rawList = subnetsInput ? subnetsInput.value.split(/[\s,;]+/).filter(Boolean) : [];
          dynAccess.textContent = subnetVal + ' ' + (card.dataset.txtAccessPlus || '+') + ' ' + rawList.length;
          dynAccess.className = 'mono';
        } else {
          dynAccess.textContent = (card.dataset.txtAccessOnly ? card.dataset.txtAccessOnly + ' ' : '') + subnetVal;
          dynAccess.className = 'mono';
        }
      }
    }
    if (dynSMB && inpServerIP) {
      dynSMB.textContent = '\\\\' + inpServerIP.value.trim() + '\\ombisos';
    }

    dynFile.textContent = isSnp ? card.dataset.txtSnpFile : card.dataset.txtIpxeFile;
    dynDriver.textContent = isSnp ? card.dataset.txtSnpDriver : card.dataset.txtIpxeDriver;
    if (isFull && isSnp && isReuse) {
      dynMenu.textContent = card.dataset.txtFastMenu;
    } else {
      dynMenu.textContent = card.dataset.txtStdMenu;
    }

    setCondition(condMode, isFull);
    setCondition(condDriver, isSnp);

    var allConditions = isFull && isSnp;
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
