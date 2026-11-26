package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const defaultDiskUsageTTL = 20 * time.Second

// LocalFilesystem describes disk capacity and free space for one unique local
// filesystem backing one or more OMB artifact roots.
type LocalFilesystem struct {
	ID          string `json:"id"`
	MountSample string `json:"mount_sample"`
	TotalBytes  int64  `json:"total_bytes"`
	FreeBytes   int64  `json:"free_bytes"`
}

// DiskUsageReport summarizes local server filesystem capacity and artifact
// directory sizes keyed by profile, driver WIM, config, and Ubuntu source.
type DiskUsageReport struct {
	Filesystems          []LocalFilesystem `json:"filesystems"`
	WindowsProfiles      map[int64]int64   `json:"windows_profiles"`
	WindowsBootImages    map[int64]int64   `json:"windows_boot_images"`
	WindowsDriverWIMs    map[string]int64  `json:"windows_driver_wims"`
	WindowsDriverConfigs map[int64]int64   `json:"windows_driver_configs"`
	WindowsAnswersBytes  int64             `json:"windows_answers_bytes"`
	UbuntuSources        map[int64]int64   `json:"ubuntu_sources"`
	DriverPackagesBytes  int64             `json:"driver_packages_bytes"`
	UEFICAUSBBytes       int64             `json:"uefica_usb_bytes"`
	CollectedAt          time.Time         `json:"collected_at"`
}

// DiskUsageRoots names the local directories owned by omb-agent.
type DiskUsageRoots struct {
	WindowsProfiles      string
	WindowsBootImages    string
	WindowsDriverWIMs    string
	WindowsDriverConfigs string
	WindowsAnswers       string
	UbuntuSources        string
	Drivers              string
	UEFICAUSB            string
}

// DiskUsageCollector computes local artifact usage with short-lived caching.
type DiskUsageCollector struct {
	Roots  DiskUsageRoots
	TTL    time.Duration
	StatFS func(path string) (fsID string, totalBytes, freeBytes int64, ok bool)
	Now    func() time.Time

	mu        sync.Mutex
	cached    DiskUsageReport
	hasCached bool
}

// Invalidate clears the cached report so the next Collect reads fresh sizes.
func (c *DiskUsageCollector) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.hasCached = false
	c.mu.Unlock()
}

// Collect returns cached or freshly scanned disk usage for OMB artifacts.
func (c *DiskUsageCollector) Collect(ctx context.Context) (DiskUsageReport, error) {
	if err := ctx.Err(); err != nil {
		return DiskUsageReport{}, err
	}
	nowFn := c.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = defaultDiskUsageTTL
	}
	now := nowFn()

	c.mu.Lock()
	if c.hasCached && now.Sub(c.cached.CollectedAt) < ttl {
		report := cloneDiskUsageReport(c.cached)
		c.mu.Unlock()
		return report, nil
	}
	c.mu.Unlock()

	report := DiskUsageReport{
		Filesystems:          c.collectFilesystems(),
		WindowsProfiles:      collectNumericChildBytes(ctx, c.Roots.WindowsProfiles),
		WindowsBootImages:    collectNumericChildBytes(ctx, c.Roots.WindowsBootImages),
		WindowsDriverWIMs:    collectKeyedChildBytes(ctx, c.Roots.WindowsDriverWIMs),
		WindowsDriverConfigs: collectNumericChildBytes(ctx, c.Roots.WindowsDriverConfigs),
		WindowsAnswersBytes:  collectDirBytes(ctx, c.Roots.WindowsAnswers),
		UbuntuSources:        collectNumericChildBytes(ctx, c.Roots.UbuntuSources),
		DriverPackagesBytes:  collectDirBytes(ctx, c.Roots.Drivers),
		UEFICAUSBBytes:       collectDirBytes(ctx, c.Roots.UEFICAUSB),
		CollectedAt:          now,
	}
	if err := ctx.Err(); err != nil {
		return DiskUsageReport{}, err
	}

	c.mu.Lock()
	c.cached = cloneDiskUsageReport(report)
	c.hasCached = true
	c.mu.Unlock()
	return report, nil
}

func (c *DiskUsageCollector) collectFilesystems() []LocalFilesystem {
	statFn := c.StatFS
	if statFn == nil {
		statFn = defaultLocalStatFS
	}
	candidates := []string{
		c.Roots.WindowsProfiles,
		c.Roots.WindowsBootImages,
		c.Roots.WindowsDriverWIMs,
		c.Roots.UbuntuSources,
		c.Roots.Drivers,
		c.Roots.UEFICAUSB,
		c.Roots.WindowsDriverConfigs,
		c.Roots.WindowsAnswers,
	}
	seen := map[string]bool{}
	out := []LocalFilesystem{}
	for _, root := range candidates {
		if strings.TrimSpace(root) == "" {
			continue
		}
		target := existingAncestor(root)
		if target == "" {
			continue
		}
		id, total, free, ok := statFn(target)
		if !ok || total <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, LocalFilesystem{
			ID:          id,
			MountSample: root,
			TotalBytes:  total,
			FreeBytes:   free,
		})
	}
	return out
}

func existingAncestor(path string) string {
	cur := filepath.Clean(path)
	for {
		if info, err := os.Stat(cur); err == nil && info.IsDir() {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func defaultLocalStatFS(path string) (string, int64, int64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, 0, false
	}
	statT, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", 0, 0, false
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "", 0, 0, false
	}
	return fmt.Sprintf("%d", statT.Dev), int64(st.Blocks) * int64(st.Bsize), int64(st.Bavail) * int64(st.Bsize), true
}

func collectNumericChildBytes(ctx context.Context, root string) map[int64]int64 {
	out := map[int64]int64{}
	if strings.TrimSpace(root) == "" {
		return out
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		id, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		out[id] = collectDirBytes(ctx, filepath.Join(root, entry.Name()))
	}
	return out
}

func collectKeyedChildBytes(ctx context.Context, root string) map[string]int64 {
	out := map[string]int64{}
	if strings.TrimSpace(root) == "" {
		return out
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if !validManagedChild(name) {
			continue
		}
		out[name] = collectDirBytes(ctx, filepath.Join(root, name))
	}
	return out
}

func collectDirBytes(ctx context.Context, root string) int64 {
	if strings.TrimSpace(root) == "" {
		return 0
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil || !fi.Mode().IsRegular() {
			return nil
		}
		total += fi.Size()
		return nil
	})
	return total
}

func cloneDiskUsageReport(r DiskUsageReport) DiskUsageReport {
	out := r
	out.Filesystems = append([]LocalFilesystem(nil), r.Filesystems...)
	out.WindowsProfiles = make(map[int64]int64, len(r.WindowsProfiles))
	for k, v := range r.WindowsProfiles {
		out.WindowsProfiles[k] = v
	}
	out.WindowsBootImages = make(map[int64]int64, len(r.WindowsBootImages))
	for k, v := range r.WindowsBootImages {
		out.WindowsBootImages[k] = v
	}
	out.WindowsDriverWIMs = make(map[string]int64, len(r.WindowsDriverWIMs))
	for k, v := range r.WindowsDriverWIMs {
		out.WindowsDriverWIMs[k] = v
	}
	out.WindowsDriverConfigs = make(map[int64]int64, len(r.WindowsDriverConfigs))
	for k, v := range r.WindowsDriverConfigs {
		out.WindowsDriverConfigs[k] = v
	}
	out.UbuntuSources = make(map[int64]int64, len(r.UbuntuSources))
	for k, v := range r.UbuntuSources {
		out.UbuntuSources[k] = v
	}
	return out
}

type statusCaptureWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCaptureWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func isArtifactMutationPath(path string) bool {
	for _, prefix := range []string{
		"/windows-iso/",
		"/windows/boot-image/",
		"/windows/install-image/",
		"/winpe-drivers/",
		"/ubuntu-iso/",
		"/uefica-usb/",
		"/windows-answer/",
		"/apply",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// HandlerWithDiskUsage exposes GET /disk-usage and invalidates the cache after
// successful artifact mutations.
func HandlerWithDiskUsage(inner http.Handler, collector *DiskUsageCollector) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /disk-usage", func(w http.ResponseWriter, r *http.Request) {
		if collector == nil {
			http.Error(w, "disk usage collector unavailable", http.StatusServiceUnavailable)
			return
		}
		report, err := collector.Collect(r.Context())
		if err != nil {
			http.Error(w, "cannot inspect disk usage", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(report)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if collector != nil && r.Method == http.MethodPost && isArtifactMutationPath(r.URL.Path) {
			cw := &statusCaptureWriter{ResponseWriter: w, status: http.StatusOK}
			inner.ServeHTTP(cw, r)
			if cw.status < 400 {
				collector.Invalidate()
			}
			return
		}
		inner.ServeHTTP(w, r)
	})
	return mux
}
