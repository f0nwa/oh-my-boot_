package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/boot"
	"omb/internal/images"
)

type maintTestStore struct {
	authOnlyStore
	locks        []MaintenanceLock
	installs     []Installation
	bootProfiles []BootProfile
	winProfiles  []WindowsProfile
	audits       []AuditEvent
}

func (m *maintTestStore) MaintenanceLocks(context.Context) ([]MaintenanceLock, error) {
	return append([]MaintenanceLock(nil), m.locks...), nil
}

func (m *maintTestStore) SetMaintenanceLock(_ context.Context, scope string, storageID int64, imagePath, reason string, userID int64) error {
	for i := range m.locks {
		if m.locks[i].Scope == scope && m.locks[i].StorageID == storageID && m.locks[i].ImagePath == imagePath {
			m.locks[i].Reason = reason
			m.locks[i].CreatedBy = userID
			m.locks[i].CreatedAt = time.Now()
			return nil
		}
	}
	m.locks = append(m.locks, MaintenanceLock{
		ID:        int64(len(m.locks) + 1),
		Scope:     scope,
		StorageID: storageID,
		ImagePath: imagePath,
		Reason:    reason,
		CreatedBy: userID,
		CreatedAt: time.Now(),
	})
	return nil
}

func (m *maintTestStore) DeleteMaintenanceLock(_ context.Context, scope string, storageID int64, imagePath string) error {
	out := m.locks[:0]
	for _, lock := range m.locks {
		if lock.Scope == scope && lock.StorageID == storageID && lock.ImagePath == imagePath {
			continue
		}
		out = append(out, lock)
	}
	m.locks = out
	return nil
}

func (m *maintTestStore) Installations(context.Context) ([]Installation, error) {
	return append([]Installation(nil), m.installs...), nil
}

func (m *maintTestStore) BootProfiles(context.Context) ([]BootProfile, error) {
	return append([]BootProfile(nil), m.bootProfiles...), nil
}

func (m *maintTestStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return append([]WindowsProfile(nil), m.winProfiles...), nil
}

func (m *maintTestStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	m.audits = append(m.audits, AuditEvent{Action: action, Result: result, Detail: detail})
	return nil
}

func (m *maintTestStore) AuditList(context.Context) ([]AuditEvent, error) { return m.audits, nil }

type fakeBootMaint struct {
	calls []boot.MaintenanceState
}

func (f *fakeBootMaint) SetMaintenance(_ context.Context, state boot.MaintenanceState) error {
	f.calls = append(f.calls, state)
	return nil
}

func TestSyncMaintenanceStorageAndImageLocks(t *testing.T) {
	store := &maintTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		winProfiles: []WindowsProfile{
			{ID: 10, WindowsProfileSource: WindowsProfileSource{StorageID: 2, Path: "win11.iso"}, State: "ready"},
			{ID: 11, WindowsProfileSource: WindowsProfileSource{StorageID: 3, Path: "win10.iso"}, State: "ready"},
		},
		bootProfiles: []BootProfile{
			{ID: 100, Kind: BootProfileWindowsISO, WindowsProfileID: 10},
			{ID: 101, Kind: BootProfileUbuntuISO, UbuntuStorageID: 2, UbuntuPath: "ubuntu-24.04.iso", UbuntuSourceID: 55},
			{ID: 102, Kind: BootProfileWindowsISO, WindowsProfileID: 11},
		},
		installs: []Installation{
			{ID: 1, Title: "Win11", BootProfileID: 100, DriverConfigID: 77},
			{ID: 2, Title: "Ubuntu", BootProfileID: 101},
			{ID: 3, Title: "Win10", BootProfileID: 102},
		},
		locks: []MaintenanceLock{
			{ID: 1, Scope: "storage", StorageID: 2, Reason: "NAS firmware"},
		},
	}
	bm := &fakeBootMaint{}
	s := &Server{Store: store, BootMaintenance: bm}

	if err := s.SyncMaintenance(context.Background()); err != nil {
		t.Fatalf("SyncMaintenance: %v", err)
	}
	if len(bm.calls) != 1 {
		t.Fatalf("expected 1 SetMaintenance call, got %d", len(bm.calls))
	}
	got := bm.calls[0]
	if len(got.InstallationIDs) != 2 || got.InstallationIDs[0] != 1 || got.InstallationIDs[1] != 2 {
		t.Fatalf("InstallationIDs = %v, want [1 2]", got.InstallationIDs)
	}
	if len(got.WindowsProfileIDs) != 1 || got.WindowsProfileIDs[0] != 10 {
		t.Fatalf("WindowsProfileIDs = %v, want [10]", got.WindowsProfileIDs)
	}
	if len(got.DriverConfigIDs) != 1 || got.DriverConfigIDs[0] != 77 {
		t.Fatalf("DriverConfigIDs = %v, want [77]", got.DriverConfigIDs)
	}
	if len(got.UbuntuSourceIDs) != 1 || got.UbuntuSourceIDs[0] != 55 {
		t.Fatalf("UbuntuSourceIDs = %v, want [55]", got.UbuntuSourceIDs)
	}

	// Switch to image-level lock on win10.iso only
	store.locks = []MaintenanceLock{
		{ID: 2, Scope: "image", StorageID: 3, ImagePath: "win10.iso"},
	}
	if err := s.SyncMaintenance(context.Background()); err != nil {
		t.Fatalf("SyncMaintenance image lock: %v", err)
	}
	got2 := bm.calls[1]
	if len(got2.InstallationIDs) != 1 || got2.InstallationIDs[0] != 3 {
		t.Fatalf("InstallationIDs = %v, want [3]", got2.InstallationIDs)
	}
	if len(got2.WindowsProfileIDs) != 1 || got2.WindowsProfileIDs[0] != 11 {
		t.Fatalf("WindowsProfileIDs = %v, want [11]", got2.WindowsProfileIDs)
	}

	// Clear locks
	store.locks = nil
	if err := s.SyncMaintenance(context.Background()); err != nil {
		t.Fatalf("SyncMaintenance empty: %v", err)
	}
	got3 := bm.calls[2]
	if len(got3.InstallationIDs) != 0 || len(got3.WindowsProfileIDs) != 0 || len(got3.UbuntuSourceIDs) != 0 {
		t.Fatalf("expected empty state, got %+v", got3)
	}
}

func TestMaintenanceEnableAndDisableHandlers(t *testing.T) {
	store := &maintTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
	}
	bm := &fakeBootMaint{}
	s := &Server{Store: store, BootMaintenance: bm}
	h := s.Handler()
	token := "test-token"
	csrf := CSRFToken(token)

	// Enable storage maintenance as admin
	form := url.Values{
		"csrf":       {csrf},
		"scope":      {"storage"},
		"storage_id": {"2"},
		"reason":     {"Обновление NAS"},
		"return_to":  {"/storages"},
	}
	req := httptest.NewRequest("POST", "/maintenance/enable", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("enable status = %d, body = %s", res.Code, res.Body.String())
	}
	if len(store.locks) != 1 || store.locks[0].Scope != "storage" || store.locks[0].StorageID != 2 || store.locks[0].Reason != "Обновление NAS" {
		t.Fatalf("unexpected locks: %+v", store.locks)
	}
	if len(bm.calls) != 1 {
		t.Fatalf("expected SetMaintenance call on enable")
	}
	if len(store.audits) != 1 || store.audits[0].Action != "maintenance.enable" || store.audits[0].Result != "ok" {
		t.Fatalf("unexpected audits: %+v", store.audits)
	}

	// Disable storage maintenance as admin
	form = url.Values{
		"csrf":       {csrf},
		"scope":      {"storage"},
		"storage_id": {"2"},
		"return_to":  {"/storages"},
	}
	req = httptest.NewRequest("POST", "/maintenance/disable", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("disable status = %d, body = %s", res.Code, res.Body.String())
	}
	if len(store.locks) != 0 {
		t.Fatalf("expected 0 locks after disable, got %+v", store.locks)
	}
	if len(store.audits) != 2 || store.audits[1].Action != "maintenance.disable" {
		t.Fatalf("unexpected audits after disable: %+v", store.audits)
	}

	// Validation errors
	badForm := url.Values{"csrf": {csrf}, "scope": {"image"}, "storage_id": {"2"}}
	req = httptest.NewRequest("POST", "/maintenance/enable", strings.NewReader(badForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("missing image path: got %d, want 400", res.Code)
	}

	// Non-admin (operator) must be rejected with 403
	store.user.Role = "operator"
	req = httptest.NewRequest("POST", "/maintenance/enable", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("operator enable: got %d, want 403", res.Code)
	}
}

func TestNowMaintenanceAlerts(t *testing.T) {
	store := &maintTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		locks: []MaintenanceLock{
			{ID: 1, Scope: "storage", StorageID: 2, Reason: "NAS"},
			{ID: 2, Scope: "image", StorageID: 1, ImagePath: "win11.iso"},
		},
	}
	s := &Server{Store: store}
	alerts := s.nowMaintenanceAlerts(context.Background(), []images.Storage{
		{ID: 1, Name: "Локальное"},
		{ID: 2, Name: "NAS-ISO"},
	})
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d: %+v", len(alerts), alerts)
	}
	if !strings.Contains(alerts[0].Title, "NAS-ISO") || alerts[0].Link != "/storages" {
		t.Fatalf("unexpected storage alert: %+v", alerts[0])
	}
	if !strings.Contains(alerts[1].Title, "win11.iso") || alerts[1].Link != "/images" {
		t.Fatalf("unexpected image alert: %+v", alerts[1])
	}
}

func TestMaintenanceBlocksMutatingWebOperations(t *testing.T) {
	store := &maintTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}},
		winProfiles: []WindowsProfile{
			{ID: 10, WindowsProfileSource: WindowsProfileSource{StorageID: 2, Path: "win11.iso"}, State: "ready"},
		},
		bootProfiles: []BootProfile{
			{ID: 100, Kind: BootProfileWindowsISO, WindowsProfileID: 10},
		},
		installs: []Installation{
			{ID: 1, Title: "Win11", BootProfileID: 100},
		},
		locks: []MaintenanceLock{
			{ID: 1, Scope: "storage", StorageID: 2, Reason: "NAS update"},
		},
	}
	s := &Server{Store: store}
	h := s.Handler()
	token := "test-token"
	csrf := CSRFToken(token)

	// 1. applyMaintenanceLocksToChoices marks choice unavailable
	choices := []bootProfileChoice{
		{BootProfile: BootProfile{ID: 100, Kind: BootProfileWindowsISO, WindowsProfileID: 10}, State: "ready"},
	}
	applyMaintenanceLocksToChoices(choices, store.winProfiles, s.loadMaintenanceLookup(context.Background()))
	if choices[0].State != "unavailable" || choices[0].Error != "Хранилище в режиме обслуживания" {
		t.Fatalf("choice after lock = %+v", choices[0])
	}

	// 2. POST /installations/1/add returns 409 Conflict
	form := url.Values{"csrf": {csrf}}
	req := httptest.NewRequest("POST", "/installations/1/add", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("add to menu during maintenance: got %d, want 409 (%s)", res.Code, res.Body.String())
	}

	// 3. POST /installations/1/prepare returns 409 Conflict
	req = httptest.NewRequest("POST", "/installations/1/prepare", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("prepare during maintenance: got %d, want 409 (%s)", res.Code, res.Body.String())
	}

	// 4. applyMenuStorageStatus flags Maintenance on menuDraftRow without blocking Available
	rows := []menuDraftRow{{Item: MenuDraftItem{InstallationID: 1}, Available: true}}
	installs := []installationRow{{ID: 1, Kind: BootProfileWindowsISO, StorageID: 2, SourcePath: "win11.iso"}}
	s.applyMenuStorageStatus(context.Background(), rows, installs)
	if !rows[0].Maintenance || rows[0].MaintenanceReason != "NAS update" || !rows[0].Available {
		t.Fatalf("unexpected menu row after applyMenuStorageStatus: %+v", rows[0])
	}
}
