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
	kind    string
	version string
	arch    string
	summary string
	err     error
}

func (a *genericISOAgent) InspectISO(ctx context.Context, r agent.WindowsISORequest) (agent.ISOInspection, error) {
	if a.err != nil {
		return agent.ISOInspection{}, a.err
	}
	u, _ := a.InspectUbuntuISO(ctx, r)
	return agent.ISOInspection{Kind: a.kind, Version: a.version, Arch: a.arch, Summary: a.summary, Ubuntu: u}, nil
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

func TestInspectISOModeInspectStaysOnImagesAndStoresMetadata(t *testing.T) {
	s, base, _ := downloadsServer(t)
	store := &ubuntuInspectStore{newInstallStore: base.newInstallStore}
	s.Store = store
	s.UbuntuISO = &genericISOAgent{
		kind:    "windows",
		version: "10.0.26100",
		arch:    "x64",
		summary: "Windows 11 Pro · install.wim",
	}
	res := menuPost(t, s, "/images/iso/inspect", url.Values{
		"storage_id": {"7"},
		"path":       {"Windows-new.iso"},
		"mode":       {"inspect"},
		"return_to":  {"/images?show=free"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	loc := res.Header().Get("Location")
	if !strings.HasPrefix(loc, "/images?show=free") || strings.Contains(loc, "/installations/new") {
		t.Fatalf("expected redirect back to /images?show=free, got %q", loc)
	}
	page := libraryGet(t, s, "/images")
	if !strings.Contains(page, "10.0.26100 · x64") || !strings.Contains(page, "Windows 11 Pro · install.wim") {
		t.Fatalf("expected inspected version and summary on /images page")
	}
}
