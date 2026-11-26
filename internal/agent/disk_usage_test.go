package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeBytes(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiskUsageCollectorCountsDirectoriesAndDeduplicatesFilesystems(t *testing.T) {
	root := t.TempDir()
	roots := DiskUsageRoots{
		WindowsProfiles:      filepath.Join(root, "tftp", "windows-profiles"),
		WindowsBootImages:    filepath.Join(root, "tftp", "windows-boot-images"),
		WindowsDriverWIMs:    filepath.Join(root, "tftp", "windows-driver-wims"),
		WindowsDriverConfigs: filepath.Join(root, "tftp", "windows-driver-configs"),
		WindowsAnswers:       filepath.Join(root, "tftp", "windows-answers"),
		UbuntuSources:        filepath.Join(root, "tftp", "ubuntu-sources"),
		Drivers:              filepath.Join(root, "lib", "drivers"),
		UEFICAUSB:            filepath.Join(root, "lib", "uefica-usb"),
	}
	wimKey := strings.Repeat("a", 64)
	writeBytes(t, filepath.Join(roots.WindowsProfiles, "17", "boot.wim"), 100)
	writeBytes(t, filepath.Join(roots.WindowsProfiles, "not-a-number", "junk"), 999)
	writeBytes(t, filepath.Join(roots.WindowsBootImages, "3", "boot.wim"), 175)
	writeBytes(t, filepath.Join(roots.WindowsDriverWIMs, wimKey, "boot.wim"), 250)
	writeBytes(t, filepath.Join(roots.WindowsDriverConfigs, "4", "omb-setup.cmd"), 30)
	writeBytes(t, filepath.Join(roots.WindowsAnswers, "2", "omb-answer.xml"), 20)
	writeBytes(t, filepath.Join(roots.UbuntuSources, "9", "abc", "vmlinuz"), 400)
	writeBytes(t, filepath.Join(roots.Drivers, "pkg1", "e1000.inf"), 150)
	writeBytes(t, filepath.Join(roots.UEFICAUSB, "k1.zip"), 80)

	// Symlinks must be ignored so external files are never double-counted.
	outside := filepath.Join(root, "outside.bin")
	writeBytes(t, outside, 5000)
	if err := os.Symlink(outside, filepath.Join(roots.WindowsProfiles, "17", "link.wim")); err != nil {
		t.Fatal(err)
	}

	collector := &DiskUsageCollector{
		Roots: roots,
		StatFS: func(string) (string, int64, int64, bool) {
			return "dev-1", 10000, 4000, true
		},
	}
	report, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Filesystems) != 1 || report.Filesystems[0].TotalBytes != 10000 || report.Filesystems[0].FreeBytes != 4000 {
		t.Fatalf("unexpected filesystems: %+v", report.Filesystems)
	}
	if report.WindowsProfiles[17] != 100 || len(report.WindowsProfiles) != 1 {
		t.Fatalf("WindowsProfiles = %+v", report.WindowsProfiles)
	}
	if report.WindowsBootImages[3] != 175 || len(report.WindowsBootImages) != 1 {
		t.Fatalf("WindowsBootImages = %+v", report.WindowsBootImages)
	}
	if report.WindowsDriverWIMs[wimKey] != 250 || len(report.WindowsDriverWIMs) != 1 {
		t.Fatalf("WindowsDriverWIMs = %+v", report.WindowsDriverWIMs)
	}
	if report.WindowsDriverConfigs[4] != 30 || report.WindowsAnswersBytes != 20 {
		t.Fatalf("configs/answers mismatch: %+v %d", report.WindowsDriverConfigs, report.WindowsAnswersBytes)
	}
	if report.UbuntuSources[9] != 400 || report.DriverPackagesBytes != 150 || report.UEFICAUSBBytes != 80 {
		t.Fatalf("ubuntu/drivers/usb mismatch: %+v %d %d", report.UbuntuSources, report.DriverPackagesBytes, report.UEFICAUSBBytes)
	}

	// Separate filesystem for /var/lib/omb-agent must show two entries.
	collector.Invalidate()
	collector.StatFS = func(path string) (string, int64, int64, bool) {
		if strings.Contains(path, string(filepath.Separator)+"lib"+string(filepath.Separator)) {
			return "dev-lib", 20000, 12000, true
		}
		return "dev-tftp", 10000, 4000, true
	}
	report, err = collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Filesystems) != 2 {
		t.Fatalf("expected 2 filesystems, got %+v", report.Filesystems)
	}
}

func TestDiskUsageCollectorIgnoresMissingRootsAndUsesCache(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	collector := &DiskUsageCollector{
		Roots: DiskUsageRoots{
			WindowsProfiles: filepath.Join(root, "missing-profiles"),
			Drivers:         filepath.Join(root, "missing-drivers"),
		},
		TTL: 20 * time.Second,
		Now: func() time.Time { return now },
		StatFS: func(string) (string, int64, int64, bool) {
			return "dev-1", 5000, 2000, true
		},
	}
	first, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.WindowsProfiles) != 0 || first.DriverPackagesBytes != 0 {
		t.Fatalf("expected empty metrics for missing dirs: %+v", first)
	}

	writeBytes(t, filepath.Join(collector.Roots.Drivers, "pkg", "net.inf"), 321)
	now = now.Add(5 * time.Second)
	cached, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cached.DriverPackagesBytes != 0 || !cached.CollectedAt.Equal(first.CollectedAt) {
		t.Fatalf("expected cached report within TTL, got %+v", cached)
	}

	collector.Invalidate()
	refreshed, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.DriverPackagesBytes != 321 {
		t.Fatalf("expected refreshed DriverPackagesBytes=321, got %d", refreshed.DriverPackagesBytes)
	}
}

func TestDiskUsageHTTPHandler(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, filepath.Join(root, "profiles", "5", "boot.wim"), 64)
	collector := &DiskUsageCollector{
		Roots: DiskUsageRoots{WindowsProfiles: filepath.Join(root, "profiles")},
		StatFS: func(string) (string, int64, int64, bool) {
			return "dev-1", 1000, 600, true
		},
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/windows-iso/prepare" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	})
	h := HandlerWithDiskUsage(inner, collector)

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/disk-usage", nil))
	if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "application/json" || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: status=%d headers=%+v body=%s", res.Code, res.Header(), res.Body.String())
	}
	var report DiskUsageReport
	if err := json.Unmarshal(res.Body.Bytes(), &report); err != nil || report.WindowsProfiles[5] != 64 {
		t.Fatalf("report=%+v err=%v", report, err)
	}

	// Successful mutation POST invalidates cache.
	writeBytes(t, filepath.Join(root, "profiles", "6", "boot.wim"), 128)
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/windows-iso/prepare", nil))
	res2 := httptest.NewRecorder()
	h.ServeHTTP(res2, httptest.NewRequest(http.MethodGet, "/disk-usage", nil))
	var report2 DiskUsageReport
	if err := json.Unmarshal(res2.Body.Bytes(), &report2); err != nil || report2.WindowsProfiles[6] != 128 {
		t.Fatalf("expected cache invalidation after POST: %+v err=%v", report2, err)
	}
}
