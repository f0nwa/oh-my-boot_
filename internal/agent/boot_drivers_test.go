package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/network"
)

func newDriverApplier(t *testing.T) (*Applier, *fakeRunner, string) {
	t.Helper()
	dir := t.TempDir()
	hosts := filepath.Join(dir, "dhcp", "hosts")
	runner := &fakeRunner{}
	a, err := NewApplier(filepath.Join(dir, "dnsmasq.conf"), filepath.Join(dir, "network.json"), hosts, runner)
	if err != nil {
		t.Fatal(err)
	}
	return a, runner, hosts
}

func TestNewApplierCreatesHostsBeforeState(t *testing.T) {
	a, r, path := newDriverApplier(t)
	_ = a
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 0 {
		t.Fatalf("hosts=%q %v", data, err)
	}
	for p, mode := range map[string]os.FileMode{path: 0644, filepath.Dir(path): 0755} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != mode {
			t.Fatalf("%s mode=%o want=%o", p, st.Mode().Perm(), mode)
		}
	}
	if r.tested != "" || r.restarts != 0 || r.reloads != 0 {
		t.Fatal("fresh initialization called runner")
	}
}

func TestUpdateBootDriversReplacesAndReloads(t *testing.T) {
	a, r, path := newDriverApplier(t)
	ctx := context.Background()
	first := []network.BootDriverRule{{Match: "a4:bb:6d:14:cb:f7", Driver: "snp"}}
	for i := 0; i < 2; i++ {
		if err := a.UpdateBootDrivers(ctx, first); err != nil {
			t.Fatal(err)
		}
	}
	if r.reloads != 2 {
		t.Fatalf("unchanged snapshot reloads=%d", r.reloads)
	}
	if err := a.UpdateBootDrivers(ctx, []network.BootDriverRule{{Match: "bc:24:11", Driver: "ipxe"}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "bc:24:11:*:*:*,set:omb-driver-p3-ipxe\n" {
		t.Fatalf("replacement=%q", data)
	}
	if err := a.Apply(ctx, agentConfig()); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "p3-ipxe") {
		t.Fatal("network apply lost hosts")
	}
	if err := a.UpdateBootDrivers(ctx, nil); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if len(data) != 0 {
		t.Fatal("empty snapshot did not clear hosts")
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".omb-candidate-*"))
	if len(leftovers) != 0 {
		t.Fatal(leftovers)
	}
}

func TestUpdateBootDriversErrors(t *testing.T) {
	a, r, path := newDriverApplier(t)
	ctx := context.Background()
	if err := a.UpdateBootDrivers(ctx, []network.BootDriverRule{{Match: "invalid", Driver: "snp"}}); err == nil {
		t.Fatal("accepted invalid rule")
	}
	if r.reloads != 0 {
		t.Fatal("invalid snapshot reloaded")
	}
	r.reloadErr = errors.New("unavailable")
	if err := a.UpdateBootDrivers(ctx, []network.BootDriverRule{{Match: "bc:24:11", Driver: "snp"}}); err == nil {
		t.Fatal("HUP failure hidden")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "p3-snp") {
		t.Fatal("HUP failure rolled back file")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	before := r.reloads
	if err := a.UpdateBootDrivers(ctx, nil); err == nil {
		t.Fatal("write failure hidden")
	}
	if r.reloads != before {
		t.Fatal("failed write reloaded")
	}
}

func TestBootDriverHTTP(t *testing.T) {
	a, r, path := newDriverApplier(t)
	h := Handler(a)
	valid := `[{"match":"a4:bb:6d:14:cb:f7","driver":"snp"}]`
	for _, tc := range []struct {
		body string
		want int
	}{
		{valid, 204}, {`[`, 400}, {valid + ` []`, 400}, {`[{"match":"bc:24:11","driver":"snp","unknown":1}]`, 400},
		{`[{"match":"invalid","driver":"snp"}]`, 400}, {`[{"match":"bc:24:11","driver":"other"}]`, 400},
		{strings.Repeat(" ", 256<<10) + valid, 400}, {`null`, 400}, {`{}`, 400},
	} {
		before, _ := os.ReadFile(path)
		hups := r.reloads
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/network/boot-drivers", strings.NewReader(tc.body)))
		if w.Code != tc.want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
		}
		after, _ := os.ReadFile(path)
		if tc.want == 400 && (string(before) != string(after) || hups != r.reloads) {
			t.Fatal("invalid HTTP changed hosts")
		}
	}
	r.reloadErr = errors.New("offline")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/network/boot-drivers", strings.NewReader(`[]`)))
	if w.Code != 500 {
		t.Fatalf("HUP error status=%d", w.Code)
	}
}

// A blocking runner lets the test inspect the critical section while HUP is in progress.
type blockingDriverRunner struct {
	fakeRunner
	entered chan struct{}
	release chan struct{}
}

func (r *blockingDriverRunner) Reload(context.Context) error {
	r.entered <- struct{}{}
	<-r.release
	return nil
}

func TestUpdateBootDriversSerializesWriteAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dhcp", "hosts")
	r := &blockingDriverRunner{entered: make(chan struct{}), release: make(chan struct{})}
	a, err := NewApplier(filepath.Join(dir, "dnsmasq.conf"), filepath.Join(dir, "state.json"), path, r)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() {
		done <- a.UpdateBootDrivers(context.Background(), []network.BootDriverRule{{Match: "bc:24:11", Driver: "snp"}})
	}()
	<-r.entered
	locked := !a.mu.TryLock()
	if !locked {
		a.mu.Unlock()
	}
	// Always release the runner before failing, so no goroutine outlives the test.
	if !locked {
		r.release <- struct{}{}
		<-done
		t.Fatal("write/HUP is not serialized with network Apply")
	}
	started := make(chan struct{})
	go func() {
		close(started)
		done <- a.UpdateBootDrivers(context.Background(), []network.BootDriverRule{{Match: "bc:24:11", Driver: "ipxe"}})
	}()
	<-started
	r.release <- struct{}{}
	<-r.entered
	data, _ := os.ReadFile(path)
	if string(data) != "bc:24:11:*:*:*,set:omb-driver-p3-ipxe\n" {
		t.Errorf("second snapshot=%q", data)
	}
	r.release <- struct{}{}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewApplierPreservesHostsAcrossRestart(t *testing.T) {
	_, _, path := newDriverApplier(t)
	want := "bc:24:11:*:*:*,set:omb-driver-p3-snp\n"
	if err := os.WriteFile(path, []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(filepath.Dir(path))
	_, err := NewApplier(filepath.Join(dir, "dnsmasq.conf"), filepath.Join(dir, "state.json"), path, &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != want {
		t.Fatalf("restart lost snapshot: %q", got)
	}
}

func TestUpdateBootDriversCancelledSnapshotPreservesNewerHosts(t *testing.T) {
	a, r, path := newDriverApplier(t)
	newer := []network.BootDriverRule{{Match: "bc:24:11", Driver: "ipxe"}}
	if err := a.UpdateBootDrivers(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	hups := r.reloads
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := a.UpdateBootDrivers(ctx, []network.BootDriverRule{{Match: "bc:24:11", Driver: "snp"}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled request error=%v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || hups != r.reloads {
		t.Fatal("cancelled old snapshot overwrote newer state")
	}
}

func TestApplyUsesConfiguredHostsPath(t *testing.T) {
	a, r, path := newDriverApplier(t)
	if err := a.Apply(context.Background(), agentConfig()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.tested, "dhcp-hostsfile="+path+"\n") {
		t.Fatalf("dnsmasq does not read configured hosts path: %s", r.tested)
	}
}
