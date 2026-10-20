package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/images"
)

type answerPageStore struct {
	authOnlyStore
	profile WindowsProfile
	answers []WindowsAnswerVariant
	states  []string
}

func (s *answerPageStore) WindowsProfile(_ context.Context, id int64) (WindowsProfile, error) {
	if id != s.profile.ID {
		return WindowsProfile{}, os.ErrNotExist
	}
	return s.profile, nil
}
func (s *answerPageStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return []WindowsProfile{s.profile}, nil
}
func (s *answerPageStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	return s.answers, nil
}
func (s *answerPageStore) CreateWindowsAnswer(_ context.Context, v WindowsAnswerVariant) (WindowsAnswerVariant, error) {
	v.ID, v.State = 7, "pending"
	s.answers = append(s.answers, v)
	return v, nil
}
func (s *answerPageStore) SetWindowsAnswerState(_ context.Context, id int64, state, lastError string) error {
	s.states = append(s.states, state)
	for i := range s.answers {
		if s.answers[i].ID == id {
			s.answers[i].State, s.answers[i].Error = state, lastError
		}
	}
	return nil
}

type answerAgent struct {
	prepared agent.WindowsAnswerRequest
}

func (a *answerAgent) PrepareWindowsAnswer(_ context.Context, req agent.WindowsAnswerRequest) error {
	a.prepared = req
	return nil
}
func (a *answerAgent) CheckWindowsAnswer(context.Context, agent.WindowsAnswerCheck) error { return nil }

func answerUploadRequest(t *testing.T, body []byte, csrf string) *http.Request {
	t.Helper()
	var data bytes.Buffer
	w := multipart.NewWriter(&data)
	for key, value := range map[string]string{"csrf": csrf, "title": "Windows 11 Lab", "folder": "Windows/Tests"} {
		if err := w.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	f, err := w.CreateFormFile("file", "autounattend.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/images/windows-profiles/17/answers", &data)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	return req
}

func TestWindowsAnswerUploadCreatesReadyVariantWithoutEchoingXML(t *testing.T) {
	xml := []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"><settings pass="windowsPE"><Password>private-token</Password></settings></unattend>`)
	store := &answerPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, profile: WindowsProfile{ID: 17, State: "ready"}}
	agentAPI := &answerAgent{}
	s := &Server{Store: store, WindowsAnswers: agentAPI, Images: &fakeImagesAPI{catalog: images.Catalog{}}}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, answerUploadRequest(t, xml, CSRFToken("test-token")))
	sum := sha256.Sum256(xml)
	if res.Code != http.StatusSeeOther || len(store.answers) != 1 || store.answers[0].State != "ready" || store.answers[0].SHA256 != hex.EncodeToString(sum[:]) || string(agentAPI.prepared.XML) != string(xml) {
		t.Fatalf("upload status=%d answers=%+v agent=%+v body=%s", res.Code, store.answers, agentAPI.prepared, res.Body.String())
	}
	page := httptest.NewRequest(http.MethodGet, "/images", nil)
	page.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	pageResult := httptest.NewRecorder()
	s.Handler().ServeHTTP(pageResult, page)
	if pageResult.Code != http.StatusOK || !strings.Contains(pageResult.Body.String(), `action="/images/windows-profiles/17/answers"`) || !strings.Contains(pageResult.Body.String(), "Windows 11 Lab") || strings.Contains(pageResult.Body.String(), "private-token") {
		t.Fatalf("bad answer list page: %d %s", pageResult.Code, pageResult.Body.String())
	}
}

func TestWindowsAnswerUploadRejectsInvalidInputs(t *testing.T) {
	valid := []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"/>`)
	for _, tc := range []struct {
		name  string
		body  []byte
		csrf  string
		state string
		want  int
	}{
		{"csrf", valid, "wrong", "ready", http.StatusForbidden},
		{"unready parent", valid, CSRFToken("test-token"), "pending", http.StatusConflict},
		{"malformed XML", []byte(`<other/>`), CSRFToken("test-token"), "ready", http.StatusBadRequest},
		{"oversize XML", bytes.Repeat([]byte("x"), 1<<20+1), CSRFToken("test-token"), "ready", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &answerPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, profile: WindowsProfile{ID: 17, State: tc.state}}
			agentAPI := &answerAgent{}
			s := &Server{Store: store, WindowsAnswers: agentAPI}
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, answerUploadRequest(t, tc.body, tc.csrf))
			if res.Code != tc.want || len(store.answers) != 0 || agentAPI.prepared.ID != 0 {
				t.Fatalf("status=%d want=%d answers=%+v agent=%+v", res.Code, tc.want, store.answers, agentAPI.prepared)
			}
		})
	}
}

func TestWindowsAnswerUploadRejectsSameXMLForSameISO(t *testing.T) {
	xml := []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"><settings pass="windowsPE"/></unattend>`)
	sum := sha256.Sum256(xml)
	store := &answerPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, profile: WindowsProfile{ID: 17, State: "ready"},
		answers: []WindowsAnswerVariant{{ID: 5, ProfileID: 17, Title: "Windows 11 key no-disk test", SHA256: hex.EncodeToString(sum[:]), State: "ready"}}}
	agentAPI := &answerAgent{}
	s := &Server{Store: store, WindowsAnswers: agentAPI, Images: &fakeImagesAPI{catalog: images.Catalog{}}}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, answerUploadRequest(t, xml, CSRFToken("test-token")))
	body := res.Body.String()
	if res.Code != http.StatusConflict || len(store.answers) != 1 || agentAPI.prepared.ID != 0 {
		t.Fatalf("duplicate upload status=%d answers=%+v body=%s", res.Code, store.answers, body)
	}
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "#5") || !strings.Contains(body, "Windows 11 key no-disk test") {
		t.Fatalf("duplicate is not explained on the page: %s", body)
	}
	other := &answerPageStore{authOnlyStore: store.authOnlyStore, profile: WindowsProfile{ID: 17, State: "ready"},
		answers: []WindowsAnswerVariant{{ID: 5, ProfileID: 18, SHA256: hex.EncodeToString(sum[:]), State: "ready"}}}
	res = httptest.NewRecorder()
	(&Server{Store: other, WindowsAnswers: &answerAgent{}, Images: &fakeImagesAPI{catalog: images.Catalog{}}}).Handler().ServeHTTP(res, answerUploadRequest(t, xml, CSRFToken("test-token")))
	if res.Code != http.StatusSeeOther {
		t.Fatalf("same XML for another ISO rejected: %d %s", res.Code, res.Body.String())
	}
}
