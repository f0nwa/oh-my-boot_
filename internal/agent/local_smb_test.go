package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeCommandCall struct {
	name  string
	args  []string
	input string
}

type fakeLocalSMBRunner struct {
	mu    sync.Mutex
	calls []fakeCommandCall
}

func (f *fakeLocalSMBRunner) Run(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCommandCall{
		name:  name,
		args:  append([]string(nil), args...),
		input: input,
	})
	return nil, nil
}

func (f *fakeLocalSMBRunner) findCalls(name string) []fakeCommandCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matches []fakeCommandCall
	for _, c := range f.calls {
		if c.name == name {
			matches = append(matches, c)
		}
	}
	return matches
}

func (f *fakeLocalSMBRunner) hasCallWithArgs(name string, expectedArgs ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.name != name {
			continue
		}
		if len(c.args) != len(expectedArgs) {
			continue
		}
		match := true
		for i, arg := range expectedArgs {
			if c.args[i] != arg {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestLocalSMBManagerDisabledKeepsOmbisosIsolated(t *testing.T) {
	dir := t.TempDir()
	smbConf := filepath.Join(dir, "smb.conf")
	userMap := filepath.Join(dir, "samba-usermap.conf")
	netState := filepath.Join(dir, "network.json")

	netJSON := `{
		"mode": "full",
		"interface": "ens19",
		"subnet": "10.30.0.0/24",
		"server_ip": "10.30.0.10"
	}`
	if err := os.WriteFile(netState, []byte(netJSON), 0644); err != nil {
		t.Fatalf("write network state: %v", err)
	}

	runner := &fakeLocalSMBRunner{}
	manager := &LocalSMBManager{
		SMBConfPath:      smbConf,
		UserMapPath:      userMap,
		NetworkStatePath: netState,
		LocalISORoot:     "/var/lib/omb-images/iso",
		Runner:           runner,
	}

	err := manager.ApplyConfig(context.Background(), LocalSMBConfig{
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("ApplyConfig failed: %v", err)
	}

	content, err := os.ReadFile(smbConf)
	if err != nil {
		t.Fatalf("read smb.conf: %v", err)
	}
	confStr := string(content)

	if !strings.Contains(confStr, "interfaces = lo ens19") {
		t.Errorf("smb.conf missing 'interfaces = lo ens19', got:\n%s", confStr)
	}
	if !strings.Contains(confStr, "bind interfaces only = yes") {
		t.Errorf("smb.conf missing 'bind interfaces only = yes'")
	}
	if !strings.Contains(confStr, "hosts allow = 127.0.0.1 10.30.0.0/24") {
		t.Errorf("smb.conf missing global 'hosts allow = 127.0.0.1 10.30.0.0/24'")
	}
	if !strings.Contains(confStr, "[ombisos]") {
		t.Errorf("smb.conf missing [ombisos] section")
	}
	ombisosIdx := strings.Index(confStr, "[ombisos]")
	ombisosSection := confStr[ombisosIdx:]
	if !strings.Contains(ombisosSection, "hosts allow = 127.0.0.1 10.30.0.0/24") {
		t.Errorf("[ombisos] missing 'hosts allow = 127.0.0.1 10.30.0.0/24'")
	}
	if strings.Contains(confStr, "[iso]") {
		t.Errorf("smb.conf should not contain [iso] share when disabled")
	}

	if !runner.hasCallWithArgs("smbcontrol", "smbd", "close-share", "iso") {
		t.Errorf("expected runner to call 'smbcontrol smbd close-share iso', calls: %+v", runner.calls)
	}
	if !runner.hasCallWithArgs("smbcontrol", "smbd", "reload-config") {
		t.Errorf("expected runner to call 'smbcontrol smbd reload-config', calls: %+v", runner.calls)
	}
}

func TestLocalSMBManagerEnabledRendersShareAndUserMap(t *testing.T) {
	dir := t.TempDir()
	smbConf := filepath.Join(dir, "smb.conf")
	userMap := filepath.Join(dir, "samba-usermap.conf")
	netState := filepath.Join(dir, "network.json")

	netJSON := `{
		"mode": "full",
		"interface": "ens19",
		"subnet": "10.30.0.0/24",
		"server_ip": "10.30.0.10"
	}`
	if err := os.WriteFile(netState, []byte(netJSON), 0644); err != nil {
		t.Fatalf("write network state: %v", err)
	}

	runner := &fakeLocalSMBRunner{}
	manager := &LocalSMBManager{
		SMBConfPath:      smbConf,
		UserMapPath:      userMap,
		NetworkStatePath: netState,
		LocalISORoot:     "/var/lib/omb-images/iso",
		Runner:           runner,
	}

	err := manager.ApplyConfig(context.Background(), LocalSMBConfig{
		Enabled:  true,
		ListenIP: "192.168.10.135",
		Users: []LocalSMBUser{
			{ID: 1, Username: "admin", Mode: "rw"},
			{ID: 2, Username: "engineer", Mode: "ro"},
		},
	})
	if err != nil {
		t.Fatalf("ApplyConfig failed: %v", err)
	}

	content, err := os.ReadFile(smbConf)
	if err != nil {
		t.Fatalf("read smb.conf: %v", err)
	}
	confStr := string(content)

	if !strings.Contains(confStr, "interfaces = lo ens19 192.168.10.135") {
		t.Errorf("smb.conf missing 'interfaces = lo ens19 192.168.10.135', got:\n%s", confStr)
	}
	if !strings.Contains(confStr, "bind interfaces only = yes") {
		t.Errorf("smb.conf missing 'bind interfaces only = yes'")
	}
	if !strings.Contains(confStr, "username map = "+userMap) {
		t.Errorf("smb.conf missing 'username map = %s'", userMap)
	}

	ombisosIdx := strings.Index(confStr, "[ombisos]")
	if ombisosIdx == -1 {
		t.Fatalf("smb.conf missing [ombisos]")
	}
	isoIdx := strings.Index(confStr, "[iso]")
	if isoIdx == -1 {
		t.Fatalf("smb.conf missing [iso]")
	}
	ombisosSection := confStr[ombisosIdx:isoIdx]
	if !strings.Contains(ombisosSection, "hosts allow = 127.0.0.1 10.30.0.0/24") {
		t.Errorf("[ombisos] missing 'hosts allow = 127.0.0.1 10.30.0.0/24'")
	}

	isoSection := confStr[isoIdx:]
	expectedISOLines := []string{
		"path = /var/lib/omb-images/iso",
		"browseable = yes",
		"read only = yes",
		"guest ok = no",
		"valid users = omb-fs-1 omb-fs-2",
		"read list = omb-fs-1 omb-fs-2",
		"write list = omb-fs-1",
		"force user = omb-images",
		"force group = omb-images",
		"create mask = 0644",
		"force create mode = 0644",
		"directory mask = 2770",
		"force directory mode = 2770",
		"veto files = /._*/.DS_Store/Thumbs.db/desktop.ini/",
		"delete veto files = yes",
	}
	for _, expected := range expectedISOLines {
		if !strings.Contains(isoSection, expected) {
			t.Errorf("[iso] section missing %q, got:\n%s", expected, isoSection)
		}
	}

	userMapContent, err := os.ReadFile(userMap)
	if err != nil {
		t.Fatalf("read userMap: %v", err)
	}
	userMapStr := string(userMapContent)
	if !strings.Contains(userMapStr, `!omb-fs-1 = "admin"`) {
		t.Errorf("user map missing '!omb-fs-1 = \"admin\"', got:\n%s", userMapStr)
	}
	if !strings.Contains(userMapStr, `!omb-fs-2 = "engineer"`) {
		t.Errorf("user map missing '!omb-fs-2 = \"engineer\"', got:\n%s", userMapStr)
	}

	if !runner.hasCallWithArgs("smbcontrol", "smbd", "reload-config") {
		t.Errorf("expected runner to call 'smbcontrol smbd reload-config'")
	}
}

func TestLocalSMBManagerReadOnlyAllOmitsWriteList(t *testing.T) {
	dir := t.TempDir()
	smbConf := filepath.Join(dir, "smb.conf")
	userMap := filepath.Join(dir, "samba-usermap.conf")
	netState := filepath.Join(dir, "network.json")

	netJSON := `{
		"mode": "full",
		"interface": "ens19",
		"subnet": "10.30.0.0/24",
		"server_ip": "10.30.0.10"
	}`
	if err := os.WriteFile(netState, []byte(netJSON), 0644); err != nil {
		t.Fatalf("write network state: %v", err)
	}

	runner := &fakeLocalSMBRunner{}
	manager := &LocalSMBManager{
		SMBConfPath:      smbConf,
		UserMapPath:      userMap,
		NetworkStatePath: netState,
		LocalISORoot:     "/var/lib/omb-images/iso",
		Runner:           runner,
	}

	err := manager.ApplyConfig(context.Background(), LocalSMBConfig{
		Enabled:     true,
		ListenIP:    "192.168.10.135",
		ReadOnlyAll: true,
		Users: []LocalSMBUser{
			{ID: 1, Username: "admin", Mode: "rw"},
			{ID: 2, Username: "engineer", Mode: "ro"},
		},
	})
	if err != nil {
		t.Fatalf("ApplyConfig failed: %v", err)
	}

	content, err := os.ReadFile(smbConf)
	if err != nil {
		t.Fatalf("read smb.conf: %v", err)
	}
	confStr := string(content)

	isoIdx := strings.Index(confStr, "[iso]")
	if isoIdx == -1 {
		t.Fatalf("smb.conf missing [iso]")
	}
	isoSection := confStr[isoIdx:]

	if !strings.Contains(isoSection, "read only = yes") {
		t.Errorf("[iso] missing 'read only = yes'")
	}
	if strings.Contains(isoSection, "write list") {
		t.Errorf("[iso] should omit 'write list' when ReadOnlyAll=true, got:\n%s", isoSection)
	}
	if !strings.Contains(isoSection, "valid users = omb-fs-1 omb-fs-2") {
		t.Errorf("[iso] missing 'valid users = omb-fs-1 omb-fs-2'")
	}
	if !strings.Contains(isoSection, "read list = omb-fs-1 omb-fs-2") {
		t.Errorf("[iso] missing 'read list = omb-fs-1 omb-fs-2'")
	}
}

func TestLocalSMBManagerSetPasswordUsesStdinOnly(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeLocalSMBRunner{}
	manager := &LocalSMBManager{
		SMBConfPath:      filepath.Join(dir, "smb.conf"),
		UserMapPath:      filepath.Join(dir, "samba-usermap.conf"),
		NetworkStatePath: filepath.Join(dir, "network.json"),
		Runner:           runner,
	}

	secret := "SuperSecretPassword123!"
	err := manager.SetPassword(context.Background(), LocalSMBPasswordRequest{
		ID:       2,
		Username: "engineer",
		Password: secret,
	})
	if err != nil {
		t.Fatalf("SetPassword failed: %v", err)
	}

	smbpasswdCalls := runner.findCalls("smbpasswd")
	if len(smbpasswdCalls) == 0 {
		t.Fatalf("expected smbpasswd command to be called")
	}

	call := smbpasswdCalls[0]
	expectedArgs := []string{"-a", "-s", "omb-fs-2"}
	if len(call.args) != len(expectedArgs) {
		t.Fatalf("smbpasswd called with args %v, expected %v", call.args, expectedArgs)
	}
	for i, arg := range expectedArgs {
		if call.args[i] != arg {
			t.Errorf("smbpasswd arg %d: got %s, expected %s", i, call.args[i], arg)
		}
	}

	expectedInput := fmt.Sprintf("%s\n%s\n", secret, secret)
	if call.input != expectedInput {
		t.Errorf("smbpasswd input got %q, expected %q", call.input, expectedInput)
	}

	// Verify the secret was NEVER passed in command line arguments of ANY command
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for _, c := range runner.calls {
		for _, arg := range c.args {
			if strings.Contains(arg, secret) {
				t.Fatalf("password leaked into command line argument of %s: %s", c.name, arg)
			}
		}
	}
}

func TestLocalSMBHTTPHandlersValidation(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeLocalSMBRunner{}
	manager := &LocalSMBManager{
		SMBConfPath:      filepath.Join(dir, "smb.conf"),
		UserMapPath:      filepath.Join(dir, "samba-usermap.conf"),
		NetworkStatePath: filepath.Join(dir, "network.json"),
		Runner:           runner,
	}

	handler := HandlerWithLocalSMB(http.NotFoundHandler(), manager)

	// Test PUT /local-smb/config validation
	t.Run("ConfigValidation", func(t *testing.T) {
		testCases := []struct {
			name       string
			payload    any
			wantStatus int
		}{
			{
				name:       "invalid JSON",
				payload:    "invalid",
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "invalid user ID",
				payload: LocalSMBConfig{
					Enabled: true,
					Users: []LocalSMBUser{
						{ID: 0, Username: "admin", Mode: "rw"},
					},
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "empty username",
				payload: LocalSMBConfig{
					Enabled: true,
					Users: []LocalSMBUser{
						{ID: 1, Username: "", Mode: "rw"},
					},
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "invalid username characters",
				payload: LocalSMBConfig{
					Enabled: true,
					Users: []LocalSMBUser{
						{ID: 1, Username: "admin;evil", Mode: "rw"},
					},
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "username too short",
				payload: LocalSMBConfig{
					Enabled: true,
					Users: []LocalSMBUser{
						{ID: 1, Username: "ab", Mode: "rw"},
					},
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "invalid mode",
				payload: LocalSMBConfig{
					Enabled: true,
					Users: []LocalSMBUser{
						{ID: 1, Username: "admin", Mode: "invalid"},
					},
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "valid disabled config",
				payload: LocalSMBConfig{
					Enabled: false,
				},
				wantStatus: http.StatusNoContent,
			},
			{
				name: "valid enabled config",
				payload: LocalSMBConfig{
					Enabled:  true,
					ListenIP: "192.168.10.135",
					Users: []LocalSMBUser{
						{ID: 1, Username: "admin", Mode: "rw"},
						{ID: 2, Username: "engineer", Mode: "ro"},
					},
				},
				wantStatus: http.StatusNoContent,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				var body []byte
				switch p := tc.payload.(type) {
				case string:
					body = []byte(p)
				default:
					b, err := json.Marshal(p)
					if err != nil {
						t.Fatalf("marshal payload: %v", err)
					}
					body = b
				}

				req := httptest.NewRequest(http.MethodPut, "/local-smb/config", bytes.NewReader(body))
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code != tc.wantStatus {
					t.Errorf("PUT /local-smb/config returned status %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
				}
			})
		}
	})

	// Test POST /local-smb/password validation
	t.Run("PasswordValidation", func(t *testing.T) {
		testCases := []struct {
			name       string
			payload    any
			wantStatus int
		}{
			{
				name:       "invalid JSON",
				payload:    "invalid",
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "invalid user ID",
				payload: LocalSMBPasswordRequest{
					ID:       0,
					Username: "engineer",
					Password: "SuperSecretPassword123!",
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "empty username",
				payload: LocalSMBPasswordRequest{
					ID:       2,
					Username: "",
					Password: "SuperSecretPassword123!",
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "short password",
				payload: LocalSMBPasswordRequest{
					ID:       2,
					Username: "engineer",
					Password: "short",
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "password with newline",
				payload: LocalSMBPasswordRequest{
					ID:       2,
					Username: "engineer",
					Password: "SuperSecret\nPassword123!",
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "password with return",
				payload: LocalSMBPasswordRequest{
					ID:       2,
					Username: "engineer",
					Password: "SuperSecret\rPassword123!",
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "invalid username characters",
				payload: LocalSMBPasswordRequest{
					ID:       2,
					Username: "engineer with spaces",
					Password: "SuperSecretPassword123!",
				},
				wantStatus: http.StatusBadRequest,
			},
			{
				name: "valid password",
				payload: LocalSMBPasswordRequest{
					ID:       2,
					Username: "engineer",
					Password: "SuperSecretPassword123!",
				},
				wantStatus: http.StatusNoContent,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				var body []byte
				switch p := tc.payload.(type) {
				case string:
					body = []byte(p)
				default:
					b, err := json.Marshal(p)
					if err != nil {
						t.Fatalf("marshal payload: %v", err)
					}
					body = b
				}

				req := httptest.NewRequest(http.MethodPost, "/local-smb/password", bytes.NewReader(body))
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				if rec.Code != tc.wantStatus {
					t.Errorf("POST /local-smb/password returned status %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
				}
			})
		}
	})

	// Test nil manager
	t.Run("NilManager", func(t *testing.T) {
		nilHandler := HandlerWithLocalSMB(http.NotFoundHandler(), nil)
		req := httptest.NewRequest(http.MethodPut, "/local-smb/config", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		nilHandler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("expected 503 for nil manager, got %d", rec.Code)
		}
	})
}

func TestLocalSMBManagerZeroValidUsersClosesShare(t *testing.T) {
	dir := t.TempDir()
	smbConf := filepath.Join(dir, "smb.conf")
	userMap := filepath.Join(dir, "samba-usermap.conf")
	netState := filepath.Join(dir, "network.json")
	isoRoot := filepath.Join(dir, "iso")

	netJSON := `{
		"mode": "full",
		"interface": "ens19",
		"subnet": "10.30.0.0/24",
		"server_ip": "10.30.0.10"
	}`
	if err := os.WriteFile(netState, []byte(netJSON), 0644); err != nil {
		t.Fatalf("write network state: %v", err)
	}

	runner := &fakeLocalSMBRunner{}
	manager := &LocalSMBManager{
		SMBConfPath:      smbConf,
		UserMapPath:      userMap,
		NetworkStatePath: netState,
		LocalISORoot:     isoRoot,
		Runner:           runner,
	}

	// Enabled=true, but Users is empty (zero valid users)
	err := manager.ApplyConfig(context.Background(), LocalSMBConfig{
		Enabled:  true,
		ListenIP: "192.168.10.135",
		Users:    []LocalSMBUser{},
	})
	if err != nil {
		t.Fatalf("ApplyConfig failed: %v", err)
	}

	// Verify iso directory was created with 0770
	info, err := os.Stat(isoRoot)
	if err != nil {
		t.Fatalf("isoRoot was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("isoRoot is not a directory")
	}

	content, err := os.ReadFile(smbConf)
	if err != nil {
		t.Fatalf("read smb.conf: %v", err)
	}
	confStr := string(content)

	if !strings.Contains(confStr, "available = no") {
		t.Errorf("smb.conf [iso] missing 'available = no' when validUsers is empty, got:\n%s", confStr)
	}

	// Verify runner called close-share iso AND reload-config
	if !runner.hasCallWithArgs("smbcontrol", "smbd", "close-share", "iso") {
		t.Errorf("expected close-share iso to be called when valid users is empty, calls: %+v", runner.calls)
	}
	if !runner.hasCallWithArgs("smbcontrol", "smbd", "reload-config") {
		t.Errorf("expected reload-config to be called, calls: %+v", runner.calls)
	}
}
