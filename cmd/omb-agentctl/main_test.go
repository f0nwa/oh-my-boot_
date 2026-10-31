package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSendsConfigOverUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "omb-ctl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/apply" || !strings.Contains(string(body), `"mode":"proxy"`) {
			t.Errorf("unexpected agent request: %s %s %s", r.Method, r.URL.Path, body)
		}
		_, _ = w.Write([]byte(`{"applied":true,"mode":"proxy"}`))
	})}
	go server.Serve(listener)
	defer server.Shutdown(context.Background())
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"mode":"proxy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := run(context.Background(), socket, configPath, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"applied":true`) {
		t.Fatalf("status was not displayed: %q", output.String())
	}
}

func TestDryRunRendersWithoutAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"mode":"proxy","interface":"enp2s0","server_ip":"192.0.2.10","subnet":"192.0.2.0/24","boot_url":"http://192.0.2.10:8080/boot/menu.ipxe","tftp_root":"/srv/omb/tftp"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := dryRun(path, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "dhcp-range=192.0.2.0,proxy") {
		t.Fatalf("dry run did not render proxy mode: %s", output.String())
	}
}
