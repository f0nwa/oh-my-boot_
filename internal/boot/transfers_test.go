package boot

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAssetEventsCarryStatusBytesAndSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "boot.wim"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	var events []Event
	h, err := NewHandler(dir, []byte("#!ipxe\n"), func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/assets/boot.wim", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/assets/missing/boot.wim", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodHead, "/assets/boot.wim", nil))
	if len(events) != 3 {
		t.Fatalf("events=%+v", events)
	}
	if e := events[0]; e.Status != 200 || e.Bytes != 10 || e.Size != 10 {
		t.Fatalf("served=%+v", e)
	}
	if e := events[1]; e.Status != 404 || e.Path != "/assets/missing/boot.wim" {
		t.Fatalf("missing=%+v", e)
	}
	if e := events[2]; e.Status != 200 || e.Size != 0 {
		t.Fatalf("head=%+v", e)
	}
}

// blockingWriter stops the transfer half way so the test can look at it.
type blockingWriter struct {
	*httptest.ResponseRecorder
	written int
	pause   chan struct{}
	resume  chan struct{}
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	if w.written == 0 {
		close(w.pause)
		<-w.resume
	}
	w.written += len(p)
	return w.ResponseRecorder.Write(p)
}

func TestTransfersListFilesBeingSent(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 1<<20)
	if err := os.WriteFile(filepath.Join(dir, "boot.wim"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	transfers := NewTransfers()
	p, err := NewPublisher(filepath.Join(dir, ".menu"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewDynamicHandlerWithTransfers(dir, p, nil, "", transfers)
	if err != nil {
		t.Fatal(err)
	}
	w := &blockingWriter{ResponseRecorder: httptest.NewRecorder(), pause: make(chan struct{}), resume: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/boot.wim", nil))
		close(done)
	}()
	<-w.pause
	res := httptest.NewRecorder()
	ControlHandlerWithTransfers(p, nil, transfers).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/transfers", nil))
	var list []Transfer
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].Path != "/assets/boot.wim" || list[0].Size != 1<<20 || list[0].Bytes <= 0 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	close(w.resume)
	<-done
	if got := transfers.List(); len(got) != 0 {
		t.Fatalf("finished transfer still listed: %+v", got)
	}
	if w.written != 1<<20 {
		t.Fatalf("written=%d", w.written)
	}
	_ = io.Discard
}
