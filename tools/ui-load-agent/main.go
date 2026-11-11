// ui-load-agent exposes only read-only fixture APIs; never run the full Agent
// against synthetic configuration because its workers manage real mounts.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"omb/internal/agent"
)

func main() {
	socket := flag.String("socket", "", "new, private Unix socket (required)")
	flag.Parse()
	if *socket == "" {
		log.Fatal("socket required")
	}
	listener, err := net.Listen("unix", *socket)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	defer os.Remove(*socket)
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]any{"applied": true, "mode": "fixture", "interface": "fixture"})
	})
	mux.HandleFunc("GET /smb-sessions", func(w http.ResponseWriter, r *http.Request) {
		respond(w, agent.SMBSessions{Sessions: []agent.SMBSession{}})
	})
	mux.HandleFunc("GET /neighbors", func(w http.ResponseWriter, r *http.Request) { respond(w, []any{}) })
	mux.HandleFunc("GET /fixture-metrics", func(w http.ResponseWriter, r *http.Request) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		respond(w, map[string]any{"heap_alloc": m.HeapAlloc, "heap_inuse": m.HeapInuse,
			"heap_objects": m.HeapObjects, "num_gc": m.NumGC, "goroutines": runtime.NumGoroutine()})
	})
	srv := &http.Server{Handler: agent.HandlerWithDHCPLog(mux, agent.NewDHCPLog()), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(listener); err != http.ErrServerClosed {
		log.Print(err)
	}
}
