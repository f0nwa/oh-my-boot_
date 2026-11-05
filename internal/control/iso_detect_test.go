package control

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"omb/internal/agent"
)

type genericISOAgent struct {
	inspectUbuntuAgent
	kind string
	err  error
}

func (a *genericISOAgent) InspectISO(ctx context.Context, r agent.WindowsISORequest) (agent.ISOInspection, error) {
	if a.err != nil {
		return agent.ISOInspection{}, a.err
	}
	u, _ := a.InspectUbuntuISO(ctx, r)
	return agent.ISOInspection{Kind: a.kind, Ubuntu: u}, nil
}

func TestLibraryCreatesDownloadByContent(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		err        error
		status     int
		location   string
		creates    int
	}{
		{"renamed Ubuntu", "ubuntu", nil, http.StatusSeeOther, "boot_profile_id=61", 1},
		{"Windows", "windows", nil, http.StatusSeeOther, "path=Windows-new.iso", 0},
		{"unavailable", "", context.DeadlineExceeded, http.StatusUnprocessableEntity, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, base, _ := downloadsServer(t)
			store := &ubuntuInspectStore{newInstallStore: base.newInstallStore}
			s.Store = store
			s.UbuntuISO = &genericISOAgent{kind: tc.kind, err: tc.err}
			page := libraryGet(t, s, "/images")
			if strings.Contains(page, "Проверить для Ubuntu") || !strings.Contains(page, `action="/images/iso/inspect"`) {
				t.Fatal("ISO list should offer a single generic action")
			}
			res := menuPost(t, s, "/images/iso/inspect", url.Values{"storage_id": {"7"}, "path": {"Windows-new.iso"}})
			if res.Code != tc.status || !strings.Contains(res.Header().Get("Location"), tc.location) || store.creates != tc.creates {
				t.Fatalf("status=%d location=%s creates=%d body=%s", res.Code, res.Header().Get("Location"), store.creates, res.Body.String())
			}
		})
	}
}
