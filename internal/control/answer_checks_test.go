package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type answerChecksHTTPStore struct {
	Store
	user      User
	readError bool
}

func (s answerChecksHTTPStore) Session(context.Context, string) (User, error) { return s.user, nil }
func (s answerChecksHTTPStore) LatestAnswerChecks(ctx context.Context) ([]AnswerCheck, error) {
	if s.readError {
		return nil, errors.New("offline")
	}
	return s.Store.LatestAnswerChecks(ctx)
}

func TestAnswerChecksHTTP(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	user, win, ubuntu := answerCheckFixtures(t, store)
	backend := &answerChecksHTTPStore{Store: store, user: User{ID: user, Username: "checker", Role: "operator", Enabled: true}}
	s := &Server{Store: backend}
	path := fmt.Sprintf("/answers/windows/%d/checks", win.ID)
	form := url.Values{"sha256": {win.SHA256}, "result": {"success"}, "machine": {"VM 113"}, "comment": {"<img src=x onerror=alert(1)>"}, "author": {"forged"}}
	res := menuPost(t, s, path, form)
	if res.Code != http.StatusOK {
		t.Fatalf("record: %d %s", res.Code, res.Body.String())
	}
	for range 23 {
		if err := store.SaveAnswerCheck(ctx, AnswerCheck{Kind: "windows", AnswerID: win.ID, SHA256: win.SHA256, Result: "failure", UserID: user}); err != nil {
			t.Fatal(err)
		}
	}
	get := func(p string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		s.Handler().ServeHTTP(r, accountRequest("GET", p, nil))
		return r
	}
	page := get(path)
	var history struct {
		Checks []AnswerCheck `json:"checks"`
		Next   int64         `json:"next"`
		SHA256 string        `json:"sha256"`
	}
	if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &history) != nil || len(history.Checks) != 20 || history.Next <= 0 || history.SHA256 != win.SHA256 {
		t.Fatalf("history: %d %s", page.Code, page.Body.String())
	}
	page = get(fmt.Sprintf("%s?before=%d", path, history.Next))
	history.Checks = nil
	if json.Unmarshal(page.Body.Bytes(), &history) != nil || len(history.Checks) != 4 || history.Checks[3].Author != "checker" {
		t.Fatalf("older: %s", page.Body.String())
	}
	maximum := url.Values{"sha256": {win.SHA256}, "result": {"success"}, "machine": {strings.Repeat("😀", 200)}, "comment": {strings.Repeat("界", 2000)}}
	if got := menuPost(t, s, path, maximum); got.Code != http.StatusOK {
		t.Fatalf("valid Unicode maximum: %d %s", got.Code, got.Body.String())
	}
	if got := menuPost(t, s, path, url.Values{"sha256": {win.SHA256}, "result": {"success"}, "comment": {strings.Repeat("x", 40000)}}); got.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized form: %d %s", got.Code, got.Body.String())
	}
	for _, tc := range []struct {
		key, value string
		status     int
	}{{"result", "invalid", 400}, {"sha256", strings.Repeat("b", 64), 409}, {"machine", strings.Repeat("x", 201), 400}, {"comment", strings.Repeat("я", 2001), 400}} {
		f := url.Values{"sha256": {win.SHA256}, "result": {"success"}}
		f.Set(tc.key, tc.value)
		if got := menuPost(t, s, path, f); got.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.key, got.Code, got.Body.String())
		}
	}
	for _, p := range []string{"/answers/invalid/1/checks", "/answers/windows/no/checks", "/answers/windows/-1/checks"} {
		if got := get(p); got.Code != 400 {
			t.Fatalf("bad path %s: %d", p, got.Code)
		}
	}
	if got := get("/answers/windows/99999/checks"); got.Code != 404 {
		t.Fatalf("missing: %d", got.Code)
	}
	if got := get(path + "?before=bad"); got.Code != 400 {
		t.Fatalf("cursor: %d", got.Code)
	}
	request := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "omb_session", Value: "different-token"})
	noCSRF := httptest.NewRecorder()
	s.Handler().ServeHTTP(noCSRF, request)
	if noCSRF.Code != 403 {
		t.Fatalf("CSRF: %d", noCSRF.Code)
	}
	backend.user.Role = "observer"
	if got := menuPost(t, s, path, form); got.Code != 403 {
		t.Fatalf("observer: %d", got.Code)
	}
	if got := get(path); got.Code != 200 {
		t.Fatalf("observer history: %d", got.Code)
	}
	backend.user.Role = "operator"
	if got := menuPost(t, s, fmt.Sprintf("/answers/ubuntu/%d/checks", ubuntu.ID), url.Values{"sha256": {ubuntu.SHA256}, "result": {"success"}}); got.Code != 200 {
		t.Fatalf("ubuntu: %d %s", got.Code, got.Body.String())
	}
	backend.readError = true
	body := libraryGet(t, s, "/answers")
	if !strings.Contains(body, "Сведения недоступны") {
		t.Fatal("read error shown as untested")
	}
}

func TestAnswerChecksPageActions(t *testing.T) {
	for _, role := range []string{"admin", "operator", "observer"} {
		data := samplePageData(langRU)
		data.Info.User.Role = role
		data.AnswerChecksKnown = true
		data.UbuntuAutoinstalls = []UbuntuAutoinstallSummary{{ID: 41, Title: "Ubuntu", SHA256: strings.Repeat("b", 64), State: "validated"}}
		body := renderPage("answers", data)
		if strings.Contains(body, "Установка ОС не проверялась") {
			t.Fatal("obsolete permanent claim")
		}
		if strings.Count(body, `>Проверка установки</th>`) != 2 {
			t.Fatal("missing shared check column")
		}
		for _, want := range []string{`id="answer-check-dialog"`, `data-answer-history`, `data-check-kind="windows"`, `data-check-kind="ubuntu"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing %s", role, want)
			}
		}
		if strings.Contains(body, `data-answer-check-open`) != (role != "observer") {
			t.Fatalf("wrong edit permissions: %s", role)
		}
	}
}
