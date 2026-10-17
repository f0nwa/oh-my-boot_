package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/images"
)

type recordingWindowsProfileStore struct {
	authOnlyStore
	created []WindowsProfileSource
	profile WindowsProfile
	state   string
	install string
}

func (s *recordingWindowsProfileStore) CreateWindowsProfile(_ context.Context, source WindowsProfileSource) (WindowsProfile, error) {
	s.created = append(s.created, source)
	return WindowsProfile{ID: 11, WindowsProfileSource: source}, nil
}

func (s *recordingWindowsProfileStore) WindowsProfile(_ context.Context, id int64) (WindowsProfile, error) {
	if s.profile.ID == id {
		return s.profile, nil
	}
	return WindowsProfile{}, os.ErrNotExist
}

func (s *recordingWindowsProfileStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	if s.profile.ID == 0 {
		return []WindowsProfile{}, nil
	}
	return []WindowsProfile{s.profile}, nil
}

func (s *recordingWindowsProfileStore) SetWindowsProfileResult(_ context.Context, _ int64, state, installFile, _ string) error {
	s.state, s.install = state, installFile
	return nil
}

type recordingWindowsISOAPI struct {
	req agent.WindowsISORequest
}

func (a *recordingWindowsISOAPI) PrepareWindowsISO(_ context.Context, req agent.WindowsISORequest) (agent.WindowsISOResult, error) {
	a.req = req
	return agent.WindowsISOResult{InstallFile: "install.wim"}, nil
}
func (a *recordingWindowsISOAPI) CheckWindowsISO(_ context.Context, req agent.WindowsISORequest) error {
	a.req = req
	return nil
}

func TestWindowsProfilePrepareUsesSavedSourceFingerprint(t *testing.T) {
	modified := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	source := WindowsProfileSource{StorageID: 7, StorageRoot: "/mnt/pxe-platform-smb/key7", Path: "Win10/RU-RU.ISO", Size: 7304607744, Modified: modified, Title: "Windows 10 x64", Folder: "Windows"}
	store := &recordingWindowsProfileStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, profile: WindowsProfile{ID: 17, WindowsProfileSource: source, State: "pending"}}
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Kind: "smb", Root: source.StorageRoot, State: "ready"}}, Images: []images.Image{{StorageID: 7, Path: source.Path, Size: source.Size, Modified: source.Modified, Present: true}}}}
	preparer := &recordingWindowsISOAPI{}
	s := &Server{Store: store, Images: api, WindowsISO: preparer}
	form := url.Values{"csrf": {CSRFToken("test-token")}}
	req := httptest.NewRequest(http.MethodPost, "/images/windows-profiles/17/prepare", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || store.state != "ready" || store.install != "install.wim" || preparer.req.ID != 17 || preparer.req.Size != source.Size || !preparer.req.Modified.Equal(modified) {
		t.Fatalf("status=%d stored=%s/%s agent=%+v body=%s", res.Code, store.state, store.install, preparer.req, res.Body.String())
	}
}

func TestImagesPageOffersCatalogISOAsWindowsProfile(t *testing.T) {
	store := &recordingWindowsProfileStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, profile: WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Windows 10 x64", Path: "Win10/RU-RU.ISO"}, State: "pending"}}
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Name: "NAS", Kind: "smb", State: "ready"}}, Images: []images.Image{{StorageID: 7, Path: "Win10/RU-RU.ISO", Present: true}, {StorageID: 7, Path: "Other.ISO", Present: true}}}}
	s := &Server{Store: store, Images: api}
	req := httptest.NewRequest(http.MethodGet, "/images", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	for _, want := range []string{`action="/images/windows-profiles"`, `name="storage_id" value="7"`, `name="path" value="Other.ISO"`, `action="/images/windows-profiles/17/prepare"`} {
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), want) {
			t.Fatalf("missing %s from Images page: status=%d body=%s", want, res.Code, res.Body.String())
		}
	}
}

func TestSMBStorageRemovalIsBlockedWhileProfileReferencesIt(t *testing.T) {
	events := []string{}
	store := &recordingWindowsProfileStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, profile: WindowsProfile{ID: 17, WindowsProfileSource: WindowsProfileSource{StorageID: 7}}}
	api := &fakeManagedImages{events: &events, fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 7, Kind: "smb", Key: "0123456789abcdef0123456789abcdef", Server: "192.168.10.10", Share: "Downloads"}}}}}
	s := &Server{Store: store, Images: api, SMB: &fakeSMBMountAPI{events: &events}}
	res := submitSMB(t, s, "/images/smb/7/remove", smbForm())
	if res.Code != http.StatusConflict || len(events) != 0 {
		t.Fatalf("removed referenced storage: status=%d events=%v", res.Code, events)
	}
}

func TestWindowsProfileCreateUsesCatalogRatherThanFormPath(t *testing.T) {
	modified := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	store := &recordingWindowsProfileStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}}
	api := &fakeImagesAPI{catalog: images.Catalog{
		Storages: []images.Storage{{ID: 7, Kind: "smb", Root: "/mnt/pxe-platform-smb/key7", State: "ready"}},
		Images:   []images.Image{{StorageID: 7, Path: "Win10/RU-RU.ISO", Size: 7304607744, Modified: modified, Present: true}},
	}}
	s := &Server{Store: store, Images: api}
	form := url.Values{"storage_id": {"7"}, "path": {"Win10/RU-RU.ISO"}, "title": {"Windows 10 x64"}, "folder": {"Windows"}, "storage_root": {"/etc"}, "csrf": {CSRFToken("test-token")}}
	req := httptest.NewRequest(http.MethodPost, "/images/windows-profiles", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || len(store.created) != 1 || store.created[0].StorageRoot != "/mnt/pxe-platform-smb/key7" {
		t.Fatalf("status=%d created=%+v body=%s", res.Code, store.created, res.Body.String())
	}
}

func TestSelectWindowsISOUsesReadyCatalogRecord(t *testing.T) {
	modified := time.Date(2026, 10, 16, 15, 0, 0, 0, time.UTC)
	catalog := images.Catalog{
		Storages: []images.Storage{{ID: 7, Kind: "smb", Root: "/mnt/pxe-platform-smb/key7", State: "ready"}},
		Images:   []images.Image{{StorageID: 7, Path: "Win10/RU-RU.ISO", Size: 7304607744, Modified: modified, Present: true}},
	}
	got, err := SelectWindowsISO(catalog, 7, "Win10/RU-RU.ISO", "Windows 10 x64", "Windows")
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageID != 7 || got.Path != "Win10/RU-RU.ISO" || got.StorageRoot != "/mnt/pxe-platform-smb/key7" || got.Size != 7304607744 || !got.Modified.Equal(modified) || got.Title != "Windows 10 x64" || got.Folder != "Windows" {
		t.Fatalf("selected source differs from catalog: %+v", got)
	}
}

func TestSelectWindowsISORejectsUnlistedOrUnavailableSource(t *testing.T) {
	base := images.Catalog{
		Storages: []images.Storage{{ID: 7, Kind: "smb", Root: "/mnt/pxe-platform-smb/key7", State: "ready"}},
		Images:   []images.Image{{StorageID: 7, Path: "Win10/RU-RU.ISO", Size: 7304607744, Present: true}},
	}
	for _, tc := range []struct {
		name    string
		catalog images.Catalog
		path    string
		title   string
		folder  string
	}{
		{"unlisted path", base, "Win10/other.ISO", "Windows 10", "Windows"},
		{"path traversal", base, "../Win10/RU-RU.ISO", "Windows 10", "Windows"},
		{"unsupported file", base, "Win10/RU-RU.exe", "Windows 10", "Windows"},
		{"unsafe title", base, "Win10/RU-RU.ISO", "Win\ngoto local", "Windows"},
		{"non-ASCII title invisible in PXE", base, "Win10/RU-RU.ISO", "Установка", "Windows"},
		{"unsafe folder", base, "Win10/RU-RU.ISO", "Windows 10", "Windows/../Admin"},
		{"ARM64 image", base, "Windows-ARM64.ISO", "Windows ARM", "Windows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SelectWindowsISO(tc.catalog, 7, tc.path, tc.title, tc.folder); err == nil {
				t.Fatal("accepted an unlisted or unsafe ISO selection")
			}
		})
	}
	unavailable := base
	unavailable.Storages = []images.Storage{{ID: 7, Kind: "smb", Root: "/mnt/pxe-platform-smb/key7", State: "unavailable"}}
	if _, err := SelectWindowsISO(unavailable, 7, "Win10/RU-RU.ISO", "Windows 10", "Windows"); err == nil {
		t.Fatal("accepted unavailable storage")
	}
	missing := base
	missing.Images = []images.Image{{StorageID: 7, Path: "Win10/RU-RU.ISO", Size: 7304607744, Present: false}}
	if _, err := SelectWindowsISO(missing, 7, "Win10/RU-RU.ISO", "Windows 10", "Windows"); err == nil {
		t.Fatal("accepted missing image")
	}
	poisoned := base
	poisoned.Images = []images.Image{{StorageID: 7, Path: "../outside.iso", Size: 100, Present: true}}
	if _, err := SelectWindowsISO(poisoned, 7, "../outside.iso", "Windows 10", "Windows"); err == nil {
		t.Fatal("accepted traversal path copied from catalog")
	}
}
