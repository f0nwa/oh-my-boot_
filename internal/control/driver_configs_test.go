package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"omb/internal/agent"
	"omb/internal/drivers"
)

type driverConfigStore struct {
	driverPageStore
	profiles      []WindowsProfile
	answers       []WindowsAnswerVariant
	configs       []DriverConfig
	installations []Installation
	audits        []string
}

func (s *driverConfigStore) BootProfiles(context.Context) ([]BootProfile, error) {
	var profiles []BootProfile
	for _, p := range s.profiles {
		profiles = append(profiles, BootProfile{ID: p.ID, Kind: BootProfileWindowsISO, Title: p.Title, WindowsProfileID: p.ID})
	}
	return profiles, nil
}
func (s *driverConfigStore) Installations(context.Context) ([]Installation, error) {
	return s.installations, nil
}
func (s *driverConfigStore) CreateInstallation(_ context.Context, item Installation) (Installation, error) {
	item.ID = int64(len(s.installations) + 1)
	s.installations = append(s.installations, item)
	return item, nil
}

func (s *driverConfigStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return s.profiles, nil
}
func (s *driverConfigStore) WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error) {
	return s.answers, nil
}
func (s *driverConfigStore) DriverConfigs(context.Context) ([]DriverConfig, error) {
	return s.configs, nil
}
func (s *driverConfigStore) DriverConfig(_ context.Context, id int64) (DriverConfig, error) {
	for _, item := range s.configs {
		if item.ID == id {
			return item, nil
		}
	}
	return DriverConfig{}, os.ErrNotExist
}
func (s *driverConfigStore) CreateDriverConfig(_ context.Context, config DriverConfig) (DriverConfig, error) {
	config.ID = int64(len(s.configs) + 1)
	config.State = "pending"
	s.configs = append(s.configs, config)
	return config, nil
}
func (s *driverConfigStore) SetDriverConfigResult(_ context.Context, id int64, state string, build agent.DriverBuildResult, message string) error {
	for i := range s.configs {
		if s.configs[i].ID == id {
			s.configs[i].State, s.configs[i].Build, s.configs[i].Error = state, build, message
			return nil
		}
	}
	return os.ErrNotExist
}
func (s *driverConfigStore) SetDriverConfigTitle(_ context.Context, id int64, title string) error {
	for i := range s.configs {
		if s.configs[i].ID == id {
			s.configs[i].Title = title
			return nil
		}
	}
	return os.ErrNotExist
}
func (s *driverConfigStore) Audit(_ context.Context, _ int64, action, result, detail string) error {
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

type fakeDriverBuilder struct{ calls []agent.DriverBuildRequest }

func (f *fakeDriverBuilder) PrepareDriverConfig(_ context.Context, req agent.DriverBuildRequest) (agent.DriverBuildResult, error) {
	f.calls = append(f.calls, req)
	return agent.DriverBuildResult{WIMKey: strings.Repeat("a", 64), WIMSHA256: strings.Repeat("b", 64), ScriptSHA256: strings.Repeat("c", 64)}, nil
}
func (f *fakeDriverBuilder) CheckDriverConfig(context.Context, agent.DriverBuildCheck) error {
	return nil
}

func configFixture() *driverConfigStore {
	hash := strings.Repeat("a", 64)
	return &driverConfigStore{
		driverPageStore: driverPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, packages: []DriverPackage{{SHA256: hash, Inventory: drivers.Inventory{SHA256: hash, INFs: []drivers.INF{{Path: "net.inf", Architecture: "amd64", Class: "Net"}, {Path: "old.inf", Architecture: "x86"}}}}}},
		profiles:        []WindowsProfile{{ID: 17, State: "ready", InstallFile: "install.wim", WindowsProfileSource: WindowsProfileSource{Title: "Windows 11", Folder: "Windows"}}},
		answers:         []WindowsAnswerVariant{{ID: 4, State: "ready", Size: 100, SHA256: strings.Repeat("d", 64)}},
	}
}

func TestDriverConfigSelectionValidatesINFAndAnswer(t *testing.T) {
	store := configFixture()
	base := url.Values{"profile_id": {"17"}, "answer_id": {"4"}, "title": {"Laptop A"}, "folder": {"Windows/Laptops"}, "inf": {strings.Repeat("a", 64) + ":net.inf"}}
	if _, err := SelectDriverConfig(base, store.profiles, store.answers, store.packages); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(url.Values){
		func(v url.Values) { v.Set("answer_id", "99") },
		func(v url.Values) { v.Set("answer_id", "4"); v.Set("profile_id", "99") },
		func(v url.Values) { v.Set("inf", strings.Repeat("a", 64)+":old.inf") },
		func(v url.Values) { v.Set("inf", strings.Repeat("b", 64)+":net.inf") },
		func(v url.Values) { v.Set("title", "Ноутбук") },
	} {
		input := url.Values{}
		for key, values := range base {
			input[key] = append([]string(nil), values...)
		}
		mutate(input)
		if _, err := SelectDriverConfig(input, store.profiles, store.answers, store.packages); err == nil {
			t.Fatalf("accepted invalid configuration: %v", input)
		}
	}
}

func TestDriverConfigPreparesOnInstallationCreate(t *testing.T) {
	store := configFixture()
	builder := &fakeDriverBuilder{}
	s := &Server{Store: store, DriverConfigs: builder}
	form := url.Values{"csrf": {CSRFToken("test-token")}, "boot_profile_id": {"17"}, "answer_id": {"4"}, "title": {"Laptop A"}, "folder": {"Windows"}, "inf": {strings.Repeat("a", 64) + ":net.inf"}}
	req := httptest.NewRequest(http.MethodPost, "/installations", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || len(store.configs) != 1 {
		t.Fatalf("create: %d %+v", res.Code, store.configs)
	}
	if store.configs[0].State != "ready" || len(builder.calls) != 1 || builder.calls[0].AnswerID != 4 {
		t.Fatalf("automatic preparation: configs=%+v calls=%+v", store.configs, builder.calls)
	}
	if store.configs[0].Author != store.user.Username {
		t.Fatalf("expected config author %q, got %+v", store.user.Username, store.configs[0])
	}
	if !strings.Contains(strings.Join(store.audits, "\n"), "driver-config.create|ok|id=1 profile=17") {
		t.Fatalf("expected driver-config.create audit, got %v", store.audits)
	}
}

func TestDriverConfigRenameValidationAndPermissions(t *testing.T) {
	store := configFixture()
	store.user = User{ID: 7, Username: "vlad", Role: "operator", Enabled: true}
	store.configs = []DriverConfig{{ID: 1, ProfileID: 17, Title: "Laptop A", State: "ready"}}
	s := &Server{Store: store}

	// Operator can rename to Russian UTF-8 up to 80 runes.
	res := menuPost(t, s, "/drivers/configs/1/title", url.Values{"title": {"  Конфигурация для офиса № 1  "}})
	loc, _ := url.QueryUnescape(res.Header().Get("Location"))
	if res.Code != http.StatusSeeOther || loc != "/drivers?message=Имя конфигурации сохранено" {
		t.Fatalf("operator rename: code=%d loc=%q body=%s", res.Code, loc, res.Body.String())
	}
	if store.configs[0].Title != "Конфигурация для офиса № 1" {
		t.Fatalf("title not updated: %q", store.configs[0].Title)
	}
	if !strings.Contains(strings.Join(store.audits, "\n"), "driver-config.rename|ok|id=1 title=Конфигурация для офиса № 1") {
		t.Fatalf("rename audit missing: %v", store.audits)
	}

	// Exactly 80 runes succeeds; 81 runes, empty, or control chars fail with 400.
	maxTitle := strings.Repeat("я", 80)
	if got := menuPost(t, s, "/drivers/configs/1/title", url.Values{"title": {maxTitle}}); got.Code != http.StatusSeeOther {
		t.Fatalf("80 runes rejected: %d", got.Code)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("я", 81), "bad\ntitle"} {
		if got := menuPost(t, s, "/drivers/configs/1/title", url.Values{"title": {bad}}); got.Code != http.StatusBadRequest {
			t.Fatalf("invalid title %q got %d, want 400", bad, got.Code)
		}
	}

	// Missing ID returns 404.
	if got := menuPost(t, s, "/drivers/configs/999/title", url.Values{"title": {"Valid"}}); got.Code != http.StatusNotFound {
		t.Fatalf("missing config got %d, want 404", got.Code)
	}

	// Admin succeeds; observer is forbidden.
	store.user.Role = "admin"
	if got := menuPost(t, s, "/drivers/configs/1/title", url.Values{"title": {"Admin Renamed"}}); got.Code != http.StatusSeeOther {
		t.Fatalf("admin rename got %d", got.Code)
	}
	store.user.Role = "observer"
	if got := menuPost(t, s, "/drivers/configs/1/title", url.Values{"title": {"Blocked"}}); got.Code != http.StatusForbidden {
		t.Fatalf("observer rename got %d, want 403", got.Code)
	}
}

func TestDriverConfigRussianTitlePassesMenuValidation(t *testing.T) {
	profile := WindowsProfile{
		ID:                   17,
		State:                "ready",
		InstallFile:          "install.wim",
		WindowsProfileSource: WindowsProfileSource{Title: "Windows 11", Folder: "Windows"},
	}
	cfg := DriverConfig{
		ID:          1,
		ProfileID:   17,
		Title:       "Конфигурация для ноутбуков (офис)",
		Folder:      "Windows",
		State:       "ready",
		PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("a", 64), INFPath: "net.inf"}},
		Build: agent.DriverBuildResult{
			WIMKey:       strings.Repeat("a", 64),
			WIMSHA256:    strings.Repeat("b", 64),
			ScriptSHA256: strings.Repeat("c", 64),
		},
	}
	inst := Installation{ID: 10, Title: "Windows 11 Office", Folder: "Windows", DriverConfigID: 1}
	entry, _, err := windowsMenuProfile(inst, profile.ID, "http://10.30.0.10:8080", []WindowsProfile{profile}, nil, []DriverConfig{cfg})
	if err != nil {
		t.Fatalf("russian DriverConfig title broke windowsMenuProfile: %v", err)
	}
	if entry.driverConfigID != 1 {
		t.Fatalf("unexpected entry: %+v", entry)
	}
}

func TestDriverConfigAuthorAndRenameMigration(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()

	var uVlad, uFonwa int64
	if err := store.Pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES('vlad','unused','admin') RETURNING id`).Scan(&uVlad); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,role) VALUES('fonwa','unused','admin') RETURNING id`).Scan(&uFonwa); err != nil {
		t.Fatal(err)
	}
	p, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 7, Path: "win11.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Windows 11"})
	if err != nil {
		t.Fatal(err)
	}
	boots, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var bootID int64
	for _, b := range boots {
		if b.WindowsProfileID == p.ID {
			bootID = b.ID
		}
	}

	c1, err := store.CreateDriverConfig(ctx, DriverConfig{ProfileID: p.ID, Title: "Direct Config", Folder: "Windows", PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("a", 64), INFPath: "net.inf"}}})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := store.CreateDriverConfig(ctx, DriverConfig{ProfileID: p.ID, Title: "Inferred Config", Folder: "Windows", PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("b", 64), INFPath: "scsi.inf"}}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := store.CreateInstallation(ctx, Installation{BootProfileID: bootID, Title: "Win 11 Inferred", Folder: "Windows", DriverConfigID: c2.ID})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Audit(ctx, uVlad, "driver-config.create", "ok", fmt.Sprintf("id=%d", c1.ID)); err != nil {
		t.Fatal(err)
	}
	if err := store.Audit(ctx, uFonwa, "installation.create", "ok", fmt.Sprintf("install_%d", inst.ID)); err != nil {
		t.Fatal(err)
	}

	// Simulate pre-migration schema without author columns.
	if _, err := store.Pool.Exec(ctx, `ALTER TABLE winpe_driver_configs DROP COLUMN author, DROP COLUMN author_inferred`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/2026-11-16-winpe-config-author-name.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}

	got1, err := store.DriverConfig(ctx, c1.ID)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := store.DriverConfig(ctx, c2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got1.Author != "vlad" || got1.AuthorInferred || got1.CreatedAt.IsZero() {
		t.Fatalf("c1 author mismatch: %+v", got1)
	}
	if got2.Author != "fonwa" || !got2.AuthorInferred || got2.CreatedAt.IsZero() {
		t.Fatalf("c2 inferred author mismatch: %+v", got2)
	}

	// SetDriverConfigTitle updates title and returns pgx.ErrNoRows for unknown ID.
	if err := store.SetDriverConfigTitle(ctx, c1.ID, "Новое русское имя"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.DriverConfig(ctx, c1.ID)
	if err != nil || updated.Title != "Новое русское имя" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	if err := store.SetDriverConfigTitle(ctx, 999999, "Nope"); err != pgx.ErrNoRows {
		t.Fatalf("missing ID err=%v, want pgx.ErrNoRows", err)
	}

	// CreateDriverConfig persists author directly; ResourceAuthors reads driver-config.create.
	c3, err := store.CreateDriverConfig(ctx, DriverConfig{ProfileID: p.ID, Title: "Explicit Author", Author: "vlad", PackageINFs: []agent.SelectedINF{{PackageSHA256: strings.Repeat("c", 64), INFPath: "net.inf"}}})
	if err != nil {
		t.Fatal(err)
	}
	got3, err := store.DriverConfig(ctx, c3.ID)
	if err != nil || got3.Author != "vlad" || got3.AuthorInferred {
		t.Fatalf("c3=%+v err=%v", got3, err)
	}
	authors, err := store.ResourceAuthors(ctx)
	if err != nil || authors.DriverConfigs[c1.ID] != "vlad" {
		t.Fatalf("ResourceAuthors DriverConfigs=%+v err=%v", authors.DriverConfigs, err)
	}
}
