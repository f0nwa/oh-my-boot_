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
			res := menuPost(t, s, "/images/ubuntu/inspect", url.Values{"storage_id": {"7"}, "path": {tc.path}})
			if res.Code != tc.status || store.creates != tc.creates {
				t.Fatalf("status=%d creates=%d body=%s", res.Code, store.creates, res.Body.String())
			}
			if tc.creates > 0 && (a.req.StorageRoot != "/mnt/omb-smb/key7" || len(store.states) != 1 || store.states[0] != "verified" || !strings.Contains(res.Header().Get("Location"), "boot_profile_id=61")) {
				t.Fatalf("request=%+v states=%v location=%s", a.req, store.states, res.Header().Get("Location"))
			}
		})
	}
}
