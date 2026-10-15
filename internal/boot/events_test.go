package boot

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type failingStore struct{}

func (failingStore) Insert(context.Context, Event) error { return errors.New("database unavailable") }

type blockedStore struct{ started chan struct{} }

func (s blockedStore) Insert(ctx context.Context, _ Event) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestRecorderFallsBackWithoutBreakingMenu(t *testing.T) {
	var logs bytes.Buffer
	recorder := NewRecorder(failingStore{}, log.New(&logs, "", 0))
	handler, err := NewHandler(t.TempDir(), []byte("#!ipxe\necho test\n"), recorder.Record)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/boot/menu.ipxe", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("menu status = %d", response.Code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := recorder.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "database unavailable") || !strings.Contains(logs.String(), "kind=menu") {
		t.Fatalf("missing fallback event: %q", logs.String())
	}
}

func TestRecorderDoesNotWaitForBlockedStore(t *testing.T) {
	store := blockedStore{started: make(chan struct{}, 1)}
	recorder := NewRecorder(store, log.New(os.Stderr, "", 0))
	recorder.Record(Event{Kind: "menu", At: time.Now()})
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("worker never reached store")
	}
	start := time.Now()
	recorder.Record(Event{Kind: "asset", At: time.Now()})
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("record blocked for %s", elapsed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := recorder.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close = %v, want canceled context", err)
	}
}
