package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/images"
)

type fakeFileServerStore struct {
	authOnlyStore
	mu           sync.Mutex
	settings     FileServerSettings
	users        []User
	audits       []AuditEvent
	maintLocks   []MaintenanceLock
	bootProfiles []BootProfile
	winProfiles  []WindowsProfile
	installs     []Installation
}

func newFakeFileServerStore() *fakeFileServerStore {
	return &fakeFileServerStore{
		settings: FileServerSettings{
			SMBEnabled:  false,
			SFTPEnabled: false,
			SFTPPort:    DefaultSFTPPort,
		},
		users: []User{
			{ID: 1, Username: "admin", Role: "admin", Enabled: true, FSAccessMode: "rw", HasFSPassword: true},
			{ID: 2, Username: "operator", Role: "operator", Enabled: true, FSAccessMode: "none", HasFSPassword: false},
			{ID: 10, Username: "engineer", Role: "operator", Enabled: true, FSAccessMode: "none", HasFSPassword: false},
		},
	}
}

func (f *fakeFileServerStore) Session(_ context.Context, token string) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch token {
	case "tok-admin":
		return f.findUserByID(1)
	case "tok-operator":
		return f.findUserByID(2)
	case "tok-engineer":
		return f.findUserByID(10)
	default:
		return User{}, errUserNotFound
	}
}

func (f *fakeFileServerStore) findUserByID(id int64) (User, error) {
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return User{}, errUserNotFound
}

func (f *fakeFileServerStore) Users(_ context.Context) ([]User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]User(nil), f.users...), nil
}

func (f *fakeFileServerStore) ChangeUser(_ context.Context, id, _ int64, role string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.users {
		if f.users[i].ID == id {
			f.users[i].Role = role
			f.users[i].Enabled = enabled
			return nil
		}
	}
	return errUserNotFound
}

func (f *fakeFileServerStore) DeleteUser(_ context.Context, id, _ int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, u := range f.users {
		if u.ID == id {
			f.users = append(f.users[:i], f.users[i+1:]...)
			return u.Username, nil
		}
	}
	return "", errUserNotFound
}

func (f *fakeFileServerStore) FileServerSettings(_ context.Context) (FileServerSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, nil
}

func (f *fakeFileServerStore) SetFileServerSettings(_ context.Context, s FileServerSettings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings = s
	return nil
}

func (f *fakeFileServerStore) FileServerUsers(_ context.Context) ([]FileServerUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var res []FileServerUser
	for _, u := range f.users {
		res = append(res, FileServerUser{
			ID:          u.ID,
			Username:    u.Username,
			Mode:        u.FSAccessMode,
			Enabled:     u.Enabled,
			HasPassword: u.HasFSPassword,
		})
	}
	return res, nil
}

func (f *fakeFileServerStore) SetUserFileAccess(_ context.Context, id int64, mode, password string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !ValidFSAccessMode(mode) {
		return "", errInvalidFSAccessMode
	}
	for i := range f.users {
		if f.users[i].ID == id {
			if (mode == FSAccessRO || mode == FSAccessRW) && password == "" && !f.users[i].HasFSPassword {
				return "", errFSPasswordRequired
			}
			f.users[i].FSAccessMode = mode
			if password != "" {
				if len(password) < 12 {
					return "", errShortPassword
				}
				f.users[i].HasFSPassword = true
				now := time.Now()
				f.users[i].FSPasswordChangedAt = &now
			} else if mode == FSAccessNone {
				f.users[i].HasFSPassword = false
				f.users[i].FSPasswordChangedAt = nil
			}
			return f.users[i].Username, nil
		}
	}
	return "", errUserNotFound
}

func (f *fakeFileServerStore) SetOwnFilePassword(_ context.Context, id int64, password string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(password) < 12 {
		return "", errShortPassword
	}
	for i := range f.users {
		if f.users[i].ID == id {
			if f.users[i].FSAccessMode != FSAccessRO && f.users[i].FSAccessMode != FSAccessRW {
				return "", errFSNotEnabled
			}
			f.users[i].HasFSPassword = true
			now := time.Now()
			f.users[i].FSPasswordChangedAt = &now
			return f.users[i].Username, nil
		}
	}
	return "", errUserNotFound
}

func (f *fakeFileServerStore) AuthenticateFSUser(_ context.Context, username, _ string) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.Username == username && u.Enabled && u.HasFSPassword {
			return u, nil
		}
	}
	return User{}, errUserNotFound
}

func (f *fakeFileServerStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, AuditEvent{
		Action: action,
		Result: result,
		Detail: detail,
	})
	return nil
}

func (f *fakeFileServerStore) AuditList(_ context.Context) ([]AuditEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]AuditEvent(nil), f.audits...), nil
}

func (f *fakeFileServerStore) MaintenanceLocks(_ context.Context) ([]MaintenanceLock, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]MaintenanceLock(nil), f.maintLocks...), nil
}

func (f *fakeFileServerStore) SetMaintenanceLock(_ context.Context, scope string, storageID int64, imagePath, reason string, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.maintLocks {
		if f.maintLocks[i].Scope == scope && f.maintLocks[i].StorageID == storageID && f.maintLocks[i].ImagePath == imagePath {
			f.maintLocks[i].Reason = reason
			f.maintLocks[i].CreatedBy = userID
			return nil
		}
	}
	f.maintLocks = append(f.maintLocks, MaintenanceLock{
		Scope:     scope,
		StorageID: storageID,
		ImagePath: imagePath,
		Reason:    reason,
		CreatedBy: userID,
	})
	return nil
}

func (f *fakeFileServerStore) DeleteMaintenanceLock(_ context.Context, scope string, storageID int64, imagePath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []MaintenanceLock
	for _, l := range f.maintLocks {
		if l.Scope == scope && l.StorageID == storageID && l.ImagePath == imagePath {
			continue
		}
		out = append(out, l)
	}
	f.maintLocks = out
	return nil
}

func (f *fakeFileServerStore) BootProfiles(_ context.Context) ([]BootProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]BootProfile(nil), f.bootProfiles...), nil
}

func (f *fakeFileServerStore) WindowsProfiles(_ context.Context) ([]WindowsProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]WindowsProfile(nil), f.winProfiles...), nil
}

func (f *fakeFileServerStore) Installations(_ context.Context) ([]Installation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Installation(nil), f.installs...), nil
}

type fakeLocalSMBClient struct {
	mu          sync.Mutex
	applyCalls  []agent.LocalSMBConfig
	passwdCalls []agent.LocalSMBPasswordRequest
}

func (f *fakeLocalSMBClient) ApplyLocalSMB(_ context.Context, cfg agent.LocalSMBConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applyCalls = append(f.applyCalls, cfg)
	return nil
}

func (f *fakeLocalSMBClient) SetLocalSMBPassword(_ context.Context, req agent.LocalSMBPasswordRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.passwdCalls = append(f.passwdCalls, req)
	return nil
}

func (f *fakeLocalSMBClient) lastApply() agent.LocalSMBConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.applyCalls) == 0 {
		return agent.LocalSMBConfig{}
	}
	return f.applyCalls[len(f.applyCalls)-1]
}

func (f *fakeLocalSMBClient) lastPasswd() agent.LocalSMBPasswordRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.passwdCalls) == 0 {
		return agent.LocalSMBPasswordRequest{}
	}
	return f.passwdCalls[len(f.passwdCalls)-1]
}

func TestFileServerSettingsHandler(t *testing.T) {
	store := newFakeFileServerStore()
	fakeSMB := &fakeLocalSMBClient{}
	sftpServer := NewLocalSFTPServer(t.TempDir(), filepath.Join(t.TempDir(), "key"), SFTPHooks{})
	defer sftpServer.Close()

	srv := &Server{
		Store:      store,
		LocalSMB:   fakeSMB,
		SFTPServer: sftpServer,
		ListenAddr: "127.0.0.1:8443",
	}
	handler := srv.Handler()

	// 1. Operator gets 403
	form := url.Values{"csrf": {CSRFToken("tok-operator")}, "smb_enabled": {"on"}, "sftp_enabled": {"on"}, "sftp_port": {"2022"}}
	req := httptest.NewRequest(http.MethodPost, "/images/file-server", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-operator"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for operator, got %d", rec.Code)
	}

	// 2. Admin toggles settings
	form = url.Values{
		"csrf":         {CSRFToken("tok-admin")},
		"smb_enabled":  {"on"},
		"sftp_enabled": {"on"},
		"sftp_port":    {"2025"},
	}
	req = httptest.NewRequest(http.MethodPost, "/images/file-server", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 SeeOther, got %d", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/storages?message=") {
		t.Fatalf("expected redirect to /storages, got %s", rec.Header().Get("Location"))
	}

	// Check store updated
	settings, err := store.FileServerSettings(context.Background())
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !settings.SMBEnabled || !settings.SFTPEnabled || settings.SFTPPort != 2025 {
		t.Fatalf("unexpected settings: %+v", settings)
	}

	// Check LocalSMB called
	lastSMB := fakeSMB.lastApply()
	if !lastSMB.Enabled {
		t.Fatalf("expected LocalSMB Enabled=true, got %+v", lastSMB)
	}

	// Check SFTPServer started
	running, addr := sftpServer.Running()
	if !running || !strings.Contains(addr, "2025") {
		t.Fatalf("expected SFTPServer running on 2025, got running=%v addr=%s", running, addr)
	}

	// Check Audit
	audits, _ := store.AuditList(context.Background())
	var foundAudit bool
	for _, a := range audits {
		if a.Action == "storage.file_server" && a.Result == "ok" {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("expected audit for storage.file_server, got %+v", audits)
	}

	// 3. Admin invalid port returns 400
	form = url.Values{
		"csrf":         {CSRFToken("tok-admin")},
		"smb_enabled":  {"on"},
		"sftp_enabled": {"on"},
		"sftp_port":    {"80"},
	}
	req = httptest.NewRequest(http.MethodPost, "/images/file-server", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for invalid port, got %d", rec.Code)
	}
}

func TestUserFileAccessHandler(t *testing.T) {
	store := newFakeFileServerStore()
	fakeSMB := &fakeLocalSMBClient{}
	sftpServer := NewLocalSFTPServer(t.TempDir(), filepath.Join(t.TempDir(), "key"), SFTPHooks{})
	defer sftpServer.Close()

	srv := &Server{
		Store:      store,
		LocalSMB:   fakeSMB,
		SFTPServer: sftpServer,
		ListenAddr: "127.0.0.1:8443",
	}
	handler := srv.Handler()

	// 1. Operator gets 403
	form := url.Values{"csrf": {CSRFToken("tok-operator")}, "user_id": {"10"}, "mode": {"rw"}, "password": {"ValidPassword123!"}}
	req := httptest.NewRequest(http.MethodPost, "/users/file-access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-operator"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for operator, got %d", rec.Code)
	}

	// 2. Enabling ro without initial password returns 400
	form = url.Values{"csrf": {CSRFToken("tok-admin")}, "user_id": {"10"}, "mode": {"ro"}, "password": {""}}
	req = httptest.NewRequest(http.MethodPost, "/users/file-access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for ro without initial password, got %d", rec.Code)
	}

	// 3. Admin grants rw with 12+ char password
	form = url.Values{"csrf": {CSRFToken("tok-admin")}, "user_id": {"10"}, "mode": {"rw"}, "password": {"ValidPassword123!"}}
	req = httptest.NewRequest(http.MethodPost, "/users/file-access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 SeeOther, got %d", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/users?message=") {
		t.Fatalf("expected redirect to /users, got %s", rec.Header().Get("Location"))
	}

	// Verify LocalSMB.SetLocalSMBPassword called
	lastPasswd := fakeSMB.lastPasswd()
	if lastPasswd.ID != 10 || lastPasswd.Username != "engineer" || lastPasswd.Password != "ValidPassword123!" {
		t.Fatalf("unexpected LocalSMB password call: %+v", lastPasswd)
	}

	// Verify LocalSMB.ApplyLocalSMB called and user 10 is included
	lastSMB := fakeSMB.lastApply()
	var userFound bool
	for _, u := range lastSMB.Users {
		if u.ID == 10 && u.Mode == "rw" {
			userFound = true
			break
		}
	}
	if !userFound {
		t.Fatalf("user 10 not found in SMB users: %+v", lastSMB.Users)
	}

	// Verify audit
	audits, _ := store.AuditList(context.Background())
	var auditFound bool
	for _, a := range audits {
		if a.Action == "user.file_access" && a.Result == "ok" {
			auditFound = true
			break
		}
	}
	if !auditFound {
		t.Fatalf("expected user.file_access audit event, got %+v", audits)
	}

	// 4. Switching rw -> ro with empty password succeeds WITHOUT calling SetLocalSMBPassword
	passwdCallsBefore := len(fakeSMB.passwdCalls)
	form = url.Values{"csrf": {CSRFToken("tok-admin")}, "user_id": {"10"}, "mode": {"ro"}, "password": {""}}
	req = httptest.NewRequest(http.MethodPost, "/users/file-access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 SeeOther, got %d", rec.Code)
	}
	if len(fakeSMB.passwdCalls) != passwdCallsBefore {
		t.Fatalf("SetLocalSMBPassword was called unexpectedly when switching mode with empty password")
	}
	lastSMB = fakeSMB.lastApply()
	userFound = false
	for _, u := range lastSMB.Users {
		if u.ID == 10 && u.Mode == "ro" {
			userFound = true
			break
		}
	}
	if !userFound {
		t.Fatalf("expected user 10 mode=ro in SMB users: %+v", lastSMB.Users)
	}

	// 5. Setting none clears access and syncs SMB (user 10 excluded from users list)
	form = url.Values{"csrf": {CSRFToken("tok-admin")}, "user_id": {"10"}, "mode": {"none"}, "password": {""}}
	req = httptest.NewRequest(http.MethodPost, "/users/file-access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 SeeOther, got %d", rec.Code)
	}
	lastSMB = fakeSMB.lastApply()
	for _, u := range lastSMB.Users {
		if u.ID == 10 {
			t.Fatalf("user 10 should not be in SMB users after mode=none, got %+v", lastSMB.Users)
		}
	}
}

func TestAccountFilePasswordHandler(t *testing.T) {
	store := newFakeFileServerStore()
	fakeSMB := &fakeLocalSMBClient{}

	srv := &Server{
		Store:      store,
		LocalSMB:   fakeSMB,
		ListenAddr: "192.0.2.10:8443",
	}
	handler := srv.Handler()

	// 1. User with mode=none gets 403 Forbidden
	form := url.Values{
		"csrf":             {CSRFToken("tok-operator")},
		"password":         {"ValidPassword123!"},
		"confirm_password": {"ValidPassword123!"},
	}
	req := httptest.NewRequest(http.MethodPost, "/account/file-password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-operator"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for user with mode=none, got %d", rec.Code)
	}

	// 2. Mismatched confirm or < 12 chars returns 400
	form = url.Values{
		"csrf":             {CSRFToken("tok-admin")},
		"password":         {"Short1!"},
		"confirm_password": {"Short1!"},
	}
	req = httptest.NewRequest(http.MethodPost, "/account/file-password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short password, got %d", rec.Code)
	}

	form = url.Values{
		"csrf":             {CSRFToken("tok-admin")},
		"password":         {"ValidPassword123!"},
		"confirm_password": {"DifferentPassword123!"},
	}
	req = httptest.NewRequest(http.MethodPost, "/account/file-password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for mismatched confirm password, got %d", rec.Code)
	}

	// 3. User with ro/rw changes own file password -> updates store and syncs LocalSMB.SetLocalSMBPassword
	form = url.Values{
		"csrf":             {CSRFToken("tok-admin")},
		"password":         {"BrandNewPass123!"},
		"confirm_password": {"BrandNewPass123!"},
	}
	req = httptest.NewRequest(http.MethodPost, "/account/file-password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 SeeOther, got %d", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/account?message=") {
		t.Fatalf("expected redirect to /account, got %s", rec.Header().Get("Location"))
	}

	lastPasswd := fakeSMB.lastPasswd()
	if lastPasswd.ID != 1 || lastPasswd.Username != "admin" || lastPasswd.Password != "BrandNewPass123!" {
		t.Fatalf("unexpected LocalSMB password call: %+v", lastPasswd)
	}

	// Verify audit
	audits, _ := store.AuditList(context.Background())
	var auditFound bool
	for _, a := range audits {
		if a.Action == "account.file_password" && a.Result == "ok" {
			auditFound = true
			break
		}
	}
	if !auditFound {
		t.Fatalf("expected account.file_password audit event, got %+v", audits)
	}
}

type fakeImagesForLifecycle struct {
	fakeImagesAPI
	catalog images.Catalog
}

func (f *fakeImagesForLifecycle) Catalog(_ context.Context) (images.Catalog, error) {
	return f.catalog, nil
}

func TestLifecycleHooksSyncLocalFileServer(t *testing.T) {
	store := newFakeFileServerStore()
	// user 10 has mode rw
	_, _ = store.SetUserFileAccess(context.Background(), 10, "rw", "InitPass123456!")
	fakeSMB := &fakeLocalSMBClient{}

	fakeImg := &fakeImagesForLifecycle{
		catalog: images.Catalog{
			Storages: []images.Storage{
				{ID: 1, Name: "Local", Kind: "local", State: "ready", Root: "/var/lib/omb-images/iso"},
				{ID: 2, Name: "NAS", Kind: "smb", State: "ready", Root: "/mnt/omb-smb/nas"},
			},
		},
	}

	srv := &Server{
		Store:      store,
		Images:     fakeImg,
		LocalSMB:   fakeSMB,
		ListenAddr: "192.0.2.10:8443",
	}
	handler := srv.Handler()

	// Initial sync to set baseline
	_ = srv.SyncLocalFileServer(context.Background())
	last := fakeSMB.lastApply()
	if len(last.Users) != 2 { // admin (1) and engineer (10)
		t.Fatalf("expected 2 active users, got %d", len(last.Users))
	}

	// 1. Disabling user 10 via /users/change triggers SyncLocalFileServer
	form := url.Values{"csrf": {CSRFToken("tok-admin")}, "id": {"10"}, "role": {"operator"}, "enabled": {"off"}}
	req := httptest.NewRequest(http.MethodPost, "/users/change", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect from /users/change, got %d", rec.Code)
	}

	last = fakeSMB.lastApply()
	for _, u := range last.Users {
		if u.ID == 10 {
			t.Fatalf("disabled user 10 still present in SMB users: %+v", last.Users)
		}
	}

	// Re-enable user 10
	form = url.Values{"csrf": {CSRFToken("tok-admin")}, "id": {"10"}, "role": {"operator"}, "enabled": {"on"}}
	req = httptest.NewRequest(http.MethodPost, "/users/change", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	last = fakeSMB.lastApply()
	var reEnabled bool
	for _, u := range last.Users {
		if u.ID == 10 {
			reEnabled = true
			break
		}
	}
	if !reEnabled {
		t.Fatalf("re-enabled user 10 not found in SMB users: %+v", last.Users)
	}

	// 2. Deleting user 10 via /users/delete triggers SyncLocalFileServer
	form = url.Values{"csrf": {CSRFToken("tok-admin")}, "id": {"10"}}
	req = httptest.NewRequest(http.MethodPost, "/users/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect from /users/delete, got %d", rec.Code)
	}

	last = fakeSMB.lastApply()
	for _, u := range last.Users {
		if u.ID == 10 {
			t.Fatalf("deleted user 10 still present in SMB users: %+v", last.Users)
		}
	}

	// 3. Enabling Maintenance on Local storage (ID 1) triggers SyncLocalFileServer with ReadOnlyAll=true
	form = url.Values{
		"csrf":       {CSRFToken("tok-admin")},
		"scope":      {"storage"},
		"storage_id": {"1"},
		"reason":     {"Upgrading disk"},
	}
	req = httptest.NewRequest(http.MethodPost, "/maintenance/enable", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect from /maintenance/enable, got %d", rec.Code)
	}

	last = fakeSMB.lastApply()
	if !last.ReadOnlyAll {
		t.Fatalf("expected ReadOnlyAll=true when Local storage is in maintenance, got %+v", last)
	}

	// Disabling Maintenance on Local storage triggers SyncLocalFileServer with ReadOnlyAll=false
	form = url.Values{
		"csrf":       {CSRFToken("tok-admin")},
		"scope":      {"storage"},
		"storage_id": {"1"},
	}
	req = httptest.NewRequest(http.MethodPost, "/maintenance/disable", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-admin"})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect from /maintenance/disable, got %d", rec.Code)
	}

	last = fakeSMB.lastApply()
	if last.ReadOnlyAll {
		t.Fatalf("expected ReadOnlyAll=false after disabling maintenance, got %+v", last)
	}
}

func TestLocalFileServerState(t *testing.T) {
	store := newFakeFileServerStore()
	store.settings = FileServerSettings{
		SMBEnabled:  true,
		SFTPEnabled: true,
		SFTPPort:    2022,
	}
	_, _ = store.SetUserFileAccess(context.Background(), 10, "ro", "ValidPass12345!")

	srv := &Server{
		Store:      store,
		ListenAddr: "192.0.2.10:8443",
	}

	state := srv.localFileServerState(context.Background(), "192.0.2.10:8443")
	if !state.SMBEnabled || !state.SFTPEnabled {
		t.Fatalf("expected both SMB and SFTP enabled, got %+v", state)
	}
	if state.SFTPPort != 2022 {
		t.Fatalf("expected port 2022, got %d", state.SFTPPort)
	}
	if state.SMBAddress != `\\192.0.2.10\iso` {
		t.Fatalf("unexpected SMB address: %s", state.SMBAddress)
	}
	if state.SFTPAddress != "sftp://192.0.2.10:2022/" {
		t.Fatalf("unexpected SFTP address: %s", state.SFTPAddress)
	}
	// Active users: admin (rw) + engineer (ro)
	if state.ActiveUsersCount != 2 || state.RWUsersCount != 1 || state.ROUsersCount != 1 {
		t.Fatalf("unexpected user counts: active=%d rw=%d ro=%d", state.ActiveUsersCount, state.RWUsersCount, state.ROUsersCount)
	}
	if state.IsLocalMaintenance {
		t.Fatalf("expected IsLocalMaintenance=false")
	}
}
