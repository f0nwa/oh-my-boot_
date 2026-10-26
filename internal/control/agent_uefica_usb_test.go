package control

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/ueficausb/ueficausbtest"
)

func ueficaAgentClient(t *testing.T, handler http.Handler) *AgentClient {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return NewAgentClient(socket)
}

func TestAgentClientUEFICAUSB(t *testing.T) {
	client := ueficaAgentClient(t, agent.HandlerWithUEFICAUSB(http.NotFoundHandler(), ueficausbtest.NewBuilder(t, "17")))
	result, err := client.BuildUEFICAUSB(context.Background(), 17, "2023")
	if err != nil || !strings.HasPrefix(result.Key, "17-2023-") {
		t.Fatalf("%+v %v", result, err)
	}
	body, size, err := client.OpenUEFICAUSB(context.Background(), result.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil || int64(len(data)) != size || size != result.Size {
		t.Fatalf("read %d of %d (%d): %v", len(data), size, result.Size, err)
	}
	if _, err := client.BuildUEFICAUSB(context.Background(), 99, "2011"); err == nil {
		t.Fatal("missing profile accepted")
	}
	if _, _, err := client.OpenUEFICAUSB(context.Background(), "17-2011-0123456789abcdef"); err == nil {
		t.Fatal("missing archive opened")
	}
}

// The archive download has no client timeout: a slow reader keeps reading.
func TestAgentClientUEFICAUSBOpenHasNoTimeout(t *testing.T) {
	client := ueficaAgentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2")
		_, _ = w.Write([]byte("a"))
		w.(http.Flusher).Flush()
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("b"))
	}))
	client.http.Timeout = 100 * time.Millisecond
	body, _, err := client.OpenUEFICAUSB(context.Background(), "17-2011-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil || string(data) != "ab" {
		t.Fatalf("%q %v", data, err)
	}
}
