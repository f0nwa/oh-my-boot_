package control

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"omb/internal/autoinstall"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootClientReadsUbuntuSource(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "source.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ubuntu/source" {
			t.Error("wrong path")
		}
		json.NewEncoder(w).Encode(autoinstall.SourceCatalog{ISOSHA256: strings.Repeat("a", 64), Sources: []autoinstall.SourceOption{{ID: "ubuntu-desktop"}}})
	})}
	go srv.Serve(listener)
	defer srv.Close()
	c, e := NewBootClient(socket).UbuntuSource(context.Background())
	if e != nil || len(c.Sources) != 1 {
		t.Fatalf("source %#v %v", c, e)
	}
}
func TestUbuntuSourceMissingOnPublicationPreservesMenu(t *testing.T) {
	s, store, h := autoinstallMenuFixture(t)
	source := &desktopSourceStub{true}
	s.UbuntuSource = source
	// The fixture's first selected automated scenario must be checked before staging.
	for id, f := range store.files {
		f.Values.SourceID = "ubuntu-desktop"
		data, err := autoinstall.Render(f.Values)
		if err != nil {
			t.Fatal(err)
		}
		f.UserData = data
		f.SHA256 = fmtDigest(data)
		store.files[id] = f
	}
	before, hash := h.publisher.Current()
	source.available = false
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code < 400 {
		t.Fatal("published missing source")
	}
	after, afterHash := h.publisher.Current()
	if string(after) != string(before) || afterHash != hash {
		t.Fatal("changed published menu")
	}
}
