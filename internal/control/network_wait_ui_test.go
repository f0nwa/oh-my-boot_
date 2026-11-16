package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/network"
)

type networkWaitTestRunner struct{}

func (networkWaitTestRunner) Test(context.Context, string) error { return nil }
func (networkWaitTestRunner) Restart(context.Context) error      { return nil }
func (networkWaitTestRunner) Reload(context.Context) error       { return nil }
func (networkWaitTestRunner) Stop(context.Context) error         { return nil }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func inMemoryAgentClient(h http.Handler) *AgentClient {
	return &AgentClient{
		http: &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, r)
				return rec.Result(), nil
			}),
		},
	}
}

type networkWaitStore struct {
	authOnlyStore
	profiles []WindowsProfile
	configs  []DriverConfig
	versions []MenuVersion
}

func (s *networkWaitStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return s.profiles, nil
}

func (s *networkWaitStore) DriverConfigs(context.Context) ([]DriverConfig, error) {
	return s.configs, nil
}

func (s *networkWaitStore) SyncWinPEDriverScripts(_ context.Context, updates []agent.DriverScriptUpdate) (string, error) {
	byID := make(map[int64]agent.DriverScriptUpdate, len(updates))
	for _, u := range updates {
		byID[u.ConfigID] = u
	}
	for i := range s.configs {
		if u, ok := byID[s.configs[i].ID]; ok {
			s.configs[i].Build.ScriptSHA256 = u.NewSHA
		}
	}
	var activeScript string
	for i := range s.versions {
		updated, changed, err := updateMenuDriverScriptHashes(s.versions[i].Script, updates)
		if err != nil {
			return "", err
		}
		if changed {
			s.versions[i].Script = updated
			sum := sha256.Sum256([]byte(updated))
			s.versions[i].SHA256 = hex.EncodeToString(sum[:])
		}
		if s.versions[i].Active {
			activeScript = s.versions[i].Script
		}
	}
	return activeScript, nil
}

func TestNetworkWaitFormAndPreview(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		data := samplePageData(lang)
		data.HasNetwork = true
		data.Network.WinPETimeoutSeconds = 120
		data.Network.WinPERetrySeconds = 3
		data.Network.WinPEExitAction = network.WinPEExitActionRebootTimer
		data.OldNetwork.WinPETimeoutSeconds = 90
		data.OldNetwork.WinPERetrySeconds = 2
		data.OldNetwork.WinPEExitAction = network.WinPEExitActionCMD

		page := httptest.NewRecorder()
		render(page, "network", data)
		body := page.Body.String()
		for _, want := range []string{
			`id="winpe-options"`,
			`name="winpe_timeout"`,
			`value="120"`,
			`name="winpe_retry"`,
			`value="3"`,
			`name="winpe_exit_action"`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("lang=%s network page missing %q:\n%s", lang, want, body)
			}
		}

		preview := httptest.NewRecorder()
		render(preview, "network_preview", data)
		pbody := preview.Body.String()
		for _, want := range []string{
			`name="winpe_timeout" value="120"`,
			`name="winpe_retry" value="3"`,
			`name="winpe_exit_action" value="reboot_timer"`,
			`45`,
			`40`,
		} {
			if !strings.Contains(pbody, want) {
				t.Fatalf("lang=%s preview page missing %q:\n%s", lang, want, pbody)
			}
		}
	}

	req := httptest.NewRequest("POST", "/network/preview", strings.NewReader("winpe_timeout=150&winpe_retry=5&winpe_exit_action=reboot_timer"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cfg := networkForm(req)
	if cfg.WinPETimeoutSeconds != 150 || cfg.WinPERetrySeconds != 5 || cfg.WinPEExitAction != "reboot_timer" {
		t.Fatalf("parsed form = %+v", cfg)
	}

	badReq := httptest.NewRequest("POST", "/network/preview", strings.NewReader("winpe_timeout=abc&winpe_retry=2"))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badCfg := networkForm(badReq)
	if err := network.Validate(badCfg); err == nil {
		t.Fatal("accepted non-numeric winpe_timeout")
	}

	badActionReq := httptest.NewRequest("POST", "/network/preview", strings.NewReader("winpe_timeout=120&winpe_retry=2&winpe_exit_action=invalid_action"))
	badActionReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badActionCfg := networkForm(badActionReq)
	if err := network.Validate(badActionCfg); err == nil {
		t.Fatal("accepted invalid winpe_exit_action")
	}
}

func TestNetworkWaitApplyBlocksWhenPreparingAndUpdatesDriverHashes(t *testing.T) {
	origIPs := InterfaceIPs
	InterfaceIPs = func(string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.0.2.10")}, nil
	}
	defer func() { InterfaceIPs = origIPs }()

	dir := t.TempDir()
	applier, err := agent.NewApplier(
		filepath.Join(dir, "dnsmasq.conf"),
		filepath.Join(dir, "state.json"),
		filepath.Join(dir, "dhcp", "hosts"),
		networkWaitTestRunner{},
	)
	if err != nil {
		t.Fatal(err)
	}

	rawTemplate, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatal(err)
	}
	fallbackTemplate := filepath.Join(dir, "share", "omb-setup.cmd.in")
	customTemplate := filepath.Join(dir, "etc", "omb-setup.cmd.in")
	if err := os.MkdirAll(filepath.Dir(fallbackTemplate), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallbackTemplate, rawTemplate, 0o644); err != nil {
		t.Fatal(err)
	}
	driversRoot := filepath.Join(dir, "tftp", "windows-driver-configs")
	if err := os.MkdirAll(filepath.Join(driversRoot, "4"), 0o755); err != nil {
		t.Fatal(err)
	}
	driverScript := strings.ReplaceAll(string(rawTemplate), "__OMB_ISO_PASSWORD__", strings.Repeat("A", 32))
	driverScript = strings.ReplaceAll(driverScript, "__OMB_PROFILE_ID__", "17")
	driverScript = strings.ReplaceAll(driverScript, "__OMB_INSTALL_FILE__", "install.wim")
	oldSum := sha256.Sum256([]byte(driverScript))
	oldSHA := hex.EncodeToString(oldSum[:])
	if err := os.WriteFile(filepath.Join(driversRoot, "4", "omb-setup.cmd"), []byte(driverScript), 0o640); err != nil {
		t.Fatal(err)
	}
	manifestBytes, _ := json.Marshal(struct {
		Request agent.DriverBuildRequest `json:"request"`
		Result  agent.DriverBuildResult  `json:"result"`
	}{
		Request: agent.DriverBuildRequest{ConfigID: 4, ProfileID: 17},
		Result:  agent.DriverBuildResult{WIMKey: strings.Repeat("b", 64), WIMSHA256: strings.Repeat("c", 64), ScriptSHA256: oldSHA},
	})
	if err := os.WriteFile(filepath.Join(driversRoot, "4", "build.json"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	applier.WinPE = &agent.WinPEWaitUpdater{
		CustomTemplatePath:  customTemplate,
		DefaultTemplatePath: fallbackTemplate,
		DriverConfigRoot:    driversRoot,
	}

	refs := []menuDriverConfigRef{{
		ID:           4,
		ProfileID:    17,
		WIMKey:       strings.Repeat("b", 64),
		WIMSHA256:    strings.Repeat("c", 64),
		ScriptSHA256: oldSHA,
	}}
	refsJSON, _ := json.Marshal(refs)
	menuScript := "#!ipxe\n" + menuDriverMarker + base64.RawURLEncoding.EncodeToString(refsJSON) + "\n"

	store := &networkWaitStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Username: "admin", Role: "admin", Enabled: true}},
		configs: []DriverConfig{{
			ID:        4,
			ProfileID: 17,
			State:     "preparing",
			Build:     agent.DriverBuildResult{WIMKey: strings.Repeat("b", 64), WIMSHA256: strings.Repeat("c", 64), ScriptSHA256: oldSHA},
		}},
		versions: []MenuVersion{{
			ID:     10,
			Active: true,
			Script: menuScript,
		}},
	}
	controlServer := &Server{
		Store:             store,
		Agent:             inMemoryAgentClient(agent.Handler(applier)),
		AllowedInterfaces: map[string]bool{"enp2s0": true},
	}

	cfg := network.Config{
		Mode: "proxy", Interface: "enp2s0", ServerIP: "192.0.2.10",
		Subnet: "192.0.2.0/24", BootURL: "http://192.0.2.10:8080/boot/menu.ipxe",
		TFTPRoot: "/srv/omb/tftp", WinPETimeoutSeconds: 120, WinPERetrySeconds: 3,
	}
	form := url.Values{
		"mode":          {cfg.Mode},
		"interface":     {cfg.Interface},
		"server_ip":     {cfg.ServerIP},
		"subnet":        {cfg.Subnet},
		"boot_url":      {cfg.BootURL},
		"tftp_root":     {cfg.TFTPRoot},
		"winpe_timeout": {"120"},
		"winpe_retry":   {"3"},
		"confirmation":  {networkConfirmation("test-token", cfg)},
	}

	// 1. Rejects with 409 Conflict when driver config is preparing.
	res := menuPost(t, controlServer, "/network/apply", form)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict while preparing, got %d: %s", res.Code, res.Body.String())
	}

	// 2. Succeeds when ready and updates both driver config and menu_versions.
	store.configs[0].State = "ready"
	res = menuPost(t, controlServer, "/network/apply", form)
	if res.Code != http.StatusOK {
		t.Fatalf("apply status = %d: %s", res.Code, res.Body.String())
	}
	newSHA := store.configs[0].Build.ScriptSHA256
	if newSHA == oldSHA || len(newSHA) != 64 {
		t.Fatalf("driver config ScriptSHA256 was not updated: %s", newSHA)
	}
	updatedRefs := menuDriverConfigRefs(store.versions[0].Script)
	if updatedRefs[4].ScriptSHA256 != newSHA {
		t.Fatalf("menu version driver ref ScriptSHA256 = %s, want %s", updatedRefs[4].ScriptSHA256, newSHA)
	}
}

func TestSyncWinPEDriverScriptsStore(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)

	var profileID, configID int64
	if err := store.Pool.QueryRow(ctx, `
		INSERT INTO windows_profiles(storage_id, path, storage_root, size_bytes, modified_at, title, folder, state)
		VALUES (1, 'win11.iso', '/iso', 100, now(), 'Win11', 'Windows', 'ready')
		RETURNING id
	`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	oldSHA := strings.Repeat("1", 64)
	newSHA := strings.Repeat("2", 64)
	if err := store.Pool.QueryRow(ctx, `
		INSERT INTO winpe_driver_configs(profile_id, title, folder, selected_infs, state, wim_key, wim_sha256, script_sha256)
		VALUES ($1, 'Drivers', 'Windows', '[{}]', 'ready', $2, $3, $4)
		RETURNING id
	`, profileID, strings.Repeat("a", 64), strings.Repeat("b", 64), oldSHA).Scan(&configID); err != nil {
		t.Fatal(err)
	}

	refs := []menuDriverConfigRef{{
		ID:           configID,
		ProfileID:    profileID,
		WIMKey:       strings.Repeat("a", 64),
		WIMSHA256:    strings.Repeat("b", 64),
		ScriptSHA256: oldSHA,
	}}
	refsJSON, _ := json.Marshal(refs)
	activeScript := "#!ipxe\n" + menuDriverMarker + base64.RawURLEncoding.EncodeToString(refsJSON) + "\n"
	savedScript := "#!ipxe\n# saved\n" + menuDriverMarker + base64.RawURLEncoding.EncodeToString(refsJSON) + "\n"
	if _, err := store.Pool.Exec(ctx, `
		INSERT INTO menu_versions(title, diagnostic_label, diagnostic_message, script, sha256, active)
		VALUES ('Active', 'Diag', 'Msg', $1, $2, true),
		       ('Saved', 'Diag', 'Msg', $3, $4, false)
	`, activeScript, strings.Repeat("c", 64), savedScript, strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}

	updatedActive, err := store.SyncWinPEDriverScripts(ctx, []agent.DriverScriptUpdate{{
		ConfigID: configID,
		OldSHA:   oldSHA,
		NewSHA:   newSHA,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if updatedActive == "" {
		t.Fatal("expected updatedActive script to be non-empty")
	}
	if menuDriverConfigRefs(updatedActive)[configID].ScriptSHA256 != newSHA {
		t.Fatalf("active script ref = %+v", menuDriverConfigRefs(updatedActive))
	}

	var dbSHA string
	if err := store.Pool.QueryRow(ctx, `SELECT script_sha256 FROM winpe_driver_configs WHERE id = $1`, configID).Scan(&dbSHA); err != nil {
		t.Fatal(err)
	}
	if dbSHA != newSHA {
		t.Fatalf("db script_sha256 = %s, want %s", dbSHA, newSHA)
	}

	rows, err := store.Pool.Query(ctx, `SELECT title, script, sha256 FROM menu_versions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var title, script, sha string
		if err := rows.Scan(&title, &script, &sha); err != nil {
			t.Fatal(err)
		}
		count++
		if got := menuDriverConfigRefs(script)[configID].ScriptSHA256; got != newSHA {
			t.Fatalf("version %s ref ScriptSHA256 = %s, want %s", title, got, newSHA)
		}
		sum := sha256.Sum256([]byte(script))
		if sha != hex.EncodeToString(sum[:]) {
			t.Fatalf("version %s sha256 mismatch: %s", title, sha)
		}
	}
	if count != 2 {
		t.Fatalf("versions count = %d", count)
	}

	// Simulate retry after transient DB failure where Agent already updated files (OldSHA == NewSHA)
	// while DB still holds an older SHA.
	staleSHA := strings.Repeat("9", 64)
	staleRefs := []menuDriverConfigRef{{
		ID:           configID,
		ProfileID:    profileID,
		WIMKey:       strings.Repeat("a", 64),
		WIMSHA256:    strings.Repeat("b", 64),
		ScriptSHA256: staleSHA,
	}}
	staleRefsJSON, _ := json.Marshal(staleRefs)
	staleActive := "#!ipxe\n" + menuDriverMarker + base64.RawURLEncoding.EncodeToString(staleRefsJSON) + "\n"
	if _, err := store.Pool.Exec(ctx, `UPDATE winpe_driver_configs SET script_sha256 = $1 WHERE id = $2`, staleSHA, configID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE menu_versions SET script = $1 WHERE active = true`, staleActive); err != nil {
		t.Fatal(err)
	}
	healedActive, err := store.SyncWinPEDriverScripts(ctx, []agent.DriverScriptUpdate{{
		ConfigID: configID,
		OldSHA:   newSHA,
		NewSHA:   newSHA,
	}})
	if err != nil {
		t.Fatalf("self-healing retry failed: %v", err)
	}
	if menuDriverConfigRefs(healedActive)[configID].ScriptSHA256 != newSHA {
		t.Fatalf("self-healed active script ref = %+v", menuDriverConfigRefs(healedActive))
	}
	if err := store.Pool.QueryRow(ctx, `SELECT script_sha256 FROM winpe_driver_configs WHERE id = $1`, configID).Scan(&dbSHA); err != nil {
		t.Fatal(err)
	}
	if dbSHA != newSHA {
		t.Fatalf("self-healed db script_sha256 = %s, want %s", dbSHA, newSHA)
	}
}

