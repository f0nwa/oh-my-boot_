package boot

import (
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Transfer is a file being sent to a client right now.
type Transfer struct {
	Path       string    `json:"path"`
	RemoteAddr string    `json:"remote_addr"`
	Started    time.Time `json:"started"`
	Size       int64     `json:"size"`
	Bytes      int64     `json:"bytes"`
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
	sent atomic.Int64
}

func NewTransfers() *Transfers {
	return &Transfers{active: map[int]*liveTransfer{}}
}

func (t *Transfers) start(path, remote string, size int64) (int, *liveTransfer) {
	if t == nil {
		return 0, &liveTransfer{}
	}
	live := &liveTransfer{Transfer: Transfer{Path: path, RemoteAddr: remote, Started: time.Now().UTC(), Size: size}}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next++
	t.active[t.next] = live
	return t.next, live
}

func (t *Transfers) finish(id int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.active, id)
	t.mu.Unlock()
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
	sent *atomic.Int64
}

func (f countingFile) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	f.sent.Add(int64(n))
	return n, err
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
