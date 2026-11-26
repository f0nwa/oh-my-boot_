package control

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"omb/internal/autoinstall"
)

type ubuntuUploadTestStore struct {
	authOnlyStore
	files  []UbuntuAutoinstallFile
	audits []string
}

func (s *ubuntuUploadTestStore) CreateUbuntuAutoinstall(_ context.Context, file UbuntuAutoinstallFile) (UbuntuAutoinstallFile, error) {
	for _, existing := range s.files {
		if existing.SHA256 == file.SHA256 {
			return UbuntuAutoinstallFile{}, &pgconn.PgError{Code: "23505"}
		}
	}
	file.ID = int64(len(s.files) + 1)
	file.CreatedAt = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	s.files = append(s.files, file)
	return file, nil
}

func (s *ubuntuUploadTestStore) UbuntuAutoinstalls(context.Context) ([]UbuntuAutoinstallSummary, error) {
	out := make([]UbuntuAutoinstallSummary, 0, len(s.files))
	for _, f := range s.files {
		out = append(out, UbuntuAutoinstallSummary{
			ID:         f.ID,
			Title:      f.Title,
			SourceKind: f.SourceKind,
			SHA256:     f.SHA256,
			State:      f.State,
			DiskMode:   f.DiskMode,
			DiskSerial: f.DiskSerial,
			CreatedAt:  f.CreatedAt,
		})
	}
	return out, nil
}

func (s *ubuntuUploadTestStore) UbuntuAutoinstall(_ context.Context, id int64) (UbuntuAutoinstallFile, error) {
	for _, f := range s.files {
		if f.ID == id {
			return f, nil
		}
	}
	return UbuntuAutoinstallFile{}, os.ErrNotExist
}

func (s *ubuntuUploadTestStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

func ubuntuUploadRequest(t *testing.T, title, filename string, body []byte, csrf string) *http.Request {
	t.Helper()
	var data bytes.Buffer
	w := multipart.NewWriter(&data)
	_ = w.WriteField("csrf", csrf)
	_ = w.WriteField("title", title)
	if filename != "" {
		f, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/ubuntu-autoinstall/upload", &data)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	return req
}

func TestUbuntuAutoinstallUploadFlowAndReadOnlyView(t *testing.T) {
	store := &ubuntuUploadTestStore{authOnlyStore: authOnlyStore{user: User{ID: 5, Username: "ops", Role: "operator", Enabled: true}}}
	srv := &Server{Store: store}
	h := srv.Handler()
	csrf := CSRFToken("test-token")

	yamlBody := []byte("#cloud-config\nautoinstall:\n  version: 1\n  locale: en_US.UTF-8\n  storage:\n    config:\n      - type: disk\n        id: disk-0\n")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, ubuntuUploadRequest(t, "Ubuntu 24.04 Server LVM", "user-data", yamlBody, csrf))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(store.files) != 1 {
		t.Fatalf("stored files = %d, want 1", len(store.files))
	}
	saved := store.files[0]
	if saved.SourceKind != "uploaded" || saved.DiskMode != "custom" || saved.Title != "Ubuntu 24.04 Server LVM" {
		t.Fatalf("saved = %+v", saved)
	}
	if len(store.audits) == 0 || store.audits[len(store.audits)-1] != "ubuntu-autoinstall.upload|ok|1" {
		t.Fatalf("audits = %v", store.audits)
	}

	// Duplicate upload returns 409 rendered via withPageErrors
	dupRec := httptest.NewRecorder()
	h.ServeHTTP(dupRec, ubuntuUploadRequest(t, "Duplicate", "user-data", yamlBody, csrf))
	if dupRec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", dupRec.Code)
	}

	// /answers lists the uploaded script with Ubuntu · YAML chip and View YAML action (no Copy)
	listReq := httptest.NewRequest(http.MethodGet, "/answers", nil)
	listReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("answers page status = %d", listRec.Code)
	}
	listHTML := listRec.Body.String()
	for _, want := range []string{`data-ubuntu-upload-open`, `id="ubuntu-upload-dialog"`, `Ubuntu · YAML`, `Просмотреть YAML`, `Своя разметка (YAML)`} {
		if !strings.Contains(listHTML, want) {
			t.Fatalf("answers page missing %q", want)
		}
	}
	if strings.Contains(listHTML, `/ubuntu-autoinstall/1/copy`) {
		t.Fatal("uploaded script should not expose /copy link")
	}

	// /ubuntu-autoinstall/1 renders read-only view without Copy button
	viewReq := httptest.NewRequest(http.MethodGet, "/ubuntu-autoinstall/1", nil)
	viewReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	viewRec := httptest.NewRecorder()
	h.ServeHTTP(viewRec, viewReq)
	if viewRec.Code != http.StatusOK {
		t.Fatalf("view status = %d", viewRec.Code)
	}
	viewHTML := viewRec.Body.String()
	for _, want := range []string{`Загружен из файла`, `Копировать YAML`, `Скачать user-data`, `srv`[:0], `id="autoinstall-yaml"`} {
		if want != "" && !strings.Contains(viewHTML, want) {
			t.Fatalf("view page missing %q", want)
		}
	}
	if strings.Contains(viewHTML, `/ubuntu-autoinstall/1/copy`) {
		t.Fatal("uploaded view should not contain /copy link")
	}

	// Direct GET /ubuntu-autoinstall/1/copy returns 404
	copyReq := httptest.NewRequest(http.MethodGet, "/ubuntu-autoinstall/1/copy", nil)
	copyReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	copyRec := httptest.NewRecorder()
	h.ServeHTTP(copyRec, copyReq)
	if copyRec.Code != http.StatusNotFound {
		t.Fatalf("copy status = %d, want 404", copyRec.Code)
	}
}

func TestUbuntuAutoinstallUploadRejectsInvalidAndObserver(t *testing.T) {
	store := &ubuntuUploadTestStore{authOnlyStore: authOnlyStore{user: User{ID: 6, Username: "ops", Role: "operator", Enabled: true}}}
	srv := &Server{Store: store}
	h := srv.Handler()
	csrf := CSRFToken("test-token")

	cases := []struct {
		name     string
		title    string
		body     []byte
		wantCode int
	}{
		{"missing header", "Bad", []byte("autoinstall:\n  version: 1\n"), http.StatusBadRequest},
		{"wrong version", "Bad", []byte("#cloud-config\nautoinstall:\n  version: 2\n"), http.StatusBadRequest},
		{"too large", "Big", bytes.Repeat([]byte("a"), autoinstall.MaxUploadBytes+10), http.StatusRequestEntityTooLarge},
		{"empty title", "   ", []byte("#cloud-config\nautoinstall:\n  version: 1\n"), http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, ubuntuUploadRequest(t, tc.title, "user-data.yaml", tc.body, csrf))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}

	// Observer cannot upload or see upload dialog
	obsStore := &ubuntuUploadTestStore{authOnlyStore: authOnlyStore{user: User{ID: 7, Username: "obs", Role: "observer", Enabled: true}}}
	obsSrv := &Server{Store: obsStore}
	obsH := obsSrv.Handler()
	rec := httptest.NewRecorder()
	obsH.ServeHTTP(rec, ubuntuUploadRequest(t, "Valid", "user-data", []byte("#cloud-config\nautoinstall:\n  version: 1\n"), csrf))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("observer upload status = %d, want 403", rec.Code)
	}
	listReq := httptest.NewRequest(http.MethodGet, "/answers", nil)
	listReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	listRec := httptest.NewRecorder()
	obsH.ServeHTTP(listRec, listReq)
	if strings.Contains(listRec.Body.String(), `id="ubuntu-upload-dialog"`) {
		t.Fatal("observer should not see ubuntu upload dialog")
	}
}
