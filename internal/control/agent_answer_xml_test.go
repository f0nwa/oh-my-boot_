package control

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pxe-platform/internal/agent"
)

func TestAgentClientWindowsAnswerXML(t *testing.T) {
	dir, err := os.MkdirTemp("", "ax")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	body := "<unattend xmlns=\"urn:schemas-microsoft-com:unattend\"/>"
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/windows-answer/xml" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.ContentLength > 0 {
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			if strings.Contains(string(buf), `"id":9`) {
				http.Error(w, "Windows answer file differs", http.StatusConflict)
				return
			}
		}
		_, _ = w.Write([]byte(body))
	})}
	go srv.Serve(ln)
	defer srv.Close()

	c := NewAgentClient(socket)
	got, err := c.WindowsAnswerXML(context.Background(), agent.WindowsAnswerCheck{ID: 4, ProfileID: 17, SHA256: strings.Repeat("a", 64)})
	if err != nil || string(got) != body {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := c.WindowsAnswerXML(context.Background(), agent.WindowsAnswerCheck{ID: 9, ProfileID: 17, SHA256: strings.Repeat("a", 64)}); err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("want 409, got %v", err)
	}
}
