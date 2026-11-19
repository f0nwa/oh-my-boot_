package agent

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	DefaultChronySourcesDir = "/etc/chrony/sources.d"
	DefaultTimesyncdConfDir = "/etc/systemd/timesyncd.conf.d"
	chronyOMBFile           = "omb.sources"
	timesyncdOMBFile        = "omb.conf"
	maxNTPServers           = 8
)

var (
	ErrInvalidSystemTime     = errors.New("invalid system time request")
	ErrNTPBackendUnavailable = errors.New("NTP backend unavailable")

	tzNamePattern   = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*$`)
	hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

// SystemTimeState describes the host clock, timezone, and NTP configuration.
type SystemTimeState struct {
	NowRFC3339      string   `json:"now_rfc3339"`
	Timezone        string   `json:"timezone"`
	NTPEnabled      bool     `json:"ntp_enabled"`
	NTPSynchronized bool     `json:"ntp_synchronized"`
	CanNTP          bool     `json:"can_ntp"`
	NTPBackend      string   `json:"ntp_backend"`
	NTPServers      []string `json:"ntp_servers"`
	Timezones       []string `json:"timezones,omitempty"`
}

// SystemTimeUpdate is the payload accepted by POST /system/time.
type SystemTimeUpdate struct {
	Timezone   string   `json:"timezone"`
	NTPEnabled bool     `json:"ntp_enabled"`
	NTPServers []string `json:"ntp_servers"`
	ManualTime string   `json:"manual_time"`
}

// SystemTimeRunner abstracts timedatectl and systemctl execution for testing.
type SystemTimeRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execSystemTimeRunner struct{}

func (execSystemTimeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// SystemTimeManager inspects and updates host timezone, NTP sources, and clock.
type SystemTimeManager struct {
	ChronySourcesDir string
	TimesyncdConfDir string
	Runner           SystemTimeRunner
	Now              func() time.Time

	mu sync.Mutex
}

func (m *SystemTimeManager) runner() SystemTimeRunner {
	if m.Runner != nil {
		return m.Runner
	}
	return execSystemTimeRunner{}
}

func (m *SystemTimeManager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *SystemTimeManager) chronyDir() string {
	if m.ChronySourcesDir != "" {
		return m.ChronySourcesDir
	}
	return DefaultChronySourcesDir
}

func (m *SystemTimeManager) timesyncdDir() string {
	if m.TimesyncdConfDir != "" {
		return m.TimesyncdConfDir
	}
	return DefaultTimesyncdConfDir
}

func (m *SystemTimeManager) detectBackend() string {
	if st, err := os.Stat(m.chronyDir()); err == nil && st.IsDir() {
		return "chrony"
	}
	if st, err := os.Stat(m.timesyncdDir()); err == nil && st.IsDir() {
		return "timesyncd"
	}
	return ""
}

// State reads current host timezone, NTP status, managed corporate servers, and timezones.
func (m *SystemTimeManager) State(ctx context.Context) (SystemTimeState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stateLocked(ctx, true)
}

func (m *SystemTimeManager) stateLocked(ctx context.Context, includeTimezones bool) (SystemTimeState, error) {
	out, err := m.runner().Run(ctx, "timedatectl", "show",
		"--property=Timezone",
		"--property=NTP",
		"--property=NTPSynchronized",
		"--property=CanNTP",
	)
	if err != nil {
		return SystemTimeState{}, err
	}
	st := parseTimedatectlShow(string(out))
	st.NTPBackend = m.detectBackend()
	st.NTPServers = m.readManagedServers(st.NTPBackend)
	if includeTimezones {
		if tzOut, err := m.runner().Run(ctx, "timedatectl", "list-timezones"); err == nil {
			st.Timezones = parseLines(string(tzOut))
		}
	}
	now := m.now()
	if st.Timezone != "" {
		if loc, err := time.LoadLocation(st.Timezone); err == nil && m.Now == nil {
			now = now.In(loc)
		}
	}
	st.NowRFC3339 = now.Format(time.RFC3339)
	return st, nil
}

// Apply validates and applies timezone, NTP mode/servers, and optional manual time.
func (m *SystemTimeManager) Apply(ctx context.Context, req SystemTimeUpdate) (SystemTimeState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current, err := m.stateLocked(ctx, true)
	if err != nil {
		return SystemTimeState{}, err
	}
	clean, err := ValidateSystemTimeUpdate(req, current.Timezones)
	if err != nil {
		return SystemTimeState{}, err
	}

	if clean.NTPEnabled && current.NTPBackend == "" && len(clean.NTPServers) > 0 {
		return SystemTimeState{}, ErrNTPBackendUnavailable
	}

	if clean.Timezone != current.Timezone {
		if _, err := m.runner().Run(ctx, "timedatectl", "set-timezone", clean.Timezone); err != nil {
			return SystemTimeState{}, err
		}
	}

	if clean.NTPEnabled {
		serversChanged := !slices.Equal(clean.NTPServers, current.NTPServers)
		if serversChanged && current.NTPBackend != "" {
			if err := m.writeManagedServers(current.NTPBackend, clean.NTPServers); err != nil {
				return SystemTimeState{}, err
			}
		}
		if !current.NTPEnabled || serversChanged {
			if _, err := m.runner().Run(ctx, "timedatectl", "set-ntp", "true"); err != nil {
				return SystemTimeState{}, err
			}
		}
		if (serversChanged || !current.NTPEnabled) && current.NTPBackend != "" {
			unit := ntpServiceUnit(current.NTPBackend)
			if unit != "" {
				if _, err := m.runner().Run(ctx, "systemctl", "restart", unit); err != nil {
					return SystemTimeState{}, err
				}
			}
		}
	} else {
		if current.NTPEnabled {
			if _, err := m.runner().Run(ctx, "timedatectl", "set-ntp", "false"); err != nil {
				return SystemTimeState{}, err
			}
		}
		if clean.ManualTime != "" {
			if _, err := m.runner().Run(ctx, "timedatectl", "set-time", clean.ManualTime); err != nil {
				return SystemTimeState{}, err
			}
		}
	}

	return m.stateLocked(ctx, true)
}

func ntpServiceUnit(backend string) string {
	switch backend {
	case "chrony":
		return "chrony"
	case "timesyncd":
		return "systemd-timesyncd"
	default:
		return ""
	}
}

func (m *SystemTimeManager) readManagedServers(backend string) []string {
	switch backend {
	case "chrony":
		data, err := os.ReadFile(filepath.Join(m.chronyDir(), chronyOMBFile))
		if err != nil {
			return nil
		}
		var servers []string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 && (fields[0] == "server" || fields[0] == "pool") {
				servers = append(servers, fields[1])
			}
		}
		return servers
	case "timesyncd":
		data, err := os.ReadFile(filepath.Join(m.timesyncdDir(), timesyncdOMBFile))
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "NTP=") {
				return strings.Fields(strings.TrimPrefix(line, "NTP="))
			}
		}
		return nil
	default:
		return nil
	}
}

func (m *SystemTimeManager) writeManagedServers(backend string, servers []string) error {
	var target string
	var content string
	switch backend {
	case "chrony":
		target = filepath.Join(m.chronyDir(), chronyOMBFile)
		if len(servers) > 0 {
			var b strings.Builder
			b.WriteString("# Managed by Oh My Boot\n")
			for _, host := range servers {
				b.WriteString("server ")
				b.WriteString(host)
				b.WriteString(" iburst prefer\n")
			}
			content = b.String()
		}
	case "timesyncd":
		target = filepath.Join(m.timesyncdDir(), timesyncdOMBFile)
		if len(servers) > 0 {
			content = "# Managed by Oh My Boot\n[Time]\nNTP=" + strings.Join(servers, " ") + "\n"
		}
	default:
		return ErrNTPBackendUnavailable
	}

	if len(servers) == 0 {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeAtomicMode(target, []byte(content), 0o644)
}

func writeAtomicMode(target string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".omb-time-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// ValidateSystemTimeUpdate validates timezone, corporate NTP hosts, and manual clock format.
func ValidateSystemTimeUpdate(req SystemTimeUpdate, allowedTimezones []string) (SystemTimeUpdate, error) {
	out := SystemTimeUpdate{
		Timezone:   strings.TrimSpace(req.Timezone),
		NTPEnabled: req.NTPEnabled,
	}
	if out.Timezone == "" || strings.Contains(out.Timezone, "..") || !tzNamePattern.MatchString(out.Timezone) {
		return SystemTimeUpdate{}, fmt.Errorf("%w: invalid timezone", ErrInvalidSystemTime)
	}
	if len(allowedTimezones) > 0 {
		if !slices.Contains(allowedTimezones, out.Timezone) {
			return SystemTimeUpdate{}, fmt.Errorf("%w: unknown timezone", ErrInvalidSystemTime)
		}
	} else {
		if _, err := time.LoadLocation(out.Timezone); err != nil {
			return SystemTimeUpdate{}, fmt.Errorf("%w: unknown timezone", ErrInvalidSystemTime)
		}
	}

	if out.NTPEnabled {
		if strings.TrimSpace(req.ManualTime) != "" {
			return SystemTimeUpdate{}, fmt.Errorf("%w: manual time not allowed when NTP is enabled", ErrInvalidSystemTime)
		}
		if len(req.NTPServers) > maxNTPServers {
			return SystemTimeUpdate{}, fmt.Errorf("%w: too many NTP servers", ErrInvalidSystemTime)
		}
		seen := make(map[string]struct{}, len(req.NTPServers))
		for _, raw := range req.NTPServers {
			host := strings.TrimSpace(raw)
			if host == "" {
				continue
			}
			if !validNTPHost(host) {
				return SystemTimeUpdate{}, fmt.Errorf("%w: invalid NTP server %q", ErrInvalidSystemTime, host)
			}
			if _, dup := seen[host]; dup {
				continue
			}
			seen[host] = struct{}{}
			out.NTPServers = append(out.NTPServers, host)
		}
	} else {
		manual := strings.TrimSpace(req.ManualTime)
		if manual != "" {
			parsed, err := parseManualTime(manual)
			if err != nil {
				return SystemTimeUpdate{}, fmt.Errorf("%w: %v", ErrInvalidSystemTime, err)
			}
			out.ManualTime = parsed
		}
	}
	return out, nil
}

func validNTPHost(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	if strings.ContainsAny(host, " \t\r\n#;") {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.IsValid() && addr.Zone() == ""
	}
	return hostnamePattern.MatchString(host)
}

func parseManualTime(raw string) (string, error) {
	layouts := []string{
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			if t.Year() < 2020 || t.Year() > 2099 {
				return "", errors.New("year out of allowed range 2020..2099")
			}
			return t.Format("2006-01-02 15:04:05"), nil
		}
	}
	return "", errors.New("invalid manual time format")
}

func parseTimedatectlShow(raw string) SystemTimeState {
	var st SystemTimeState
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "Timezone":
			st.Timezone = v
		case "NTP":
			st.NTPEnabled = v == "yes"
		case "NTPSynchronized":
			st.NTPSynchronized = v == "yes"
		case "CanNTP":
			st.CanNTP = v == "yes"
		}
	}
	return st
}

func parseLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
