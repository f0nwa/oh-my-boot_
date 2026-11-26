package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
	"omb/internal/images"

	"github.com/jackc/pgx/v5"
)

type fakeWIMStore struct {
	*newInstallStore
	bootImages    []WindowsBootImage
	installImages []WindowsInstallImage
	nextBootID    int64
	nextInstallID int64
	locks         []MaintenanceLock
	versions      []MenuVersion
}

func newFakeWIMStore(t *testing.T, role string) (*Server, *fakeWIMStore, *fakeWindowsWIMAPI) {
	t.Helper()
	s, base := newInstallServer(t)
	base.user.Role = role
	ws := &fakeWIMStore{
		newInstallStore: base,
		nextBootID:      10,
		nextInstallID:   20,
	}
	wimAPI := &fakeWindowsWIMAPI{}
	s.Store = ws
	s.WindowsWIM = wimAPI
	return s, ws, wimAPI
}

func (s *fakeWIMStore) ListWindowsBootImages(context.Context) ([]WindowsBootImage, error) {
	return append([]WindowsBootImage(nil), s.bootImages...), nil
}

func (s *fakeWIMStore) GetWindowsBootImage(_ context.Context, id int64) (WindowsBootImage, error) {
	for _, img := range s.bootImages {
		if img.ID == id {
			return img, nil
		}
	}
	return WindowsBootImage{}, pgx.ErrNoRows
}

func (s *fakeWIMStore) UpsertFileBootImage(_ context.Context, src WindowsBootImageSource) (WindowsBootImage, error) {
	for i := range s.bootImages {
		if s.bootImages[i].SourceKind == WIMSourceKindFile && s.bootImages[i].StorageID == src.StorageID && s.bootImages[i].Path == src.Path {
			s.bootImages[i].Title = src.Title
			s.bootImages[i].Size = src.Size
			s.bootImages[i].StorageRoot = src.StorageRoot
			s.bootImages[i].State = "pending"
			s.bootImages[i].Error = ""
			return s.bootImages[i], nil
		}
	}
	s.nextBootID++
	mod := src.Modified
	img := WindowsBootImage{
		ID:          s.nextBootID,
		SourceKind:  WIMSourceKindFile,
		StorageID:   src.StorageID,
		Path:        src.Path,
		StorageRoot: src.StorageRoot,
		Size:        src.Size,
		Modified:    &mod,
		Title:       src.Title,
		State:       "pending",
	}
	s.bootImages = append(s.bootImages, img)
	return img, nil
}

func (s *fakeWIMStore) SetBootImageReady(_ context.Context, id int64, sha string, hasSetupExe bool, info agent.WindowsWIMInfo) error {
	for i := range s.bootImages {
		if s.bootImages[i].ID == id {
			s.bootImages[i].SHA256 = sha
			s.bootImages[i].HasSetupExe = hasSetupExe
			s.bootImages[i].WIMInfo = info
			s.bootImages[i].State = "ready"
			s.bootImages[i].Error = ""
			return nil
		}
	}
	return pgx.ErrNoRows
}

func (s *fakeWIMStore) SetBootImageError(_ context.Context, id int64, state, lastErr string) error {
	for i := range s.bootImages {
		if s.bootImages[i].ID == id {
			s.bootImages[i].State = state
			s.bootImages[i].Error = lastErr
			return nil
		}
	}
	return pgx.ErrNoRows
}

func (s *fakeWIMStore) SetBootImageTitle(_ context.Context, id int64, title string) error {
	for i := range s.bootImages {
		if s.bootImages[i].ID == id {
			s.bootImages[i].Title = title
			return nil
		}
	}
	return pgx.ErrNoRows
}

func (s *fakeWIMStore) DeleteWindowsBootImage(_ context.Context, id int64) error {
	for i := range s.bootImages {
		if s.bootImages[i].ID == id {
			s.bootImages = append(s.bootImages[:i], s.bootImages[i+1:]...)
			return nil
		}
	}
	return pgx.ErrNoRows
}

func (s *fakeWIMStore) ListWindowsInstallImages(context.Context) ([]WindowsInstallImage, error) {
	return append([]WindowsInstallImage(nil), s.installImages...), nil
}

func (s *fakeWIMStore) GetWindowsInstallImage(_ context.Context, id int64) (WindowsInstallImage, error) {
	for _, img := range s.installImages {
		if img.ID == id {
			return img, nil
		}
	}
	return WindowsInstallImage{}, pgx.ErrNoRows
}

func (s *fakeWIMStore) UpsertWindowsInstallImage(_ context.Context, src WindowsInstallImageSource) (WindowsInstallImage, error) {
	for i := range s.installImages {
		if s.installImages[i].StorageID == src.StorageID && s.installImages[i].Path == src.Path {
			s.installImages[i].Title = src.Title
			s.installImages[i].Size = src.Size
			s.installImages[i].StorageRoot = src.StorageRoot
			s.installImages[i].State = "pending"
			s.installImages[i].Error = ""
			return s.installImages[i], nil
		}
	}
	s.nextInstallID++
	installFile := src.InstallFile
	if installFile == "" {
		if strings.HasSuffix(strings.ToLower(src.Path), ".esd") {
			installFile = "install.esd"
		} else {
			installFile = "install.wim"
		}
	}
	img := WindowsInstallImage{
		ID:          s.nextInstallID,
		StorageID:   src.StorageID,
		Path:        src.Path,
		StorageRoot: src.StorageRoot,
		InstallFile: installFile,
		Size:        src.Size,
		Modified:    src.Modified,
		Title:       src.Title,
		State:       "pending",
	}
	s.installImages = append(s.installImages, img)
	return img, nil
}

func (s *fakeWIMStore) SetInstallImageReady(_ context.Context, id int64, installFile string, info agent.WindowsWIMInfo) error {
	for i := range s.installImages {
		if s.installImages[i].ID == id {
			if installFile != "" {
				s.installImages[i].InstallFile = installFile
			}
			s.installImages[i].WIMInfo = info
			s.installImages[i].State = "ready"
			s.installImages[i].Error = ""
			return nil
		}
	}
	return pgx.ErrNoRows
}

func (s *fakeWIMStore) SetInstallImageError(_ context.Context, id int64, state, lastErr string) error {
	for i := range s.installImages {
		if s.installImages[i].ID == id {
			s.installImages[i].State = state
			s.installImages[i].Error = lastErr
			return nil
		}
	}
	return pgx.ErrNoRows
}

func (s *fakeWIMStore) SetInstallImageTitle(_ context.Context, id int64, title string) error {
	for i := range s.installImages {
		if s.installImages[i].ID == id {
			s.installImages[i].Title = title
			return nil
		}
	}
	return pgx.ErrNoRows
}

func (s *fakeWIMStore) DeleteWindowsInstallImage(_ context.Context, id int64) error {
	for i := range s.installImages {
		if s.installImages[i].ID == id {
			s.installImages = append(s.installImages[:i], s.installImages[i+1:]...)
			return nil
		}
	}
	return pgx.ErrNoRows
}

func (s *fakeWIMStore) MenuVersions(context.Context) ([]MenuVersion, error) {
	if s.versions != nil {
		return append([]MenuVersion(nil), s.versions...), nil
	}
	return s.newInstallStore.MenuVersions(context.Background())
}

func (s *fakeWIMStore) MaintenanceLocks(context.Context) ([]MaintenanceLock, error) {
	return append([]MaintenanceLock(nil), s.locks...), nil
}

func (s *fakeWIMStore) SetMaintenanceLock(_ context.Context, scope string, storageID int64, imagePath, reason string, userID int64) error {
	s.locks = append(s.locks, MaintenanceLock{Scope: scope, StorageID: storageID, ImagePath: imagePath, Reason: reason, CreatedBy: userID})
	return nil
}

func (s *fakeWIMStore) DeleteMaintenanceLock(_ context.Context, scope string, storageID int64, imagePath string) error {
	out := s.locks[:0]
	for _, l := range s.locks {
		if l.Scope == scope && l.StorageID == storageID && l.ImagePath == imagePath {
			continue
		}
		out = append(out, l)
	}
	s.locks = out
	return nil
}

type fakeWindowsWIMAPI struct {
	prepareBootRes    agent.WindowsBootImageResult
	prepareBootErr    error
	prepareInstallRes agent.WindowsInstallImageResult
	prepareInstallErr error
	removedBootIDs    []int64
	removedInstallIDs []int64
}

func (f *fakeWindowsWIMAPI) PrepareBootImage(_ context.Context, _ agent.WindowsBootImageRequest) (agent.WindowsBootImageResult, error) {
	return f.prepareBootRes, f.prepareBootErr
}

func (f *fakeWindowsWIMAPI) RemoveBootImage(_ context.Context, id int64) error {
	f.removedBootIDs = append(f.removedBootIDs, id)
	return nil
}

func (f *fakeWindowsWIMAPI) PrepareInstallImage(_ context.Context, _ agent.WindowsInstallImageRequest) (agent.WindowsInstallImageResult, error) {
	return f.prepareInstallRes, f.prepareInstallErr
}

func (f *fakeWindowsWIMAPI) RemoveInstallImage(_ context.Context, id int64) error {
	f.removedInstallIDs = append(f.removedInstallIDs, id)
	return nil
}

type fakeBootMaintCapture struct {
	state boot.MaintenanceState
	calls int
}

func (f *fakeBootMaintCapture) SetMaintenance(_ context.Context, st boot.MaintenanceState) error {
	f.state = st
	f.calls++
	return nil
}

func wimPost(t *testing.T, s *Server, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf", CSRFToken("test-token"))
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	return res
}

func TestDeleteUsedBootOrInstallWIMBlocked(t *testing.T) {
	t.Run("boot_used_by_installation_blocked", func(t *testing.T) {
		s, store, api := newFakeWIMStore(t, "admin")
		store.bootImages = []WindowsBootImage{{ID: 11, SourceKind: WIMSourceKindFile, StorageID: 7, Path: "wim/boot.wim", Title: "Win11 Boot", State: "ready"}}
		store.installations = []Installation{{ID: 101, BootProfileID: 50, Title: "Win11", BootImageID: 11}}

		res := wimPost(t, s, "/images/boot/delete", url.Values{"id": {"11"}})
		if res.Code != http.StatusConflict || len(api.removedBootIDs) != 0 || len(store.bootImages) != 1 {
			t.Fatalf("status=%d removed=%v count=%d body=%s", res.Code, api.removedBootIDs, len(store.bootImages), res.Body.String())
		}
		if !strings.Contains(res.Body.String(), "101") {
			t.Fatalf("expected installation id in error message, got %q", res.Body.String())
		}
	})

	t.Run("install_used_by_installation_blocked", func(t *testing.T) {
		s, store, api := newFakeWIMStore(t, "admin")
		store.installImages = []WindowsInstallImage{{ID: 21, StorageID: 7, Path: "wim/install.wim", Title: "Win11 Install", State: "ready"}}
		store.installations = []Installation{{ID: 102, BootProfileID: 50, Title: "Win11 Custom", InstallImageID: 21}}

		res := wimPost(t, s, "/images/install/delete", url.Values{"id": {"21"}})
		if res.Code != http.StatusConflict || len(api.removedInstallIDs) != 0 || len(store.installImages) != 1 {
			t.Fatalf("status=%d removed=%v count=%d body=%s", res.Code, api.removedInstallIDs, len(store.installImages), res.Body.String())
		}
		if !strings.Contains(res.Body.String(), "102") {
			t.Fatalf("expected installation id in error message, got %q", res.Body.String())
		}
	})

	t.Run("boot_used_by_menu_draft_blocked", func(t *testing.T) {
		s, store, api := newFakeWIMStore(t, "admin")
		store.bootImages = []WindowsBootImage{{ID: 12, SourceKind: WIMSourceKindFile, StorageID: 7, Path: "wim/boot-draft.wim", Title: "Draft Boot", State: "ready"}}
		store.draft = &MenuDraft{
			Items: []MenuDraftItem{{
				InstallationID: 105,
				Title:          "Draft Win11",
				Components:     &InstallationComponents{BootImageID: 12},
			}},
		}

		res := wimPost(t, s, "/images/boot/delete", url.Values{"id": {"12"}})
		if res.Code != http.StatusConflict || len(api.removedBootIDs) != 0 || len(store.bootImages) != 1 {
			t.Fatalf("status=%d removed=%v count=%d body=%s", res.Code, api.removedBootIDs, len(store.bootImages), res.Body.String())
		}
		if !strings.Contains(res.Body.String(), "черновик") {
			t.Fatalf("expected draft reference in error message, got %q", res.Body.String())
		}
	})

	t.Run("install_used_by_published_menu_version_blocked", func(t *testing.T) {
		s, store, api := newFakeWIMStore(t, "admin")
		store.installImages = []WindowsInstallImage{{ID: 22, StorageID: 7, Path: "wim/install-pub.wim", Title: "Pub Install", State: "ready"}}
		store.versions = []MenuVersion{{
			ID:     7,
			Active: true,
			Items: []MenuVersionItem{{
				InstallationID: 106,
				Title:          "Published Win11",
				Components:     &InstallationComponents{InstallImageID: 22},
			}},
		}}

		res := wimPost(t, s, "/images/install/delete", url.Values{"id": {"22"}})
		if res.Code != http.StatusConflict || len(api.removedInstallIDs) != 0 || len(store.installImages) != 1 {
			t.Fatalf("status=%d removed=%v count=%d body=%s", res.Code, api.removedInstallIDs, len(store.installImages), res.Body.String())
		}
		if !strings.Contains(res.Body.String(), "публикац") {
			t.Fatalf("expected published menu reason in error message, got %q", res.Body.String())
		}
	})

	t.Run("unused_boot_and_install_deleted_successfully", func(t *testing.T) {
		s, store, api := newFakeWIMStore(t, "admin")
		store.bootImages = []WindowsBootImage{{ID: 13, SourceKind: WIMSourceKindFile, StorageID: 7, Path: "wim/boot-free.wim", Title: "Free Boot", State: "ready"}}
		store.installImages = []WindowsInstallImage{{ID: 23, StorageID: 7, Path: "wim/install-free.wim", Title: "Free Install", State: "ready"}}

		resBoot := wimPost(t, s, "/images/boot/delete", url.Values{"id": {"13"}})
		if resBoot.Code != http.StatusSeeOther || len(api.removedBootIDs) != 1 || api.removedBootIDs[0] != 13 || len(store.bootImages) != 0 {
			t.Fatalf("boot delete failed: status=%d removed=%v left=%d body=%s", resBoot.Code, api.removedBootIDs, len(store.bootImages), resBoot.Body.String())
		}

		resInst := wimPost(t, s, "/images/install/delete", url.Values{"id": {"23"}})
		if resInst.Code != http.StatusSeeOther || len(api.removedInstallIDs) != 1 || api.removedInstallIDs[0] != 23 || len(store.installImages) != 0 {
			t.Fatalf("install delete failed: status=%d removed=%v left=%d body=%s", resInst.Code, api.removedInstallIDs, len(store.installImages), resInst.Body.String())
		}
	})
}

func TestInstallImageSwapRevalidatesAnswerXML(t *testing.T) {
	s, store, _ := newFakeWIMStore(t, "operator")

	// Base Windows ISO has Professional edition; bound answer requires Professional.
	store.profiles[0].WIMInfo = agent.WindowsWIMInfo{
		Known: true,
		Images: []agent.WindowsWIMImage{
			{Index: 1, Name: "Windows 11 Pro", Architecture: "amd64", EditionID: "Professional", Build: 26100},
		},
	}
	store.answers = []WindowsAnswerVariant{{
		ID:    31,
		Title: "Win11 Pro Auto",
		State: "ready",
		Target: WindowsAnswerTarget{
			Architectures: []string{"amd64"},
			ImageMode:     "edition",
			EditionID:     "pro",
			EditionLabel:  "Pro",
		},
	}}
	store.installations = []Installation{{
		ID:            100,
		BootProfileID: 50,
		Title:         "Win11 Pro",
		AnswerID:      31,
	}}
	// Install image 21 has only Core (Home), missing Professional.
	// Install image 22 has Professional.
	store.installImages = []WindowsInstallImage{
		{
			ID:        21,
			StorageID: 7,
			Path:      "wim/home-only.wim",
			Title:     "Win11 Home Only",
			State:     "ready",
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Windows 11 Home", Architecture: "amd64", EditionID: "Core", Build: 26100},
				},
			},
		},
		{
			ID:        22,
			StorageID: 7,
			Path:      "wim/pro.wim",
			Title:     "Win11 Pro Standalone",
			State:     "ready",
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Windows 11 Pro", Architecture: "amd64", EditionID: "Professional", Build: 26100},
				},
			},
		},
	}

	// Swapping install_image_id to 21 (Home only) must be rejected cleanly.
	res := wimPost(t, s, "/installations/100", url.Values{
		"boot_profile_id":  {"50"},
		"title":            {"Win11 Pro"},
		"answer_id":        {"31"},
		"install_image_id": {"21"},
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when swapping install.wim missing edition, got %d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "несовместим") {
		t.Fatalf("expected incompatibility error message, got %q", res.Body.String())
	}
	if store.installations[0].InstallImageID != 0 {
		t.Fatalf("installation was mutated despite rejection: %+v", store.installations[0])
	}

	// Swapping install_image_id to 22 (Pro) must succeed and persist InstallImageID=22.
	resOK := wimPost(t, s, "/installations/100", url.Values{
		"boot_profile_id":  {"50"},
		"title":            {"Win11 Pro"},
		"answer_id":        {"31"},
		"install_image_id": {"22"},
	})
	if resOK.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 when swapping compatible install.wim, got %d body=%s", resOK.Code, resOK.Body.String())
	}
	if store.installations[0].InstallImageID != 22 {
		t.Fatalf("expected InstallImageID=22, got %+v", store.installations[0])
	}
}

func TestWIMInspectAndRenameHandlers(t *testing.T) {
	s, store, api := newFakeWIMStore(t, "operator")
	now := time.Now().UTC().Truncate(time.Second)
	s.Images.(*fakeImagesAPI).catalog.Storages = []images.Storage{{ID: 7, Name: "Local", Kind: "local", Root: "/srv/omb/iso", State: "ready"}}
	s.Images.(*fakeImagesAPI).catalog.Images = []images.Image{
		{StorageID: 7, Path: "wim/boot.wim", Size: 500 << 20, Modified: now, Present: true, Kind: images.ImageKindWIM},
		{StorageID: 7, Path: "wim/install.esd", Size: 4 << 30, Modified: now, Present: true, Kind: images.ImageKindESD},
	}
	api.prepareBootRes = agent.WindowsBootImageResult{
		SHA256:      strings.Repeat("a", 64),
		HasSetupExe: true,
		WIMInfo:     agent.WindowsWIMInfo{Known: true, Images: []agent.WindowsWIMImage{{Index: 2, Architecture: "amd64", Build: 26100}}},
	}
	api.prepareInstallRes = agent.WindowsInstallImageResult{
		InstallFile: "install.esd",
		WIMInfo:     agent.WindowsWIMInfo{Known: true, Images: []agent.WindowsWIMImage{{Index: 1, Architecture: "amd64", EditionID: "Professional", Build: 26100}}},
	}

	resBoot := wimPost(t, s, "/images/boot/inspect", url.Values{"storage_id": {"7"}, "path": {"wim/boot.wim"}})
	if resBoot.Code != http.StatusSeeOther || len(store.bootImages) != 1 || store.bootImages[0].State != "ready" || !store.bootImages[0].HasSetupExe {
		t.Fatalf("boot inspect failed: status=%d imgs=%+v body=%s", resBoot.Code, store.bootImages, resBoot.Body.String())
	}

	resInst := wimPost(t, s, "/images/install/inspect", url.Values{"storage_id": {"7"}, "path": {"wim/install.esd"}})
	if resInst.Code != http.StatusSeeOther || len(store.installImages) != 1 || store.installImages[0].State != "ready" || store.installImages[0].InstallFile != "install.esd" {
		t.Fatalf("install inspect failed: status=%d imgs=%+v body=%s", resInst.Code, store.installImages, resInst.Body.String())
	}

	// Rename boot.wim and install.wim
	resRenameBoot := wimPost(t, s, "/images/boot/rename", url.Values{"id": {strings.TrimSpace("11")}, "title": {"Win11 24H2 Boot"}})
	if resRenameBoot.Code != http.StatusSeeOther || store.bootImages[0].Title != "Win11 24H2 Boot" {
		t.Fatalf("boot rename failed: status=%d imgs=%+v body=%s", resRenameBoot.Code, store.bootImages, resRenameBoot.Body.String())
	}

	resRenameInst := wimPost(t, s, "/images/install/rename", url.Values{"id": {"21"}, "title": {"Win11 24H2 ESD"}})
	if resRenameInst.Code != http.StatusSeeOther || store.installImages[0].Title != "Win11 24H2 ESD" {
		t.Fatalf("install rename failed: status=%d imgs=%+v body=%s", resRenameInst.Code, store.installImages, resRenameInst.Body.String())
	}

	// Invalid WIM type from agent returns 422 and marks error.
	api.prepareInstallErr = errors.New("invalid Windows WIM image type: pure WinPE")
	resBad := wimPost(t, s, "/images/install/inspect", url.Values{"storage_id": {"7"}, "path": {"wim/install.esd"}})
	if resBad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid WIM type, got %d body=%s", resBad.Code, resBad.Body.String())
	}
}

func TestWIMMaintenanceLockSync(t *testing.T) {
	s, store, _ := newFakeWIMStore(t, "admin")
	capture := &fakeBootMaintCapture{}
	s.BootMaintenance = capture

	store.bootImages = []WindowsBootImage{{ID: 11, SourceKind: WIMSourceKindFile, StorageID: 7, Path: "wim/boot.wim", State: "ready"}}
	store.installImages = []WindowsInstallImage{{ID: 21, StorageID: 7, Path: "wim/install.wim", State: "ready"}}
	store.bootProfiles = append(store.bootProfiles, BootProfile{ID: 60, Kind: BootProfileWindowsWIM, InstallImageID: 21, Title: "Standalone WIM"})
	store.installations = []Installation{
		{ID: 201, BootProfileID: 50, Title: "ISO with custom boot.wim", BootImageID: 11},
		{ID: 202, BootProfileID: 60, Title: "Standalone WIM", BootImageID: 11, InstallImageID: 21},
	}

	// Lock only wim/install.wim -> installation 202 must be paused, 201 must not.
	store.locks = []MaintenanceLock{{Scope: "image", StorageID: 7, ImagePath: "wim/install.wim"}}
	if err := s.SyncMaintenance(context.Background()); err != nil {
		t.Fatalf("SyncMaintenance: %v", err)
	}
	if len(capture.state.InstallationIDs) != 1 || capture.state.InstallationIDs[0] != 202 {
		t.Fatalf("expected only [202] locked for install.wim, got %+v", capture.state.InstallationIDs)
	}

	// Lock wim/boot.wim -> both 201 and 202 must be paused.
	store.locks = []MaintenanceLock{{Scope: "image", StorageID: 7, ImagePath: "wim/boot.wim"}}
	if err := s.SyncMaintenance(context.Background()); err != nil {
		t.Fatalf("SyncMaintenance: %v", err)
	}
	if len(capture.state.InstallationIDs) != 2 {
		t.Fatalf("expected both [201, 202] locked for boot.wim, got %+v", capture.state.InstallationIDs)
	}
}

func TestWIMDeleteFileChecksStorageAndSurfacesErrors(t *testing.T) {
	t.Run("readonly_nfs_blocked_before_agent_or_db_delete", func(t *testing.T) {
		s, store, api := newFakeWIMStore(t, "admin")
		s.Images.(*fakeImagesAPI).catalog.Storages = []images.Storage{{ID: 7, Name: "RO NFS", Kind: "nfs", State: "ready", Uploads: false}}
		s.Images.(*fakeImagesAPI).catalog.Images = []images.Image{{StorageID: 7, Path: "wim/boot.wim", Present: true, Kind: images.ImageKindWIM}}
		store.bootImages = []WindowsBootImage{{ID: 11, SourceKind: WIMSourceKindFile, StorageID: 7, Path: "wim/boot.wim", Title: "RO Boot", State: "ready"}}

		res := wimPost(t, s, "/images/boot/delete", url.Values{"id": {"11"}, "delete_file": {"1"}})
		if res.Code != http.StatusForbidden || len(api.removedBootIDs) != 0 || len(store.bootImages) != 1 {
			t.Fatalf("expected 403 and no deletion, got status=%d removed=%v left=%d body=%s", res.Code, api.removedBootIDs, len(store.bootImages), res.Body.String())
		}
	})

	t.Run("smb_enables_upload_and_deletes_file", func(t *testing.T) {
		s, store, api := newFakeWIMStore(t, "admin")
		s.Images.(*fakeImagesAPI).catalog.Storages = []images.Storage{{ID: 7, Name: "NAS", Kind: "smb", Key: "nas1", Server: "10.0.0.5", Share: "ISO", State: "ready"}}
		s.Images.(*fakeImagesAPI).catalog.Images = []images.Image{{StorageID: 7, Path: "wim/install.wim", Present: true, Kind: images.ImageKindWIM}}
		store.installImages = []WindowsInstallImage{{ID: 21, StorageID: 7, Path: "wim/install.wim", Title: "NAS Install", State: "ready"}}
		events := []string{}
		s.SMB = &fakeSMBMountAPI{events: &events}

		res := wimPost(t, s, "/images/install/delete", url.Values{"id": {"21"}, "delete_file": {"1"}})
		if res.Code != http.StatusSeeOther || strings.Join(events, ",") != "enable-upload" || len(api.removedInstallIDs) != 1 || len(s.Images.(*fakeImagesAPI).deleted) != 1 || len(store.installImages) != 0 {
			t.Fatalf("smb delete_file failed: status=%d events=%v removed=%v deleted=%+v left=%d body=%s", res.Code, events, api.removedInstallIDs, s.Images.(*fakeImagesAPI).deleted, len(store.installImages), res.Body.String())
		}
	})

	t.Run("storage_delete_error_is_surfaced_not_ignored", func(t *testing.T) {
		s, store, _ := newFakeWIMStore(t, "admin")
		imgAPI := s.Images.(*fakeImagesAPI)
		imgAPI.catalog.Storages = []images.Storage{{ID: 7, Name: "Local", Kind: "local", State: "ready"}}
		imgAPI.catalog.Images = []images.Image{{StorageID: 7, Path: "wim/boot.wim", Present: true, Kind: images.ImageKindWIM}}
		imgAPI.deleteErr = errors.New("permission denied")
		store.bootImages = []WindowsBootImage{{ID: 12, SourceKind: WIMSourceKindFile, StorageID: 7, Path: "wim/boot.wim", Title: "Err Boot", State: "ready"}}

		res := wimPost(t, s, "/images/boot/delete", url.Values{"id": {"12"}, "delete_file": {"1"}})
		if res.Code != http.StatusServiceUnavailable || len(store.bootImages) != 1 {
			t.Fatalf("expected 503 and preserved record on DeleteImage failure, got status=%d left=%d body=%s", res.Code, len(store.bootImages), res.Body.String())
		}
	})
}

func TestInstallImageSwapWithDefaultISOBootChecksCompat(t *testing.T) {
	s, store, _ := newFakeWIMStore(t, "operator")

	// Base Windows 10 x64 ISO (profile ID 1, build 19041) with default ISO boot.wim.
	for i := range store.profiles {
		if store.profiles[i].ID == 1 {
			store.profiles[i].WIMInfo = agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Windows 10 Pro", Architecture: "amd64", EditionID: "Professional", Build: 19041},
				},
			}
		}
	}
	store.installations = []Installation{{
		ID:            100,
		BootProfileID: 50,
		Title:         "Win10 ISO",
	}}
	store.installImages = []WindowsInstallImage{
		{
			ID:        21,
			StorageID: 7,
			Path:      "wim/win11-arm64.wim",
			Title:     "Win11 ARM64",
			State:     "ready",
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Windows 11 Pro ARM64", Architecture: "arm64", EditionID: "Professional", Build: 26100},
				},
			},
		},
		{
			ID:        22,
			StorageID: 7,
			Path:      "wim/win11-x64.wim",
			Title:     "Win11 x64",
			State:     "ready",
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Windows 11 Pro", Architecture: "amd64", EditionID: "Professional", Build: 26100},
				},
			},
		},
	}

	// Architecture mismatch (x64 ISO boot.wim + arm64 install.wim) must be rejected with 400.
	resArch := wimPost(t, s, "/installations/100", url.Values{
		"boot_profile_id":  {"50"},
		"title":            {"Win10 ISO"},
		"install_image_id": {"21"},
	})
	if resArch.Code != http.StatusBadRequest || !strings.Contains(resArch.Body.String(), "Несовместимая пара") {
		t.Fatalf("expected 400 for arch mismatch with default ISO boot.wim, got %d body=%s", resArch.Code, resArch.Body.String())
	}

	// Older ISO boot.wim (19041) + newer Win11 install.wim (26100) requires confirm_warnings=1.
	resWarn := wimPost(t, s, "/installations/100", url.Values{
		"boot_profile_id":  {"50"},
		"title":            {"Win10 ISO"},
		"install_image_id": {"22"},
	})
	if resWarn.Code != http.StatusBadRequest || !strings.Contains(resWarn.Body.String(), "Подтвердите предупреждения") {
		t.Fatalf("expected 400 warning confirmation for older ISO boot.wim, got %d body=%s", resWarn.Code, resWarn.Body.String())
	}

	// With confirm_warnings=1 it succeeds.
	resConfirm := wimPost(t, s, "/installations/100", url.Values{
		"boot_profile_id":  {"50"},
		"title":            {"Win10 ISO"},
		"install_image_id": {"22"},
		"confirm_warnings": {"1"},
	})
	if resConfirm.Code != http.StatusSeeOther || store.installations[0].InstallImageID != 22 {
		t.Fatalf("expected 303 with confirm_warnings=1, got %d inst=%+v body=%s", resConfirm.Code, store.installations[0], resConfirm.Body.String())
	}
}
