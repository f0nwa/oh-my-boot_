package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/drivers"
)

type driverConfigStore struct {
	driverPageStore
	profiles      []WindowsProfile
	answers       []WindowsAnswerVariant
	configs       []DriverConfig
	installations []Installation
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
}
