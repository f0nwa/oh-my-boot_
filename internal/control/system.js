(function () {
  var card = document.getElementById("server-resources-card");
  if (!card) return;

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
        if (!d || !d.available) return;
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
})();
