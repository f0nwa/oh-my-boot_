package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"omb/internal/boot"
	"strings"
	"testing"
	"time"
)

func TestBootClientReadsStatusOverUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "omb-api-")
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

func TestBootClientStagesAndChecksAutoinstallOverUnixSocket(t *testing.T) {
	root := t.TempDir()
	assets, err := boot.NewAutoinstallAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := boot.NewPublisher(filepath.Join(root, "menu.ipxe"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "boot.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: boot.ControlHandler(p, nil, assets)}
	go server.Serve(listener)
	defer server.Close()
	client := NewBootClient(socket)
	data := []byte("#cloud-config\nautoinstall:\n  version: 1\n")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if _, err := client.CheckAutoinstall(context.Background(), 7, digest); err == nil {
		t.Fatal("missing pair reported available")
	}
	staged, err := client.StageAutoinstall(context.Background(), 7, digest, data)
	if err != nil || staged.UserDataSHA256 != digest {
		t.Fatalf("stage=%+v err=%v", staged, err)
	}
	checked, err := client.CheckAutoinstall(context.Background(), 7, digest)
	if err != nil || checked != staged || len(checked.MetaDataSHA256) != 64 {
		t.Fatalf("check=%+v err=%v", checked, err)
	}
	if _, err := client.StageAutoinstall(context.Background(), 7, digest, []byte("changed")); err == nil {
		t.Fatal("mismatched hash accepted")
	}
	if _, err := client.CheckAutoinstall(context.Background(), 0, digest); err == nil {
		t.Fatal("invalid installation accepted")
	}
}

func TestBootClientRejectsOversizedAutoinstallBeforeDial(t *testing.T) {
	client := NewBootClient(filepath.Join(t.TempDir(), "missing.sock"))
	_, err := client.StageAutoinstall(context.Background(), 7, strings.Repeat("0", 64), make([]byte, 64<<10+1))
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversize should fail before dialing: %v", err)
	}
}

func TestBootClientEventsPaged(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "boot.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var gotPath string
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]boot.Event{
			{ID: 10, Kind: "menu", MAC: "aa:bb:cc:dd:ee:01"},
		})
	})}
	go server.Serve(listener)
	defer server.Close()

	client := NewBootClient(socket)
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	events, err := client.EventsPaged(context.Background(), 25, &now, 42, "aa:bb:cc:dd:ee:01")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != 10 {
		t.Fatalf("unexpected events: %+v", events)
	}
	if !strings.Contains(gotPath, "limit=25") || !strings.Contains(gotPath, "before_id=42") || !strings.Contains(gotPath, "mac=aa%3Abb%3Acc%3Add%3Aee%3A01") {
		t.Fatalf("unexpected request URI: %s", gotPath)
	}
}

func TestImagesClientSurvivesSlowStorageSpaceAndCommit(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "omb-img-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "images.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate statfs / SMB flush taking 2.2s (within omb-images 3s spaceTimeout).
		time.Sleep(2200 * time.Millisecond)
		switch r.URL.Path {
		case "/catalog":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"storages":[{"id":1,"name":"Local","kind":"local","root":"/iso","state":"ready"}],"images":[]}`))
		case "/images/migrations/mig_1/commit-delete-source":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})}
	go server.Serve(listener)
	defer server.Close()

	client := NewImagesClient(socket)
	t.Run("group", func(t *testing.T) {
		t.Run("catalog", func(t *testing.T) {
			t.Parallel()
			catalog, err := client.Catalog(context.Background())
			if err != nil {
				t.Fatalf("Catalog failed on slow storage statfs: %v", err)
			}
			if len(catalog.Storages) != 1 || catalog.Storages[0].ID != 1 {
				t.Fatalf("unexpected catalog: %+v", catalog)
			}
		})
		t.Run("commit-delete-source", func(t *testing.T) {
			t.Parallel()
			if err := client.CommitDeleteSource(context.Background(), "mig_1"); err != nil {
				t.Fatalf("CommitDeleteSource failed on slow unlink: %v", err)
			}
		})
	})
}
