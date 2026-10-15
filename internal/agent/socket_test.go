package agent

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestListenUnixReplacesStaleSocketButNotRegularFile(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "pxe-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "agent.sock")
	first, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	first.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := ListenUnix(path)
	if err != nil {
		t.Fatalf("stale socket was not replaced: %v", err)
	}
	listener.Close()
	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(regular); err == nil {
		t.Fatal("regular file was replaced")
	}
	if got, _ := os.ReadFile(regular); string(got) != "keep me" {
		t.Fatalf("regular file changed: %q", got)
	}
}
