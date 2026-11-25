package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type answerRenameStore struct {
	authOnlyStore
	windows map[int64]WindowsAnswerVariant
	ubuntu  map[int64]UbuntuAutoinstallSummary
	audits  []string
}

func (s *answerRenameStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	out := make([]WindowsAnswerVariant, 0, len(s.windows))
	for _, v := range s.windows {
		out = append(out, v)
	}
	return out, nil
}

func (s *answerRenameStore) UbuntuAutoinstalls(context.Context) ([]UbuntuAutoinstallSummary, error) {
	out := make([]UbuntuAutoinstallSummary, 0, len(s.ubuntu))
	for _, v := range s.ubuntu {
		out = append(out, v)
	}
	return out, nil
}

func (s *answerRenameStore) SetWindowsAnswerTitle(_ context.Context, id int64, title string) error {
	v, ok := s.windows[id]
	if !ok {
		return pgx.ErrNoRows
	}
	v.Title = title
	s.windows[id] = v
	return nil
}

func (s *answerRenameStore) SetUbuntuAutoinstallTitle(_ context.Context, id int64, title string) error {
	v, ok := s.ubuntu[id]
	if !ok {
		return pgx.ErrNoRows
	}
	v.Title = title
	s.ubuntu[id] = v
	return nil
}

func (s *answerRenameStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

func newAnswerRenameServer(role string, store *answerRenameStore) (*Server, string, string) {
	store.user = User{ID: 7, Username: role, Role: role, Language: "ru"}
	token := "tok-" + role
	s := &Server{Store: store}
	return s, token, CSRFToken(token)
}

func TestSafeAnswersReturnURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "/answers"},
		{"/answers", "/answers"},
		{"/answers?q=win&sort=title&dir=desc&message=old", "/answers?dir=desc&q=win&sort=title"},
		{"/images?q=win", "/answers"},
		{"//evil.example/answers", "/answers"},
		{"https://evil.example/answers", "/answers"},
	}
	for _, tc := range cases {
		if got := safeAnswersReturnURL(tc.in); got != tc.want {
			t.Errorf("safeAnswersReturnURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAnswerRenameWindowsAndUbuntu(t *testing.T) {
	sha := strings.Repeat("a", 64)
	store := &answerRenameStore{
		windows: map[int64]WindowsAnswerVariant{
			4: {ID: 4, Title: "Win 11 Lab", SHA256: sha, State: "ready"},
		},
		ubuntu: map[int64]UbuntuAutoinstallSummary{
			2: {ID: 2, Title: "Ubuntu 24.04", SHA256: sha, State: "validated"},
		},
	}

	post := func(s *Server, token, csrf, path string, form url.Values) *httptest.ResponseRecorder {
		form.Set("csrf", csrf)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: token})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)
		return res
	}

	// Operator can rename Windows script to Russian UTF-8 and preserve filters.
	sOp, tokOp, csrfOp := newAnswerRenameServer("operator", store)
	res := post(sOp, tokOp, csrfOp, "/answers/windows/4/rename", url.Values{
		"title":     {"Офис Windows 11 № 1"},
		"return_to": {"/answers?q=win&sort=title&dir=asc&message=stale"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("windows rename: code=%d body=%s", res.Code, res.Body.String())
	}
	loc := res.Header().Get("Location")
	if !strings.HasPrefix(loc, "/answers?dir=asc&q=win&sort=title&message=") {
		t.Fatalf("unexpected redirect location: %q", loc)
	}
	if store.windows[4].Title != "Офис Windows 11 № 1" {
		t.Fatalf("windows title not updated: %q", store.windows[4].Title)
	}

	// Admin can rename Ubuntu script.
	sAdm, tokAdm, csrfAdm := newAnswerRenameServer("admin", store)
	res = post(sAdm, tokAdm, csrfAdm, "/answers/ubuntu/2/rename", url.Values{
		"title": {"Ubuntu 24.04 Офис"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("ubuntu rename: code=%d body=%s", res.Code, res.Body.String())
	}
	if store.ubuntu[2].Title != "Ubuntu 24.04 Офис" {
		t.Fatalf("ubuntu title not updated: %q", store.ubuntu[2].Title)
	}

	audits := strings.Join(store.audits, "\n")
	if !strings.Contains(audits, "windows-answer.rename|ok|id=4 title=Офис Windows 11 № 1") {
		t.Fatalf("missing windows audit: %v", store.audits)
	}
	if !strings.Contains(audits, "ubuntu-autoinstall.rename|ok|id=2 title=Ubuntu 24.04 Офис") {
		t.Fatalf("missing ubuntu audit: %v", store.audits)
	}

	// Validation errors and missing IDs.
	for _, tc := range []struct {
		path  string
		title string
		want  int
	}{
		{"/answers/windows/4/rename", "   ", http.StatusBadRequest},
		{"/answers/windows/4/rename", strings.Repeat("а", 81), http.StatusBadRequest},
		{"/answers/ubuntu/2/rename", "bad\nname", http.StatusBadRequest},
		{"/answers/other/2/rename", "Valid", http.StatusNotFound},
		{"/answers/windows/999/rename", "Valid", http.StatusNotFound},
		{"/answers/ubuntu/999/rename", "Valid", http.StatusNotFound},
	} {
		got := post(sAdm, tokAdm, csrfAdm, tc.path, url.Values{"title": {tc.title}})
		if got.Code != tc.want {
			t.Errorf("POST %s title=%q: got %d, want %d", tc.path, tc.title, got.Code, tc.want)
		}
	}

	// Observer is forbidden.
	sObs, tokObs, csrfObs := newAnswerRenameServer("observer", store)
	gotObs := post(sObs, tokObs, csrfObs, "/answers/windows/4/rename", url.Values{"title": {"Nope"}})
	if gotObs.Code != http.StatusForbidden {
		t.Fatalf("observer rename got %d, want 403", gotObs.Code)
	}
}

func TestAnswersPageRendersRenameControlsForNonObserversOnly(t *testing.T) {
	sha := strings.Repeat("b", 64)
	store := &answerRenameStore{
		windows: map[int64]WindowsAnswerVariant{
			4: {ID: 4, Title: "Win 11", SHA256: sha, State: "ready"},
		},
		ubuntu: map[int64]UbuntuAutoinstallSummary{
			2: {ID: 2, Title: "Ubuntu 24.04", SHA256: sha, State: "validated"},
		},
	}

	for _, role := range []string{"admin", "operator", "observer"} {
		s, tok, _ := newAnswerRenameServer(role, store)
		req := httptest.NewRequest(http.MethodGet, "/answers", nil)
		req.AddCookie(&http.Cookie{Name: "omb_session", Value: tok})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET /answers (%s): code=%d", role, res.Code)
		}
		body := res.Body.String()
		wantControls := role != "observer"
		if got := strings.Contains(body, `data-rename-kind="windows"`); got != wantControls {
			t.Errorf("%s: windows rename button present=%v, want %v", role, got, wantControls)
		}
		if got := strings.Contains(body, `data-rename-kind="ubuntu"`); got != wantControls {
			t.Errorf("%s: ubuntu rename button present=%v, want %v", role, got, wantControls)
		}
		if got := strings.Contains(body, `id="answer-rename-dialog"`); got != wantControls {
			t.Errorf("%s: rename dialog present=%v, want %v", role, got, wantControls)
		}
	}
}
