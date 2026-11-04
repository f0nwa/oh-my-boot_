package control

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"omb/internal/agent"
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

func TestBootClientReadsSpecificUbuntuSource(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "source.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ubuntu/sources/11/"+digest {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(autoinstall.SourceCatalog{ISOSHA256: digest, Sources: []autoinstall.SourceOption{{ID: "ubuntu-desktop"}}})
	})}
	go server.Serve(listener)
	defer server.Close()
	client, ok := any(NewBootClient(socket)).(interface {
		UbuntuSourceFor(context.Context, int64, string) (autoinstall.SourceCatalog, error)
	})
	if !ok {
		t.Fatal("Boot client cannot request a specific Ubuntu ISO")
	}
	catalog, err := client.UbuntuSourceFor(context.Background(), 11, digest)
	if err != nil || catalog.ISOSHA256 != digest || len(catalog.Sources) != 1 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	if _, err := client.UbuntuSourceFor(context.Background(), 11, "bad"); err == nil {
		t.Fatal("invalid digest accepted")
	}
}

func TestAgentClientInspectsAndPreparesUbuntuISO(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ubuntu-iso/inspect":
			_ = json.NewEncoder(w).Encode(agent.UbuntuISOInspection{SHA256: strings.Repeat("a", 64), Sources: []autoinstall.SourceOption{{ID: "ubuntu-desktop"}}})
		case "/ubuntu-iso/prepare":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	client, ok := any(NewAgentClient(socket)).(interface {
		InspectUbuntuISO(context.Context, agent.WindowsISORequest) (agent.UbuntuISOInspection, error)
		PrepareUbuntuISO(context.Context, agent.UbuntuISORequest) error
	})
	if !ok {
		t.Fatal("Agent client cannot inspect and prepare Ubuntu ISO")
	}
	result, err := client.InspectUbuntuISO(context.Background(), agent.WindowsISORequest{ID: 6})
	if err != nil || result.SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("inspect=%+v err=%v", result, err)
	}
	if err := client.PrepareUbuntuISO(context.Background(), agent.UbuntuISORequest{}); err != nil {
		t.Fatal(err)
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

type multiUbuntuSourceStub struct{}

func (multiUbuntuSourceStub) UbuntuLegacySource(context.Context) (int64, string, error) {
	return 17, strings.Repeat("a", 64), nil
}

func (multiUbuntuSourceStub) UbuntuSource(context.Context) (autoinstall.SourceCatalog, error) {
	return autoinstall.SourceCatalog{ISOSHA256: strings.Repeat("a", 64), Sources: []autoinstall.SourceOption{{ID: "ubuntu-desktop-alpha"}}}, nil
}

func (multiUbuntuSourceStub) UbuntuSourceFor(_ context.Context, id int64, digest string) (autoinstall.SourceCatalog, error) {
	if id == 11 && digest == strings.Repeat("a", 64) {
		return autoinstall.SourceCatalog{ISOSHA256: digest, Sources: []autoinstall.SourceOption{{ID: "ubuntu-desktop-alpha"}}}, nil
	}
	return autoinstall.SourceCatalog{ISOSHA256: digest, Sources: []autoinstall.SourceOption{{ID: "ubuntu-desktop-beta"}}}, nil
}

func TestUbuntuAutoinstallIsValidatedAgainstItsSelectedISO(t *testing.T) {
	s, store, _ := autoinstallMenuFixture(t)
	s.UbuntuSource = multiUbuntuSourceStub{}
	file := store.files[0]
	file.Values.SourceID = "ubuntu-desktop-alpha"
	body, err := autoinstall.Render(file.Values)
	if err != nil {
		t.Fatal(err)
	}
	file.UserData, file.SHA256 = body, fmtDigest(body)
	store.files[0] = file
	entry := menuProfile{id: "install_71", autoinstallID: file.ID, autoinstallSHA256: file.SHA256, ubuntuSourceID: 11, ubuntuSourceSHA256: strings.Repeat("a", 64)}
	if err := s.stageMenuAutoinstalls(t.Context(), []menuProfile{entry}); err != nil {
		t.Fatalf("matching source rejected: %v", err)
	}
	entry.id, entry.ubuntuSourceID, entry.ubuntuSourceSHA256 = "install_73", 12, strings.Repeat("b", 64)
	if err := s.stageMenuAutoinstalls(t.Context(), []menuProfile{entry}); err == nil {
		t.Fatal("file answer for a different ISO was accepted")
	}
}
