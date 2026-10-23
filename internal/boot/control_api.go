package boot

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

type EventReader interface {
	Recent(limit int) ([]Event, error)
}

// ControlHandler is served only over a permission-restricted Unix socket.
func ControlHandler(p *Publisher, events EventReader) http.Handler {
	return ControlHandlerWithTransfers(p, events, nil)
}

// ControlHandlerWithTransfers also answers GET /transfers: the files being
// sent right now.
func ControlHandlerWithTransfers(p *Publisher, events EventReader, transfers *Transfers) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /transfers", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(transfers.List())
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		_, hash := p.Current()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ready": true, "sha256": hash})
	})
	mux.HandleFunc("GET /menu", func(w http.ResponseWriter, _ *http.Request) {
		menu, _ := p.Current()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(menu)
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		if events == nil {
			http.Error(w, "event store unavailable", http.StatusServiceUnavailable)
			return
		}
		limit := 30
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				http.Error(w, "invalid limit", 400)
				return
			}
			limit = n
		}
		if limit > 1000 {
			limit = 1000
		}
		rows, err := events.Recent(limit)
		if err != nil {
			http.Error(w, "event store unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rows)
	})
	mux.HandleFunc("POST /publish", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMenuSize+1))
		if err != nil {
			http.Error(w, "menu too large", 413)
			return
		}
		hash, err := p.Publish(body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"sha256": hash})
	})
	return mux
}
