package control

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"omb/internal/boot"
	"omb/internal/images"
)

//go:embed maintenance.js
var maintenanceJS []byte

func maintenanceScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(maintenanceJS)
}

// MaintenanceLock marks a storage or an individual ISO image as paused for
// new PXE deployments (Drain) while in-flight installations finish.
type MaintenanceLock struct {
	ID        int64
	Scope     string // "storage" or "image"
	StorageID int64
	ImagePath string
	Reason    string
	CreatedBy int64
	CreatedAt time.Time
}

// MaintenanceStore persists active maintenance mode locks.
type MaintenanceStore interface {
	MaintenanceLocks(context.Context) ([]MaintenanceLock, error)
	SetMaintenanceLock(ctx context.Context, scope string, storageID int64, imagePath, reason string, userID int64) error
	DeleteMaintenanceLock(ctx context.Context, scope string, storageID int64, imagePath string) error
}

// BootMaintenanceAPI pushes the derived maintenance state to omb-boot.
type BootMaintenanceAPI interface {
	SetMaintenance(context.Context, boot.MaintenanceState) error
}

type maintenanceLookup struct {
	locks     []MaintenanceLock
	byStorage map[int64]MaintenanceLock
	byImage   map[string]MaintenanceLock
}

func newMaintenanceLookup(locks []MaintenanceLock) maintenanceLookup {
	l := maintenanceLookup{
		locks:     locks,
		byStorage: make(map[int64]MaintenanceLock),
		byImage:   make(map[string]MaintenanceLock),
	}
	for _, lock := range locks {
		if lock.Scope == "storage" {
			l.byStorage[lock.StorageID] = lock
		} else if lock.Scope == "image" {
			l.byImage[imageAuthorKey(lock.StorageID, lock.ImagePath)] = lock
		}
	}
	return l
}

func (l maintenanceLookup) StorageLock(storageID int64) (MaintenanceLock, bool) {
	lock, ok := l.byStorage[storageID]
	return lock, ok
}

// ImageLock returns either the image's own maintenance lock (inherited=false) or
// its parent storage lock (inherited=true).
func (l maintenanceLookup) ImageLock(storageID int64, path string) (MaintenanceLock, bool, bool) {
	if lock, ok := l.byImage[imageAuthorKey(storageID, path)]; ok {
		return lock, false, true
	}
	if lock, ok := l.byStorage[storageID]; ok {
		return lock, true, true
	}
	return MaintenanceLock{}, false, false
}

func (s *Server) bootMaintenanceAPI() BootMaintenanceAPI {
	if !isNilProvider(s.BootMaintenance) {
		return s.BootMaintenance
	}
	if s.Boot != nil {
		return s.Boot
	}
	return nil
}

func (s *Server) loadMaintenanceLookup(ctx context.Context) maintenanceLookup {
	store, ok := s.Store.(MaintenanceStore)
	if !ok {
		return newMaintenanceLookup(nil)
	}
	locks, err := store.MaintenanceLocks(ctx)
	if err != nil {
		return newMaintenanceLookup(nil)
	}
	return newMaintenanceLookup(locks)
}

func (s *Server) installationMaintenanceLocked(ctx context.Context, inst Installation, sources InstallationMenuSources, profiles []WindowsProfile) bool {
	lookup := s.loadMaintenanceLookup(ctx)
	if len(lookup.locks) == 0 {
		return false
	}
	for _, bp := range sources.BootProfiles {
		if bp.ID != inst.BootProfileID {
			continue
		}
		switch bp.Kind {
		case BootProfileWindowsISO, BootProfileWinPEISO:
			for _, wp := range profiles {
				if wp.ID == bp.WindowsProfileID {
					_, _, locked := lookup.ImageLock(wp.StorageID, wp.Path)
					return locked
				}
			}
		case BootProfileUbuntuISO, BootProfileUbuntuBuiltin:
			if bp.UbuntuStorageID > 0 {
				_, _, locked := lookup.ImageLock(bp.UbuntuStorageID, bp.UbuntuPath)
				return locked
			}
		}
	}
	return false
}

// SyncMaintenance recomputes the set of paused installations and boot asset
// namespaces from maintenance_locks and pushes it to omb-boot.
func (s *Server) SyncMaintenance(ctx context.Context) error {
	api := s.bootMaintenanceAPI()
	if api == nil {
		return nil
	}
	store, ok := s.Store.(MaintenanceStore)
	if !ok {
		return nil
	}
	locks, err := store.MaintenanceLocks(ctx)
	if err != nil {
		return err
	}
	if len(locks) == 0 {
		return api.SetMaintenance(ctx, boot.MaintenanceState{})
	}
	lookup := newMaintenanceLookup(locks)
	installs, err := s.Store.Installations(ctx)
	if err != nil {
		return err
	}
	bootProfiles, err := s.Store.BootProfiles(ctx)
	if err != nil {
		return err
	}
	winProfiles, err := s.Store.WindowsProfiles(ctx)
	if err != nil {
		return err
	}
	bpByID := make(map[int64]BootProfile, len(bootProfiles))
	for _, bp := range bootProfiles {
		bpByID[bp.ID] = bp
	}
	wpByID := make(map[int64]WindowsProfile, len(winProfiles))
	for _, wp := range winProfiles {
		wpByID[wp.ID] = wp
	}

	var state boot.MaintenanceState
	// Always block matching Windows profiles and Ubuntu sources even if they have no installation yet.
	for _, wp := range winProfiles {
		if _, _, blocked := lookup.ImageLock(wp.StorageID, wp.Path); blocked {
			state.WindowsProfileIDs = append(state.WindowsProfileIDs, wp.ID)
		}
	}
	for _, bp := range bootProfiles {
		if bp.Kind == BootProfileUbuntuISO && bp.UbuntuStorageID > 0 {
			if _, _, blocked := lookup.ImageLock(bp.UbuntuStorageID, bp.UbuntuPath); blocked {
				if bp.UbuntuSourceID > 0 {
					state.UbuntuSourceIDs = append(state.UbuntuSourceIDs, bp.UbuntuSourceID)
				}
			}
		} else if bp.Kind == BootProfileUbuntuBuiltin && bp.UbuntuStorageID > 0 {
			if _, _, blocked := lookup.ImageLock(bp.UbuntuStorageID, bp.UbuntuPath); blocked {
				state.BlockLegacyUbuntu = true
			}
		}
	}
	for _, inst := range installs {
		bp, ok := bpByID[inst.BootProfileID]
		if !ok {
			continue
		}
		blocked := false
		switch bp.Kind {
		case BootProfileWindowsISO, BootProfileWinPEISO:
			if wp, ok := wpByID[bp.WindowsProfileID]; ok {
				_, _, blocked = lookup.ImageLock(wp.StorageID, wp.Path)
			}
		case BootProfileUbuntuISO:
			if bp.UbuntuStorageID > 0 {
				_, _, blocked = lookup.ImageLock(bp.UbuntuStorageID, bp.UbuntuPath)
			}
		case BootProfileUbuntuBuiltin:
			if bp.UbuntuStorageID > 0 {
				_, _, blocked = lookup.ImageLock(bp.UbuntuStorageID, bp.UbuntuPath)
			}
		}
		if blocked {
			state.InstallationIDs = append(state.InstallationIDs, inst.ID)
			if inst.DriverConfigID > 0 {
				state.DriverConfigIDs = append(state.DriverConfigIDs, inst.DriverConfigID)
			}
		}
	}
	return api.SetMaintenance(ctx, state)
}

type maintenanceDrainCounts struct {
	byStorage map[int64]int
	byImage   map[string]int
}

// countActiveDrainClients counts live SMB sessions and in-flight HTTP transfers
// currently touching each storage and ISO image.
func (s *Server) countActiveDrainClients(ctx context.Context) maintenanceDrainCounts {
	counts := maintenanceDrainCounts{
		byStorage: map[int64]int{},
		byImage:   map[string]int{},
	}
	var liveSMB []int64
	if rawSMB, ok := s.loadRawSMBSessions(ctx); ok {
		for _, sess := range rawSMB.Sessions {
			if sess.Ended != nil {
				continue
			}
			if pid, err := strconv.ParseInt(strings.TrimSpace(sess.Profile), 10, 64); err == nil && pid > 0 {
				liveSMB = append(liveSMB, pid)
			}
		}
	}
	transfers := s.bootTransfers(ctx)
	if len(liveSMB) == 0 && len(transfers) == 0 {
		return counts
	}
	winProfiles, _ := s.Store.WindowsProfiles(ctx)
	wpByID := make(map[int64]WindowsProfile, len(winProfiles))
	for _, wp := range winProfiles {
		wpByID[wp.ID] = wp
	}
	add := func(storageID int64, imgPath string) {
		if storageID <= 0 {
			return
		}
		counts.byStorage[storageID]++
		if imgPath != "" {
			counts.byImage[imageAuthorKey(storageID, imgPath)]++
		}
	}
	for _, pid := range liveSMB {
		if wp, ok := wpByID[pid]; ok {
			add(wp.StorageID, wp.Path)
		}
	}
	if len(transfers) > 0 {
		bootProfiles, _ := s.Store.BootProfiles(ctx)
		ubuBySourceID := map[int64]BootProfile{}
		for _, bp := range bootProfiles {
			if bp.UbuntuSourceID > 0 {
				ubuBySourceID[bp.UbuntuSourceID] = bp
			}
		}
		driverConfigs, _ := s.Store.DriverConfigs(ctx)
		cfgByID := map[int64]DriverConfig{}
		for _, dc := range driverConfigs {
			cfgByID[dc.ID] = dc
		}
		for _, tr := range transfers {
			if rest, ok := strings.CutPrefix(tr.Path, "/assets/windows-profiles/"); ok {
				seg, _, _ := strings.Cut(rest, "/")
				if id, err := strconv.ParseInt(seg, 10, 64); err == nil {
					if wp, ok := wpByID[id]; ok {
						add(wp.StorageID, wp.Path)
					}
				}
			} else if rest, ok := strings.CutPrefix(tr.Path, "/assets/windows-driver-configs/"); ok {
				seg, _, _ := strings.Cut(rest, "/")
				if id, err := strconv.ParseInt(seg, 10, 64); err == nil {
					if dc, ok := cfgByID[id]; ok {
						if wp, ok := wpByID[dc.ProfileID]; ok {
							add(wp.StorageID, wp.Path)
						}
					}
				}
			} else if rest, ok := strings.CutPrefix(tr.Path, "/assets/ubuntu/"); ok {
				seg, _, _ := strings.Cut(rest, "/")
				if id, err := strconv.ParseInt(seg, 10, 64); err == nil {
					if bp, ok := ubuBySourceID[id]; ok {
						add(bp.UbuntuStorageID, bp.UbuntuPath)
					}
				}
			} else if rest, ok := strings.CutPrefix(tr.Path, "/images/ubuntu/"); ok {
				seg, _, _ := strings.Cut(rest, "/")
				if id, err := strconv.ParseInt(seg, 10, 64); err == nil {
					if bp, ok := ubuBySourceID[id]; ok {
						add(bp.UbuntuStorageID, bp.UbuntuPath)
					}
				}
			}
		}
	}
	return counts
}

func (s *Server) maintenanceEnable(w http.ResponseWriter, r *http.Request) {
	store, ok := s.Store.(MaintenanceStore)
	if !ok {
		http.Error(w, "Режим обслуживания недоступен", http.StatusServiceUnavailable)
		return
	}
	scope := strings.TrimSpace(r.PostFormValue("scope"))
	if scope != "storage" && scope != "image" {
		http.Error(w, "Неверная область обслуживания", http.StatusBadRequest)
		return
	}
	storageID, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("storage_id")), 10, 64)
	if err != nil || storageID <= 0 {
		http.Error(w, "Неверное хранилище", http.StatusBadRequest)
		return
	}
	imagePath := strings.TrimSpace(r.PostFormValue("path"))
	if scope == "storage" {
		imagePath = ""
	} else if imagePath == "" {
		http.Error(w, "Укажите образ ISO", http.StatusBadRequest)
		return
	}
	reason, valid := cleanMachineText(r.PostFormValue("reason"), 160)
	if !valid {
		http.Error(w, "Причина обслуживания слишком длинная или содержит недопустимые символы", http.StatusBadRequest)
		return
	}
	userID := requestInfo(r).User.ID
	if err := store.SetMaintenanceLock(r.Context(), scope, storageID, imagePath, reason, userID); err != nil {
		http.Error(w, "Не удалось включить режим обслуживания", http.StatusServiceUnavailable)
		return
	}
	syncErr := s.SyncMaintenance(r.Context())
	_ = s.SyncLocalFileServer(r.Context())
	detail := fmt.Sprintf("scope=%s storage=%d path=%s", scope, storageID, imagePath)
	if syncErr != nil {
		_ = s.Store.Audit(r.Context(), userID, "maintenance.enable", "apply_error", detail)
		s.recordSystemEvent(r.Context(), SystemEvent{
			Severity:  "error",
			Category:  "task",
			SourceKey: fmt.Sprintf("maintenance:%s:%d:%s", scope, storageID, imagePath),
			Title:     "Не удалось применить режим обслуживания в Boot",
			Detail:    syncErr.Error(),
			Link:      "/storages",
		})
	} else {
		_ = s.Store.Audit(r.Context(), userID, "maintenance.enable", "ok", detail)
		link := "/storages"
		if scope == "image" {
			link = "/images"
		}
		s.recordSystemEvent(r.Context(), SystemEvent{
			Severity:  "info",
			Category:  "task",
			SourceKey: fmt.Sprintf("maintenance:%s:%d:%s", scope, storageID, imagePath),
			Title:     "Включён режим обслуживания",
			Detail:    strings.TrimSpace(detail + " " + reason),
			Link:      link,
		})
	}
	fallback := "/storages"
	if scope == "image" {
		fallback = "/images"
	}
	target := safeSessionReturnPath(r.PostFormValue("return_to"), fallback)
	if target == "/images" {
		target = safeImagesReturnURL(r.PostFormValue("return_to"))
	}
	msg := "Режим обслуживания включён"
	if syncErr != nil {
		msg = "Режим обслуживания сохранён, но не применён в Boot"
	}
	redirectWithNotice(w, r, target, msg)
}

func (s *Server) maintenanceDisable(w http.ResponseWriter, r *http.Request) {
	store, ok := s.Store.(MaintenanceStore)
	if !ok {
		http.Error(w, "Режим обслуживания недоступен", http.StatusServiceUnavailable)
		return
	}
	scope := strings.TrimSpace(r.PostFormValue("scope"))
	if scope != "storage" && scope != "image" {
		http.Error(w, "Неверная область обслуживания", http.StatusBadRequest)
		return
	}
	storageID, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("storage_id")), 10, 64)
	if err != nil || storageID <= 0 {
		http.Error(w, "Неверное хранилище", http.StatusBadRequest)
		return
	}
	imagePath := strings.TrimSpace(r.PostFormValue("path"))
	if scope == "storage" {
		imagePath = ""
	} else if imagePath == "" {
		http.Error(w, "Укажите образ ISO", http.StatusBadRequest)
		return
	}
	userID := requestInfo(r).User.ID
	if err := store.DeleteMaintenanceLock(r.Context(), scope, storageID, imagePath); err != nil {
		http.Error(w, "Не удалось завершить режим обслуживания", http.StatusServiceUnavailable)
		return
	}
	syncErr := s.SyncMaintenance(r.Context())
	_ = s.SyncLocalFileServer(r.Context())
	detail := fmt.Sprintf("scope=%s storage=%d path=%s", scope, storageID, imagePath)
	if syncErr != nil {
		_ = s.Store.Audit(r.Context(), userID, "maintenance.disable", "apply_error", detail)
		s.recordSystemEvent(r.Context(), SystemEvent{
			Severity:  "error",
			Category:  "task",
			SourceKey: fmt.Sprintf("maintenance:%s:%d:%s", scope, storageID, imagePath),
			Title:     "Не удалось снять режим обслуживания в Boot",
			Detail:    syncErr.Error(),
			Link:      "/storages",
		})
	} else {
		_ = s.Store.Audit(r.Context(), userID, "maintenance.disable", "ok", detail)
		link := "/storages"
		if scope == "image" {
			link = "/images"
		}
		s.recordSystemEvent(r.Context(), SystemEvent{
			Severity:  "info",
			Category:  "task",
			SourceKey: fmt.Sprintf("maintenance:%s:%d:%s", scope, storageID, imagePath),
			Title:     "Режим обслуживания завершён",
			Detail:    detail,
			Link:      link,
		})
	}
	fallback := "/storages"
	if scope == "image" {
		fallback = "/images"
	}
	target := safeSessionReturnPath(r.PostFormValue("return_to"), fallback)
	if target == "/images" {
		target = safeImagesReturnURL(r.PostFormValue("return_to"))
	}
	msg := "Режим обслуживания завершён"
	if syncErr != nil {
		msg = "Режим обслуживания снят, но не применён в Boot"
	}
	redirectWithNotice(w, r, target, msg)
}

func (s *Server) nowMaintenanceAlerts(ctx context.Context, storages []images.Storage) []nowAlert {
	lookup := s.loadMaintenanceLookup(ctx)
	if len(lookup.locks) == 0 {
		return nil
	}
	names := map[int64]string{}
	for _, st := range storages {
		names[st.ID] = st.Name
	}
	counts := s.countActiveDrainClients(ctx)
	var alerts []nowAlert
	for _, lock := range lookup.locks {
		if lock.Scope == "storage" {
			name := names[lock.StorageID]
			if name == "" {
				name = fmt.Sprintf("#%d", lock.StorageID)
			}
			active := counts.byStorage[lock.StorageID]
			detail := "Новые загрузки заблокированы · 0 активных клиентов"
			if active > 0 {
				detail = fmt.Sprintf("Новые загрузки заблокированы · активных клиентов: %d", active)
			}
			alerts = append(alerts, nowAlert{
				State:  "accent",
				Title:  fmt.Sprintf("Хранилище «%s» на обслуживании", name),
				Detail: detail,
				Link:   "/storages",
			})
		} else if lock.Scope == "image" {
			active := counts.byImage[imageAuthorKey(lock.StorageID, lock.ImagePath)]
			detail := "Новые загрузки заблокированы · 0 активных клиентов"
			if active > 0 {
				detail = fmt.Sprintf("Новые загрузки заблокированы · активных клиентов: %d", active)
			}
			alerts = append(alerts, nowAlert{
				State:  "accent",
				Title:  fmt.Sprintf("Образ «%s» на обслуживании", lock.ImagePath),
				Detail: detail,
				Link:   "/images",
			})
		}
	}
	return alerts
}
