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
	"omb/internal/images"
)

// The fake retains the real store's source/link/state side effects. Mounting
// and systemd are replaced at the Agent boundary, not in the HTTP workflow.
type downloadsStore struct {
	*newInstallStore
	sourceCreates int
}

func (s *downloadsStore) CreateWindowsProfile(_ context.Context, source WindowsProfileSource) (WindowsProfile, error) {
	s.sourceCreates++
	p := WindowsProfile{ID: 80, WindowsProfileSource: source, State: "pending"}
	s.profiles = append(s.profiles, p)
	kind := BootProfileWindowsISO
	if source.BootMode == agent.ISOStrelecMode {
		kind = BootProfileWinPEISO
	}
	s.bootProfiles = append(s.bootProfiles, BootProfile{ID: 81, Kind: kind, WindowsProfileID: p.ID, Title: source.Title})
	return p, nil
}
func (s *downloadsStore) WindowsProfile(_ context.Context, id int64) (WindowsProfile, error) {
	for _, p := range s.profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return WindowsProfile{}, errors.New("missing source")
}
func (s *downloadsStore) SetWindowsProfileResult(ctx context.Context, id int64, state, install, problem string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for i := range s.profiles {
		if s.profiles[i].ID == id {
			s.profiles[i].State, s.profiles[i].InstallFile, s.profiles[i].Error = state, install, problem
			return nil
		}
	}
	return errors.New("missing source")
}
func (s *downloadsStore) UpdateInstallation(_ context.Context, item Installation) error {
	for i := range s.installations {
		if s.installations[i].ID == item.ID {
			s.installations[i] = item
			return nil
		}
	}
	return errors.New("missing download")
}

type downloadPreparer struct {
	store  *downloadsStore
	err    error
	calls  int
	req    agent.WindowsISORequest
	cancel context.CancelFunc
}

func (p *downloadPreparer) PrepareWindowsISO(_ context.Context, req agent.WindowsISORequest) (agent.WindowsISOResult, error) {
	p.calls++
	p.req = req
	if p.cancel != nil {
		p.cancel()
	}
	if len(p.store.installations) == 0 {
		return agent.WindowsISOResult{}, errors.New("download must be saved before preparation")
	}
	if req.BootMode == agent.ISOStrelecMode {
		return agent.WindowsISOResult{}, p.err
	}
	return agent.WindowsISOResult{InstallFile: "install.wim"}, p.err
}
func (*downloadPreparer) CheckWindowsISO(context.Context, agent.WindowsISORequest) error { return nil }

func downloadsServer(t *testing.T) (*Server, *downloadsStore, *downloadPreparer) {
	t.Helper()
	s, base := newInstallServer(t)
	store := &downloadsStore{newInstallStore: base}
	s.Store = store
	s.Images = &fakeImagesAPI{catalog: images.Catalog{
		Storages: []images.Storage{{ID: base.profiles[0].StorageID, Kind: "smb", Root: base.profiles[0].StorageRoot, State: "ready"}, {ID: 7, Name: "NAS", Kind: "smb", Root: "/mnt/omb-smb/key7", State: "ready"}},
		Images:   []images.Image{{StorageID: base.profiles[0].StorageID, Path: base.profiles[0].Path, Size: base.profiles[0].Size, Modified: base.profiles[0].Modified, Present: true}, {StorageID: 7, Path: "Windows-new.iso", Size: 1234, Modified: time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC), Present: true}},
	}}
	prep := &downloadPreparer{store: store}
	s.WindowsISO = prep
	return s, store, prep
}

func TestDownloadCreatesSourceAndPreparesAutomatically(t *testing.T) {
	s, store, prep := downloadsServer(t)
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:Windows-new.iso"}, "title": {"New Windows"}, "folder": {"Windows"}})
	if res.Code != http.StatusSeeOther || len(store.installations) != 1 || prep.calls != 1 || store.sourceCreates != 1 {
		t.Fatalf("status=%d downloads=%+v created=%d preparation=%d body=%s", res.Code, store.installations, store.sourceCreates, prep.calls, res.Body.String())
	}
	if store.installations[0].BootProfileID != 81 || store.profiles[len(store.profiles)-1].State != "ready" || lastDraftKey(store.newInstallStore) != "install_100" {
		t.Fatalf("downloads=%+v source=%+v draft=%+v", store.installations, store.profiles, store.draft)
	}
	// Repeating the source selection reuses its technical record and files.
	res = menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:Windows-new.iso"}, "title": {"Another Windows"}})
	if res.Code != http.StatusSeeOther || store.sourceCreates != 1 || prep.calls != 1 {
		t.Fatalf("duplicate source or preparation: status=%d creates=%d calls=%d", res.Code, store.sourceCreates, prep.calls)
	}
}

func TestDownloadPreparationErrorPersistsAndCanRetry(t *testing.T) {
	s, store, prep := downloadsServer(t)
	prep.err = errors.New("Windows ISO preparation failed")
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:Windows-new.iso"}, "title": {"Retry Windows"}})
	if res.Code != http.StatusSeeOther || len(store.installations) != 1 {
		t.Fatalf("lost failed download: status=%d body=%s", res.Code, res.Body.String())
	}
	body := libraryGet(t, s, "/installations")
	if !strings.Contains(body, "Retry Windows") || !strings.Contains(body, `action="/installations/100/prepare"`) {
		t.Fatalf("failed download cannot be retried: %s", body)
	}
	// A failed source must never become a publishable menu item.
	sources, _ := s.installationMenuSources(context.Background())
	if _, err := BuildMenuFromDraft(*store.draft, testBootURL, store.profiles, store.answers, store.configs, sources); err == nil {
		t.Fatal("unprepared download can be published")
	}
	prep.err = nil
	res = menuPost(t, s, "/installations/100/prepare", url.Values{})
	if res.Code != http.StatusSeeOther || prep.calls != 2 || len(store.installations) != 1 || store.profiles[len(store.profiles)-1].State != "ready" {
		t.Fatalf("retry status=%d calls=%d downloads=%+v sources=%+v", res.Code, prep.calls, store.installations, store.profiles)
	}
}

func TestDownloadRejectsInvalidOptionsBeforeCreatingSource(t *testing.T) {
	for _, extra := range []url.Values{{"title": {"Кириллица"}}, {"inf": {"bad"}}, {"answer_id": {"31"}}, {"folder": {"../escape"}}} {
		s, store, prep := downloadsServer(t)
		form := url.Values{"boot_profile_id": {"iso:7:Windows-new.iso"}, "title": {"Windows"}}
		for k, v := range extra {
			form[k] = v
		}
		res := menuPost(t, s, "/installations", form)
		if res.Code != http.StatusBadRequest || store.sourceCreates != 0 || prep.calls != 0 || len(store.installations) != 0 {
			t.Fatalf("form=%v status=%d creates=%d calls=%d", form, res.Code, store.sourceCreates, prep.calls)
		}
	}
}

func TestDownloadFormOffersRawISOAndHidesProfileTerminology(t *testing.T) {
	s, _, _ := downloadsServer(t)
	body := libraryGet(t, s, "/installations/new?storage_id=7&path=Windows-new.iso")
	if !strings.Contains(body, `value="iso:7:Windows-new.iso"`) || !strings.Contains(body, "Windows-new.iso") || strings.Contains(body, "Профиль загрузки") {
		t.Fatalf("form does not select a raw image: %s", body)
	}
}

func TestDownloadEditPreservesIDAndPublishedMenu(t *testing.T) {
	s, store, _ := downloadsServer(t)
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "title": {"Original"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", res.Code)
	}
	before := len(store.installations)
	body := libraryGet(t, s, "/installations/100/edit")
	if !strings.Contains(body, `value="Original"`) || !strings.Contains(body, `action="/installations/100"`) {
		t.Fatalf("missing editable download: %s", body)
	}
	res = menuPost(t, s, "/installations/100", url.Values{"boot_profile_id": {"51"}, "title": {"Changed"}, "folder": {"Lab"}, "answer_id": {"31"}})
	if res.Code != http.StatusSeeOther || len(store.installations) != before || store.installations[0].ID != 100 || store.installations[0].Title != "Changed" || store.installations[0].AnswerID != 31 || store.draft.Items[0].Folder != "Lab" {
		t.Fatalf("edit status=%d downloads=%+v draft=%+v body=%s", res.Code, store.installations, store.draft, res.Body.String())
	}
}

func TestISOLibraryHasOneFileRowAndNoProfileActions(t *testing.T) {
	s, store, _ := downloadsServer(t)
	store.profiles = append(store.profiles, WindowsProfile{ID: 80, WindowsProfileSource: WindowsProfileSource{StorageID: 7, Path: "Windows-new.iso", Title: "Internal"}, State: "pending"})
	body := libraryGet(t, s, "/images")
	if strings.Count(body, `class="path-cell">Windows-new.iso`) != 1 {
		t.Fatalf("ISO is not shown once: %s", body)
	}
	for _, bad := range []string{"Создать профиль", `/images/windows-profiles/80/prepare`, ">pending<", "Название в меню"} {
		if strings.Contains(body, bad) {
			t.Fatalf("library exposes %q", bad)
		}
	}
}

func TestDownloadRejectsMalformedEditIDInsteadOfCreating(t *testing.T) {
	s, store, _ := downloadsServer(t)
	res := menuPost(t, s, "/installations/not-an-id", url.Values{"boot_profile_id": {"51"}, "title": {"Accidental copy"}})
	if res.Code != http.StatusBadRequest || len(store.installations) != 0 {
		t.Fatalf("status=%d downloads=%+v", res.Code, store.installations)
	}
}

func TestDownloadPreparesSelectedDriversAutomatically(t *testing.T) {
	s, store, prep := downloadsServer(t)
	builder := &fakeDriverBuilder{}
	s.DriverConfigs = builder
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:Windows-new.iso"}, "title": {"Windows VirtIO"}, "inf": {strings.Repeat("a", 64) + ":NetKVM/netkvm.inf"}})
	if res.Code != http.StatusSeeOther || len(builder.calls) != 1 || prep.calls != 1 || store.installations[0].DriverConfigID == 0 {
		t.Fatalf("status=%d drivers=%v ISO=%d downloads=%+v", res.Code, builder.calls, prep.calls, store.installations)
	}
	body := libraryGet(t, s, "/installations")
	if strings.Contains(body, "Ошибка подготовки") || strings.Contains(body, "Нужна сборка") {
		t.Fatalf("automatic driver preparation did not finish: %s", body)
	}
	sources, _ := s.installationMenuSources(context.Background())
	if _, err := BuildMenuFromDraft(*store.draft, testBootURL, store.profiles, store.answers, store.configs, sources); err != nil {
		t.Fatalf("prepared entry cannot be published: %v", err)
	}
}

func TestDownloadPersistsPreparationAfterDisconnectWithCatalogFingerprint(t *testing.T) {
	s, store, prep := downloadsServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prep.cancel = cancel
	req := accountRequest(http.MethodPost, "/installations", url.Values{"boot_profile_id": {"iso:7:Windows-new.iso"}, "title": {"Disconnect"}, "storage_root": {"/etc"}}).WithContext(ctx)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || prep.req.StorageRoot != "/mnt/omb-smb/key7" || prep.req.Size != 1234 || !prep.req.Modified.Equal(time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)) || store.profiles[len(store.profiles)-1].State != "ready" {
		t.Fatalf("status=%d request=%+v sources=%+v", res.Code, prep.req, store.profiles)
	}
}

func TestDownloadEditAndRetryRequireRoleAndCSRF(t *testing.T) {
	for _, route := range []string{"/installations/100", "/installations/100/prepare"} {
		for _, role := range []string{"operator", "observer"} {
			s, store, prep := downloadsServer(t)
			store.user.Role = role
			req := accountRequest(http.MethodPost, route, url.Values{"boot_profile_id": {"51"}, "title": {"Denied"}})
			if role == "operator" {
				req = httptest.NewRequest(http.MethodPost, route, strings.NewReader("csrf=bad"))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
			}
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusForbidden || prep.calls != 0 || len(store.installations) != 0 {
				t.Fatalf("route=%s role=%s status=%d", route, role, res.Code)
			}
		}
	}
}

func TestDownloadComponentEditIsPendingAndPreservesPublishedSnapshot(t *testing.T) {
	s, store, _ := downloadsServer(t)
	if res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "title": {"Windows"}, "answer_id": {"31"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", res.Code)
	}
	if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("publish: %d %s", res.Code, res.Body.String())
	}
	original := store.versions[len(store.versions)-1]
	// Only the answer changes: title, folder, source and entry ID stay equal.
	if res := menuPost(t, s, "/installations/100", url.Values{"boot_profile_id": {"51"}, "title": {"Windows"}, "answer_id": {"0"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("edit: %d", res.Code)
	}
	draft, active, err := s.currentDraft(context.Background())
	if err != nil || len(diffMenuDraft(active, draft)) == 0 {
		t.Fatalf("component edit is invisible to publishing: err=%v changes=%v", err, diffMenuDraft(active, draft))
	}
	sources, _ := s.installationMenuSources(context.Background())
	old, err := BuildMenuFromDraft(active, testBootURL, store.profiles, store.answers, store.configs, sources)
	if err != nil || string(old) != original.Script {
		t.Fatalf("editing changed the published snapshot: %v", err)
	}
	if err := s.verifyMenuVersion(context.Background(), original); err != nil {
		t.Fatalf("old snapshot cannot be restored: %v", err)
	}
	// Deletion protection must use the published snapshot's answer, even when
	// the editable entry has already stopped referring to it.
	used, err := s.activeMenuContains(accountRequest(http.MethodGet, "/images", nil), installationComponent{answerID: 31})
	if err != nil || !used {
		t.Fatalf("published answer lost deletion protection: used=%v err=%v", used, err)
	}
}

type offlineDownloadImages struct{ ImagesAPI }

func (offlineDownloadImages) Catalog(context.Context) (images.Catalog, error) {
	return images.Catalog{}, errors.New("offline")
}

func TestDownloadEditWorksWhileImageDiscoveryIsOffline(t *testing.T) {
	s, store, _ := downloadsServer(t)
	if res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "title": {"Windows"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", res.Code)
	}
	s.Images = offlineDownloadImages{ImagesAPI: s.Images}
	body := libraryGet(t, s, "/installations/100/edit")
	if !strings.Contains(body, `value="Windows"`) {
		t.Fatal("offline source prevents editing")
	}
	res := menuPost(t, s, "/installations/100", url.Values{"boot_profile_id": {"51"}, "title": {"Renamed offline"}})
	if res.Code != http.StatusSeeOther || store.installations[0].Title != "Renamed offline" {
		t.Fatalf("status=%d entries=%+v", res.Code, store.installations)
	}
}

func TestDownloadReturnUsesSavedEditsAtPublishedPosition(t *testing.T) {
	s, store, _ := downloadsServer(t)
	for _, form := range []url.Values{{"boot_profile_id": {"51"}, "title": {"Windows"}, "answer_id": {"31"}}, {"boot_profile_id": {"51"}, "title": {"Second"}}} {
		if res := menuPost(t, s, "/installations", form); res.Code != http.StatusSeeOther {
			t.Fatalf("create: %d", res.Code)
		}
	}
	if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("publish: %d", res.Code)
	}
	if res := menuPost(t, s, "/menu/items/install_100/remove", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", res.Code)
	}
	if res := menuPost(t, s, "/installations/100", url.Values{"boot_profile_id": {"51"}, "title": {"Edited Windows"}, "folder": {"Lab"}, "answer_id": {"0"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("edit: %d", res.Code)
	}
	if res := menuPost(t, s, "/installations/100/add", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("return: %d", res.Code)
	}
	item := store.draft.Items[0]
	if item.InstallationID != 100 || item.Components == nil || item.Components.AnswerID != 0 || item.Title != "Edited Windows" || item.Folder != "Lab" {
		t.Fatalf("return discarded saved edit: %+v components=%+v", item, item.Components)
	}
	_, active, _ := s.currentDraft(context.Background())
	if len(diffMenuDraft(active, *store.draft)) == 0 {
		t.Fatal("saved edit vanished from pending changes")
	}
}

func TestDownloadCanRetryInterruptedPreparation(t *testing.T) {
	s, store, prep := downloadsServer(t)
	if res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:Windows-new.iso"}, "title": {"Interrupted"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", res.Code)
	}
	// The previous Control process exited after saving 'preparing'.
	if err := store.SetWindowsProfileResult(context.Background(), 80, "preparing", "", ""); err != nil {
		t.Fatal(err)
	}
	body := libraryGet(t, s, "/installations")
	if !strings.Contains(body, `action="/installations/100/prepare"`) {
		t.Fatal("interrupted preparation cannot be retried")
	}
	res := menuPost(t, s, "/installations/100/prepare", url.Values{})
	if res.Code != http.StatusSeeOther || prep.calls != 2 || store.profiles[len(store.profiles)-1].State != "ready" {
		t.Fatalf("retry status=%d calls=%d sources=%+v", res.Code, prep.calls, store.profiles)
	}
}
