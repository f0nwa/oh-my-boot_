package control

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omb/internal/boot"
)

type journalStore struct{ events []time.Time }

func (j *journalStore) DeleteBefore(_ context.Context, before time.Time) (int64, error) {
	var kept []time.Time
	var n int64
	for _, at := range j.events {
		if at.Before(before) {
			n++
		} else {
			kept = append(kept, at)
		}
	}
	j.events = kept
	return n, nil
}

func (j *journalStore) Stats(context.Context) (boot.JournalStats, error) {
	return boot.JournalStats{Count: int64(len(j.events)), Bytes: 8192}, nil
}

func journalBoot(t *testing.T, store *journalStore) *BootClient {
	t.Helper()
	dir, err := os.MkdirTemp("", "journal")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := boot.NewPublisher(filepath.Join(dir, "menu"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	j := &boot.Journal{Path: filepath.Join(dir, "journal.json"), Store: store}
	listener, err := net.Listen("unix", filepath.Join(dir, "b.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: boot.ControlHandlerWithJournal(p, nil, nil, j, nil)}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })
	return NewBootClient(filepath.Join(dir, "b.sock"))
}

func TestJournalRetentionAndClearFromTheJournalPage(t *testing.T) {
	store := &journalStore{events: []time.Time{time.Now().AddDate(0, 0, -40), time.Now()}}
	client := journalBoot(t, store)
	admin := newDraftStore(t, "admin", nil)
	s := &Server{Store: admin, Boot: client}
	res := menuPost(t, s, "/events/retention", url.Values{"days": {"30"}})
	if res.Code != http.StatusSeeOther || len(store.events) != 1 {
		t.Fatalf("retention: %d %s, %d left", res.Code, res.Body, len(store.events))
	}
	if state, err := client.Journal(context.Background()); err != nil || state.Days != 30 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	page := libraryGet(t, s, "/events")
	if !strings.Contains(page, `<option value="30" selected>Старше 30 дней</option>`) || !strings.Contains(page, `action="/events/clear"`) {
		t.Fatalf("page: %s", page)
	}
	if res := menuPost(t, s, "/events/clear", url.Values{}); res.Code != http.StatusSeeOther || len(store.events) != 0 {
		t.Fatalf("clear: %d, %d left", res.Code, len(store.events))
	}
	if cleared := s.journalClearedAt(context.Background()); cleared.IsZero() {
		t.Fatal("clear time not kept")
	}
	// An operator cannot change or clear the log.
	op := &Server{Store: newDraftStore(t, "operator", nil), Boot: client}
	if res := menuPost(t, op, "/events/clear", url.Values{}); res.Code != http.StatusForbidden {
		t.Fatalf("operator clear: %d", res.Code)
	}
	if page := libraryGet(t, op, "/events"); strings.Contains(page, `action="/events/clear"`) {
		t.Fatal("operator sees clear")
	}
}

func TestEventsPageRedesignHasNoDetailsAndUsesModalDialogAndServiceStrip(t *testing.T) {
	store := &journalStore{events: []time.Time{time.Now()}}
	client := journalBoot(t, store)

	// Admin view
	admin := newDraftStore(t, "admin", nil)
	sAdmin := &Server{Store: admin, Boot: client}
	pageAdmin := libraryGet(t, sAdmin, "/events")

	// Must NOT contain <details> or <summary> inside <main>
	if mainIdx := strings.Index(pageAdmin, `<main class="content">`); mainIdx != -1 {
		mainContent := pageAdmin[mainIdx:]
		if strings.Contains(mainContent, "<details") || strings.Contains(mainContent, "<summary") {
			t.Fatalf("events page content contains forbidden <details> or <summary>: %s", mainContent)
		}
	} else {
		t.Fatal("could not find <main class=\"content\"> in page")
	}

	// Must contain the service strip, dialog, and exactly 1 clear button
	for _, want := range []string{
		"service-strip events-strip",
		"journal-clear-dialog",
		"data-journal-clear-open",
		"/assets/events.js",
		"btn btn-danger",
		"page-head-title",
		`action="/events/retention"`,
		`<button class="btn" disabled>`,
	} {
		if !strings.Contains(pageAdmin, want) {
			t.Errorf("admin events page missing %q", want)
		}
	}
	if cnt := strings.Count(pageAdmin, "data-journal-clear-open"); cnt != 1 {
		t.Errorf("expected exactly 1 data-journal-clear-open button, got %d", cnt)
	}

	// Observer view
	obs := newDraftStore(t, "observer", nil)
	sObs := &Server{Store: obs, Boot: client}
	pageObs := libraryGet(t, sObs, "/events")

	if mainIdx := strings.Index(pageObs, `<main class="content">`); mainIdx != -1 {
		mainContent := pageObs[mainIdx:]
		if strings.Contains(mainContent, "<details") || strings.Contains(mainContent, "<summary") {
			t.Fatalf("observer events page content contains forbidden <details> or <summary>")
		}
	}
	if strings.Contains(pageObs, "data-journal-clear-open") || strings.Contains(pageObs, "journal-clear-dialog") {
		t.Errorf("observer should not see clear dialog triggers or clear dialog")
	}
}
