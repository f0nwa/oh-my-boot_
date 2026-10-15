package images

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

type APIRepository interface {
	ScanRepository
	Catalog(context.Context) (Catalog, error)
	MarkScanning(context.Context, int64) error
}

type Server struct {
	repo   APIRepository
	mu     sync.Mutex
	active map[int64]bool
}

func NewServer(repo APIRepository) *Server {
	return &Server{repo: repo, active: map[int64]bool{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /catalog", func(w http.ResponseWriter, r *http.Request) {
		catalog, err := s.repo.Catalog(r.Context())
		if err != nil {
			http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(catalog)
	})
	mux.HandleFunc("POST /scan", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			StorageID int64 `json:"storage_id"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.StorageID <= 0 {
			http.Error(w, "invalid storage ID", http.StatusBadRequest)
			return
		}
		storage, err := s.repo.Storage(r.Context(), input.StorageID)
		if err != nil || storage.ID != input.StorageID {
			http.Error(w, "storage not found", http.StatusNotFound)
			return
		}
		s.mu.Lock()
		if s.active[input.StorageID] {
			s.mu.Unlock()
			http.Error(w, "scan already running", http.StatusConflict)
			return
		}
		s.active[input.StorageID] = true
		s.mu.Unlock()
		if err := s.repo.MarkScanning(r.Context(), input.StorageID); err != nil {
			s.mu.Lock()
			delete(s.active, input.StorageID)
			s.mu.Unlock()
			http.Error(w, "cannot start scan", http.StatusServiceUnavailable)
			return
		}
		go func(id int64) {
			defer func() {
				s.mu.Lock()
				delete(s.active, id)
				s.mu.Unlock()
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if err := ScanStorage(ctx, s.repo, id); err != nil {
				log.Printf("Images scan for storage %d failed: %v", id, err)
			}
		}(input.StorageID)
		w.WriteHeader(http.StatusAccepted)
	})
	return mux
}
