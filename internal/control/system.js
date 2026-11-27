(function () {
  var card = document.getElementById("server-resources-card");
  var elSysTimeNow = document.getElementById("sys-time-now");
  var elSysTimeTz = document.getElementById("sys-time-tz");
  var elSysTimeSync = document.getElementById("sys-time-sync");
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
          if (d.time_status) {
            var ts = d.time_status;
            if (elSysTimeTz && ts.timezone) {
              elSysTimeTz.textContent = ts.timezone;
            }
            if (elSysTimeSync && typeof elSysTimeSync.replaceChildren === "function") {
              var dot = document.createElement("span");
              dot.className = ts.ntp_enabled && ts.ntp_synchronized ? "dot dot-ok" : "dot dot-accent";
              if (ts.ntp_enabled) {
                var label = document.createTextNode((ts.status_label || "NTP") + " · ");
                var tail = document.createElement("span");
                if (ts.servers_text) {
                  tail.className = "mono";
                  tail.textContent = ts.servers_text;
                } else {
                  tail.className = "muted";
                  tail.textContent = ts.default_label || "";
                }
                elSysTimeSync.replaceChildren(dot, label, tail);
              } else {
                elSysTimeSync.replaceChildren(dot, document.createTextNode(ts.status_label || ""));
              }
            }
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
})();

(function () {
  "use strict";
  const dialog = document.getElementById("omb-update-dialog");
  if (!dialog) return;

  const byID = (name) => document.getElementById("omb-update-" + name);
  const trigger = byID("open");
  const openLabel = trigger.textContent;
  const form = byID("form");
  const file = byID("file");
  const upload = byID("upload");
  const install = byID("install");
  const status = byID("status");
  const cardStatus = byID("card-status");
  const confirm = byID("confirm");
  const csrf = form.elements.csrf.value;
  let state = { state: "idle" };
  let pending = false;
  let polling = false;
  let choosing = false;
  let reloading = false;
  let generation = 0;
  const rejectionReasons = ["invalid_archive", "verification_failed", "too_large"];
  const maxArchiveBytes = 1024 ** 3;

  function label(key) {
    return dialog.getAttribute("data-" + key.replaceAll("_", "-"));
  }

  function changeMessage(out, reconnecting) {
    let text = label(out.state) || label("unavailable");
    if (out.state === "rejected" && rejectionReasons.includes(out.reason)) text = label(out.reason);
    if (out.state === "installing" && out.phase) text = label(out.phase) || text;
    if (reconnecting) text = label("reconnect");
    if (out.state === "installed" && out.release && out.release.target) {
      text += " " + displayVersion(out.release.target);
    }
    return text;
  }

  function displayVersion(version) {
    return "v" + version.replace("~dev.", "-dev.");
  }

  function render(reconnecting = false) {
    const active = state.state === "checking" || state.state === "installing";
    const ready = state.state === "ready" && !choosing;
    const recover = state.state === "recovery_required";
    const release = state.release || {};
    const message = changeMessage(state, reconnecting);
    if (cardStatus.textContent !== message) cardStatus.textContent = message;
    if (status.textContent !== message) status.textContent = message;
    status.dataset.tone = ["installed", "ready"].includes(state.state) ? "ok"
      : ["rejected", "failed", "rolled_back", "recovery_required", "unavailable"].includes(state.state) ? "error"
      : "";
    status.hidden = state.state === "idle" || choosing && state.state === "ready";
    trigger.textContent = active || ready || recover ? label("open-update") : openLabel;
    form.hidden = active || ready || recover;
    upload.disabled = pending || file.files.length === 0;
    confirm.hidden = !ready;
    install.disabled = pending || !ready;
    byID("background").hidden = state.state !== "installing";
    byID("recovery").hidden = !recover;
    if (ready && release.current && release.target) {
      byID("transition").textContent = displayVersion(release.current) + " → " + displayVersion(release.target) +
        " · " + release.channel;
    }
  }

  async function request(path, body, contentType) {
    const options = {headers: {Accept: "application/json"}, redirect: "error"};
    if (body !== undefined) {
      options.method = "POST";
      options.body = body;
      options.headers["Content-Type"] = contentType;
      options.headers["X-CSRF-Token"] = csrf;
    }
    const res = await fetch(path, options);
    const data = await res.json();
    if (!data || typeof data.state !== "string") throw new Error("update service returned no status");
    return data;
  }

  async function refresh() {
    if (pending || polling || document.hidden || reloading) return;
    polling = true;
    const expected = generation;
    try {
      const next = await request("/api/system/update");
      if (pending || generation !== expected) return;
      if (next.state === "unavailable" && state.state === "installing") {
        render(true);
        return;
      }
      if (next.id !== state.id) choosing = false;
      const version = next.release && next.release.target;
      if (state.state === "installing" && next.state === "installed" && next.id === state.id &&
          version && dialog.dataset.version && version !== dialog.dataset.version) {
        reloading = true;
        window.location.reload();
        return;
      }
      state = next;
      render();
    } catch {
      if (state.state === "installing") render(true);
    } finally {
      polling = false;
    }
  }

  async function submit(path, body, contentType, optimisticState) {
    const prior = state;
    generation++;
    pending = true;
    state = { ...state, state: optimisticState };
    render();
    try {
      const next = await request(path, body, contentType);
      // Control can stop after accepting installation but before responding.
      if (optimisticState !== "installing" || next.state !== "unavailable") state = next;
    } catch {
      if (optimisticState !== "installing") state = {state: "unavailable"};
    } finally {
      choosing = false;
      pending = false;
      if (state.state === "unavailable" && prior.state === "ready") state = prior;
      render();
      if (state.state === "installing") refresh();
    }
  }

  trigger.addEventListener("click", () => {
    dialog.showModal();
    if (form.hidden) byID("title").focus();
    refresh();
  });
  file.addEventListener("change", () => {
    file.setCustomValidity("");
    render();
  });
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const archive = file.files[0];
    if (!archive || pending) return;
    if (!archive.name.toLowerCase().endsWith(".tar.gz")) {
      file.setCustomValidity(label("invalid_archive"));
      file.reportValidity();
      return;
    }
    if (archive.size > maxArchiveBytes) {
      file.setCustomValidity(label("too_large"));
      file.reportValidity();
      return;
    }
    form.reset();
    submit("/system/update/upload", archive, "application/gzip", "checking");
    byID("title").focus();
  });
  byID("replace").addEventListener("click", () => {
    choosing = true;
    form.reset();
    render();
    file.focus();
  });
  install.addEventListener("click", () => {
    if (state.state !== "ready" || !state.id || pending) return;
    submit("/system/update/install", JSON.stringify({id: state.id}), "application/json", "installing");
    byID("title").focus();
  });
  document.addEventListener("visibilitychange", refresh);
  window.setInterval(refresh, 2500);
  refresh();
})();
