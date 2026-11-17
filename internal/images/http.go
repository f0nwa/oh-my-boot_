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
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"omb/internal/nfsstorage"
	"omb/internal/smbstorage"
)

type APIRepository interface {
	ScanRepository
	Catalog(context.Context) (Catalog, error)
	MarkScanning(context.Context, int64) error
	CreateSMB(context.Context, ManagedStorageInput) (Storage, error)
	DeleteSMB(context.Context, int64) error
	CreateNFS(context.Context, NFSStorageInput) (Storage, error)
	UpdateNFSConfig(context.Context, int64, string) error
	SetUploadsEnabled(context.Context, int64, bool) error
	DeleteNFS(context.Context, int64) error
	RenameStorage(context.Context, int64, string) error
	RecordUpload(context.Context, Image) error
	DeleteImage(context.Context, int64, string) error
	Settings(context.Context) (CatalogSettings, error)
	UpdateSettings(context.Context, CatalogSettings) (CatalogSettings, error)
}

type Server struct {
	repo          APIRepository
	mu            sync.Mutex
	active        map[int64]bool
	uploads       map[int64]bool
	uploadRoot    func(string) string
	nfsUploadRoot func(string) string
	mountIdentity func(string) (string, bool)
	writable      func(string) bool
	space         func(string) (total, free int64, ok bool)
}

func NewServer(repo APIRepository) *Server {
	return &Server{repo: repo, active: map[int64]bool{}, uploads: map[int64]bool{}, uploadRoot: smbstorage.UploadRoot, nfsUploadRoot: nfsstorage.UploadRoot, mountIdentity: MountIdentity, writable: Writable, space: Space}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /storages/{id}/folders", s.folders)
	mux.HandleFunc("POST /storages/{id}/upload", s.uploadISO)
	mux.HandleFunc("DELETE /storages/{id}/images", s.deleteISO)
	// Earlier names, from when only SMB storages took uploads.
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
	mux.HandleFunc("POST /nfs-storages", func(w http.ResponseWriter, r *http.Request) {
		var input NFSStorageInput
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
		if strings.TrimSpace(input.Name) == "" || len(input.Name) > 120 || nfsstorage.Validate(input.Key, input.Server, input.ExportPath, input.Version) != nil {
			http.Error(w, "invalid storage request", http.StatusBadRequest)
			return
		}
		storage, err := s.repo.CreateNFS(r.Context(), input)
		if err != nil {
			http.Error(w, "cannot reserve storage", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(storage)
	})
	mux.HandleFunc("POST /nfs-storages/{id}/version", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid storage ID", http.StatusBadRequest)
			return
		}
		var input struct {
			Version string `json:"version"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid version request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(w, "invalid version request", http.StatusBadRequest)
			return
		}
		if input.Version != "4.1" && input.Version != "3" {
			http.Error(w, "invalid NFS version", http.StatusBadRequest)
			return
		}
		if err := s.repo.UpdateNFSConfig(r.Context(), id, input.Version); err != nil {
			http.Error(w, "cannot update NFS version", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /nfs-storages/{id}/uploads", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid storage ID", http.StatusBadRequest)
			return
		}
		var input struct {
			UploadsEnabled bool `json:"uploads_enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid uploads request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(w, "invalid uploads request", http.StatusBadRequest)
			return
		}
		if err := s.repo.SetUploadsEnabled(r.Context(), id, input.UploadsEnabled); err != nil {
			http.Error(w, "cannot update uploads setting", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /nfs-storages/{id}/error", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid storage ID", http.StatusBadRequest)
			return
		}
		if err := s.repo.ScanError(r.Context(), id, "Не удалось подключить NFS"); err != nil {
			http.Error(w, "cannot mark storage unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /nfs-storages/{id}/scan-error", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("DELETE /nfs-storages/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid storage ID", http.StatusBadRequest)
			return
		}
		if err := s.repo.DeleteNFS(r.Context(), id); err != nil {
			http.Error(w, "cannot delete storage", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /storages/{id}/rename", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid storage ID", http.StatusBadRequest)
			return
		}
		var input struct {
			Name string `json:"name"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid rename request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(w, "invalid rename request", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(input.Name)
		if name == "" || utf8.RuneCountInString(name) > 120 {
			http.Error(w, "invalid storage name", http.StatusBadRequest)
			return
		}
		if err := s.repo.RenameStorage(r.Context(), id, name); err != nil {
			if errors.Is(err, ErrStorageNameTaken) {
				http.Error(w, "storage name already taken", http.StatusConflict)
				return
			}
			if errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "storage not found", http.StatusNotFound)
				return
			}
			http.Error(w, "cannot rename storage", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("GET /catalog", func(w http.ResponseWriter, r *http.Request) {
		catalog, err := s.repo.Catalog(r.Context())
		if err != nil {
			http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
			return
		}
		s.describeSpace(catalog.Storages)
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
		switch err := s.StartScan(r.Context(), input.StorageID); {
		case errors.Is(err, errStorageNotFound):
			http.Error(w, "storage not found", http.StatusNotFound)
		case errors.Is(err, errScanRunning):
			http.Error(w, "scan already running", http.StatusConflict)
		case err != nil:
			http.Error(w, "cannot start scan", http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	})
	mux.HandleFunc("GET /settings", s.getSettings)
	mux.HandleFunc("PUT /settings", s.putSettings)
	return mux
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.repo.Settings(r.Context())
	if err != nil {
		http.Error(w, "cannot load catalog settings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(settings)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var input CatalogSettings
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if input.RescanIntervalMinutes < 1 || input.RescanIntervalMinutes > 1440 {
		http.Error(w, "rescan_interval_minutes must be between 1 and 1440", http.StatusBadRequest)
		return
	}
	updated, err := s.repo.UpdateSettings(r.Context(), input)
	if err != nil {
		http.Error(w, "cannot update catalog settings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

var (
	errStorageNotFound = errors.New("storage not found")
	errScanRunning     = errors.New("scan already running")
)

// StartScan marks a storage as scanning and scans it in the background.
func (s *Server) StartScan(ctx context.Context, id int64) error {
	storage, err := s.repo.Storage(ctx, id)
	if err != nil || storage.ID != id {
		return errStorageNotFound
	}
	s.mu.Lock()
	if s.active[id] {
		s.mu.Unlock()
		return errScanRunning
	}
	s.active[id] = true
	s.mu.Unlock()
	if err := s.repo.MarkScanning(ctx, id); err != nil {
		s.mu.Lock()
		delete(s.active, id)
		s.mu.Unlock()
		return err
	}
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.active, id)
			s.mu.Unlock()
		}()
		scanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := ScanStorage(scanCtx, s.repo, id); err != nil {
			log.Printf("Images scan for storage %d failed: %v", id, err)
		}
	}()
	return nil
}
