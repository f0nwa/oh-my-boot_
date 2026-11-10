package agent

import (
	"bufio"
	"bytes"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

// DHCPLog shares one bounded journal snapshot among all HTTP readers.
type DHCPLog struct {
	// Read is retained for callers supplying a complete journal fixture. The
	// production reader streams stdout instead of buffering subprocess output.
	Read     func(context.Context) ([]byte, error)
	stream   func(context.Context, journalPosition, func(io.Reader) error) error
	now      func() time.Time
	mu       sync.Mutex
	buffer   dnsmasqBuffer
	since    time.Time
	events   []DHCPEvent
	err      error
	expires  time.Time
	inflight *dhcpRefresh
}

type journalPosition struct {
	cursor string
	since  time.Time
}
type dhcpRefresh struct {
	done   chan struct{}
	cancel context.CancelFunc
	events []DHCPEvent
	err    error
}

// NewDHCPLog initially streams seven days, then only records after its cursor.
func NewDHCPLog() *DHCPLog { return &DHCPLog{stream: streamDnsmasqJournal} }
func (l *DHCPLog) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}
func (l *DHCPLog) snapshot(ctx context.Context) ([]DHCPEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.clock().Before(l.expires) {
		events, err := append([]DHCPEvent{}, l.events...), l.err
		l.mu.Unlock()
		return events, err
	}
	refresh := l.inflight
	if refresh == nil {
		// A short-lived HTTP reader must not continually cancel a useful
		// cold scan. Shared work has its own bounded subprocess lifetime.
		work, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		refresh = &dhcpRefresh{done: make(chan struct{}), cancel: cancel}
		l.inflight = refresh
		go l.refresh(work, refresh)
	}
	l.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-refresh.done:
		return append([]DHCPEvent{}, refresh.events...), refresh.err
	}
}
func (l *DHCPLog) refresh(ctx context.Context, work *dhcpRefresh) {
	defer work.cancel()
	// Only the refresh goroutine writes buffer, and refreshes never overlap.
	b := dnsmasqBuffer{events: make(dnsmasqHeap, len(l.buffer.events), maxDHCPLog), cursor: l.buffer.cursor, next: l.buffer.next}
	copy(b.events, l.buffer.events)
	since := l.since
	var err error
	if l.Read != nil {
		var data []byte
		data, err = l.Read(ctx)
		b = dnsmasqBuffer{}
		if err == nil {
			err = b.read(bytes.NewReader(data))
		}
	} else if l.stream != nil {
		cursor := b.cursor
		started := l.clock()
		err = l.stream(ctx, journalPosition{cursor: cursor, since: since}, b.read)
		if err != nil && cursor != "" && ctx.Err() == nil {
			// Vacuum/reset can invalidate the cursor. Rebuild rather than merging
			// a partial failed read with the old journal.
			log.Printf("dhcp journal cursor refresh failed; rebuilding: %v", err)
			b = dnsmasqBuffer{}
			since = time.Time{}
			err = l.stream(ctx, journalPosition{}, b.read)
		}
		if err == nil {
			// If the filtered journal is empty, use this watermark until a
			// real cursor arrives instead of rescanning seven days each time.
			if since.IsZero() {
				since = started
			}
			before := l.clock().Add(-7 * 24 * time.Hour)
			for len(b.events) > 0 && b.events[0].At.Before(before) {
				heap.Pop(&b.events)
			}
		}
	} else {
		err = errors.New("dhcp journal source unavailable")
	}
	if err == nil {
		err = ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	work.err = err
	if err == nil {
		l.buffer = b
		l.since = since
		l.events = b.ordered()
	} else {
		log.Printf("dhcp journal refresh: %v", err)
	}
	work.events = l.events
	l.err = err
	l.expires = l.clock().Add(2 * time.Second)
	l.inflight = nil
	close(work.done)
}
func streamDnsmasqJournal(ctx context.Context, position journalPosition, consume func(io.Reader) error) error {
	args := []string{"-u", "omb-dnsmasq", "-t", "dnsmasq-dhcp", "-t", "dnsmasq-tftp", "-o", "json", "--no-pager", "-q", "--show-cursor"}
	if position.cursor == "" {
		since := "-7d"
		if !position.since.IsZero() {
			since = fmt.Sprintf("@%d.%06d", position.since.Unix(), position.since.Nanosecond()/1000)
		}
		args = append(args, "--since", since)
	} else {
		// --after-cursor can silently seek a nearby record after vacuum.
		// Include and verify the saved record before consuming new entries.
		args = append(args, "--cursor", position.cursor)
	}
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	cmd.WaitDelay = time.Second
	stderr := &limitedJournalError{}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	var parseErr error
	if position.cursor == "" {
		parseErr = consume(stdout)
	} else {
		reader := bufio.NewReaderSize(stdout, 1<<20)
		line, err := reader.ReadSlice('\n')
		var entry struct {
			Cursor string `json:"__CURSOR"`
		}
		if err != nil || json.Unmarshal(line, &entry) != nil || entry.Cursor != position.cursor {
			parseErr = errors.New("dnsmasq journal cursor no longer available")
		} else {
			parseErr = consume(reader)
		}
	}
	if parseErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if parseErr != nil {
		return fmt.Errorf("parse dnsmasq journal: %w", parseErr)
	}
	if waitErr != nil {
		return fmt.Errorf("journalctl: %w: %s", waitErr, stderr.data)
	}
	return nil
}

// journalctl diagnostics must not turn an error into unbounded allocation.
type limitedJournalError struct{ data []byte }

func (b *limitedJournalError) Write(p []byte) (int, error) {
	n := len(p)
	if left := 4096 - len(b.data); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
