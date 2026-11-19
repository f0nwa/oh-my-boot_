package boot

import (
	"errors"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var errTransferCanceled = errors.New("transfer canceled")

// Transfer is a file being sent to a client right now.
type Transfer struct {
	ID           int       `json:"id"`
	Path         string    `json:"path"`
	RemoteAddr   string    `json:"remote_addr"`
	Started      time.Time `json:"started"`
	LastActivity time.Time `json:"last_activity"`
	Size         int64     `json:"size"`
	Bytes        int64     `json:"bytes"`
}

// Transfers lists the files being sent, so that Control can show how far a
// machine got with boot.wim before the request is logged at its end.
type Transfers struct {
	mu     sync.Mutex
	next   int
	active map[int]*liveTransfer
}

type liveTransfer struct {
	Transfer
	sent         atomic.Int64
	lastActivity atomic.Int64
	canceled     atomic.Bool
	cancelFn     func()
}

func NewTransfers() *Transfers {
	return &Transfers{active: map[int]*liveTransfer{}}
}

func (t *Transfers) start(path, remote string, size int64) (int, *liveTransfer) {
	return t.startWithCancel(path, remote, size, nil)
}

func (t *Transfers) startWithCancel(path, remote string, size int64, cancelFn func()) (int, *liveTransfer) {
	now := time.Now().UTC()
	if t == nil {
		live := &liveTransfer{Transfer: Transfer{Path: path, RemoteAddr: remote, Started: now, LastActivity: now, Size: size}, cancelFn: cancelFn}
		live.lastActivity.Store(now.UnixNano())
		return 0, live
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next++
	id := t.next
	live := &liveTransfer{
		Transfer: Transfer{ID: id, Path: path, RemoteAddr: remote, Started: now, LastActivity: now, Size: size},
		cancelFn: cancelFn,
	}
	live.lastActivity.Store(now.UnixNano())
	t.active[id] = live
	return id, live
}

func (t *Transfers) finish(id int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.active, id)
	t.mu.Unlock()
}

// Cancel forcibly aborts an in-flight transfer by ID.
func (t *Transfers) Cancel(id int) bool {
	if t == nil || id <= 0 {
		return false
	}
	t.mu.Lock()
	live := t.active[id]
	t.mu.Unlock()
	if live == nil {
		return false
	}
	live.canceled.Store(true)
	if live.cancelFn != nil {
		live.cancelFn()
	}
	return true
}

// List returns the running transfers, oldest first.
func (t *Transfers) List() []Transfer {
	out := []Transfer{}
	if t == nil {
		return out
	}
	t.mu.Lock()
	for _, live := range t.active {
		copy := live.Transfer
		copy.Bytes = live.sent.Load()
		if ns := live.lastActivity.Load(); ns > 0 {
			copy.LastActivity = time.Unix(0, ns).UTC()
		}
		out = append(out, copy)
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// countingFile counts what is read from a served file; the HTTP server reads
// it as it sends, so the count follows the transfer.
type countingFile struct {
	*os.File
	sent         *atomic.Int64
	lastActivity *atomic.Int64
	canceled     *atomic.Bool
}

func (f countingFile) Read(p []byte) (int, error) {
	if f.canceled != nil && f.canceled.Load() {
		return 0, errTransferCanceled
	}
	n, err := f.File.Read(p)
	if n > 0 {
		if f.sent != nil {
			f.sent.Add(int64(n))
		}
		if f.lastActivity != nil {
			f.lastActivity.Store(time.Now().UTC().UnixNano())
		}
	}
	return n, err
}

func cancelWriterDeadline(w http.ResponseWriter) func() {
	rc := http.NewResponseController(w)
	return func() {
		_ = rc.SetWriteDeadline(time.Now())
	}
}

// Seek keeps ServeContent's size probe from counting as sent.
func (f countingFile) Seek(offset int64, whence int) (int64, error) {
	return f.File.Seek(offset, whence)
}

var _ io.ReadSeeker = countingFile{}

// statusWriter remembers the status and counts the body bytes written.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
