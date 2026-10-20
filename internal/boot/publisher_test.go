package boot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublisherSwitchesLiveMenu(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "menu.ipxe")
	first := []byte("#!ipxe\necho first\n")
	if err := os.WriteFile(path, first, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := NewPublisher(path, first)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewDynamicHandler(dir, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"first", "second"} {
		if want == "second" {
			if _, err := p.Publish([]byte("#!ipxe\necho second\n")); err != nil {
				t.Fatal(err)
			}
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest("GET", "/boot/menu.ipxe", nil))
		if res.Code != 200 || !strings.Contains(res.Body.String(), want) {
			t.Fatalf("menu=%d %q", res.Code, res.Body.String())
		}
	}
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(disk, []byte("#!ipxe\necho second\n")) {
		t.Fatalf("disk=%q", disk)
	}
}

func TestPublisherRejectsBadScriptWithoutChangingMenu(t *testing.T) {
	path := filepath.Join(t.TempDir(), "menu.ipxe")
	first := []byte("#!ipxe\necho safe\n")
	p, err := NewPublisher(path, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish([]byte("echo unsafe\n")); err == nil {
		t.Fatal("invalid script accepted")
	}
	got, _ := p.Current()
	if !bytes.Equal(got, first) {
		t.Fatalf("menu changed: %q", got)
	}
}

func TestPublisherWriteFailurePreservesMenu(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "menu.ipxe")
	first := []byte("#!ipxe\necho safe\n")
	p, err := NewPublisher(path, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish([]byte("#!ipxe\necho new\n")); err == nil {
		t.Fatal("expected write failure")
	}
	got, _ := p.Current()
	if !bytes.Equal(got, first) {
		t.Fatalf("menu changed: %q", got)
	}
}

func TestControlAPIStatusAndPublish(t *testing.T) {
	first := []byte("#!ipxe\necho first\n")
	p, err := NewPublisher(filepath.Join(t.TempDir(), "menu.ipxe"), first)
	if err != nil {
		t.Fatal(err)
	}
	h := ControlHandler(p, nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("GET", "/status", nil))
	var state struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	_, want := p.Current()
	if state.SHA256 != want {
		t.Fatalf("hash=%q want %q", state.SHA256, want)
	}
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("POST", "/publish", strings.NewReader("#!ipxe\necho next\n")))
	if res.Code != http.StatusOK {
		t.Fatalf("publish=%d %s", res.Code, res.Body.String())
	}
}

type recordingEvents struct{ limit int }

func (e *recordingEvents) Recent(limit int) ([]Event, error) { e.limit = limit; return []Event{}, nil }

func TestControlAPIClampsEventLimit(t *testing.T) {
	p, err := NewPublisher(filepath.Join(t.TempDir(), "menu.ipxe"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	events := &recordingEvents{}
	res := httptest.NewRecorder()
	ControlHandler(p, events).ServeHTTP(res, httptest.NewRequest("GET", "/events?limit=10000", nil))
	if res.Code != 200 || events.limit != 1000 {
		t.Fatalf("status=%d limit=%d", res.Code, events.limit)
	}
}

func TestControlAPIReturnsCurrentScriptForRollback(t *testing.T) {
	script := []byte("#!ipxe\necho current\n")
	p, err := NewPublisher(filepath.Join(t.TempDir(), "menu.ipxe"), script)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	ControlHandler(p, nil).ServeHTTP(res, httptest.NewRequest("GET", "/menu", nil))
	if res.Code != 200 || !bytes.Equal(res.Body.Bytes(), script) {
		t.Fatalf("status=%d body=%q", res.Code, res.Body.String())
	}
}
