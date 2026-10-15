package control

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBootClientReadsStatusOverUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "pxe-api-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "boot.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ready":true,"sha256":"abc"}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	client := NewBootClient(path)
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.SHA256 != "abc" {
		t.Fatalf("status=%+v", status)
	}
}

func TestBootClientReportsMissingSocket(t *testing.T) {
	client := NewBootClient(filepath.Join(t.TempDir(), "missing.sock"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Status(ctx); err == nil {
		t.Fatal("missing service reported healthy")
	}
}

func TestBootClientRejectsOversizedPublish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unused.sock")
	client := NewBootClient(path)
	if _, err := client.Publish(context.Background(), make([]byte, 256<<10+1)); err == nil {
		t.Fatal("oversized menu accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected socket: %v", err)
	}
}
