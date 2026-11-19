package control

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"omb/internal/agent"
)

func TestAgentClientLocalSMB(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	var appliedConfig agent.LocalSMBConfig
	var setPasswordReq agent.LocalSMBPasswordRequest

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/local-smb/config":
			if err := json.NewDecoder(r.Body).Decode(&appliedConfig); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/local-smb/password":
			if err := json.NewDecoder(r.Body).Decode(&setPasswordReq); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	client := NewAgentClient(sock)

	// Verify interface compliance
	var api LocalSMBAPI = client

	cfg := agent.LocalSMBConfig{
		Enabled:     true,
		ListenIP:    "192.168.10.135",
		ReadOnlyAll: false,
		Users: []agent.LocalSMBUser{
			{ID: 1, Username: "admin", Mode: "rw"},
		},
	}
	if err := api.ApplyLocalSMB(context.Background(), cfg); err != nil {
		t.Fatalf("ApplyLocalSMB error: %v", err)
	}
	if !appliedConfig.Enabled || appliedConfig.ListenIP != "192.168.10.135" || len(appliedConfig.Users) != 1 {
		t.Fatalf("unexpected applied config: %+v", appliedConfig)
	}

	pwReq := agent.LocalSMBPasswordRequest{
		ID:       1,
		Username: "admin",
		Password: "SecretPassword123!",
	}
	if err := api.SetLocalSMBPassword(context.Background(), pwReq); err != nil {
		t.Fatalf("SetLocalSMBPassword error: %v", err)
	}
	if setPasswordReq.ID != 1 || setPasswordReq.Username != "admin" || setPasswordReq.Password != "SecretPassword123!" {
		t.Fatalf("unexpected password request: %+v", setPasswordReq)
	}
}
