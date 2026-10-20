package control

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pxe-platform/internal/images"
)

func libraryGet(t *testing.T, s *Server, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("%s: status %d", path, res.Code)
	}
	return res.Body.String()
}

func libraryServer() *Server {
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 5, Name: "DSM", Kind: "smb", State: "ready"}}}}
	return &Server{Store: authOnlyStore{user: User{ID: 1, Role: "admin", Enabled: true}}, Images: api}
}

func TestImagesPageNoLongerShowsStoragesOrAnswerForms(t *testing.T) {
	body := libraryGet(t, libraryServer(), "/images")
	if strings.Contains(body, `action="/images/smb"`) || strings.Contains(body, "autounattend") || !strings.Contains(body, "iso-upload-form") {
		t.Fatalf("images page: %s", body)
	}
}

func TestStoragesPageShowsSMBAndRecovery(t *testing.T) {
	body := libraryGet(t, libraryServer(), "/storages")
	if !strings.Contains(body, `action="/images/smb"`) || !strings.Contains(body, "DSM") || !strings.Contains(body, "Автоматическое восстановление подключений") {
		t.Fatalf("storages page: %s", body)
	}
}

func TestSMBErrorRendersOnStoragesPage(t *testing.T) {
	s := libraryServer()
	form := url.Values{"csrf": {CSRFToken("test-token")}}
	req := httptest.NewRequest(http.MethodPost, "/images/smb", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	body := res.Body.String()
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, `action="/images/smb"`) || strings.Contains(body, "iso-upload-form") {
		t.Fatalf("status=%d body=%s", res.Code, body)
	}
}

func TestAnswerUploadFromAnswersPage(t *testing.T) {
	store := &answerPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, profile: WindowsProfile{ID: 3, State: "ready"}}
	s := &Server{Store: store, WindowsAnswers: &answerAgent{}, Images: &fakeImagesAPI{}}
	var data bytes.Buffer
	w := multipart.NewWriter(&data)
	for key, value := range map[string]string{"csrf": CSRFToken("test-token"), "profile_id": "3", "title": "Win11 key", "folder": ""} {
		_ = w.WriteField(key, value)
	}
	f, _ := w.CreateFormFile("file", "autounattend.xml")
	_, _ = f.Write([]byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"/>`))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/answers/upload", &data)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/answers?message=") || len(store.answers) != 1 || store.answers[0].ProfileID != 3 {
		t.Fatalf("status=%d location=%q answers=%+v body=%s", res.Code, res.Header().Get("Location"), store.answers, res.Body.String())
	}
	page := libraryGet(t, s, "/answers")
	if !strings.Contains(page, "Win11 key") || !strings.Contains(page, `action="/answers/upload"`) {
		t.Fatalf("answers page: %s", page)
	}
}
