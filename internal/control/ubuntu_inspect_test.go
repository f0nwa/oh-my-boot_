package control

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/autoinstall"
)

type ubuntuInspectStore struct {
	*newInstallStore
	creates int
	states  []string
}

func (s *ubuntuInspectStore) CreateUbuntuSource(_ context.Context, storage int64, path, digest, title string) (BootProfile, error) {
	s.creates++
	return BootProfile{ID: 61, Kind: BootProfileUbuntuISO, UbuntuSourceID: 8, UbuntuStorageID: storage, UbuntuPath: path, UbuntuSHA256: digest, UbuntuState: "unverified", Title: title}, nil
}
func (s *ubuntuInspectStore) SetUbuntuSourceState(_ context.Context, _ int64, state, _ string) error {
	s.states = append(s.states, state)
	return nil
}

type inspectUbuntuAgent struct {
	calls int
	req   agent.WindowsISORequest
	fail  bool
}

func (a *inspectUbuntuAgent) InspectUbuntuISO(_ context.Context, r agent.WindowsISORequest) (agent.UbuntuISOInspection, error) {
	a.calls++
	a.req = r
	if a.fail {
		return agent.UbuntuISOInspection{}, context.DeadlineExceeded
	}
	return agent.UbuntuISOInspection{SHA256: strings.Repeat("a", 64), Sources: []autoinstall.SourceOption{{ID: "ubuntu-desktop"}}}, nil
}
func (a *inspectUbuntuAgent) InspectISO(ctx context.Context, r agent.WindowsISORequest) (agent.ISOInspection, error) {
	result, err := a.InspectUbuntuISO(ctx, r)
	return agent.ISOInspection{Kind: "ubuntu", Ubuntu: result}, err
}
func (*inspectUbuntuAgent) PrepareUbuntuISO(context.Context, agent.UbuntuISORequest) error {
	return nil
}
func TestInspectUbuntuISORequiresPresentCatalogFileAndValidInspection(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		fail       bool
		status     int
		creates    int
	}{{"valid", "Windows-new.iso", false, http.StatusSeeOther, 1}, {"missing", "missing.iso", false, http.StatusBadRequest, 0}, {"invalid image", "Windows-new.iso", true, http.StatusUnprocessableEntity, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			s, base, _ := downloadsServer(t)
			store := &ubuntuInspectStore{newInstallStore: base.newInstallStore}
			s.Store = store
			a := &inspectUbuntuAgent{fail: tc.fail}
			s.UbuntuISO = a
			res := menuPost(t, s, "/images/iso/inspect", url.Values{"storage_id": {"7"}, "path": {tc.path}})
			if res.Code != tc.status || store.creates != tc.creates {
				t.Fatalf("status=%d creates=%d body=%s", res.Code, store.creates, res.Body.String())
			}
			if tc.creates > 0 && (a.req.StorageRoot != "/mnt/omb-smb/key7" || len(store.states) != 1 || store.states[0] != "verified" || !strings.Contains(res.Header().Get("Location"), "boot_profile_id=61")) {
				t.Fatalf("request=%+v states=%v location=%s", a.req, store.states, res.Header().Get("Location"))
			}
		})
	}
}

func TestUbuntuISOCreatedFromLibraryAndDownloadForm(t *testing.T) {
	s, _, _ := downloadsServer(t)
	catalog := s.Images.(*fakeImagesAPI)
	catalog.catalog.Images[1].Path = "ubuntu-desktop.iso"
	library := libraryGet(t, s, "/images")
	if !strings.Contains(library, `action="/images/iso/inspect"`) || !strings.Contains(library, "Создать загрузку") {
		t.Fatal("library lacks create action for Ubuntu ISO")
	}
	page := libraryGet(t, s, "/installations/new?storage_id=7&path=ubuntu-desktop.iso")
	if !strings.Contains(page, `action="/images/iso/inspect"`) || !strings.Contains(page, `name="path" value="ubuntu-desktop.iso"`) {
		t.Fatal("new download page cannot inspect its selected Ubuntu ISO")
	}
	if !strings.Contains(page, `data-ubuntu-inspect`) || !strings.Contains(library, `data-ubuntu-inspect`) {
		t.Fatal("inspection forms do not expose progress feedback")
	}
}

func TestMigratedUbuntuISOIsNotOfferedTwice(t *testing.T) {
	s, base, _ := downloadsServer(t)
	s.Images.(*fakeImagesAPI).catalog.Images[1].Path = "ubuntu-desktop.iso"
	base.bootProfiles = []BootProfile{{ID: 52, Kind: BootProfileUbuntuBuiltin, Title: "Ubuntu Desktop", UbuntuSourceID: 17, UbuntuStorageID: 7, UbuntuPath: "ubuntu-desktop.iso", UbuntuSHA256: strings.Repeat("a", 64), UbuntuState: "ready"}}
	choices, err := s.downloadSourceChoices(context.Background(), base.bootProfiles, base.profiles, true)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, choice := range choices {
		if choice.Path == "ubuntu-desktop.iso" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("migrated ISO appears %d times: %+v", count, choices)
	}
}

func TestAddDownloadExplainsItIsInDraftAndLinksToMenu(t *testing.T) {
	s, store, _ := downloadsServer(t)
	if res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "title": {"Windows"}}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	res := menuPost(t, s, "/installations/100/add", url.Values{})
	if res.Code != http.StatusSeeOther || store.draft == nil {
		t.Fatalf("status=%d", res.Code)
	}
	body := libraryGet(t, s, res.Header().Get("Location"))
	if !strings.Contains(body, "Добавлено в черновик меню") || !strings.Contains(body, `href="/menu"`) {
		t.Fatal("missing draft confirmation or menu link")
	}
}
