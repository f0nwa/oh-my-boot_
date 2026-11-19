package control

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"omb/internal/boot"
)

const (
	systemEventRetention = 30 * 24 * time.Hour
	systemEventCooldown  = 30 * time.Minute
)

// SystemEvent is a server-level incident, warning, or recovery recorded in omb_control.
type SystemEvent struct {
	ID         int64
	OccurredAt time.Time
	Severity   string // error, warning, info
	Category   string // storage, service, network, task
	SourceKey  string
	Title      string
	Detail     string
	Link       string
}

// SystemEventFilter narrows the /events?tab=system feed.
type SystemEventFilter struct {
	Severity   string
	Category   string
	Query      string
	BeforeTime *time.Time
	BeforeID   int64
}

// SystemEventStore persists and queries server-level system events.
type SystemEventStore interface {
	RecordSystemEvent(ctx context.Context, ev SystemEvent) error
	SystemEventsPaged(ctx context.Context, filter SystemEventFilter, limit int) ([]SystemEvent, error)
	CountRecentSystemErrors(ctx context.Context, since time.Time) (int, error)
	PruneSystemEvents(ctx context.Context, olderThan time.Time) error
}

func (s *Server) recordSystemEvent(ctx context.Context, ev SystemEvent) {
	store, ok := s.Store.(SystemEventStore)
	if !ok {
		return
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	_ = store.RecordSystemEvent(ctx, ev)
}

func (s *Server) recentSystemErrorCount(ctx context.Context) int {
	store, ok := s.Store.(SystemEventStore)
	if !ok {
		return 0
	}
	n, err := store.CountRecentSystemErrors(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		return 0
	}
	return n
}

func systemEventSeverityLabel(sev string) string {
	switch sev {
	case "error":
		return "Ошибка"
	case "warning":
		return "Предупреждение"
	default:
		return "Событие"
	}
}

func systemEventCategoryLabel(cat string) string {
	switch cat {
	case "storage":
		return "Хранилища"
	case "service":
		return "Службы"
	case "network":
		return "Сеть и DHCP"
	case "task":
		return "Фоновые задачи"
	default:
		return cat
	}
}

func systemEventLinkLabel(link string) string {
	switch {
	case strings.HasPrefix(link, "/storages"):
		return "Хранилища →"
	case strings.HasPrefix(link, "/images"):
		return "Образы ISO →"
	case strings.HasPrefix(link, "/installations"):
		return "Загрузки →"
	case strings.HasPrefix(link, "/events/machines/"):
		return "Машина →"
	case strings.HasPrefix(link, "/machines"):
		return "Машины →"
	case strings.HasPrefix(link, "/system"):
		return "Система →"
	case link == "/":
		return "Сейчас →"
	default:
		return "Открыть →"
	}
}

// SystemEventMonitor watches OMB services, storages, DHCP flood, and stalled
// client sessions in the background and records transitions into system_events.
type SystemEventMonitor struct {
	server *Server

	mu           sync.Mutex
	serviceUp    map[string]bool
	storageState map[int64]string
	storageName  map[int64]string
	lastAlert    map[string]time.Time
	lastPrune    time.Time
}

// NewSystemEventMonitor creates a background monitor attached to Server.
func NewSystemEventMonitor(s *Server) *SystemEventMonitor {
	return &SystemEventMonitor{
		server:       s,
		serviceUp:    map[string]bool{},
		storageState: map[int64]string{},
		storageName:  map[int64]string{},
		lastAlert:    map[string]time.Time{},
	}
}

// Start runs the periodic monitor loop until ctx is cancelled.
func (m *SystemEventMonitor) Start(ctx context.Context) {
	go func() {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				tickCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
				m.CheckOnce(tickCtx, time.Now())
				cancel()
				timer.Reset(30 * time.Second)
			}
		}
	}()
}

// CheckOnce performs one observation pass and records state transitions.
func (m *SystemEventMonitor) CheckOnce(ctx context.Context, now time.Time) {
	if m.server == nil {
		return
	}
	store, ok := m.server.Store.(SystemEventStore)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.lastPrune.IsZero() || now.Sub(m.lastPrune) >= 12*time.Hour {
		_ = store.PruneSystemEvents(ctx, now.UTC().Add(-systemEventRetention))
		m.lastPrune = now
	}

	// 1. Check omb-boot availability
	bootEvents, bootOK := m.checkBoot(ctx, store, now)
	// 2. Check omb-agent availability
	m.checkAgent(ctx, store, now)
	// 3. Check omb-images availability and storage state transitions
	m.checkImagesAndStorages(ctx, store, now)
	// 4. Check DHCP flood and stalled SMB/HTTP sessions
	m.checkNetworkAndSessions(ctx, store, bootEvents, bootOK, now)
}

func (m *SystemEventMonitor) recordServiceTransition(ctx context.Context, store SystemEventStore, key, displayName string, up bool, errText string, now time.Time) {
	prev, known := m.serviceUp[key]
	m.serviceUp[key] = up
	if !known {
		if !up {
			_ = store.RecordSystemEvent(ctx, SystemEvent{
				OccurredAt: now.UTC(),
				Severity:   "error",
				Category:   "service",
				SourceKey:  "service:" + key,
				Title:      fmt.Sprintf("Служба %s недоступна", displayName),
				Detail:     errText,
				Link:       "/system",
			})
		}
		return
	}
	if prev && !up {
		_ = store.RecordSystemEvent(ctx, SystemEvent{
			OccurredAt: now.UTC(),
			Severity:   "error",
			Category:   "service",
			SourceKey:  "service:" + key,
			Title:      fmt.Sprintf("Служба %s недоступна", displayName),
			Detail:     errText,
			Link:       "/system",
		})
	} else if !prev && up {
		_ = store.RecordSystemEvent(ctx, SystemEvent{
			OccurredAt: now.UTC(),
			Severity:   "info",
			Category:   "service",
			SourceKey:  "service:" + key,
			Title:      fmt.Sprintf("Служба %s снова отвечает", displayName),
			Detail:     "Связь со службой восстановлена",
			Link:       "/system",
		})
	}
}

func (m *SystemEventMonitor) checkBoot(ctx context.Context, store SystemEventStore, now time.Time) ([]boot.Event, bool) {
	if m.server.Boot == nil {
		return nil, false
	}
	ev, err := m.server.Boot.Events(ctx, 200)
	if err != nil {
		m.recordServiceTransition(ctx, store, "omb-boot", "omb-boot", false, err.Error(), now)
		return nil, false
	}
	m.recordServiceTransition(ctx, store, "omb-boot", "omb-boot", true, "", now)
	return ev, true
}

func (m *SystemEventMonitor) checkAgent(ctx context.Context, store SystemEventStore, now time.Time) {
	if m.server.Agent == nil {
		return
	}
	if _, err := m.server.Agent.Config(ctx); err != nil {
		m.recordServiceTransition(ctx, store, "omb-agent", "omb-agent", false, err.Error(), now)
		return
	}
	m.recordServiceTransition(ctx, store, "omb-agent", "omb-agent", true, "", now)
}

func (m *SystemEventMonitor) checkImagesAndStorages(ctx context.Context, store SystemEventStore, now time.Time) {
	if m.server.Images == nil {
		return
	}
	catalog, err := m.server.Images.Catalog(ctx)
	if err != nil {
		m.recordServiceTransition(ctx, store, "omb-images", "omb-images", false, err.Error(), now)
		return
	}
	m.recordServiceTransition(ctx, store, "omb-images", "omb-images", true, "", now)

	seen := map[int64]bool{}
	for _, st := range catalog.Storages {
		seen[st.ID] = true
		prev, known := m.storageState[st.ID]
		m.storageState[st.ID] = st.State
		m.storageName[st.ID] = st.Name

		if !known {
			if st.State == "unavailable" {
				detail := strings.TrimSpace(st.Error)
				if detail == "" {
					detail = st.Kind
				}
				_ = store.RecordSystemEvent(ctx, SystemEvent{
					OccurredAt: now.UTC(),
					Severity:   "error",
					Category:   "storage",
					SourceKey:  fmt.Sprintf("storage:%d", st.ID),
					Title:      fmt.Sprintf("Хранилище «%s» недоступно", st.Name),
					Detail:     detail,
					Link:       "/storages",
				})
			}
			continue
		}
		if prev != "unavailable" && st.State == "unavailable" {
			detail := strings.TrimSpace(st.Error)
			if detail == "" {
				detail = st.Kind
			}
			_ = store.RecordSystemEvent(ctx, SystemEvent{
				OccurredAt: now.UTC(),
				Severity:   "error",
				Category:   "storage",
				SourceKey:  fmt.Sprintf("storage:%d", st.ID),
				Title:      fmt.Sprintf("Хранилище «%s» недоступно", st.Name),
				Detail:     detail,
				Link:       "/storages",
			})
		} else if prev == "unavailable" && st.State == "ready" {
			_ = store.RecordSystemEvent(ctx, SystemEvent{
				OccurredAt: now.UTC(),
				Severity:   "info",
				Category:   "storage",
				SourceKey:  fmt.Sprintf("storage:%d", st.ID),
				Title:      fmt.Sprintf("Хранилище «%s» восстановлено", st.Name),
				Detail:     "Подключение снова доступно",
				Link:       "/storages",
			})
		}
	}
	for id := range m.storageState {
		if !seen[id] {
			delete(m.storageState, id)
			delete(m.storageName, id)
		}
	}
}

func (m *SystemEventMonitor) allowAlert(key string, now time.Time) bool {
	if last, ok := m.lastAlert[key]; ok && now.Sub(last) < systemEventCooldown {
		return false
	}
	m.lastAlert[key] = now
	return true
}

func (m *SystemEventMonitor) checkNetworkAndSessions(ctx context.Context, store SystemEventStore, httpEvents []boot.Event, bootOK bool, now time.Time) {
	if m.server.Agent == nil && !bootOK {
		return
	}
	dhcp, _ := m.server.dhcpEvents(ctx)
	rawSMB, smbKnown := m.server.loadRawSMBSessions(ctx)
	var install installLog
	if smbKnown {
		install = installEvents(rawSMB, httpEvents)
	}
	transfers := m.server.bootTransfers(ctx)
	rows := machineRowsWith(httpEvents, dhcp, install, nil, now)
	attachActiveHTTPTransfers(rows, transfers, now)
	if smbKnown {
		attachActiveSMBSessions(rows, rawSMB, httpEvents, now)
	}
	detectDHCPFlood(rows, dhcp, now)
	describeMachines(rows, m.server.machineBook(ctx, rows, httpEvents), m.server.macRules(ctx))

	for _, row := range rows {
		if row.DHCPIgnored {
			continue
		}
		if row.DHCPFlood && m.allowAlert("dhcp_flood:"+row.MAC, now) {
			_ = store.RecordSystemEvent(ctx, SystemEvent{
				OccurredAt: now.UTC(),
				Severity:   "warning",
				Category:   "network",
				SourceKey:  "dhcp_flood:" + row.MAC,
				Title:      fmt.Sprintf("Обнаружен DHCP-флуд от %s", row.MAC),
				Detail:     fmt.Sprintf("Запросов за 2 мин: %d", row.DHCPFloodCount),
				Link:       "/events/machines/" + row.MAC,
			})
		}
		if row.ActiveSMB != nil && row.ActiveSMB.Stale && m.allowAlert("stale_smb:"+row.ActiveSMB.ID, now) {
			label := row.Name
			if label == "" {
				label = row.MAC
			}
			_ = store.RecordSystemEvent(ctx, SystemEvent{
				OccurredAt: now.UTC(),
				Severity:   "warning",
				Category:   "network",
				SourceKey:  "stale_smb:" + row.ActiveSMB.ID,
				Title:      fmt.Sprintf("Зависшая SMB-сессия WinPE на %s", label),
				Detail:     fmt.Sprintf("Нет обмена %d мин (PID %s)", row.ActiveSMB.IdleMinutes, row.ActiveSMB.PID),
				Link:       "/events/machines/" + row.MAC,
			})
		}
		if row.ActiveHTTP != nil && row.ActiveHTTP.Stale {
			httpKey := fmt.Sprintf("stale_http:%d", row.ActiveHTTP.ID)
			if m.allowAlert(httpKey, now) {
				label := row.Name
				if label == "" {
					label = row.MAC
				}
				_ = store.RecordSystemEvent(ctx, SystemEvent{
					OccurredAt: now.UTC(),
					Severity:   "warning",
					Category:   "network",
					SourceKey:  httpKey,
					Title:      fmt.Sprintf("Зависшая HTTP-передача на %s", label),
					Detail:     fmt.Sprintf("Файл %s, нет обмена %d мин", fileName(row.ActiveHTTP.Path), row.ActiveHTTP.IdleMinutes),
					Link:       "/events/machines/" + row.MAC,
				})
			}
		}
	}
}
