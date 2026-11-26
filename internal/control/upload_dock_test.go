package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUploadDockScriptEndpointAndGuards(t *testing.T) {
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/upload-dock.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/upload-dock.js = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/javascript") {
		t.Fatalf("Content-Type = %q, want text/javascript", ct)
	}
	body := rec.Body.String()
	for _, needle := range []string{
		"createUploadDock",
		"formatUploadBytes",
		"beforeunload",
		"upload-dock",
		"setMinimized",
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("upload-dock.js missing %q", needle)
		}
	}
}

func TestImagesAndDriversPagesWireUploadDock(t *testing.T) {
	s := &Server{
		Store:  authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
		Images: &fakeImagesAPI{},
	}

	reqImg := httptest.NewRequest(http.MethodGet, "/images", nil)
	reqImg.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok"})
	recImg := httptest.NewRecorder()
	s.Handler().ServeHTTP(recImg, reqImg)
	if recImg.Code != http.StatusOK {
		t.Fatalf("GET /images = %d, want 200", recImg.Code)
	}
	imgBody := recImg.Body.String()
	dockIdx := strings.Index(imgBody, `/assets/upload-dock.js`)
	isoIdx := strings.Index(imgBody, `/assets/iso-upload.js`)
	if dockIdx < 0 || isoIdx < 0 || dockIdx > isoIdx {
		t.Fatalf("/images must load /assets/upload-dock.js before /assets/iso-upload.js (dock=%d iso=%d)", dockIdx, isoIdx)
	}
	if !strings.Contains(imgBody, `id="image-tabs"`) {
		t.Errorf("/images missing id=\"image-tabs\"")
	}
	if !strings.Contains(imgBody, `data-live-targets="#image-results,#image-filters,#image-tabs"`) {
		t.Errorf("/images missing #image-tabs in data-live-targets")
	}

	reqDrv := httptest.NewRequest(http.MethodGet, "/drivers", nil)
	reqDrv.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok"})
	recDrv := httptest.NewRecorder()
	s.Handler().ServeHTTP(recDrv, reqDrv)
	if recDrv.Code != http.StatusOK {
		t.Fatalf("GET /drivers = %d, want 200", recDrv.Code)
	}
	drvBody := recDrv.Body.String()
	drvDockIdx := strings.Index(drvBody, `/assets/upload-dock.js`)
	drvUploadIdx := strings.Index(drvBody, `/assets/driver-upload.js`)
	if drvDockIdx < 0 || drvUploadIdx < 0 || drvDockIdx > drvUploadIdx {
		t.Fatalf("/drivers must load /assets/upload-dock.js before /assets/driver-upload.js (dock=%d drv=%d)", drvDockIdx, drvUploadIdx)
	}
	if !strings.Contains(drvBody, `id="driver-upload-minimize"`) {
		t.Errorf("/drivers missing id=\"driver-upload-minimize\"")
	}
	if !strings.Contains(drvBody, `id="driver-packages-section"`) {
		t.Errorf("/drivers missing id=\"driver-packages-section\"")
	}
}

func TestUploadScriptsSupportMinimizeAndDock(t *testing.T) {
	isoBody := string(isoUploadJS)
	if strings.Contains(isoBody, `dialog.addEventListener('cancel'`) {
		t.Errorf("iso_upload.js must not block dialog cancel/Esc during upload")
	}
	for _, needle := range []string{"createUploadDock", "setMinimized", "omb:refresh", "Свернуть"} {
		if !strings.Contains(isoBody, needle) {
			t.Errorf("iso_upload.js missing %q", needle)
		}
	}

	drvBody := string(driverUploadJS)
	for _, needle := range []string{"createUploadDock", "setMinimized", "driver-upload-minimize", "refreshPackagesSection"} {
		if !strings.Contains(drvBody, needle) {
			t.Errorf("driver_upload.js missing %q", needle)
		}
	}
}
