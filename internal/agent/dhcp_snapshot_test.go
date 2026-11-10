package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func journalLine(cursor string, micros int64, detail string) string {
	return fmt.Sprintf(`{"__CURSOR":%q,"__REALTIME_TIMESTAMP":%q,"SYSLOG_IDENTIFIER":"dnsmasq-dhcp","MESSAGE":%q}`+"\n", cursor, fmt.Sprint(micros), "DHCPACK(ens19) 10.30.0.150 52:54:00:12:34:56 "+detail)
}

func TestDHCPSnapshotCursorAndStableOrder(t *testing.T) {
	now := time.Now().UTC()
	stamp := now.UnixMicro()
	var cursors []string
	l := &DHCPLog{now: func() time.Time { return now }}
	l.stream = func(ctx context.Context, position journalPosition, consume func(io.Reader) error) error {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 11*time.Second {
			t.Error("source must have a bounded deadline")
		}
		cursor := position.cursor
		cursors = append(cursors, cursor)
		if len(cursors) == 1 {
			return consume(strings.NewReader(journalLine("first", stamp, "first") + journalLine("second", stamp, "second")))
		}
		return consume(strings.NewReader(journalLine("third", stamp+1, "third") + journalLine("fourth", stamp, "fourth")))
	}
	first, err := l.snapshot(context.Background())
	if err != nil || len(first) != 2 || first[0].Detail != "first" || first[1].Detail != "second" {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	// A caller cannot mutate the shared snapshot.
	first[0].Detail = "changed"
	now = now.Add(3 * time.Second)
	second, err := l.snapshot(context.Background())
	if err != nil || len(second) != 4 || second[0].Detail != "third" || second[1].Detail != "first" || second[2].Detail != "second" || second[3].Detail != "fourth" {
		t.Fatalf("second=%+v error=%v", second, err)
	}
	if fmt.Sprint(cursors) != "[ second]" {
		t.Fatalf("cursor queries=%q", cursors)
	}
}

func TestDHCPSnapshotCursorRecoveryReplacesOldJournal(t *testing.T) {
	now := time.Now().UTC()
	var cursors []string
	l := &DHCPLog{now: func() time.Time { return now }}
	l.stream = func(_ context.Context, position journalPosition, consume func(io.Reader) error) error {
		cursor := position.cursor
		cursors = append(cursors, cursor)
		switch len(cursors) {
		case 1:
			return consume(strings.NewReader(journalLine("old", now.UnixMicro(), "old")))
		case 2:
			return errors.New("Failed to seek to cursor: Invalid argument")
		default:
			return consume(strings.NewReader(journalLine("new", now.UnixMicro(), "new")))
		}
	}
	if _, err := l.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	events, err := l.snapshot(context.Background())
	if err != nil || len(events) != 1 || events[0].Detail != "new" || fmt.Sprint(cursors) != "[ old ]" {
		t.Fatalf("events=%+v cursors=%q error=%v", events, cursors, err)
	}
}

func TestDHCPSnapshotExpiresSevenDayEvents(t *testing.T) {
	now := time.Now().UTC()
	l := &DHCPLog{now: func() time.Time { return now }}
	l.stream = func(_ context.Context, position journalPosition, consume func(io.Reader) error) error {
		if position.cursor != "" {
			return consume(strings.NewReader(""))
		}
		return consume(strings.NewReader(journalLine("old", now.Add(-7*24*time.Hour+time.Second).UnixMicro(), "old")))
	}
	if events, err := l.snapshot(context.Background()); err != nil || len(events) != 1 {
		t.Fatalf("first=%+v error=%v", events, err)
	}
	now = now.Add(3 * time.Second)
	if events, err := l.snapshot(context.Background()); err != nil || len(events) != 0 {
		t.Fatalf("expired=%+v error=%v", events, err)
	}
}

func TestDHCPSnapshotCoalescesReaders(t *testing.T) {
	var reads atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	l := &DHCPLog{Read: func(ctx context.Context) ([]byte, error) {
		if reads.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return []byte(sampleJournal), nil
		}
	}}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			events, err := l.snapshot(context.Background())
			if err != nil || len(events) != 4 {
				t.Errorf("snapshot=%+v error=%v", events, err)
			}
		})
	}
	<-entered
	close(release)
	wg.Wait()
	if reads.Load() != 1 {
		t.Fatalf("concurrent reads=%d", reads.Load())
	}
}

func TestDHCPSnapshotCancellationAllowsColdRefreshToFinish(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	l := &DHCPLog{Read: func(ctx context.Context) ([]byte, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return []byte(sampleJournal), nil
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := l.snapshot(ctx); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not return")
	}
	close(release)
	if events, err := l.snapshot(context.Background()); err != nil || len(events) != 4 {
		t.Fatalf("cancelled HTTP request prevented cold cache population: events=%+v error=%v", events, err)
	}
}

func TestDHCPSnapshotCancellationKeepsOtherReader(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	l := &DHCPLog{Read: func(ctx context.Context) ([]byte, error) {
		close(entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return []byte(sampleJournal), nil
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := l.snapshot(ctx); first <- err }()
	<-entered
	second := make(chan error, 1)
	go func() {
		events, err := l.snapshot(context.Background())
		if err == nil && len(events) != 4 {
			err = fmt.Errorf("events=%+v", events)
		}
		second <- err
	}()
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reader: %v", err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("remaining reader lost shared source: %v", err)
	}
}

func TestDHCPSnapshotFailedRefreshKeepsCursorAndReportsError(t *testing.T) {
	now := time.Now().UTC()
	var positions []journalPosition
	l := &DHCPLog{now: func() time.Time { return now }}
	l.stream = func(_ context.Context, position journalPosition, consume func(io.Reader) error) error {
		positions = append(positions, position)
		switch len(positions) {
		case 1:
			return consume(strings.NewReader(journalLine("one", now.UnixMicro(), "one")))
		case 2, 3:
			return errors.New("journal is unavailable")
		default:
			return consume(strings.NewReader(journalLine("two", now.UnixMicro(), "two")))
		}
	}
	if _, err := l.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	if _, err := l.snapshot(context.Background()); err == nil {
		t.Fatal("stale snapshot concealed source failure")
	}
	now = now.Add(3 * time.Second)
	events, err := l.snapshot(context.Background())
	if err != nil || len(events) != 2 || events[0].Detail != "two" || events[1].Detail != "one" || positions[3].cursor != "one" {
		t.Fatalf("failed read lost snapshot or cursor: events=%+v positions=%+v err=%v", events, positions, err)
	}
}

func TestDHCPSnapshotSourceAndParseErrorsAreObservable(t *testing.T) {
	for _, mode := range []string{"source", "overlong"} {
		t.Run(mode, func(t *testing.T) {
			l := &DHCPLog{stream: func(_ context.Context, _ journalPosition, consume func(io.Reader) error) error {
				if mode == "source" {
					return errors.New("journal unavailable")
				}
				return consume(strings.NewReader(strings.Repeat("x", 2<<20)))
			}}
			res := httptest.NewRecorder()
			HandlerWithDHCPLog(http.NotFoundHandler(), l).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/dhcp-events", nil))
			if res.Code != http.StatusServiceUnavailable {
				t.Fatalf("source error hidden: %d %s", res.Code, res.Body)
			}
		})
	}
}

func TestDnsmasqBufferBoundAndNewestOrder(t *testing.T) {
	var b dnsmasqBuffer
	r, w := io.Pipe()
	go func() {
		defer w.Close()
		for n := 5000; n > 0; n-- {
			fmt.Fprint(w, journalLine(fmt.Sprint(n), int64(n), fmt.Sprint(n)))
		}
	}()
	if err := b.read(r); err != nil {
		t.Fatal(err)
	}
	if len(b.events) != 1000 || cap(b.events) > 1000 {
		t.Fatalf("unbounded buffer len=%d cap=%d", len(b.events), cap(b.events))
	}
	events := b.ordered()
	if len(events) != 1000 || events[0].Detail != "5000" || events[999].Detail != "4001" || b.cursor != "1" {
		t.Fatalf("wrong selection: count=%d first=%+v last=%+v cursor=%q", len(events), events[0], events[len(events)-1], b.cursor)
	}
}

func TestDHCPSnapshotEmptyJournalKeepsWatermark(t *testing.T) {
	now := time.Now().UTC()
	var positions []journalPosition
	l := &DHCPLog{now: func() time.Time { return now }, stream: func(_ context.Context, position journalPosition, consume func(io.Reader) error) error {
		positions = append(positions, position)
		return consume(strings.NewReader(""))
	}}
	for range 3 {
		if events, err := l.snapshot(context.Background()); err != nil || len(events) != 0 {
			t.Fatalf("empty events=%+v error=%v", events, err)
		}
		now = now.Add(3 * time.Second)
	}
	if len(positions) != 3 || !positions[0].since.IsZero() || positions[1].since.IsZero() || !positions[2].since.Equal(positions[1].since) {
		t.Fatalf("empty source repeatedly scans seven days: %+v", positions)
	}
}

func fakeJournalctl(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestJournalctlStreamsFilteredRecordsAfterVerifiedCursor(t *testing.T) {
	dir := fakeJournalctl(t, `printf '%s\n' "$@" > "$JOURNAL_ARGS"
printf '%s' "$JOURNAL_DATA"
`)
	argsPath := filepath.Join(dir, "args")
	t.Setenv("JOURNAL_ARGS", argsPath)
	t.Setenv("JOURNAL_DATA", journalLine("saved", 100, "old")+journalLine("next", 101, "new"))
	var b dnsmasqBuffer
	if err := streamDnsmasqJournal(context.Background(), journalPosition{cursor: "saved"}, b.read); err != nil {
		t.Fatal(err)
	}
	events := b.ordered()
	if len(events) != 1 || events[0].Detail != "new" || b.cursor != "next" {
		t.Fatalf("cursor duplicated or skipped: %+v cursor=%q", events, b.cursor)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "-u\nomb-dnsmasq\n-t\ndnsmasq-dhcp\n-t\ndnsmasq-tftp\n-o\njson\n--no-pager\n-q\n--show-cursor\n--cursor\nsaved\n" {
		t.Fatalf("journal source query=%q", args)
	}
}

func TestJournalctlDetectsSilentCursorReplacement(t *testing.T) {
	fakeJournalctl(t, `printf '%s' "$JOURNAL_DATA"`)
	t.Setenv("JOURNAL_DATA", journalLine("nearby-after-vacuum", 101, "new"))
	called := false
	err := streamDnsmasqJournal(context.Background(), journalPosition{cursor: "saved"}, func(io.Reader) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("replaced cursor accepted: err=%v consumed=%v", err, called)
	}
}

func TestJournalctlCancellationAndInitialWindow(t *testing.T) {
	dir := fakeJournalctl(t, `printf '%s\n' "$@" > "$JOURNAL_ARGS"
exec sleep 30
`)
	argsPath := filepath.Join(dir, "args")
	t.Setenv("JOURNAL_ARGS", argsPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var b dnsmasqBuffer
	done := make(chan error, 1)
	go func() { done <- streamDnsmasqJournal(ctx, journalPosition{}, b.read) }()
	var args []byte
	deadline := time.Now().Add(5 * time.Second)
	for {
		var err error
		args, err = os.ReadFile(argsPath)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("journalctl did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	started := time.Now()
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("journalctl did not stop")
	}
	if !errors.Is(err, context.Canceled) || time.Since(started) > 2*time.Second {
		t.Fatalf("subprocess did not stop: %v, elapsed %v", err, time.Since(started))
	}
	if !strings.HasSuffix(string(args), "--since\n-7d\n") || strings.Contains(string(args), "\n-n\n") {
		t.Fatalf("initial history unexpectedly limited: %q", args)
	}
}

func TestJournalctlEmptyWatermarkQuery(t *testing.T) {
	dir := fakeJournalctl(t, `printf '%s\n' "$@" > "$JOURNAL_ARGS"`)
	argsPath := filepath.Join(dir, "args")
	t.Setenv("JOURNAL_ARGS", argsPath)
	var b dnsmasqBuffer
	if err := streamDnsmasqJournal(context.Background(), journalPosition{since: time.Unix(1794355200, 123456000)}, b.read); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(args), "--since\n@1794355200.123456\n") {
		t.Fatalf("empty journal rescan: %q", args)
	}
}
