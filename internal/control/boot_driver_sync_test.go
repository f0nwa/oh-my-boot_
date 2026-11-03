package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"omb/internal/network"
)

func TestAgentClientSetBootDrivers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	status := 204
	var bodies []string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/network/boot-drivers" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.WriteHeader(status)
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	c := NewAgentClient(path)
	if err := c.SetBootDrivers(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	rules := []network.BootDriverRule{{Match: "bc:24:11", Driver: "snp"}}
	if err := c.SetBootDrivers(context.Background(), rules); err != nil {
		t.Fatal(err)
	}
	if bodies[0] != "[]" {
		t.Fatal(bodies)
	}
	var got []network.BootDriverRule
	if err := json.Unmarshal([]byte(bodies[1]), &got); err != nil || !reflect.DeepEqual(got, rules) {
		t.Fatalf("payload=%s %v", bodies[1], err)
	}
	status = 500
	if err := c.SetBootDrivers(context.Background(), rules); err == nil {
		t.Fatal("ignored Agent failure")
	}
}

type driverBook struct {
	*rulesBook
	readErr error
}

func (b *driverBook) BootDriverRules(context.Context) ([]network.BootDriverRule, error) {
	if b.readErr != nil {
		return nil, b.readErr
	}
	out := []network.BootDriverRule{}
	for mac, m := range b.machines {
		if m.NetworkDriver != "" {
			out = append(out, network.BootDriverRule{Match: mac, Driver: m.NetworkDriver})
		}
	}
	for prefix, r := range b.rules {
		if r.NetworkDriver != "" {
			out = append(out, network.BootDriverRule{Match: prefix, Driver: r.NetworkDriver})
		}
	}
	return out, nil
}

type driverSyncFunc func(context.Context, []network.BootDriverRule) error

func (f driverSyncFunc) SetBootDrivers(ctx context.Context, rules []network.BootDriverRule) error {
	return f(ctx, rules)
}

func TestBootDriverSynchronization(t *testing.T) {
	book := &driverBook{rulesBook: newRulesBook("operator")}
	book.machines = map[string]MachineInfo{"bc:24:11:04:2a:ed": {MAC: "bc:24:11:04:2a:ed", NetworkDriver: "snp"}}
	book.rules["bc:24:11"] = MacRule{Prefix: "bc:24:11", NetworkDriver: "ipxe"}
	var snapshots [][]network.BootDriverRule
	s := &Server{Store: book, BootDrivers: driverSyncFunc(func(_ context.Context, rules []network.BootDriverRule) error {
		snapshots = append(snapshots, append([]network.BootDriverRule{}, rules...))
		return nil
	})}
	if err := s.SyncBootDrivers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || len(snapshots[0]) != 2 {
		t.Fatal(snapshots)
	}
	book.readErr = errors.New("db failed")
	if err := s.SyncBootDrivers(context.Background()); err == nil || len(snapshots) != 1 {
		t.Fatal("DB failure sent a snapshot")
	}
	book.readErr = nil
	book.machines = map[string]MachineInfo{}
	book.rules = map[string]MacRule{}
	if err := s.SyncBootDrivers(context.Background()); err != nil || len(snapshots[1]) != 0 {
		t.Fatalf("empty snapshot=%v %v", snapshots, err)
	}
	s.BootDrivers = driverSyncFunc(func(context.Context, []network.BootDriverRule) error { return errors.New("offline") })
	saved, err := s.mutateBootDrivers(context.Background(), func() error { return book.SetMachineNetworkDriver(context.Background(), "bc:24:11:04:2a:ed", "snp") })
	if !saved || err == nil || book.machines["bc:24:11:04:2a:ed"].NetworkDriver != "snp" {
		t.Fatalf("saved=%v err=%v machines=%v", saved, err, book.machines)
	}
}

func TestBootDriverSynchronizationSerializesMutations(t *testing.T) {
	book := &driverBook{rulesBook: newRulesBook("operator")}
	book.machines = map[string]MachineInfo{}
	book.rules = map[string]MacRule{}
	entered := make(chan []network.BootDriverRule, 2)
	release := make(chan struct{})
	done := make(chan error, 2)
	s := &Server{Store: book}
	s.BootDrivers = driverSyncFunc(func(_ context.Context, rules []network.BootDriverRule) error {
		// The serialization lock must cover the external call, including startup sync.
		if s.driverMu.TryLock() {
			s.driverMu.Unlock()
			t.Error("snapshot sent without mutation lock")
		}
		entered <- rules
		<-release
		return nil
	})
	go func() {
		_, err := s.mutateBootDrivers(context.Background(), func() error { return book.SetMachineNetworkDriver(context.Background(), "bc:24:11:04:2a:ed", "snp") })
		done <- err
	}()
	first := <-entered
	if len(first) != 1 || first[0].Driver != "snp" {
		t.Fatal(first)
	}
	started := make(chan struct{})
	go func() {
		close(started)
		_, err := s.mutateBootDrivers(context.Background(), func() error { return book.SetMachineNetworkDriver(context.Background(), "bc:24:11:04:2a:ed", "ipxe") })
		done <- err
	}()
	<-started
	release <- struct{}{}
	second := <-entered
	if len(second) != 1 || second[0].Driver != "ipxe" {
		t.Fatal(second)
	}
	release <- struct{}{}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestNetworkDriverHTTP(t *testing.T) {
	book := &driverBook{rulesBook: newRulesBook("operator")}
	mac := "bc:24:11:04:2a:ed"
	calls := 0
	s := &Server{Store: book, BootDrivers: driverSyncFunc(func(context.Context, []network.BootDriverRule) error { calls++; return nil })}
	path := "/events/machines/" + mac + "/network-driver"
	res := menuPost(t, s, path, url.Values{"network_driver": {"snp"}})
	if res.Code != 303 || book.machines[mac].NetworkDriver != "snp" || calls != 1 {
		t.Fatalf("save=%d machine=%+v calls=%d %s", res.Code, book.machines[mac], calls, res.Body.String())
	}
	res = menuPost(t, s, path, url.Values{"network_driver": {"other"}})
	if res.Code != 400 || calls != 1 {
		t.Fatalf("invalid=%d calls=%d", res.Code, calls)
	}
	if !strings.Contains(res.Body.String(), "Недопустимый сетевой драйвер") {
		t.Fatal("validation error is hidden on machine page")
	}
	book.user.Role = "observer"
	res = menuPost(t, s, path, url.Values{"network_driver": {"ipxe"}})
	if res.Code != 403 {
		t.Fatalf("observer=%d", res.Code)
	}
	book.user.Role = "operator"
	s.BootDrivers = driverSyncFunc(func(context.Context, []network.BootDriverRule) error { return errors.New("offline") })
	res = menuPost(t, s, path, url.Values{"network_driver": {"ipxe"}})
	loc, _ := url.QueryUnescape(res.Header().Get("Location"))
	if res.Code != 303 || !strings.Contains(loc, "сохранена, но не применена") || book.machines[mac].NetworkDriver != "ipxe" {
		t.Fatalf("apply failure=%d %s", res.Code, loc)
	}
	if !strings.Contains(strings.Join(book.audits, "\n"), "machine.network_driver apply_error "+mac) {
		t.Fatal(book.audits)
	}
}

func TestBootDriverDeletionSnapshots(t *testing.T) {
	for _, path := range []string{"/events/machines/bc:24:11:04:2a:ed/delete", "/machines/bc:24:11:04:2a:ed/delete", "/machine-rules/bc:24:11/delete"} {
		t.Run(path, func(t *testing.T) {
			book := &driverBook{rulesBook: newRulesBook("admin")}
			mac := "bc:24:11:04:2a:ed"
			m := book.machines[mac]
			m.NetworkDriver = "snp"
			book.machines[mac] = m
			r := book.rules["bc:24:11"]
			r.NetworkDriver = "ipxe"
			book.rules[r.Prefix] = r
			var got []network.BootDriverRule
			s := &Server{Store: book, BootDrivers: driverSyncFunc(func(_ context.Context, rules []network.BootDriverRule) error { got = rules; return nil })}
			res := menuPost(t, s, path, url.Values{})
			if res.Code != 303 || len(got) != 1 {
				t.Fatalf("delete=%d snapshot=%v", res.Code, got)
			}
			deleted := mac
			if strings.HasPrefix(path, "/machine-rules/") {
				deleted = "bc:24:11"
			}
			if got[0].Match == deleted {
				t.Fatal(got)
			}
		})
	}
}

func TestNetworkDriverRejectsMatchesThatWouldPoisonSnapshot(t *testing.T) {
	for _, tc := range []struct {
		path, match string
		form        url.Values
	}{
		{"/events/machines/00:00:00:00:00:00/network-driver", "00:00:00:00:00:00", url.Values{"network_driver": {"snp"}}},
		{"/events/machines/01:00:5e:00:00:01/network-driver", "01:00:5e:00:00:01", url.Values{"network_driver": {"ipxe"}}},
		{"/machine-rules", "00:00:00", url.Values{"prefix": {"00:00:00"}, "title": {"zero"}, "network_driver": {"snp"}}},
		{"/machine-rules/00:00:00", "00:00:00", url.Values{"title": {"zero"}, "network_driver": {"ipxe"}}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			book := &driverBook{rulesBook: newRulesBook("admin")}
			calls := 0
			s := &Server{Store: book, BootDrivers: driverSyncFunc(func(context.Context, []network.BootDriverRule) error { calls++; return nil })}
			res := menuPost(t, s, tc.path, tc.form)
			if res.Code != 400 || calls != 0 || book.machines[tc.match].NetworkDriver != "" || book.rules[tc.match].NetworkDriver != "" {
				t.Fatalf("invalid match saved: status=%d calls=%d machine=%+v rule=%+v", res.Code, calls, book.machines[tc.match], book.rules[tc.match])
			}
		})
	}
	// Invalid existing records can always be cleared to restore synchronization.
	book := &driverBook{rulesBook: newRulesBook("admin")}
	book.rules["00:00:00"] = MacRule{Prefix: "00:00:00", Title: "zero", NetworkDriver: "snp"}
	s := &Server{Store: book, BootDrivers: driverSyncFunc(func(_ context.Context, rules []network.BootDriverRule) error {
		return network.ValidateBootDriverRules(rules)
	})}
	res := menuPost(t, s, "/machine-rules/00:00:00", url.Values{"title": {"zero"}, "network_driver": {""}})
	if res.Code != 303 || book.rules["00:00:00"].NetworkDriver != "" {
		t.Fatalf("cannot clear invalid selection: %d", res.Code)
	}
}
