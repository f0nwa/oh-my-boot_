package control

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"omb/internal/boot"
	"omb/internal/network"
)

func TestNetworkClientAccessUIAndBootSync(t *testing.T) {
	dir := t.TempDir()
	pub, err := boot.NewPublisher(filepath.Join(dir, "menu.ipxe"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	access := boot.NewAccessPolicyManager(filepath.Join(dir, "access.json"), "")
	pub.SetAccess(access)

	sock := filepath.Join(dir, "boot.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: boot.ControlHandler(pub, nil, nil)}
	go srv.Serve(ln)
	defer srv.Close()

	cfg := network.Config{
		Mode:                 "proxy",
		Interface:            "ens18",
		ServerIP:             "192.168.10.135",
		Subnet:               "192.168.10.0/24",
		ClientAccess:         network.ClientAccessSubnets,
		AllowedClientSubnets: []string{"10.20.0.0/16", "172.16.5.0/24"},
		BootURL:              "http://192.168.10.135:8080/boot/menu.ipxe",
		TFTPRoot:             "/srv/omb/tftp",
	}

	bootClient := NewBootClient(sock)
	if err := bootClient.SetAccess(context.Background(), bootAccessPolicyFromNetwork(cfg)); err != nil {
		t.Fatalf("BootClient.SetAccess: %v", err)
	}
	if !access.Allows("192.168.10.50:1234") || !access.Allows("10.20.3.4:5678") || access.Allows("10.30.0.19:1234") {
		t.Fatalf("unexpected boot access state: %+v", access.Current())
	}

	// Verify /network renders Variant 1 controls and preview highlights changes.
	form := url.Values{
		"mode":                   {"proxy"},
		"interface":              {"ens18"},
		"server_ip":              {"192.168.10.135"},
		"subnet":                 {"192.168.10.0/24"},
		"client_access":          {"subnets"},
		"allowed_client_subnets": {"10.20.0.0/16\n172.16.5.0/24"},
		"boot_url":               {"http://192.168.10.135:8080/boot/menu.ipxe"},
		"tftp_root":              {"/srv/omb/tftp"},
	}
	req := httptest.NewRequest(http.MethodPost, "/network/preview", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	parsed := networkForm(req)
	if parsed.ClientAccess != network.ClientAccessSubnets || len(parsed.AllowedClientSubnets) != 2 {
		t.Fatalf("parsed form = %+v", parsed)
	}

	rec := httptest.NewRecorder()
	data := pageData{
		Info:       requestContext{User: User{Username: "admin", Role: "admin"}, CSRF: "tok", Token: "tok"},
		Network:    parsed,
		OldNetwork: network.Config{Mode: "proxy", Interface: "ens18", ServerIP: "192.168.10.135", Subnet: "192.168.10.0/24"},
		HasNetwork: true,
	}
	if !data.NetworkChangedClientAccess() {
		t.Fatal("expected NetworkChangedClientAccess() to be true")
	}
	render(rec, "network", data)
	html := rec.Body.String()
	for _, want := range []string{`name="client_access"`, `name="allowed_client_subnets"`, `data-client-any-warn`, `id="dyn-access"`, `id="dyn-smb"`} {
		if !strings.Contains(html, want) {
			t.Errorf("network.html missing %q", want)
		}
	}
}
