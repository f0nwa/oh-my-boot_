package images

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"pxe-platform/internal/smbstorage"
)

type APIRepository interface {
	ScanRepository
	Catalog(context.Context) (Catalog, error)
	MarkScanning(context.Context, int64) error
	CreateSMB(context.Context, ManagedStorageInput) (Storage, error)
	DeleteSMB(context.Context, int64) error
	RecordUpload(context.Context, Image) error
}

type Server struct {
	repo          APIRepository
	mu            sync.Mutex
	active        map[int64]bool
	uploads       map[int64]bool
	uploadRoot    func(string) string
	mountIdentity func(string) (string, bool)
}

func NewServer(repo APIRepository) *Server {
	return &Server{repo: repo, active: map[int64]bool{}, uploads: map[int64]bool{}, uploadRoot: smbstorage.UploadRoot, mountIdentity: MountIdentity}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /smb-storages/{id}/folders", s.folders)
	mux.HandleFunc("POST /smb-storages/{id}/upload", s.uploadISO)
	mux.HandleFunc("POST /smb-storages", func(w http.ResponseWriter, r *http.Request) {
		var input ManagedStorageInput
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid storage request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(w, "invalid storage request", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(input.Name) == "" || len(input.Name) > 120 || smbstorage.Validate(input.Key, input.Server, input.Share, input.Subpath) != nil {
			http.Error(w, "invalid storage request", http.StatusBadRequest)
			return
		}
		storage, err := s.repo.CreateSMB(r.Context(), input)
		if err != nil {
			http.Error(w, "cannot reserve storage", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(storage)
	})
	mux.HandleFunc("POST /smb-storages/{id}/error", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid storage ID", http.StatusBadRequest)
			return
		}
		if err := s.repo.ScanError(r.Context(), id, "Не удалось подключить SMB"); err != nil {
			http.Error(w, "cannot mark storage unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /smb-storages/{id}/scan-error", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid storage ID", http.StatusBadRequest)
			return
		}
		if err := s.repo.ScanError(r.Context(), id, "Не удалось запустить сканирование"); err != nil {
			http.Error(w, "cannot mark scan failure", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /smb-storages/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid storage ID", http.StatusBadRequest)
			return
		}
		if err := s.repo.DeleteSMB(r.Context(), id); err != nil {
			http.Error(w, "cannot delete storage", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
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
