package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/autoinstall"
	"omb/internal/unattend"
)

type editSaveTestStore struct {
	authOnlyStore
	winAnswers []WindowsAnswerVariant
	ubuntu     []UbuntuAutoinstallFile
	audits     []string
}

func (s *editSaveTestStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	return append([]WindowsAnswerVariant(nil), s.winAnswers...), nil
}
func (s *editSaveTestStore) WindowsAnswer(_ context.Context, id int64) (WindowsAnswerVariant, error) {
	for _, a := range s.winAnswers {
		if a.ID == id {
			return a, nil
		}
	}
	return WindowsAnswerVariant{}, os.ErrNotExist
}
func (s *editSaveTestStore) CreateWindowsAnswer(_ context.Context, v WindowsAnswerVariant) (WindowsAnswerVariant, error) {
	v.ID = int64(len(s.winAnswers) + 10)
	v.State = "pending"
	s.winAnswers = append(s.winAnswers, v)
	return v, nil
}
func (s *editSaveTestStore) UpdateWindowsAnswer(_ context.Context, v WindowsAnswerVariant) (WindowsAnswerVariant, error) {
	for i := range s.winAnswers {
		if s.winAnswers[i].ID == v.ID {
			v.State = "ready"
			s.winAnswers[i] = v
			return v, nil
		}
	}
	return WindowsAnswerVariant{}, os.ErrNotExist
}
func (s *editSaveTestStore) SetWindowsAnswerState(_ context.Context, id int64, state, errMsg string) error {
	for i := range s.winAnswers {
		if s.winAnswers[i].ID == id {
			s.winAnswers[i].State, s.winAnswers[i].Error = state, errMsg
			return nil
		}
	}
	return os.ErrNotExist
}

func (s *editSaveTestStore) CreateUbuntuAutoinstall(_ context.Context, f UbuntuAutoinstallFile) (UbuntuAutoinstallFile, error) {
	f.ID = int64(len(s.ubuntu) + 1)
	f.CreatedAt = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	s.ubuntu = append(s.ubuntu, f)
	return f, nil
}
func (s *editSaveTestStore) UpdateUbuntuAutoinstall(_ context.Context, f UbuntuAutoinstallFile) (UbuntuAutoinstallFile, error) {
	for i := range s.ubuntu {
		if s.ubuntu[i].ID == f.ID {
			f.CreatedAt = s.ubuntu[i].CreatedAt
			s.ubuntu[i] = f
			return f, nil
		}
	}
	return UbuntuAutoinstallFile{}, os.ErrNotExist
}
func (s *editSaveTestStore) UbuntuAutoinstall(_ context.Context, id int64) (UbuntuAutoinstallFile, error) {
	for _, f := range s.ubuntu {
		if f.ID == id {
			return f, nil
		}
	}
	return UbuntuAutoinstallFile{}, os.ErrNotExist
}
func (s *editSaveTestStore) UbuntuAutoinstalls(context.Context) ([]UbuntuAutoinstallSummary, error) {
	out := make([]UbuntuAutoinstallSummary, 0, len(s.ubuntu))
	for _, f := range s.ubuntu {
		out = append(out, UbuntuAutoinstallSummary{ID: f.ID, Title: f.Title, SourceKind: f.SourceKind, SHA256: f.SHA256, State: f.State, DiskMode: f.DiskMode, DiskSerial: f.DiskSerial, CreatedAt: f.CreatedAt})
	}
	return out, nil
}
func (s *editSaveTestStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

type editSaveAgent struct {
	xmls     map[int64][]byte
	prepared []agent.WindowsAnswerRequest
}

func (a *editSaveAgent) PrepareWindowsAnswer(_ context.Context, req agent.WindowsAnswerRequest) error {
	a.prepared = append(a.prepared, req)
	if a.xmls == nil {
		a.xmls = map[int64][]byte{}
	}
	a.xmls[req.ID] = append([]byte(nil), req.XML...)
	return nil
}
func (a *editSaveAgent) CheckWindowsAnswer(context.Context, agent.WindowsAnswerCheck) error { return nil }
func (a *editSaveAgent) WindowsAnswerXML(_ context.Context, req agent.WindowsAnswerCheck) ([]byte, error) {
	return a.xmls[req.ID], nil
}

func TestWindowsAnswerEditorSaveUpdateAndSaveAsNew(t *testing.T) {
	d, err := unattend.LoadData()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := editorPreset("lab")
	doc, err := unattend.Generate(d, cfg)
	if err != nil {
		t.Fatal(err)
	}
	xmlV1 := unattend.Serialize(doc)
	sumV1 := sha256.Sum256(xmlV1)
	shaV1 := hex.EncodeToString(sumV1[:])

	store := &editSaveTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}},
		winAnswers:    []WindowsAnswerVariant{{ID: 10, Title: "Win11 Base", SHA256: shaV1, Size: int64(len(xmlV1)), State: "ready"}},
	}
	ag := &editSaveAgent{xmls: map[int64][]byte{10: xmlV1}}
	srv := &Server{Store: store, WindowsAnswers: ag}
	h := srv.Handler()
	csrf := CSRFToken("test-token")

	// GET /answers/10/edit shows both Save and Save as new buttons
	editReq := httptest.NewRequest(http.MethodGet, "/answers/10/edit", nil)
	editReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	editRec := httptest.NewRecorder()
	h.ServeHTTP(editRec, editReq)
	if editRec.Code != http.StatusOK {
		t.Fatalf("edit status = %d", editRec.Code)
	}
	html := editRec.Body.String()
	for _, want := range []string{`name="save_mode" value="update"`, `name="save_mode" value="new"`, `Сохранить как новый`} {
		if !strings.Contains(html, want) {
			t.Fatalf("edit page missing %q", want)
		}
	}

	// POST /answers/editor/save with save_mode=update updates #10 in-place
	form := unattend.FormValues(cfg)
	form.Set("csrf", csrf)
	form.Set("source_id", "10")
	form.Set("source_title", "Win11 Base")
	form.Set("title", "Win11 Офис Обновлённый")
	form.Set("save_mode", "update")
	form.Set("BypassRequirementsCheck", "true")
	reqUpd := httptest.NewRequest(http.MethodPost, "/answers/editor/save", strings.NewReader(form.Encode()))
	reqUpd.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUpd.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	recUpd := httptest.NewRecorder()
	h.ServeHTTP(recUpd, reqUpd)
	if recUpd.Code != http.StatusSeeOther {
		t.Fatalf("update status = %d, body = %s", recUpd.Code, recUpd.Body.String())
	}
	if len(store.winAnswers) != 1 || store.winAnswers[0].ID != 10 || store.winAnswers[0].Title != "Win11 Офис Обновлённый" || store.winAnswers[0].SHA256 == shaV1 {
		t.Fatalf("after update winAnswers = %+v", store.winAnswers)
	}
	if len(ag.prepared) != 1 || ag.prepared[0].ID != 10 || ag.prepared[0].SHA256 != store.winAnswers[0].SHA256 {
		t.Fatalf("agent prepared = %+v", ag.prepared)
	}

	// POST /answers/editor/save with save_mode=new creates a new script leaving #10 intact
	cfg2, _ := editorPreset("windows")
	formNew := unattend.FormValues(cfg2)
	formNew.Set("csrf", csrf)
	formNew.Set("source_id", "10")
	formNew.Set("source_title", "Win11 Base")
	formNew.Set("title", "Win11 Копия")
	formNew.Set("save_mode", "new")
	reqNew := httptest.NewRequest(http.MethodPost, "/answers/editor/save", strings.NewReader(formNew.Encode()))
	reqNew.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqNew.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	recNew := httptest.NewRecorder()
	h.ServeHTTP(recNew, reqNew)
	if recNew.Code != http.StatusSeeOther {
		t.Fatalf("save-as-new status = %d, body = %s", recNew.Code, recNew.Body.String())
	}
	if len(store.winAnswers) != 2 || store.winAnswers[0].Title != "Win11 Офис Обновлённый" || store.winAnswers[1].Title != "Win11 Копия" {
		t.Fatalf("after save-as-new winAnswers = %+v", store.winAnswers)
	}
}

func TestUbuntuConstructorEditPreservesPasswordAndSupportsUpdateOrNew(t *testing.T) {
	vals, data, err := autoinstall.Compile(autoinstall.Form{
		Locale: "en_US.UTF-8", Keyboard: "us", TimeZone: "UTC",
		Hostname: "ubu-orig", Username: "ops", Password: "initial-secret-password", DiskMode: "interactive",
	})
	if err != nil {
		t.Fatal(err)
	}
	origHash := vals.PasswordHash
	sum := sha256.Sum256(data)
	origSHA := hex.EncodeToString(sum[:])

	store := &editSaveTestStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}},
		ubuntu: []UbuntuAutoinstallFile{{
			ID: 1, Title: "Ubuntu Base", SourceKind: "editor", Values: vals, UserData: data, SHA256: origSHA, State: "validated", DiskMode: "interactive",
		}},
	}
	srv := &Server{Store: store}
	h := srv.Handler()
	csrf := CSRFToken("test-token")

	// GET /ubuntu-autoinstall/1 as operator opens the constructor editor with Save & Save as new
	getReq := httptest.NewRequest(http.MethodGet, "/ubuntu-autoinstall/1", nil)
	getReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get editor status = %d", getRec.Code)
	}
	body := getRec.Body.String()
	for _, want := range []string{`id="ubuntu-editor"`, `name="edit_id" value="1"`, `name="save_mode" value="update"`, `name="save_mode" value="new"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("ubuntu editor missing %q", want)
		}
	}

	// Preview with edit_id=1 and EMPTY password preserves origHash
	prevForm := url.Values{
		"csrf": {csrf}, "edit_id": {"1"}, "title": {"Ubuntu Updated"},
		"locale": {"en_US.UTF-8"}, "keyboard": {"us"}, "timezone": {"UTC"},
		"hostname": {"ubu-updated"}, "username": {"ops"}, "disk_mode": {"interactive"},
	}
	prevReq := httptest.NewRequest(http.MethodPost, "/ubuntu-autoinstall/preview", strings.NewReader(prevForm.Encode()))
	prevReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	prevReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	prevRec := httptest.NewRecorder()
	h.ServeHTTP(prevRec, prevReq)
	if prevRec.Code != http.StatusOK {
		t.Fatalf("preview without password status = %d", prevRec.Code)
	}
	// Extract preview_token from HTML
	prevHTML := prevRec.Body.String()
	marker := `name="preview_token" value="`
	idx := strings.Index(prevHTML, marker)
	if idx < 0 {
		t.Fatal("preview_token not returned when editing without re-entering password")
	}
	rest := prevHTML[idx+len(marker):]
	token := rest[:strings.IndexByte(rest, '"')]

	// Save with save_mode=update updates #1 in-place preserving PasswordHash
	prevForm.Set("preview_token", token)
	prevForm.Set("save_mode", "update")
	updReq := httptest.NewRequest(http.MethodPost, "/ubuntu-autoinstall", strings.NewReader(prevForm.Encode()))
	updReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	updRec := httptest.NewRecorder()
	h.ServeHTTP(updRec, updReq)
	if updRec.Code != http.StatusSeeOther {
		t.Fatalf("update status = %d, body = %s", updRec.Code, updRec.Body.String())
	}
	if len(store.ubuntu) != 1 || store.ubuntu[0].ID != 1 || store.ubuntu[0].Title != "Ubuntu Updated" || store.ubuntu[0].Values.Hostname != "ubu-updated" || store.ubuntu[0].Values.PasswordHash != origHash || store.ubuntu[0].SHA256 == origSHA {
		t.Fatalf("after update ubuntu = %+v", store.ubuntu)
	}

	// Save with save_mode=new creates #2 leaving #1 intact
	newForm := url.Values{
		"csrf": {csrf}, "edit_id": {"1"}, "title": {"Ubuntu Copy"},
		"locale": {"en_US.UTF-8"}, "keyboard": {"us"}, "timezone": {"UTC"},
		"hostname": {"ubu-copy"}, "username": {"ops"}, "disk_mode": {"interactive"},
	}
	p2Req := httptest.NewRequest(http.MethodPost, "/ubuntu-autoinstall/preview", strings.NewReader(newForm.Encode()))
	p2Req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	p2Req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	p2Rec := httptest.NewRecorder()
	h.ServeHTTP(p2Rec, p2Req)
	p2HTML := p2Rec.Body.String()
	idx2 := strings.Index(p2HTML, marker)
	if idx2 < 0 {
		t.Fatal("preview_token missing for save-as-new")
	}
	rest2 := p2HTML[idx2+len(marker):]
	newForm.Set("preview_token", rest2[:strings.IndexByte(rest2, '"')])
	newForm.Set("save_mode", "new")
	newReq := httptest.NewRequest(http.MethodPost, "/ubuntu-autoinstall", strings.NewReader(newForm.Encode()))
	newReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	newReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	newRec := httptest.NewRecorder()
	h.ServeHTTP(newRec, newReq)
	if newRec.Code != http.StatusSeeOther {
		t.Fatalf("save-as-new status = %d", newRec.Code)
	}
	if len(store.ubuntu) != 2 || store.ubuntu[0].Title != "Ubuntu Updated" || store.ubuntu[1].Title != "Ubuntu Copy" {
		t.Fatalf("after save-as-new ubuntu = %+v", store.ubuntu)
	}

	// Observer still gets read-only view, not editor
	store.user = User{ID: 2, Role: "observer", Enabled: true}
	obsReq := httptest.NewRequest(http.MethodGet, "/ubuntu-autoinstall/1", nil)
	obsReq.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	obsRec := httptest.NewRecorder()
	h.ServeHTTP(obsRec, obsReq)
	if obsRec.Code != http.StatusOK || strings.Contains(obsRec.Body.String(), `id="ubuntu-editor"`) {
		t.Fatalf("observer should get read-only view, status=%d", obsRec.Code)
	}
}
