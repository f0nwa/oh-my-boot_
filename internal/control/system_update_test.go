package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omb/internal/update"
)

type updateRoundTrip func(*http.Request) (*http.Response, error)

func (fn updateRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

type unreadUpload struct{ t *testing.T }

func (r unreadUpload) Read([]byte) (int, error) {
	r.t.Fatal("upload body read before authorization or size validation")
	return 0, io.EOF
}

func TestSystemUpdateUploadProtectsStream(t *testing.T) {
	for _, tc := range []struct {
		role, csrf string
		length     int64
		want       int
	}{
		{"operator", CSRFToken("test-token"), 8, 403},
		{"observer", CSRFToken("test-token"), 8, 403},
		{"admin", "", 8, 403},
		{"admin", CSRFToken("test-token"), update.MaxArchiveBytes + 1, 413},
	} {
		s := &Server{Store: authOnlyStore{user: User{ID: 1, Username: tc.role, Role: tc.role, Enabled: true}}}
		r := httptest.NewRequest("POST", "/system/update/upload", unreadUpload{t})
		r.ContentLength = tc.length
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-CSRF-Token", tc.csrf)
		r.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: %d, want %d: %s", tc.role, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestSystemUpdateRoutesAndDialog(t *testing.T) {
	var calls []string
	client := &http.Client{Transport: updateRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == "POST" && r.URL.Path == "/upload" {
			data, err := io.ReadAll(r.Body)
			if err != nil || string(data) != "archive-data" {
				t.Fatalf("body = %q, %v", data, err)
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"state":"ready","id":"example","release":{"current":"0.1.0","target":"0.2.0","channel":"stable"}}`))}, nil
	})}
	s := &Server{Store: authOnlyStore{user: User{ID: 1, Username: "test", Role: "admin", Enabled: true, Language: langEN}}, updateClient: client}
	page := libraryGet(t, s, "/system")
	for _, marker := range []string{`id="omb-update-dialog"`, `id="omb-update-upload" disabled`, "Upload and verify", "Offline update"} {
		if !strings.Contains(page, marker) {
			t.Errorf("system page missing %s", marker)
		}
	}
	r := httptest.NewRequest("POST", "/system/update/upload", strings.NewReader("archive-data"))
	r.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	r.Header.Set("X-CSRF-Token", CSRFToken("test-token"))
	r.Header.Set("Content-Type", "application/gzip")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"target":"0.2.0"`) {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.Join(calls, ","), "POST /upload") {
		t.Fatal(calls)
	}
}
