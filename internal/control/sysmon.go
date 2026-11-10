package control

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SystemSnapshot holds the latest hardware resource metrics and pre-formatted
// localized strings for the /system overview page and /api/system/metrics.
type SystemSnapshot struct {
	Available       bool       `json:"available"`
	CPUCores        int        `json:"cpu_cores"`
	CPUPercent      float64    `json:"cpu_percent"`
	LoadAvg         [3]float64 `json:"load_avg"`
	LoadAvgStr      string     `json:"load_avg_str"`
	MemTotal        uint64     `json:"mem_total"`
	MemUsed         uint64     `json:"mem_used"`
	MemCache        uint64     `json:"mem_cache"`
	MemFree         uint64     `json:"mem_free"`
	MemActive       uint64     `json:"mem_active"`
	MemPercent      float64    `json:"mem_percent"`
	MemTotalHuman   string     `json:"mem_total_human"`
	MemUsedHuman    string     `json:"mem_used_human"`
	MemCacheHuman   string     `json:"mem_cache_human"`
	MemFreeHuman    string     `json:"mem_free_human"`
	MemActiveHuman  string     `json:"mem_active_human"`
	MemUsedPercent  float64    `json:"mem_used_percent"`
	MemCachePercent float64    `json:"mem_cache_percent"`
	RxRate          uint64     `json:"rx_rate"`
	TxRate          uint64     `json:"tx_rate"`
	RxTotal         uint64     `json:"rx_total"`
	TxTotal         uint64     `json:"tx_total"`
	RxRateHuman     string     `json:"rx_rate_human"`
	TxRateHuman     string     `json:"tx_rate_human"`
	RxTotalHuman    string     `json:"rx_total_human"`
	TxTotalHuman    string     `json:"tx_total_human"`
	UptimeSeconds   uint64     `json:"uptime_seconds"`
	UptimeHuman     string     `json:"uptime_human"`
}

type SystemMonitor struct {
	fsys fs.FS

	mu             sync.RWMutex
	hasPrev        bool
	prevSampleTime time.Time
	prevCPUTotal   uint64
	prevCPUIdle    uint64
	prevRxBytes    uint64
	prevTxBytes    uint64
	current        SystemSnapshot
}

func NewSystemMonitor(fsys fs.FS) *SystemMonitor {
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	m := &SystemMonitor{
		fsys: fsys,
		current: SystemSnapshot{
			CPUCores:   max(1, runtime.NumCPU()),
			LoadAvgStr: "0.00 · 0.00 · 0.00",
		},
	}
	m.sampleAt(time.Now())
	return m
}

// Start runs periodic sampling in the background until ctx is canceled.
func (m *SystemMonitor) Start(ctx context.Context) {
	m.sampleAt(time.Now())
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				m.sampleAt(now)
			}
		}
	}()
}

func (m *SystemMonitor) sampleAt(now time.Time) {
	cpuTotal, cpuIdle, cores, okStat := m.readProcStat()
	memTotal, memUsed, memCache, memFree, okMem := m.readProcMeminfo()
	rxBytes, txBytes, okNet := m.readProcNetDev()
	loadAvg, _ := m.readProcLoadAvg()
	uptimeSec, _ := m.readProcUptime()

	m.mu.Lock()
	defer m.mu.Unlock()

	if !okStat && !okMem && !okNet {
		m.current = SystemSnapshot{
			Available:  false,
			CPUCores:   max(1, runtime.NumCPU()),
			LoadAvgStr: "0.00 · 0.00 · 0.00",
		}
		return
	}

	var cpuPercent float64
	var rxRate, txRate uint64

	if m.hasPrev {
		if okStat && cpuTotal > m.prevCPUTotal {
			dTotal := cpuTotal - m.prevCPUTotal
			var dIdle uint64
			if cpuIdle > m.prevCPUIdle {
				dIdle = cpuIdle - m.prevCPUIdle
			}
			if dTotal > dIdle {
				cpuPercent = float64(dTotal-dIdle) / float64(dTotal) * 100.0
			}
		}
		dt := now.Sub(m.prevSampleTime).Seconds()
		if okNet && dt > 0 {
			if rxBytes >= m.prevRxBytes {
				rxRate = uint64(math.Round(float64(rxBytes-m.prevRxBytes) / dt))
			}
			if txBytes >= m.prevTxBytes {
				txRate = uint64(math.Round(float64(txBytes-m.prevTxBytes) / dt))
			}
		}
	}

	m.hasPrev = true
	m.prevSampleTime = now
	m.prevCPUTotal = cpuTotal
	m.prevCPUIdle = cpuIdle
	m.prevRxBytes = rxBytes
	m.prevTxBytes = txBytes

	if cores < 1 {
		cores = max(1, runtime.NumCPU())
	}

	memActive := memUsed + memCache
	var memPercent, memUsedPercent, memCachePercent float64
	if memTotal > 0 {
		memUsedPercent = float64(memUsed) / float64(memTotal) * 100.0
		memCachePercent = float64(memCache) / float64(memTotal) * 100.0
		memPercent = float64(memActive) / float64(memTotal) * 100.0
	}

	m.current = SystemSnapshot{
		Available:       true,
		CPUCores:        cores,
		CPUPercent:      math.Min(100, math.Max(0, cpuPercent)),
		LoadAvg:         loadAvg,
		LoadAvgStr:      fmt.Sprintf("%.2f · %.2f · %.2f", loadAvg[0], loadAvg[1], loadAvg[2]),
		MemTotal:        memTotal,
		MemUsed:         memUsed,
		MemCache:        memCache,
		MemFree:         memFree,
		MemActive:       memActive,
		MemPercent:      math.Min(100, math.Max(0, memPercent)),
		MemUsedPercent:  math.Min(100, math.Max(0, memUsedPercent)),
		MemCachePercent: math.Min(100, math.Max(0, memCachePercent)),
		RxRate:          rxRate,
		TxRate:          txRate,
		RxTotal:         rxBytes,
		TxTotal:         txBytes,
		UptimeSeconds:   uptimeSec,
	}
}

// Snapshot returns a copy of the latest metrics with human-readable strings
// formatted for the requested language ("en" or "ru").
func (m *SystemMonitor) Snapshot(lang string) SystemSnapshot {
	m.mu.RLock()
	snap := m.current
	m.mu.RUnlock()

	snap.MemTotalHuman = formatHumanBytes(snap.MemTotal, lang)
	snap.MemUsedHuman = formatHumanBytes(snap.MemUsed, lang)
	snap.MemCacheHuman = formatHumanBytes(snap.MemCache, lang)
	snap.MemFreeHuman = formatHumanBytes(snap.MemFree, lang)
	snap.MemActiveHuman = formatHumanBytes(snap.MemActive, lang)
	snap.RxRateHuman = formatHumanRate(snap.RxRate, lang)
	snap.TxRateHuman = formatHumanRate(snap.TxRate, lang)
	snap.RxTotalHuman = formatHumanBytes(snap.RxTotal, lang)
	snap.TxTotalHuman = formatHumanBytes(snap.TxTotal, lang)
	snap.UptimeHuman = formatHumanUptime(snap.UptimeSeconds, lang)
	return snap
}

func (m *SystemMonitor) readProcStat() (total uint64, idle uint64, cores int, ok bool) {
	b, err := fs.ReadFile(m.fsys, "proc/stat")
	if err != nil {
		return 0, 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "cpu" && len(fields) >= 5 {
			for i := 1; i < len(fields); i++ {
				v, err := strconv.ParseUint(fields[i], 10, 64)
				if err != nil {
					continue
				}
				total += v
				// index 4 is idle, index 5 is iowait
				if i == 4 || i == 5 {
					idle += v
				}
			}
			ok = true
		} else if strings.HasPrefix(fields[0], "cpu") && len(fields[0]) > 3 {
			if _, err := strconv.Atoi(fields[0][3:]); err == nil {
				cores++
			}
		}
	}
	return total, idle, cores, ok
}

func (m *SystemMonitor) readProcMeminfo() (total, used, cache, free uint64, ok bool) {
	b, err := fs.ReadFile(m.fsys, "proc/meminfo")
	if err != nil {
		return 0, 0, 0, 0, false
	}
	vals := make(map[string]uint64)
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		v, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			continue
		}
		// /proc/meminfo reports values in KiB
		vals[key] = v * 1024
	}
	total, ok = vals["MemTotal"]
	if !ok || total == 0 {
		return 0, 0, 0, 0, false
	}
	free = vals["MemFree"]
	rawCache := vals["Buffers"] + vals["Cached"] + vals["SReclaimable"]
	if shmem := vals["Shmem"]; rawCache > shmem {
		cache = rawCache - shmem
	} else {
		cache = rawCache
	}
	if total > free+cache {
		used = total - free - cache
	} else if total > free {
		used = total - free
		cache = 0
	}
	return total, used, cache, free, true
}

func (m *SystemMonitor) readProcNetDev() (rxTotal, txTotal uint64, ok bool) {
	b, err := fs.ReadFile(m.fsys, "proc/net/dev")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colon])
		if iface == "" || iface == "lo" {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[0], 10, 64)
		tx, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 == nil && err2 == nil {
			rxTotal += rx
			txTotal += tx
			ok = true
		}
	}
	return rxTotal, txTotal, ok
}

func (m *SystemMonitor) readProcLoadAvg() (la [3]float64, ok bool) {
	b, err := fs.ReadFile(m.fsys, "proc/loadavg")
	if err != nil {
		return la, false
	}
	fields := strings.Fields(string(b))
	if len(fields) < 3 {
		return la, false
	}
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return la, false
		}
		la[i] = v
	}
	return la, true
}

func (m *SystemMonitor) readProcUptime() (seconds uint64, ok bool) {
	b, err := fs.ReadFile(m.fsys, "proc/uptime")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return uint64(v), true
}

func formatHumanBytes(b uint64, lang string) string {
	unitB := translate(lang, "Б")
	unitKB := translate(lang, "КБ")
	unitMB := translate(lang, "МБ")
	unitGB := translate(lang, "ГБ")
	unitTB := translate(lang, "ТБ")
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	switch {
	case b >= tb:
		return fmt.Sprintf("%.1f %s", float64(b)/float64(tb), unitTB)
	case b >= gb:
		return fmt.Sprintf("%.1f %s", float64(b)/float64(gb), unitGB)
	case b >= mb:
		val := float64(b) / float64(mb)
		if val >= 10 {
			return fmt.Sprintf("%.0f %s", val, unitMB)
		}
		return fmt.Sprintf("%.1f %s", val, unitMB)
	case b >= kb:
		return fmt.Sprintf("%.0f %s", float64(b)/float64(kb), unitKB)
	default:
		return fmt.Sprintf("%d %s", b, unitB)
	}
}

func formatHumanRate(bytesPerSec uint64, lang string) string {
	return formatHumanBytes(bytesPerSec, lang) + translate(lang, "/с")
}

func formatHumanUptime(sec uint64, lang string) string {
	dUnit := translate(lang, "дн")
	hUnit := translate(lang, "ч")
	mUnit := translate(lang, "мин")
	days := sec / 86400
	hours := (sec % 86400) / 3600
	mins := (sec % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%d %s %d %s", days, dUnit, hours, hUnit)
	}
	if hours > 0 {
		return fmt.Sprintf("%d %s %d %s", hours, hUnit, mins, mUnit)
	}
	return fmt.Sprintf("%d %s", mins, mUnit)
}
