package agent

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"pxe-platform/internal/ueficausb"
	"pxe-platform/internal/ueficausb/ueficausbtest"
)

func ueficaHandler(t *testing.T) http.Handler {
	t.Helper()
	inner := http.NewServeMux()
	inner.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "inner") })
	return HandlerWithUEFICAUSB(inner, ueficausbtest.NewBuilder(t, "17"))
}

func ueficaBuild(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/uefica-usb/build", strings.NewReader(body)))
	return rec
}

func TestUEFICAUSBBuildAndFile(t *testing.T) {
	h := ueficaHandler(t)
	rec := ueficaBuild(t, h, `{"profile_id":17,"loader":"2011"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("build: %d %s", rec.Code, rec.Body)
	}
	var result ueficausb.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || !strings.HasPrefix(result.Key, "17-2011-") {
		t.Fatalf("result %s: %v", rec.Body, err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/uefica-usb/file/"+result.Key, nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" || rec.Header().Get("Content-Length") != strconv.FormatInt(result.Size, 10) {
		t.Fatalf("file: %d %v", rec.Code, rec.Header())
	}
	if _, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len())); err != nil {
		t.Fatal(err)
	}
}

func TestUEFICAUSBBuildErrors(t *testing.T) {
	h := ueficaHandler(t)
	for body, code := range map[string]int{
		`{"profile_id":17,"loader":"x"}`:                                              http.StatusBadRequest,
		`{"profile_id":99,"loader":"2011"}`:                                           http.StatusNotFound,
		`{"profile_id":17,"loader":"2011","pad":"` + strings.Repeat("a", 2048) + `"}`: http.StatusBadRequest,
		`{"profile_id":17,"loader":"2011","extra":1}`:                                 http.StatusBadRequest,
	} {
		if rec := ueficaBuild(t, h, body); rec.Code != code {
			t.Errorf("%.60s: %d, want %d", body, rec.Code, code)
		}
	}
}

func TestUEFICAUSBFileErrors(t *testing.T) {
	h := ueficaHandler(t)
	for path, code := range map[string]int{
		"/uefica-usb/file/..%2Fetc":                 http.StatusBadRequest,
		"/uefica-usb/file/17-2011-0123456789abcdef": http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != code {
			t.Errorf("%s: %d, want %d", path, rec.Code, code)
		}
	}
}

func TestUEFICAUSBPassesOtherRoutes(t *testing.T) {
	rec := httptest.NewRecorder()
	ueficaHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rec.Body.String() != "inner" {
		t.Fatalf("got %q", rec.Body)
	}
}

func TestUEFICAUSBArchives(t *testing.T) {
	h := ueficaHandler(t)
	list := func() string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/uefica-usb/archives", nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("archives: %d %v", rec.Code, rec.Header())
		}
		return strings.TrimSpace(rec.Body.String())
	}
	if got := list(); got != "[]" {
		t.Fatalf("empty: %s", got)
	}
	if rec := ueficaBuild(t, h, `{"profile_id":17,"loader":"2011"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if got := list(); !strings.HasPrefix(got, `[{"profile_id":17,"loader":"2011","size":`) {
		t.Fatalf("after build: %s", got)
	}
}
