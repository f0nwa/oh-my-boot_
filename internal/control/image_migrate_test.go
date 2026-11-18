package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/images"
)

type migrationMockImages struct {
	fakeImagesAPI
	migrations map[string]*images.MigrationJob
	committed  []string
}

func (m *migrationMockImages) StartMigration(_ context.Context, srcID int64, path string, dstID int64) (*images.MigrationJob, error) {
	if m.migrations == nil {
		m.migrations = make(map[string]*images.MigrationJob)
	}
	job := &images.MigrationJob{
		ID:              "mig_123",
		SourceStorageID: srcID,
		Path:            path,
		TargetStorageID: dstID,
		State:           "copying",
		TotalBytes:      6000,
	}
	m.migrations[job.ID] = job
	return job, nil
}

func (m *migrationMockImages) ListMigrations(context.Context) ([]*images.MigrationJob, error) {
	var list []*images.MigrationJob
	for _, j := range m.migrations {
		list = append(list, j)
	}
	return list, nil
}

func (m *migrationMockImages) GetMigration(_ context.Context, id string) (*images.MigrationJob, error) {
	if j, ok := m.migrations[id]; ok {
		return j, nil
	}
	return nil, errors.New("not found")
}

func (m *migrationMockImages) CancelMigration(_ context.Context, id string) error {
	if j, ok := m.migrations[id]; ok {
		j.State = "canceled"
		return nil
	}
	return errors.New("not found")
}

func (m *migrationMockImages) CommitDeleteSource(_ context.Context, id string) error {
	m.committed = append(m.committed, id)
	if j, ok := m.migrations[id]; ok {
		j.State = "completed"
	}
	return nil
}

type migrationMockAgent struct {
	reboundWin []agent.WindowsISORequest
	reboundUbu []agent.UbuntuISORequest
	sessions   []agent.SMBSession
}

func (a *migrationMockAgent) PrepareWindowsISO(context.Context, agent.WindowsISORequest) (agent.WindowsISOResult, error) {
	return agent.WindowsISOResult{InstallFile: "install.wim"}, nil
}

func (a *migrationMockAgent) CheckWindowsISO(context.Context, agent.WindowsISORequest) (agent.WindowsISOResult, error) {
	return agent.WindowsISOResult{}, nil
}

func (a *migrationMockAgent) RebindWindowsISO(_ context.Context, req agent.WindowsISORequest) (agent.WindowsISOResult, error) {
	a.reboundWin = append(a.reboundWin, req)
	return agent.WindowsISOResult{InstallFile: "install.wim"}, nil
}

func (a *migrationMockAgent) InspectUbuntuISO(context.Context, agent.WindowsISORequest) (agent.UbuntuISOInspection, error) {
	return agent.UbuntuISOInspection{}, nil
}

func (a *migrationMockAgent) PrepareUbuntuISO(context.Context, agent.UbuntuISORequest) error {
	return nil
}

func (a *migrationMockAgent) RebindUbuntuISO(_ context.Context, req agent.UbuntuISORequest) error {
	a.reboundUbu = append(a.reboundUbu, req)
	return nil
}

func (a *migrationMockAgent) SMBSessions(context.Context) ([]agent.SMBSession, error) {
	return a.sessions, nil
}

func TestImageMigrationPreflightChecks(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()

	mockImg := &migrationMockImages{
		fakeImagesAPI: fakeImagesAPI{
			catalog: images.Catalog{
				Storages: []images.Storage{
					{ID: 1, Name: "Local", Kind: "local", Root: "/iso", State: "ready", Free: 100 << 30, Uploads: true},
					{ID: 2, Name: "NAS-RW", Kind: "nfs", Root: "/mnt/nfs", State: "ready", Free: 10 << 30, Uploads: true},
					{ID: 3, Name: "NAS-RO", Kind: "nfs", Root: "/mnt/ro", State: "ready", Free: 10 << 30, Uploads: false},
					{ID: 4, Name: "NAS-Full", Kind: "smb", Root: "/mnt/smb", State: "ready", Free: 100 << 20, Uploads: true}, // 100MB
				},
				Images: []images.Image{
					{StorageID: 1, Path: "win.iso", Size: 4 << 30, Present: true},
					{StorageID: 1, Path: "existing.iso", Size: 100, Present: true},
					{StorageID: 2, Path: "existing.iso", Size: 100, Present: true},
				},
			},
		},
	}
	mockAgent := &migrationMockAgent{}

	coordinator := NewMigrationCoordinator(store, mockImg, mockAgent, mockAgent, mockAgent)

	// 1. Same storage rejected
	err := coordinator.CheckPreflight(ctx, 1, "win.iso", 1)
	if err == nil || (!strings.Contains(err.Error(), "не могут совпадать") && !strings.Contains(err.Error(), "не может совпадать")) {
		t.Fatalf("expected same storage error, got %v", err)
	}

	// 2. Read-only target rejected
	err = coordinator.CheckPreflight(ctx, 1, "win.iso", 3)
	if err == nil || !strings.Contains(err.Error(), "только для чтения") {
		t.Fatalf("expected RO error, got %v", err)
	}

	// 3. Insufficient space rejected
	err = coordinator.CheckPreflight(ctx, 1, "win.iso", 4)
	if err == nil || !strings.Contains(err.Error(), "недостаточно свободного места") {
		t.Fatalf("expected space error, got %v", err)
	}

	// 4. Target already has file
	err = coordinator.CheckPreflight(ctx, 1, "existing.iso", 2)
	if err == nil || !strings.Contains(err.Error(), "уже существует") {
		t.Fatalf("expected already exists error, got %v", err)
	}

	// 5. Active SMB session blocks migration
	p, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{
		StorageID:   1,
		Path:        "win.iso",
		StorageRoot: "/iso",
		Size:        4 << 30,
		Modified:    time.Now(),
		Title:       "Win 11",
	})
	if err != nil {
		t.Fatal(err)
	}

	mockAgent.sessions = []agent.SMBSession{
		{IP: "192.168.10.200", Profile: strings.TrimSpace(p.Title)},
	}

	err = coordinator.CheckPreflight(ctx, 1, "win.iso", 2)
	if err == nil || !errors.Is(err, ErrImageInUse) {
		t.Fatalf("expected ErrImageInUse error, got %v", err)
	}

	// 6. Clear sessions -> preflight succeeds!
	mockAgent.sessions = nil
	if err := coordinator.CheckPreflight(ctx, 1, "win.iso", 2); err != nil {
		t.Fatalf("preflight should pass, got %v", err)
	}
}

func TestImageMigrationRebindFlow(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()

	mockImg := &migrationMockImages{
		fakeImagesAPI: fakeImagesAPI{
			catalog: images.Catalog{
				Storages: []images.Storage{
					{ID: 1, Name: "Local", Kind: "local", Root: "/srv/omb/iso", State: "ready", Uploads: true},
					{ID: 2, Name: "NAS", Kind: "nfs", Root: "/mnt/omb-nfs/nas", State: "ready", Uploads: true, Free: 100 << 30},
				},
				Images: []images.Image{
					{StorageID: 1, Path: "win.iso", Size: 4 << 30, Present: true},
				},
			},
		},
	}
	mockAgent := &migrationMockAgent{}
	coordinator := NewMigrationCoordinator(store, mockImg, mockAgent, mockAgent, mockAgent)

	// Create Windows profile using (1, "win.iso")
	winProfile, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{
		StorageID:   1,
		Path:        "win.iso",
		StorageRoot: "/srv/omb/iso",
		Size:        4 << 30,
		Modified:    time.Now().Truncate(time.Microsecond),
		Title:       "Windows 11 Setup",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set image display name and author
	if err := store.SetImageName(ctx, 1, "win.iso", "Кастомное имя Win11"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(ctx, "operator1", "SecretPass123!", "admin"); err != nil {
		t.Fatal(err)
	}
	users, err := store.Users(ctx)
	if err != nil || len(users) == 0 {
		t.Fatalf("failed to fetch users: %v", err)
	}
	user := users[0]
	if err := store.Audit(ctx, user.ID, "iso.upload", "ok", "1 win.iso sha256placeholder"); err != nil {
		t.Fatal(err)
	}

	// Start migration
	job, err := coordinator.StartMigration(ctx, user, 1, "win.iso", 2)
	if err != nil {
		t.Fatalf("StartMigration failed: %v", err)
	}

	// Simulate images engine finishing copy
	job.State = "ready_to_rebind"

	// Process Rebind
	if err := coordinator.ProcessCompletedMigration(ctx, job.ID); err != nil {
		t.Fatalf("ProcessCompletedMigration failed: %v", err)
	}

	// 1. Verify agent rebind called
	if len(mockAgent.reboundWin) != 1 || mockAgent.reboundWin[0].StorageRoot != "/mnt/omb-nfs/nas" {
		t.Fatalf("agent rebind mismatch: %+v", mockAgent.reboundWin)
	}

	// 2. Verify windows_profiles updated
	pAfter, err := store.WindowsProfile(ctx, winProfile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pAfter.StorageID != 2 || pAfter.StorageRoot != "/mnt/omb-nfs/nas" {
		t.Fatalf("profile not rebound in DB: %+v", pAfter)
	}

	// 3. Verify image display name updated
	names, err := store.ImageNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if names[imageAuthorKey(2, "win.iso")] != "Кастомное имя Win11" {
		t.Fatalf("display name not rebound: %+v", names)
	}

	// 4. Verify commit delete source called
	if len(mockImg.committed) != 1 || mockImg.committed[0] != job.ID {
		t.Fatalf("commit delete source not called: %+v", mockImg.committed)
	}

	// 5. Verify author preserved
	authors, err := store.ResourceAuthors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if authors.Images[imageAuthorKey(2, "win.iso")] != user.Username {
		t.Fatalf("author not preserved on migrated image: %+v", authors.Images)
	}
}

func TestImageMigrationHTTP(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()

	// Create admin and operator users in store
	if err := store.CreateUser(ctx, "admin1", "AdminPass123!", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(ctx, "oper1", "OperPass123!", "operator"); err != nil {
		t.Fatal(err)
	}

	mockImg := &migrationMockImages{
		fakeImagesAPI: fakeImagesAPI{
			catalog: images.Catalog{
				Storages: []images.Storage{
					{ID: 1, Name: "Local", Kind: "local", Root: "/srv/omb/iso", State: "ready", Uploads: true},
					{ID: 2, Name: "NAS", Kind: "nfs", Root: "/mnt/omb-nfs/nas", State: "ready", Uploads: true, Free: 100 << 30},
				},
				Images: []images.Image{
					{StorageID: 1, Path: "win.iso", Size: 4 << 30, Present: true},
				},
			},
		},
	}
	mockAgent := &migrationMockAgent{}

	s := &Server{
		Store:      store,
		Images:     mockImg,
		WindowsISO: mockAgent,
		UbuntuISO:  mockAgent,
		Agent:      nil,
	}

	// 1. Operator attempts to start migration -> 403 Forbidden
	_, operToken, err := store.Login(ctx, "oper1", "OperPass123!")
	if err != nil {
		t.Fatal(err)
	}
	operForm := url.Values{
		"source_storage_id": {"1"},
		"target_storage_id": {"2"},
		"path":              {"win.iso"},
		"csrf":              {CSRFToken(operToken)},
	}
	req := httptest.NewRequest(http.MethodPost, "/images/migrate", strings.NewReader(operForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: operToken})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("operator should be forbidden from migrating images, got status %d", rec.Code)
	}

	// 2. Admin starts migration via JSON API
	_, adminToken, err := store.Login(ctx, "admin1", "AdminPass123!")
	if err != nil {
		t.Fatal(err)
	}
	adminForm := url.Values{
		"source_storage_id": {"1"},
		"target_storage_id": {"2"},
		"path":              {"win.iso"},
		"csrf":              {CSRFToken(adminToken)},
	}
	reqAdmin := httptest.NewRequest(http.MethodPost, "/images/migrate", strings.NewReader(adminForm.Encode()))
	reqAdmin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqAdmin.Header.Set("Accept", "application/json")
	reqAdmin.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})

	recAdmin := httptest.NewRecorder()
	s.Handler().ServeHTTP(recAdmin, reqAdmin)
	if recAdmin.Code != http.StatusCreated {
		t.Fatalf("admin migration start expected 201 Created, got %d (body: %s)", recAdmin.Code, recAdmin.Body.String())
	}
	var startedJob images.MigrationJob
	if err := json.Unmarshal(recAdmin.Body.Bytes(), &startedJob); err != nil {
		t.Fatalf("invalid json returned: %v", err)
	}
	if startedJob.ID != "mig_123" {
		t.Fatalf("unexpected job id: %s", startedJob.ID)
	}

	// 3. Poll active migrations: GET /api/images/migrations
	reqPoll := httptest.NewRequest(http.MethodGet, "/api/images/migrations", nil)
	reqPoll.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
	recPoll := httptest.NewRecorder()
	s.Handler().ServeHTTP(recPoll, reqPoll)
	if recPoll.Code != http.StatusOK {
		t.Fatalf("poll expected 200 OK, got %d", recPoll.Code)
	}
	var jobs []*images.MigrationJob
	if err := json.Unmarshal(recPoll.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("invalid poll json: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "mig_123" {
		t.Fatalf("expected 1 active migration job, got %+v", jobs)
	}

	// 4. Admin cancels migration
	cancelForm := url.Values{"csrf": {CSRFToken(adminToken)}}
	reqCancel := httptest.NewRequest(http.MethodPost, "/images/migrate/mig_123/cancel", strings.NewReader(cancelForm.Encode()))
	reqCancel.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqCancel.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
	recCancel := httptest.NewRecorder()
	s.Handler().ServeHTTP(recCancel, reqCancel)
	if recCancel.Code != http.StatusNoContent {
		t.Fatalf("cancel expected 204 No Content, got %d", recCancel.Code)
	}
}

type concurrentRebindMockImages struct {
	migrationMockImages
	commitErr error
}

func (m *concurrentRebindMockImages) CommitDeleteSource(ctx context.Context, id string) error {
	if m.commitErr != nil {
		return m.commitErr
	}
	return m.migrationMockImages.CommitDeleteSource(ctx, id)
}

type blockingRebindAgent struct {
	migrationMockAgent
	mu          sync.Mutex
	calls       int
	startedCh   chan struct{}
	releaseCh   chan struct{}
	startedOnce sync.Once
}

func (a *blockingRebindAgent) RebindWindowsISO(ctx context.Context, req agent.WindowsISORequest) (agent.WindowsISOResult, error) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	a.startedOnce.Do(func() { close(a.startedCh) })
	<-a.releaseCh
	return a.migrationMockAgent.RebindWindowsISO(ctx, req)
}

func TestImageMigrationCoordinatorSerializesConcurrentRebindAndRetriesCommit(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()

	if err := store.CreateUser(ctx, "admin2", "AdminPass123!", "admin"); err != nil {
		t.Fatal(err)
	}
	users, err := store.Users(ctx)
	if err != nil || len(users) == 0 {
		t.Fatalf("users: %v", err)
	}
	user := users[0]
	_, adminToken, err := store.Login(ctx, "admin2", "AdminPass123!")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{
		StorageID:   1,
		Path:        "win.iso",
		StorageRoot: "/srv/omb/iso",
		Size:        4 << 30,
		Modified:    time.Now().Truncate(time.Microsecond),
		Title:       "Win 11",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Audit(ctx, user.ID, "iso.upload", "ok", "1 win.iso sha"); err != nil {
		t.Fatal(err)
	}

	mockImg := &concurrentRebindMockImages{
		migrationMockImages: migrationMockImages{
			fakeImagesAPI: fakeImagesAPI{
				catalog: images.Catalog{
					Storages: []images.Storage{
						{ID: 1, Name: "Local", Kind: "local", Root: "/srv/omb/iso", State: "ready", Uploads: true},
						{ID: 2, Name: "NAS", Kind: "nfs", Root: "/mnt/omb-nfs/nas", State: "ready", Uploads: true, Free: 100 << 30},
					},
					Images: []images.Image{
						{StorageID: 1, Path: "win.iso", Size: 4 << 30, Present: true},
					},
				},
			},
		},
		commitErr: errors.New("temporary unlink timeout"),
	}
	blockingAgent := &blockingRebindAgent{
		startedCh: make(chan struct{}),
		releaseCh: make(chan struct{}),
	}

	s := &Server{
		Store:      store,
		Images:     mockImg,
		WindowsISO: blockingAgent,
		UbuntuISO:  blockingAgent,
	}

	// Warm resourceAuthors cache before migration so cache invalidation is exercised.
	beforeAuthors := s.resourceAuthors(ctx)
	if beforeAuthors.Images[imageAuthorKey(1, "win.iso")] != user.Username {
		t.Fatalf("expected cached upload author for 1:win.iso, got %+v", beforeAuthors.Images)
	}

	coord := s.migrationCoordinator()
	job, err := coord.StartMigration(ctx, user, 1, "win.iso", 2)
	if err != nil {
		t.Fatalf("StartMigration: %v", err)
	}
	job.State = "ready_to_rebind"

	// Fire two concurrent GET /api/images/migrations polls while RebindWindowsISO blocks.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/api/images/migrations", nil)
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
		s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-blockingAgent.startedCh
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/api/images/migrations", nil)
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
		s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}()
	time.Sleep(50 * time.Millisecond)
	close(blockingAgent.releaseCh)
	wg.Wait()

	blockingAgent.mu.Lock()
	rebindCalls := blockingAgent.calls
	blockingAgent.mu.Unlock()
	if rebindCalls != 1 {
		t.Fatalf("expected RebindWindowsISO to run once across concurrent polls, got %d", rebindCalls)
	}

	// CommitDeleteSource failed on the first poll, so job is still ready_to_rebind; clear error and poll again.
	mockImg.commitErr = nil
	reqRetry := httptest.NewRequest(http.MethodGet, "/api/images/migrations", nil)
	reqRetry.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
	recRetry := httptest.NewRecorder()
	s.Handler().ServeHTTP(recRetry, reqRetry)
	if recRetry.Code != http.StatusOK {
		t.Fatalf("retry poll status=%d", recRetry.Code)
	}
	if len(mockImg.committed) != 1 || mockImg.committed[0] != job.ID {
		t.Fatalf("expected CommitDeleteSource retry to succeed after partial commit, got %+v", mockImg.committed)
	}

	var successAuditCount int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='iso.migrate_success'`).Scan(&successAuditCount); err != nil {
		t.Fatal(err)
	}
	if successAuditCount != 1 {
		t.Fatalf("expected iso.migrate_success audit row without FK failure, got %d", successAuditCount)
	}

	afterAuthors := s.resourceAuthors(ctx)
	if afterAuthors.Images[imageAuthorKey(2, "win.iso")] != user.Username {
		t.Fatalf("expected invalidated ResourceAuthors cache to show author on target storage, got %+v", afterAuthors.Images)
	}
}

type nfsUploadCheckMock struct {
	writableByKey map[string]bool
	checkedKeys   []string
}

func (n *nfsUploadCheckMock) MountNFS(context.Context, string, string, string, string) (bool, error) {
	return false, nil
}
func (n *nfsUploadCheckMock) EnableNFSUpload(context.Context, string, string, string, string) error {
	return nil
}
func (n *nfsUploadCheckMock) CheckNFSUpload(_ context.Context, key string) (bool, error) {
	n.checkedKeys = append(n.checkedKeys, key)
	return n.writableByKey[key], nil
}
func (n *nfsUploadCheckMock) RemoveNFS(context.Context, string) error { return nil }

type recordingNFSUploadsImages struct {
	migrationMockImages
	updatedUploads map[int64]bool
}

func (m *recordingNFSUploadsImages) SetNFSUploads(_ context.Context, id int64, enabled bool) error {
	if m.updatedUploads == nil {
		m.updatedUploads = map[int64]bool{}
	}
	m.updatedUploads[id] = enabled
	return nil
}

func TestImageMigrationPreflightRejectsStaleWritableNFS(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()

	mockImg := &recordingNFSUploadsImages{
		migrationMockImages: migrationMockImages{
			fakeImagesAPI: fakeImagesAPI{
				catalog: images.Catalog{
					Storages: []images.Storage{
						{ID: 20, Name: "NFS", Kind: "nfs", Key: "nfskey20", Root: "/mnt/omb-nfs/nfskey20", State: "ready", Uploads: true, UploadsEnabled: true},
						{ID: 6, Name: "DSM", Kind: "smb", Key: "smbkey6", Root: "/mnt/omb-smb/smbkey6", State: "ready", Uploads: true, Free: 100 << 30},
					},
					Images: []images.Image{
						{StorageID: 20, Path: "win8.iso", Size: 4 << 30, Present: true},
					},
				},
			},
		},
	}
	nfsMock := &nfsUploadCheckMock{writableByKey: map[string]bool{"nfskey20": false}}

	s := &Server{
		Store:  store,
		Images: mockImg,
		NFS:    nfsMock,
	}

	err := s.migrationCoordinator().CheckPreflight(ctx, 20, "win8.iso", 6)
	if err == nil || !strings.Contains(err.Error(), "только для чтения") {
		t.Fatalf("expected read-only error when live CheckNFSUpload is false, got %v", err)
	}
	if val, ok := mockImg.updatedUploads[20]; !ok || val {
		t.Fatalf("expected SetNFSUploads(20, false) to sync stale DB state, got %+v", mockImg.updatedUploads)
	}
}

type permanentFailCommitImages struct {
	migrationMockImages
}

func (m *permanentFailCommitImages) CommitDeleteSource(_ context.Context, id string) error {
	if j, ok := m.migrations[id]; ok {
		j.State = "failed"
		j.Error = "failed to remove source file from disk: read-only file system"
	}
	return errors.New("400 failed to remove source file from disk: read-only file system")
}

func TestImageMigrationSyncsFailedStateWhenCommitDeleteSourceFailsPermanently(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()

	if err := store.CreateUser(ctx, "admin3", "AdminPass123!", "admin"); err != nil {
		t.Fatal(err)
	}
	users, err := store.Users(ctx)
	if err != nil || len(users) == 0 {
		t.Fatal(err)
	}
	user := users[0]
	_, adminToken, err := store.Login(ctx, "admin3", "AdminPass123!")
	if err != nil {
		t.Fatal(err)
	}

	mockImg := &permanentFailCommitImages{
		migrationMockImages: migrationMockImages{
			fakeImagesAPI: fakeImagesAPI{
				catalog: images.Catalog{
					Storages: []images.Storage{
						{ID: 20, Name: "NFS", Kind: "nfs", Root: "/mnt/omb-nfs/20", State: "ready", Uploads: true},
						{ID: 6, Name: "DSM", Kind: "smb", Root: "/mnt/omb-smb/6", State: "ready", Uploads: true, Free: 100 << 30},
					},
					Images: []images.Image{
						{StorageID: 20, Path: "win8.iso", Size: 4 << 30, Present: true},
					},
				},
			},
		},
	}
	s := &Server{Store: store, Images: mockImg}

	job, err := s.migrationCoordinator().StartMigration(ctx, user, 20, "win8.iso", 6)
	if err != nil {
		t.Fatal(err)
	}
	job.State = "ready_to_rebind"

	req := httptest.NewRequest(http.MethodGet, "/api/images/migrations", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}

	var got []*images.MigrationJob
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].State != "failed" || got[0].Error == "" {
		t.Fatalf("expected failed migration with error instead of stuck ready_to_rebind, got %+v", got)
	}

	var dbState, dbErr string
	if err := store.Pool.QueryRow(ctx, `SELECT state, error FROM image_migrations WHERE id=$1`, job.ID).Scan(&dbState, &dbErr); err != nil {
		t.Fatal(err)
	}
	if dbState != "failed" || dbErr == "" {
		t.Fatalf("expected DB state='failed' with error, got state=%q error=%q", dbState, dbErr)
	}
}

type roStartFailImages struct {
	migrationMockImages
}

func (m *roStartFailImages) StartMigration(context.Context, int64, string, int64) (*images.MigrationJob, error) {
	return nil, errors.New("local API /images/migrate: 403 source storage is not writable")
}

func TestImageMigrationStartMapsSourceNotWritableToLocalizedError(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()

	if err := store.CreateUser(ctx, "admin4", "AdminPass123!", "admin"); err != nil {
		t.Fatal(err)
	}
	_, adminToken, err := store.Login(ctx, "admin4", "AdminPass123!")
	if err != nil {
		t.Fatal(err)
	}

	mockImg := &roStartFailImages{
		migrationMockImages: migrationMockImages{
			fakeImagesAPI: fakeImagesAPI{
				catalog: images.Catalog{
					Storages: []images.Storage{
						{ID: 6, Name: "DSM", Kind: "smb", Root: "/mnt/omb-smb/6", State: "ready", Uploads: true},
						{ID: 20, Name: "NFS", Kind: "nfs", Root: "/mnt/omb-nfs/20", State: "ready", Uploads: true, Free: 100 << 30},
					},
					Images: []images.Image{
						{StorageID: 6, Path: "win.iso", Size: 4 << 30, Present: true},
					},
				},
			},
		},
	}
	s := &Server{Store: store, Images: mockImg}

	form := url.Values{
		"source_storage_id": {"6"},
		"target_storage_id": {"20"},
		"path":              {"win.iso"},
		"csrf":              {CSRFToken(adminToken)},
	}
	req := httptest.NewRequest(http.MethodPost, "/images/migrate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "исходное хранилище доступно только для чтения") {
		t.Fatalf("expected localized read-only source error, got: %s", rec.Body.String())
	}
}
