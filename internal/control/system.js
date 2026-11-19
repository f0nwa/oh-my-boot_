(function () {
  var card = document.getElementById("server-resources-card");
  var elSysTimeNow = document.getElementById("sys-time-now");
  var elDialogClock = document.getElementById("sys-time-dialog-clock");

  if (card) {
    var elCpuVal = document.getElementById("mon-cpu-val");
    var elCpuFill = document.getElementById("mon-cpu-fill");
    var elLoadAvg = document.getElementById("mon-loadavg");
    var elMemActive = document.getElementById("mon-mem-active");
    var elMemPct = document.getElementById("mon-mem-pct");
    var elMemFillUsed = document.getElementById("mon-mem-fill-used");
    var elMemFillCache = document.getElementById("mon-mem-fill-cache");
    var elMemUsed = document.getElementById("mon-mem-used");
    var elMemCache = document.getElementById("mon-mem-cache");
    var elMemFree = document.getElementById("mon-mem-free");
    var elNetRxRate = document.getElementById("mon-net-rx-rate");
    var elNetTxRate = document.getElementById("mon-net-tx-rate");
    var elNetTotals = document.getElementById("mon-net-totals");
    var elUptime = document.getElementById("mon-uptime");

    var timer = null;
    var inflight = false;

    function updateMetrics() {
      if (document.hidden || inflight) return;
      inflight = true;
      fetch("/api/system/metrics", {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" }
      })
        .then(function (res) {
          if (!res.ok) throw new Error("status " + res.status);
          return res.json();
        })
        .then(function (d) {
          if (!d) return;
          if (d.server_time) {
            if (elSysTimeNow) elSysTimeNow.textContent = d.server_time;
            if (elDialogClock) elDialogClock.textContent = d.server_time;
          }
          if (!d.available) return;
          var cpuPct = Math.round(d.cpu_percent || 0);
          if (elCpuVal) elCpuVal.textContent = cpuPct + "%";
          if (elCpuFill) {
            elCpuFill.style.width = cpuPct + "%";
            if (d.cpu_percent > 90) {
              elCpuFill.classList.add("meter-danger");
            } else {
              elCpuFill.classList.remove("meter-danger");
            }
          }
          if (elLoadAvg && d.load_avg_str) {
            elLoadAvg.textContent = "LA: " + d.load_avg_str;
          }
          if (elMemActive && d.mem_active_human && d.mem_total_human) {
            elMemActive.textContent = d.mem_active_human + " / " + d.mem_total_human;
          }
          if (elMemPct) {
            elMemPct.textContent = "(" + Math.round(d.mem_percent || 0) + "%)";
          }
          if (elMemFillUsed) {
            elMemFillUsed.style.width = (d.mem_used_percent || 0).toFixed(1) + "%";
          }
          if (elMemFillCache) {
            elMemFillCache.style.width = (d.mem_cache_percent || 0).toFixed(1) + "%";
          }
          if (elMemUsed && d.mem_used_human) elMemUsed.textContent = d.mem_used_human;
          if (elMemCache && d.mem_cache_human) elMemCache.textContent = d.mem_cache_human;
          if (elMemFree && d.mem_free_human) elMemFree.textContent = d.mem_free_human;
          if (elNetRxRate && d.rx_rate_human) elNetRxRate.textContent = d.rx_rate_human;
          if (elNetTxRate && d.tx_rate_human) elNetTxRate.textContent = d.tx_rate_human;
          if (elNetTotals && d.rx_total_human && d.tx_total_human) {
            elNetTotals.textContent = "↓ " + d.rx_total_human + " · ↑ " + d.tx_total_human;
          }
          if (elUptime && d.uptime_human) {
            elUptime.textContent = d.uptime_human;
          }
        })
        .catch(function () {})
        .finally(function () {
          inflight = false;
        });
    }

    function startPolling() {
      if (timer) return;
      timer = setInterval(updateMetrics, 2500);
    }

    function stopPolling() {
      if (!timer) return;
      clearInterval(timer);
      timer = null;
    }

    document.addEventListener("visibilitychange", function () {
      if (document.hidden) {
        stopPolling();
      } else {
        updateMetrics();
        startPolling();
      }
    });

    if (!document.hidden) {
      startPolling();
    }
  }

  var timeDialog = document.getElementById("system-time-dialog");
  if (!timeDialog) return;

  var form = timeDialog.querySelector("form");
  var tzSelect = timeDialog.querySelector('select[name="timezone"]');
  var radioNtp = timeDialog.querySelector('input[name="mode"][value="ntp"]');
  var radioManual = timeDialog.querySelector('input[name="mode"][value="manual"]');
  var ntpInput = timeDialog.querySelector('input[name="ntp_servers"]');
  var manualInput = timeDialog.querySelector('input[name="manual_time"]');
  var browserBtn = timeDialog.querySelector("[data-time-browser-now]");
  var submitBtn = timeDialog.querySelector("button.btn-primary");
  var cancelBtn = timeDialog.querySelector("[data-dialog-cancel]");
  var modeCards = timeDialog.querySelectorAll("[data-time-mode-card]");

  if (!form || !tzSelect || !radioNtp || !radioManual || !submitBtn) return;

  var initialTZ = form.getAttribute("data-initial-tz") || "";
  var initialMode = form.getAttribute("data-initial-mode") || "ntp";
  var initialNTP = normalizeServers(form.getAttribute("data-initial-ntp") || "");
  var initialManual = form.getAttribute("data-initial-manual") || "";

  function normalizeServers(raw) {
    return String(raw || "")
      .split(/[\s,]+/)
      .filter(Boolean)
      .join(" ");
  }

  function currentMode() {
    return radioManual.checked ? "manual" : "ntp";
  }

  function syncModeUI() {
    var mode = currentMode();
    var isNtp = mode === "ntp";
    if (ntpInput) ntpInput.disabled = !isNtp;
    if (manualInput) manualInput.disabled = isNtp;
    if (browserBtn) browserBtn.disabled = isNtp;
    modeCards.forEach(function (el) {
      var kind = el.getAttribute("data-time-mode-card");
      el.classList.toggle("is-selected", kind === mode);
    });
  }

  function updateSubmitGate() {
    var mode = currentMode();
    var tzChanged = tzSelect.value !== initialTZ;
    var modeChanged = mode !== initialMode;
    var ntpChanged = mode === "ntp" && ntpInput && normalizeServers(ntpInput.value) !== initialNTP;
    var manualChanged = mode === "manual" && manualInput && manualInput.value !== initialManual;
    var dirty = tzChanged || modeChanged || ntpChanged || manualChanged;
    submitBtn.disabled = !dirty;
  }

  function resetDialog() {
    tzSelect.value = initialTZ;
    radioNtp.checked = initialMode === "ntp";
    radioManual.checked = initialMode === "manual";
    if (ntpInput) ntpInput.value = form.getAttribute("data-initial-ntp") || "";
    if (manualInput) manualInput.value = initialManual;
    syncModeUI();
    updateSubmitGate();
  }

  document.querySelectorAll("[data-system-time-open]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      resetDialog();
      timeDialog.showModal();
    });
  });

  modeCards.forEach(function (el) {
    el.addEventListener("click", function (e) {
      if (e.target && (e.target.tagName === "INPUT" && e.target.type !== "radio" || e.target.tagName === "BUTTON")) {
        return;
      }
      var kind = el.getAttribute("data-time-mode-card");
      if (kind === "ntp") {
        radioNtp.checked = true;
        radioManual.checked = false;
      } else {
        radioNtp.checked = false;
        radioManual.checked = true;
      }
      syncModeUI();
      updateSubmitGate();
    });
  });

  form.addEventListener("change", function () {
    syncModeUI();
    updateSubmitGate();
  });

  form.addEventListener("input", function () {
    updateSubmitGate();
  });

  form.addEventListener("submit", function (e) {
    if (submitBtn.disabled) {
      e.preventDefault();
    }
  });

  if (browserBtn && manualInput) {
    browserBtn.addEventListener("click", function () {
      var d = new Date();
      var pad = function (n) { return String(n).padStart(2, "0"); };
      manualInput.value =
        d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) +
        "T" + pad(d.getHours()) + ":" + pad(d.getMinutes());
      updateSubmitGate();
    });
  }

  if (cancelBtn) {
    cancelBtn.addEventListener("click", function () {
      timeDialog.close();
    });
  }

  timeDialog.addEventListener("click", function (e) {
    if (e.target === timeDialog) {
      timeDialog.close();
    }
  });
})();
