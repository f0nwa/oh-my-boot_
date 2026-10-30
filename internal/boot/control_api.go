package boot

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type EventReader interface {
	Recent(limit int) ([]Event, error)
}

// ControlHandler is served only over a permission-restricted Unix socket.
func ControlHandler(p *Publisher, events EventReader, assets *AutoinstallAssets) http.Handler {
	return ControlHandlerWithTransfers(p, events, nil, assets)
}

// ControlHandlerWithJournal also manages the request log's cleanup.
func ControlHandlerWithJournal(p *Publisher, events EventReader, transfers *Transfers, journal *Journal, assets *AutoinstallAssets) http.Handler {
	mux := controlMux(p, events, transfers, assets)
	handleJournal(mux, journal)
	return mux
}

// ControlHandlerWithTransfers also answers GET /transfers: the files being
// sent right now.
func ControlHandlerWithTransfers(p *Publisher, events EventReader, transfers *Transfers, assets *AutoinstallAssets) http.Handler {
	return controlMux(p, events, transfers, assets)
}

func controlMux(p *Publisher, events EventReader, transfers *Transfers, assets *AutoinstallAssets) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/autoinstall/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut && r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET, PUT")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if assets == nil {
			http.Error(w, "autoinstall assets unavailable", http.StatusServiceUnavailable)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/autoinstall/"), "/")
		if len(parts) != 2 || !canonicalPositiveID(parts[0]) || !validSHA256(parts[1]) {
			http.Error(w, "invalid autoinstall path", http.StatusBadRequest)
			return
		}
		id, _ := strconv.ParseInt(parts[0], 10, 64)
		var info AssetInfo
		var err error
		if r.Method == http.MethodPut {
			body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxAutoinstallUserDataSize))
			if readErr != nil {
				http.Error(w, "autoinstall user-data too large or unreadable", http.StatusRequestEntityTooLarge)
				return
			}
			info, err = assets.Stage(id, parts[1], body)
		} else {
			info, err = assets.Check(id, parts[1])
		}
		if err != nil {
			status := http.StatusBadRequest
			if r.Method == http.MethodGet {
				status = http.StatusNotFound
			}
			http.Error(w, "autoinstall assets unavailable or invalid", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(info)
	})
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
