package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/images"
	"omb/internal/unattend"
)

// editorStore is a store with one ready profile, its answers and an audit log.
type editorStore struct {
	*answerPageStore
	audit []string
}

func (s *editorStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audit = append(s.audit, action+" "+result+" "+detail)
	return nil
}

func (s *editorStore) WindowsAnswer(_ context.Context, id int64) (WindowsAnswerVariant, error) {
	for _, a := range s.answers {
		if a.ID == id {
			return a, nil
		}
	}
	return WindowsAnswerVariant{}, os.ErrNotExist
}

// editorAgent stores answer files and gives them back.
type editorAgent struct {
	answerAgent
	files map[int64][]byte
}

func (a *editorAgent) PrepareWindowsAnswer(ctx context.Context, req agent.WindowsAnswerRequest) error {
	if a.files == nil {
		a.files = map[int64][]byte{}
	}
	a.files[req.ID] = req.XML
	return a.answerAgent.PrepareWindowsAnswer(ctx, req)
}

func (a *editorAgent) WindowsAnswerXML(_ context.Context, req agent.WindowsAnswerCheck) ([]byte, error) {
	if data, ok := a.files[req.ID]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

func editorServer(role, lang string) (*Server, *editorStore, *editorAgent) {
	store := &editorStore{answerPageStore: &answerPageStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Username: "u", Role: role, Enabled: true, Language: lang}},
	}}
	ag := &editorAgent{}
	return &Server{Store: store, WindowsAnswers: ag, Images: &fakeImagesAPI{catalog: images.Catalog{}}}, store, ag
}

func editorGet(s *Server, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	return res
}

func editorPost(s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	form.Set("csrf", CSRFToken("test-token"))
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	return res
}

func labForm() url.Values {
	c, _ := editorPreset("lab")
	form := unattend.FormValues(c)
	form.Set("title", "Windows 11 Lab")
	form.Set("folder", "Windows")
	return form
}

func TestEditorPageForEveryRole(t *testing.T) {
	for _, role := range []string{"admin", "operator", "observer"} {
		s, _, _ := editorServer(role, "ru")
		res := editorGet(s, "/answers/editor")
		body := res.Body.String()
		if res.Code != http.StatusOK || !strings.Contains(body, "Редактор файла ответов") || !strings.Contains(body, "Скачать autounattend.xml") {
			t.Fatalf("%s: %d", role, res.Code)
		}
		if canSave := strings.Contains(body, `formaction="/answers/editor/save"`); canSave != (role != "observer") {
			t.Errorf("%s: save button shown=%t", role, canSave)
		}
	}
	s, _, _ := editorServer("observer", "ru")
	if res := editorPost(s, "/answers/editor/save", labForm()); res.Code != http.StatusForbidden {
		t.Fatalf("observer saved: %d", res.Code)
	}
}

func TestEditorDownloadAndSave(t *testing.T) {
	s, store, ag := editorServer("operator", "ru")
	form := labForm()
	form.Set("do", "download")
	res := editorPost(s, "/answers/editor", form)
	downloaded := res.Body.Bytes()
	if res.Code != http.StatusOK || !strings.Contains(res.Header().Get("Content-Disposition"), "autounattend.xml") || !strings.Contains(string(downloaded), `<Editor version="1"`) {
		t.Fatalf("download: %d %s", res.Code, res.Header())
	}
	res = editorPost(s, "/answers/editor/save", labForm())
	if res.Code != http.StatusSeeOther || len(store.answers) != 1 {
		t.Fatalf("save: %d %s", res.Code, res.Body.String())
	}
	if string(ag.files[7]) != string(downloaded) {
		t.Fatal("the agent got other bytes than the download")
	}
	if a := store.answers[0]; a.Title != "Windows 11 Lab" || len(a.Target.Architectures) == 0 || a.State != "ready" {
		t.Fatalf("answer %+v", a)
	}
	found := false
	for _, line := range store.audit {
		found = found || (strings.HasPrefix(line, "windows-answer.create ok") && strings.HasSuffix(line, "editor=1"))
	}
	if !found {
		t.Fatalf("audit %v", store.audit)
	}
	// Saved by the editor, the file opens exactly.
	page := editorGet(s, "/answers/7/edit")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "открыт без потерь") {
		t.Fatalf("edit page: %d", page.Code)
	}
}

func TestEditorEditSavesNewVariant(t *testing.T) {
	s, store, ag := editorServer("operator", "ru")
	editorPost(s, "/answers/editor/save", labForm())
	store.answers[0].ID = 3 // the stored one
	ag.files[3] = ag.files[7]
	delete(ag.files, 7)
	page := editorGet(s, "/answers/3/edit")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="source_id" value="3"`) {
		t.Fatalf("edit page: %d", page.Code)
	}
	form := labForm()
	form.Set("ComputerNameSettings.$type", "CustomComputerNameSettings")
	form.Set("ComputerNameSettings.CustomComputerNameSettings.Name", "LAB-01")
	form.Set("source_id", "3")
	res := editorPost(s, "/answers/editor/save", form)
	if res.Code != http.StatusSeeOther || len(store.answers) != 2 || store.answers[0].ID != 3 || store.answers[1].ID != 7 {
		t.Fatalf("save: %d %+v", res.Code, store.answers)
	}
	if !strings.Contains(string(ag.files[7]), "LAB-01") || strings.Contains(string(ag.files[3]), "LAB-01") {
		t.Fatal("the old file changed or the new one lacks the change")
	}
}

func TestEditorKeepsInputOnGeneratorError(t *testing.T) {
	s, store, _ := editorServer("operator", "ru")
	form := labForm()
	script := "echo " + strings.Repeat("x", 300)
	form.Set("PESettings.$type", "ScriptPESetttings")
	form.Set("PESettings.ScriptPESetttings.Script", script)
	form.Set("section", "disk")
	res := editorPost(s, "/answers/editor/save", form)
	body := res.Body.String()
	if res.Code != http.StatusUnprocessableEntity || !strings.Contains(body, "is too long") || !strings.Contains(body, script) || len(store.answers) != 0 {
		t.Fatalf("status %d, answers %d", res.Code, len(store.answers))
	}
	// Not a number: the field keeps what was typed.
	form = labForm()
	form.Set("PESettings.$type", "GeneratePESettings")
	form.Set("PESettings.GeneratePESettings.PartitionSettings.UnattendedPartitionSettings.SystemSize", "много")
	body = editorPost(s, "/answers/editor", form).Body.String()
	if !strings.Contains(body, "Нужно целое число в поле SystemSize") || !strings.Contains(body, `value="много"`) {
		t.Fatal("number error or typed value missing")
	}
}

func TestEditorRefusesFilesOver1MB(t *testing.T) {
	s, store, _ := editorServer("operator", "ru")
	form := labForm()
	form.Set("ScriptSettings.Scripts.0.Content", strings.Repeat("rem padding\n", 50000))
	form.Set("ScriptSettings.Scripts.0.Phase", "System")
	form.Set("ScriptSettings.Scripts.0.Type", "Cmd")
	res := editorPost(s, "/answers/editor/save", form)
	if res.Code != http.StatusRequestEntityTooLarge || !strings.Contains(res.Body.String(), "Файл больше 1 МБ") || len(store.answers) != 0 {
		t.Fatalf("status %d", res.Code)
	}
	form.Set("do", "download")
	if res := editorPost(s, "/answers/editor", form); res.Code != http.StatusOK || res.Header().Get("Content-Disposition") == "" {
		t.Fatalf("download refused: %d", res.Code)
	}
}

func TestEditorSectionsPresetsRowsAndPreview(t *testing.T) {
	s, _, _ := editorServer("operator", "ru")
	form := labForm()
	form.Set("section", "accounts")
	form.Set("add", "AccountSettings.UnattendedAccountSettings.Accounts")
	body := editorPost(s, "/answers/editor", form).Body.String()
	if !strings.Contains(body, `data-section="accounts">`) || !strings.Contains(body, "AccountSettings.UnattendedAccountSettings.Accounts.3.Name") {
		t.Fatal("section or added row missing")
	}
	form = labForm()
	form.Set("preset", "windows")
	body = editorPost(s, "/answers/editor", form).Body.String()
	if !strings.Contains(body, `value="InteractiveMicrosoftAccountSettings" data-variant checked`) {
		t.Fatal("preset not applied")
	}
	form = labForm()
	form.Set("do", "preview")
	res := editorPost(s, "/answers/editor", form)
	var preview struct {
		Error   string
		XML     string
		Summary []editorLine
		Marks   map[string]map[string]any
	}
	if err := json.Unmarshal(res.Body.Bytes(), &preview); err != nil || preview.Error != "" || !strings.Contains(preview.XML, "<unattend") || len(preview.Summary) == 0 || preview.Marks["accounts"]["mark"] != "changed" {
		t.Fatalf("preview %v %+v", err, preview)
	}
	// A removed row goes away.
	form = labForm()
	form.Set("AccountSettings.UnattendedAccountSettings.Accounts.1.Name", "extra")
	form.Set("AccountSettings.UnattendedAccountSettings.Accounts.1.Group", "Users")
	form.Set("AccountSettings.UnattendedAccountSettings.Accounts.1.remove", "true")
	form.Set("do", "download")
	if xml := editorPost(s, "/answers/editor", form).Body.String(); strings.Contains(xml, "<Name>extra</Name>") {
		t.Fatal("removed row is in the file")
	}
}

func TestEditorEnglishPageHasNoRussian(t *testing.T) {
	s, _, ag := editorServer("admin", "en")
	for _, path := range []string{"/answers/editor"} {
		body := editorGet(s, path).Body.String()
		if found := cyrillic.FindAllString(body, 5); len(found) > 0 {
			t.Errorf("%s has Russian text %q", path, found)
		}
	}
	// An opened foreign file, with its report.
	raw, err := os.ReadFile("../unattend/testdata/foreign/schneegans-old.xml")
	if err != nil {
		t.Fatal(err)
	}
	ag.files = map[int64][]byte{5: raw}
	store := s.Store.(*editorStore)
	store.answers = []WindowsAnswerVariant{{ID: 5, Title: "Old", Folder: "Windows", State: "ready"}}
	body := editorGet(s, "/answers/5/edit").Body.String()
	if !strings.Contains(body, "was not made by the editor") {
		t.Fatal("no import report")
	}
	if found := cyrillic.FindAllString(body, 5); len(found) > 0 {
		t.Errorf("opened file page has Russian text %q", found)
	}
}
