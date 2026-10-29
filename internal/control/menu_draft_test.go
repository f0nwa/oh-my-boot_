package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const testBootURL = "http://10.30.0.10:8080"

func draftFixture() ([]WindowsProfile, []WindowsAnswerVariant, []DriverConfig) {
	profile, answer, configs := driverMenuFixture()
	return []WindowsProfile{profile}, []WindowsAnswerVariant{answer}, configs
}

func TestBuildMenuFromDraftKeepsOrderAndTitles(t *testing.T) {
	profiles, answers, configs := draftFixture()
	d := MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "windriver_2", Title: "Win11 key"}, {Key: "winiso_17"}}}
	script, err := BuildMenuFromDraft(d, testBootURL, profiles, answers, configs)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	first, second := strings.Index(text, "item windriver_2 Win11 key${sp"), strings.Index(text, "item winiso_17 Windows 11\n")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("order or titles wrong:\n%s", text)
	}
}

func TestBuildMenuFromDraftRejectsCyrillicTitle(t *testing.T) {
	profiles, answers, configs := draftFixture()
	d := MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17", Title: "Ключ"}}}
	if _, err := BuildMenuFromDraft(d, testBootURL, profiles, answers, configs); err == nil {
		t.Fatal("Cyrillic title accepted")
	}
}

func TestBuildMenuFromDraftRejectsMissingObject(t *testing.T) {
	profiles, answers, configs := draftFixture()
	d := MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winanswer_99"}}}
	_, err := BuildMenuFromDraft(d, testBootURL, profiles, answers, configs)
	if err == nil || !strings.Contains(err.Error(), "winanswer_99") {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildMenuFromDraftUsesItemFolder(t *testing.T) {
	profiles, answers, configs := draftFixture()
	d := MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Ubuntu: true, Items: []MenuDraftItem{{Key: "winanswer_31", Folder: "Windows/Tests"}}}
	script, err := BuildMenuFromDraft(d, testBootURL, profiles, answers, configs)
	if err != nil {
		t.Fatal(err)
	}
	if menuFolderPaths(string(script))["winanswer_31"] != "Windows/Tests" || !strings.Contains(string(script), "item ubuntu ") {
		t.Fatalf("folder or ubuntu missing:\n%s", script)
	}
}

func TestDraftFromVersionRoundTrip(t *testing.T) {
	profiles, answers, configs := draftFixture()
	want := MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Ubuntu: true, UbuntuFolder: "Linux", Items: []MenuDraftItem{
		{Key: "winiso_17", Folder: "", Title: "Windows 11"},
		{Key: "windriver_1", Folder: "Windows/Laptops", Title: "Laptop A"},
		{Key: "winanswer_31", Folder: "Windows/Tests", Title: "Lab"},
	}}
	script, err := BuildMenuFromDraft(want, testBootURL, profiles, answers, configs)
	if err != nil {
		t.Fatal(err)
	}
	got := draftFromVersion(MenuVersion{Title: want.Title, Label: want.Label, Message: want.Message, Script: string(script)})
	if got.Title != want.Title || got.Label != want.Label || got.Message != want.Message || got.Ubuntu != want.Ubuntu || got.UbuntuFolder != want.UbuntuFolder || len(got.Items) != len(want.Items) {
		t.Fatalf("got %+v", got)
	}
	for i := range want.Items {
		if got.Items[i] != want.Items[i] {
			t.Fatalf("item %d: got %+v want %+v", i, got.Items[i], want.Items[i])
		}
	}
}

func TestDiffMenuDraft(t *testing.T) {
	base := MenuDraft{Title: "PXE", Label: "D", Message: "M", Items: []MenuDraftItem{{Key: "winiso_1", Title: "A"}, {Key: "winiso_2", Title: "B"}}}
	clone := func(edit func(*MenuDraft)) MenuDraft {
		d := base
		d.Items = append([]MenuDraftItem(nil), base.Items...)
		edit(&d)
		return d
	}
	for name, tc := range map[string]struct {
		draft MenuDraft
		kinds []string
	}{
		"same":     {clone(func(*MenuDraft) {}), nil},
		"add":      {clone(func(d *MenuDraft) { d.Items = append(d.Items, MenuDraftItem{Key: "winanswer_3", Title: "C"}) }), []string{"add"}},
		"remove":   {clone(func(d *MenuDraft) { d.Items = d.Items[:1] }), []string{"remove"}},
		"rename":   {clone(func(d *MenuDraft) { d.Items[0].Title = "A2" }), []string{"change"}},
		"reorder":  {clone(func(d *MenuDraft) { d.Items[0], d.Items[1] = d.Items[1], d.Items[0] }), []string{"order"}},
		"settings": {clone(func(d *MenuDraft) { d.Ubuntu = true }), []string{"settings"}},
	} {
		changes := diffMenuDraft(base, tc.draft)
		var kinds []string
		for _, c := range changes {
			kinds = append(kinds, c.Kind)
		}
		if strings.Join(kinds, ",") != strings.Join(tc.kinds, ",") {
			t.Errorf("%s: got %v", name, kinds)
		}
	}
}

type draftStore struct {
	menuPageStore
	draft    *MenuDraft
	saved    []MenuDraft
	profiles []WindowsProfile
	answers  []WindowsAnswerVariant
	configs  []DriverConfig
}

func (s *draftStore) MenuDraft(context.Context) (MenuDraft, bool, error) {
	if s.draft == nil {
		return MenuDraft{}, false, nil
	}
	return *s.draft, true, nil
}
func (s *draftStore) SaveMenuDraft(_ context.Context, d MenuDraft) error {
	s.saved = append(s.saved, d)
	s.draft = &d
	return nil
}
func (s *draftStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return s.profiles, nil
}
func (s *draftStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	return s.answers, nil
}
func (s *draftStore) DriverConfigs(context.Context) ([]DriverConfig, error) { return s.configs, nil }

func newDraftStore(t *testing.T, role string, active []MenuDraftItem) *draftStore {
	t.Helper()
	profiles, answers, configs := draftFixture()
	store := &draftStore{menuPageStore: menuPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: role, Enabled: true}}}, profiles: profiles, answers: answers, configs: configs}
	if active != nil {
		d := MenuDraft{Title: "oh my boot_", Label: "Diagnostics", Message: "Ready", Items: active}
		script, err := BuildMenuFromDraft(d, testBootURL, profiles, answers, configs)
		if err != nil {
			t.Fatal(err)
		}
		store.versions = []MenuVersion{{ID: 32, Active: true, Title: d.Title, Label: d.Label, Message: d.Message, Script: string(script)}}
	}
	return store
}

func TestCurrentDraftWithoutStoredDraftEqualsActive(t *testing.T) {
	store := newDraftStore(t, "admin", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}, {Key: "windriver_1", Folder: "Windows/Laptops", Title: "Laptop A"}})
	draft, active, err := (&Server{Store: store}).currentDraft(context.Background())
	if err != nil || len(draft.Items) != 2 || len(diffMenuDraft(active, draft)) != 0 {
		t.Fatalf("err=%v draft=%+v changes=%v", err, draft, diffMenuDraft(active, draft))
	}
}

func (s *draftStore) MenuByID(_ context.Context, id int64) (MenuVersion, error) {
	for _, v := range s.versions {
		if v.ID == id {
			return v, nil
		}
	}
	return MenuVersion{}, errors.New("not found")
}

func (s *draftStore) SaveActiveMenu(_ context.Context, _ int64, m MenuVersion) error {
	for i := range s.versions {
		s.versions[i].Active = false
	}
	m.ID, m.Active = int64(len(s.versions)+100), true
	s.versions = append(s.versions, m)
	return nil
}

func TestStructuredDraftFromVersionIgnoresScript(t *testing.T) {
	v := MenuVersion{Title: productMenuTitle, Label: "Tools", Message: "Ready", Script: "#!ipxe\nitem ubuntu Old Ubuntu\nitem winiso_9 Old Windows\n", NoDiagnostics: true, LocalTimeout: 31, NoLocalTimer: true, Look: menuLook, Items: []MenuVersionItem{{InstallationID: 8, Position: 1, Title: "Windows", Folder: "Win"}, {InstallationID: 7, Position: 0, Title: "Ubuntu", Folder: "Linux"}}}
	want := MenuDraft{Title: productMenuTitle, Label: "Tools", Message: "Ready", NoDiagnostics: true, LocalTimeout: 31, NoLocalTimer: true, look: menuLook, Items: []MenuDraftItem{{InstallationID: 7, Title: "Ubuntu", Folder: "Linux"}, {InstallationID: 8, Title: "Windows", Folder: "Win"}}}
	if got := draftFromVersion(v); !reflect.DeepEqual(got, want) {
		t.Fatalf("draft=%+v want=%+v", got, want)
	}
	v.Items = []MenuVersionItem{}
	if got := draftFromVersion(v); len(got.Items) != 0 || got.Ubuntu {
		t.Fatalf("empty structured version parsed legacy items: %+v", got)
	}
}

func TestStructuredDraftOperationsAndChanges(t *testing.T) {
	d := MenuDraft{}
	if err := d.AddInstallation(Installation{ID: 7, Title: "Ubuntu", Folder: "Linux"}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddInstallation(Installation{ID: 8, Title: "Windows", Folder: "Win"}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddInstallation(Installation{ID: 7, Title: "Duplicate"}); err == nil {
		t.Fatal("duplicate installation accepted")
	}
	if err := d.AddInstallation(Installation{Title: "No ID"}); err == nil {
		t.Fatal("missing installation ID accepted")
	}
	active := d
	active.Items = append([]MenuDraftItem(nil), d.Items...)
	if err := d.UpdateInstallation(7, "Lab Ubuntu", "Lab"); err != nil {
		t.Fatal(err)
	}
	if err := d.MoveInstallation(8, -1); err != nil {
		t.Fatal(err)
	}
	changes := diffMenuDraft(active, d)
	var kinds []string
	for _, change := range changes {
		kinds = append(kinds, change.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{"change", "change", "order"}) {
		t.Fatalf("changes=%+v", changes)
	}
	if err := d.ReorderInstallations([]int64{7, 7}); err == nil || d.Items[0].InstallationID != 8 {
		t.Fatalf("invalid reorder mutated draft: %+v", d)
	}
	if err := d.ReorderInstallations([]int64{7, 8}); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveInstallation(8); err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 1 || d.Items[0] != (MenuDraftItem{InstallationID: 7, Title: "Lab Ubuntu", Folder: "Lab"}) {
		t.Fatalf("items=%+v", d.Items)
	}
	if err := d.RemoveInstallation(8); err == nil {
		t.Fatal("removed missing installation")
	}
	if err := d.UpdateInstallation(99, "Missing", ""); err == nil {
		t.Fatal("updated missing installation")
	}
	if err := d.MoveInstallation(99, 1); err == nil {
		t.Fatal("moved missing installation")
	}
}
