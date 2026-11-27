package update

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testInstaller struct {
	check     func() (Release, error)
	apply     func(func(string)) error
	pending   bool
	committed bool
}

func (i *testInstaller) Check(context.Context, string) (Release, error) { return i.check() }
func (i *testInstaller) Apply(_ string, _ Release, progress func(string)) error {
	return i.apply(progress)
}
func (i *testInstaller) Pending() (bool, error) { return i.pending, nil }
func (i *testInstaller) Committed(Release) bool { return i.committed }

func updateRequest(t *testing.T, svc http.Handler, method, path string, body []byte, want int) Status {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	var out Status
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func awaitState(t *testing.T, svc http.Handler, want string) Status {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got := updateRequest(t, svc, "GET", "/status", nil, 200)
		if got.State == want {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("did not reach %s", want)
	return Status{}
}

func TestUpdateServiceInstallAndRestart(t *testing.T) {
	dir := t.TempDir()
	unblock := make(chan struct{})
	installer := &testInstaller{
		check: func() (Release, error) { return Release{Current: "0.1.0", Target: "0.2.0", Channel: "stable"}, nil },
		apply: func(progress func(string)) error {
			progress("apply")
			<-unblock
			return nil
		},
	}
	svc, err := NewService(dir, installer)
	if err != nil {
		t.Fatal(err)
	}
	archive := updateArchive(t, tar.Header{Name: "apt-repo/repo-manifest.json", Mode: 0600, Size: 1})
	ready := updateRequest(t, svc, "POST", "/upload", archive, 200)
	if ready.State != "ready" || ready.Release.Target != "0.2.0" || ready.ID == "" {
		t.Fatal(ready)
	}
	updateRequest(t, svc, "POST", "/install", []byte(`{"id":"stale"}`), 409)
	id, _ := json.Marshal(map[string]string{"id": ready.ID})
	updateRequest(t, svc, "POST", "/install", id, 202)
	updateRequest(t, svc, "POST", "/install", id, 409)
	if svc.BeginRestart() {
		t.Fatal("updater can restart during installation")
	}
	close(unblock)
	awaitState(t, svc, "installed")
	if !svc.BeginRestart() {
		t.Fatal("updater cannot restart after installation")
	}
	updateRequest(t, svc, "POST", "/upload", archive, 503)
	restarted, err := NewService(dir, installer)
	if err != nil {
		t.Fatal(err)
	}
	if got := awaitState(t, restarted, "installed"); got.ID != ready.ID {
		t.Fatal(got)
	}
}

func TestUpdateServiceFailures(t *testing.T) {
	archive := updateArchive(t, tar.Header{Name: "apt-repo/repo-manifest.json", Mode: 0600, Size: 1})
	for _, want := range []string{"failed", "rolled_back", "recovery_required"} {
		t.Run(want, func(t *testing.T) {
			dir := t.TempDir()
			installer := &testInstaller{
				check: func() (Release, error) { return Release{Current: "0.1.0", Target: "0.2.0"}, nil },
			}
			installer.apply = func(progress func(string)) error {
				if want == "rolled_back" {
					progress("rolled_back")
				}
				if want == "recovery_required" {
					installer.pending = true
				}
				return errors.New("installer error")
			}
			svc, err := NewService(dir, installer)
			if err != nil {
				t.Fatal(err)
			}
			ready := updateRequest(t, svc, "POST", "/upload", archive, 200)
			id, _ := json.Marshal(map[string]string{"id": ready.ID})
			updateRequest(t, svc, "POST", "/install", id, 202)
			awaitState(t, svc, want)
		})
	}
	t.Run("unverified archives leave no staging files", func(t *testing.T) {
		dir := t.TempDir()
		installer := &testInstaller{check: func() (Release, error) { return Release{}, errors.New("bad signature") }}
		svc, err := NewService(dir, installer)
		if err != nil {
			t.Fatal(err)
		}
		got := updateRequest(t, svc, "POST", "/upload", archive, 422)
		if got.State != "rejected" || got.Reason != "verification_failed" {
			t.Fatal(got)
		}
		if _, err := os.Stat(filepath.Join(dir, "stage")); !os.IsNotExist(err) {
			t.Fatal(err)
		}
		got = updateRequest(t, svc, "POST", "/upload", archive[:len(archive)/2], 422)
		if got.State != "rejected" || got.Reason != "invalid_archive" {
			t.Fatal(got)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 {
			t.Fatalf("leftovers: %v, %v", entries, err)
		}
	})
}

func TestUpdateInterruptedWithPending(t *testing.T) {
	dir := t.TempDir()
	if err := writeStatus(dir, Status{ID: "operation", State: "installing", Release: Release{Target: "0.2.0"}}); err != nil {
		t.Fatal(err)
	}
	installer := &testInstaller{pending: true}
	svc, err := NewService(dir, installer)
	if err != nil {
		t.Fatal(err)
	}
	awaitState(t, svc, "recovery_required")
}
