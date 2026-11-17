package control

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/autoinstall"
)

type ubuntuPrepareStore struct {
	*newInstallStore
	states []string
}

func TestCreateUbuntuDownloadKeepsItOutOfMenuAndPreparesSelectedISO(t *testing.T) {
	s, base, _ := downloadsServer(t)
	store := &ubuntuPrepareStore{newInstallStore: base.newInstallStore}
	digest := strings.Repeat("a", 64)
	store.bootProfiles = []BootProfile{{ID: 61, Kind: BootProfileUbuntuISO, Title: "Ubuntu Desktop", UbuntuSourceID: 8, UbuntuStorageID: 7, UbuntuPath: "Windows-new.iso", UbuntuSHA256: digest, UbuntuState: "verified"}}
	s.Store = store
	prep := &fakeUbuntuPreparer{}
	s.UbuntuISO = prep
	s.UbuntuSource = fakeUbuntuCatalog{digest: digest}
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"61"}, "title": {"Ubuntu Lab"}})
	if res.Code != http.StatusSeeOther || len(store.installations) != 1 || store.installations[0].Title != "Ubuntu Lab" || prep.calls != 1 || lastDraftKey(store.newInstallStore) != "" {
		t.Fatalf("status=%d installations=%+v calls=%d draft=%+v", res.Code, store.installations, prep.calls, store.draft)
	}
}

func (s *ubuntuPrepareStore) SetUbuntuSourceState(_ context.Context, _ int64, state, _ string) error {
	s.states = append(s.states, state)
	return nil
}

type fakeUbuntuPreparer struct {
	calls   int
	fail    bool
	request agent.UbuntuISORequest
}

func (p *fakeUbuntuPreparer) InspectUbuntuISO(context.Context, agent.WindowsISORequest) (agent.UbuntuISOInspection, error) {
	return agent.UbuntuISOInspection{}, nil
}
func (p *fakeUbuntuPreparer) PrepareUbuntuISO(_ context.Context, req agent.UbuntuISORequest) error {
	p.calls++
	p.request = req
	if p.fail {
		return errors.New("failed")
	}
	return nil
}
func (p *fakeUbuntuPreparer) RebindUbuntuISO(_ context.Context, _ agent.UbuntuISORequest) error {
	return nil
}

type fakeUbuntuCatalog struct {
	digest string
	fail   bool
}

func (c fakeUbuntuCatalog) UbuntuSource(context.Context) (autoinstall.SourceCatalog, error) {
	return autoinstall.SourceCatalog{}, errors.New("unused")
}
func (c fakeUbuntuCatalog) UbuntuSourceFor(context.Context, int64, string) (autoinstall.SourceCatalog, error) {
	if c.fail {
		return autoinstall.SourceCatalog{}, errors.New("unavailable")
	}
	return autoinstall.SourceCatalog{ISOSHA256: c.digest, Sources: []autoinstall.SourceOption{{ID: "ubuntu-desktop"}}}, nil
}

func TestUbuntuDownloadPreparationRequiresVerifiedBootCatalog(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "catalog unavailable"}[fail], func(t *testing.T) {
			s, base, _ := downloadsServer(t)
			store := &ubuntuPrepareStore{newInstallStore: base.newInstallStore}
			digest := strings.Repeat("a", 64)
			store.bootProfiles = []BootProfile{{ID: 61, Kind: BootProfileUbuntuISO, UbuntuSourceID: 8, UbuntuStorageID: 7, UbuntuPath: "Windows-new.iso", UbuntuSHA256: digest, UbuntuState: "verified"}}
			s.Store = store
			prep := &fakeUbuntuPreparer{}
			s.UbuntuISO = prep
			s.UbuntuSource = fakeUbuntuCatalog{digest: digest, fail: fail}
			err := s.prepareDownload(context.Background(), Installation{BootProfileID: 61}, 1)
			if (err != nil) != fail || prep.calls != 1 || prep.request.ID != 8 || prep.request.RelativePath != "Windows-new.iso" || prep.request.SHA256 != digest {
				t.Fatalf("err=%v calls=%d request=%+v", err, prep.calls, prep.request)
			}
			want := "ready"
			if fail {
				want = "error"
			}
			if len(store.states) != 2 || store.states[0] != "preparing" || store.states[1] != want {
				t.Fatalf("states=%v", store.states)
			}
		})
	}
}
