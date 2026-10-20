package boot

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type EventStore interface {
	Insert(context.Context, Event) error
}

// Recorder keeps database work outside the HTTP request path.
type Recorder struct {
	mu     sync.Mutex
	queue  chan Event
	done   chan struct{}
	closed bool
	store  EventStore
	// lookupMAC finds a client hardware address by IP; tests replace it.
	lookupMAC func(string) string
	fallback  *log.Logger
}

func NewRecorder(store EventStore, fallback *log.Logger) *Recorder {
	if fallback == nil {
		fallback = log.Default()
	}
	r := &Recorder{
		queue:     make(chan Event, 256),
		done:      make(chan struct{}),
		store:     store,
		lookupMAC: readNeighborMAC,
		fallback:  fallback,
	}
	go r.run()
	return r
}

func (r *Recorder) Record(event Event) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	select {
	case r.queue <- event:
		r.mu.Unlock()
	default:
		r.mu.Unlock()
		r.logFallback(event, fmt.Errorf("event queue full"))
	}
}

func (r *Recorder) Close(ctx context.Context) error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	for event := range r.queue {
		if event.MAC == "" && r.lookupMAC != nil {
			event.MAC = r.lookupMAC(remoteIP(event.RemoteAddr))
		}
		if r.store == nil {
			r.logFallback(event, nil)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := r.store.Insert(ctx, event)
		cancel()
		if err != nil {
			r.logFallback(event, err)
		}
	}
}

func (r *Recorder) logFallback(event Event, cause error) {
	if cause == nil {
		r.fallback.Printf("boot event kind=%s path=%s remote=%s at=%s", event.Kind, event.Path, event.RemoteAddr, event.At.Format(time.RFC3339Nano))
		return
	}
	r.fallback.Printf("boot event fallback kind=%s path=%s remote=%s at=%s error=%v", event.Kind, event.Path, event.RemoteAddr, event.At.Format(time.RFC3339Nano), cause)
}
