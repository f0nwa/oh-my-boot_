package control

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestSysMonProcParsingAndDeltas(t *testing.T) {
	fsys := fstest.MapFS{
		"proc/stat": &fstest.MapFile{
			Data: []byte(strings.Join([]string{
				"cpu  1000 100 500 8000 400 0 0 0 0 0",
				"cpu0 250 25 125 2000 100 0 0 0 0 0",
				"cpu1 250 25 125 2000 100 0 0 0 0 0",
				"cpu2 250 25 125 2000 100 0 0 0 0 0",
				"cpu3 250 25 125 2000 100 0 0 0 0 0",
				"intr 12345",
			}, "\n")),
		},
		"proc/meminfo": &fstest.MapFile{
			Data: []byte(strings.Join([]string{
				"MemTotal:        8388608 kB", // 8 GiB
				"MemFree:         2097152 kB", // 2 GiB
				"MemAvailable:    6291456 kB",
				"Buffers:          524288 kB", // 0.5 GiB
				"Cached:          3670016 kB", // 3.5 GiB
				"SReclaimable:     629145 kB", // ~0.6 GiB
				"Shmem:            104857 kB", // ~0.1 GiB -> Cache = 0.5 + 3.5 + 0.6 - 0.1 = 4.5 GiB (4718592 kB)
			}, "\n")),
		},
		"proc/net/dev": &fstest.MapFile{
			Data: []byte(strings.Join([]string{
				"Inter-|   Receive                                                |  Transmit",
				" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed",
				"    lo: 999999999  1000    0    0    0     0          0         0 999999999   1000    0    0    0     0       0          0",
				" ens19: 1048576000 5000    0    0    0     0          0         0 524288000   4000    0    0    0     0       0          0",
				"  eth1: 1048576000 5000    0    0    0     0          0         0 524288000   4000    0    0    0     0       0          0",
			}, "\n")),
		},
		"proc/loadavg": &fstest.MapFile{
			Data: []byte("0.18 0.22 0.15 2/245 9876\n"),
		},
		"proc/uptime": &fstest.MapFile{
			// 14 days 6 hours = 14*86400 + 6*3600 = 1209600 + 21600 = 1231200 seconds
			Data: []byte("1231200.45 4500000.12\n"),
		},
	}

	mon := NewSystemMonitor(fsys)
	t0 := time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC)
	mon.sampleAt(t0)

	snap1 := mon.Snapshot("ru")
	if !snap1.Available {
		t.Fatalf("expected snapshot Available=true")
	}
	if snap1.CPUCores != 4 {
		t.Fatalf("expected 4 CPU cores, got %d", snap1.CPUCores)
	}
	if snap1.CPUPercent != 0 {
		t.Fatalf("expected initial CPUPercent=0, got %v", snap1.CPUPercent)
	}
	if snap1.LoadAvgStr != "0.18 · 0.22 · 0.15" {
		t.Fatalf("unexpected LoadAvgStr: %q", snap1.LoadAvgStr)
	}
	if snap1.UptimeHuman != "14 дн 6 ч" {
		t.Fatalf("unexpected RU UptimeHuman: %q", snap1.UptimeHuman)
	}

	// Second sample 2 seconds later:
	// CPU total increases by 1000 (from 10000 to 11000):
	// user +200, sys +50 (busy +250), idle +700, iowait +50 (idle +750) -> 25% CPU
	fsys["proc/stat"] = &fstest.MapFile{
		Data: []byte(strings.Join([]string{
			"cpu  1200 100 550 8700 450 0 0 0 0 0",
			"cpu0 300 25 135 2175 110 0 0 0 0 0",
			"cpu1 300 25 135 2175 110 0 0 0 0 0",
			"cpu2 300 25 140 2175 115 0 0 0 0 0",
			"cpu3 300 25 140 2175 115 0 0 0 0 0",
		}, "\n")),
	}
	// Net: ens19 rx increases by 3 MiB (3145728 bytes), tx increases by 900 KiB (921600 bytes) over 2s
	// Rate: RxRate = 1.5 MiB/s, TxRate = 450 KiB/s
	fsys["proc/net/dev"] = &fstest.MapFile{
		Data: []byte(strings.Join([]string{
			"Inter-|   Receive                                                |  Transmit",
			" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed",
			"    lo: 9999999999 2000    0    0    0     0          0         0 9999999999  2000    0    0    0     0       0          0",
			" ens19: 1051721728 5500    0    0    0     0          0         0 525209600   4200    0    0    0     0       0          0",
			"  eth1: 1048576000 5000    0    0    0     0          0         0 524288000   4000    0    0    0     0       0          0",
		}, "\n")),
	}

	mon.sampleAt(t0.Add(2 * time.Second))

	snapRU := mon.Snapshot("ru")
	if snapRU.CPUPercent != 25.0 {
		t.Fatalf("expected CPUPercent=25.0, got %v", snapRU.CPUPercent)
	}
	if snapRU.MemTotalHuman != "8.0 ГБ" {
		t.Fatalf("unexpected MemTotalHuman: %q", snapRU.MemTotalHuman)
	}
	if snapRU.MemUsedHuman != "1.5 ГБ" {
		t.Fatalf("unexpected MemUsedHuman: %q", snapRU.MemUsedHuman)
	}
	if snapRU.MemCacheHuman != "4.5 ГБ" {
		t.Fatalf("unexpected MemCacheHuman: %q", snapRU.MemCacheHuman)
	}
	if snapRU.MemFreeHuman != "2.0 ГБ" {
		t.Fatalf("unexpected MemFreeHuman: %q", snapRU.MemFreeHuman)
	}
	if snapRU.RxRateHuman != "1.5 МБ/с" {
		t.Fatalf("unexpected RxRateHuman: %q", snapRU.RxRateHuman)
	}
	if snapRU.TxRateHuman != "450 КБ/с" {
		t.Fatalf("unexpected TxRateHuman: %q", snapRU.TxRateHuman)
	}

	snapEN := mon.Snapshot("en")
	if snapEN.UptimeHuman != "14 d 6 h" {
		t.Fatalf("unexpected EN UptimeHuman: %q", snapEN.UptimeHuman)
	}
	if snapEN.RxRateHuman != "1.5 MB/s" {
		t.Fatalf("unexpected EN RxRateHuman: %q", snapEN.RxRateHuman)
	}
	if snapEN.MemTotalHuman != "8.0 GB" {
		t.Fatalf("unexpected EN MemTotalHuman: %q", snapEN.MemTotalHuman)
	}
}

func TestSysMonFallbackWithoutProc(t *testing.T) {
	mon := NewSystemMonitor(fstest.MapFS{})
	mon.sampleAt(time.Now())
	snap := mon.Snapshot("ru")
	if snap.Available {
		t.Fatalf("expected Available=false when /proc is missing")
	}
	if snap.CPUCores < 1 {
		t.Fatalf("expected fallback CPUCores >= 1, got %d", snap.CPUCores)
	}
	if snap.RxRateHuman != "0 Б/с" {
		t.Fatalf("unexpected fallback RxRateHuman: %q", snap.RxRateHuman)
	}
}
