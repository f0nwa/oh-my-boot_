package images

import (
	"encoding/json"
	"errors"
	"net/http"
)

type MigrateRequest struct {
	SourceStorageID int64  `json:"source_storage_id"`
	Path            string `json:"path"`
	TargetStorageID int64  `json:"target_storage_id"`
}

func (s *Server) startMigration(w http.ResponseWriter, r *http.Request) {
	var req MigrateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.SourceStorageID <= 0 || req.TargetStorageID <= 0 || req.Path == "" {
		http.Error(w, "invalid migration parameters", http.StatusBadRequest)
		return
	}

	srcStorage, err := s.repo.Storage(r.Context(), req.SourceStorageID)
	if err != nil {
		http.Error(w, "source storage not found", http.StatusNotFound)
		return
	}
	srcUploadRoot, srcStatus := s.storageUploadRoot(srcStorage)
	if srcStatus != 0 {
		http.Error(w, "source storage unavailable or read-only", srcStatus)
		return
	}

	targetStorage, err := s.repo.Storage(r.Context(), req.TargetStorageID)
	if err != nil {
		http.Error(w, "target storage not found", http.StatusNotFound)
		return
	}
	targetRoot, status := s.storageUploadRoot(targetStorage)
	if status != 0 {
		http.Error(w, "target storage unavailable or not writable", status)
		return
	}

	job, err := s.migrations.StartMigration(r.Context(), req.SourceStorageID, req.Path, srcUploadRoot, req.TargetStorageID, targetRoot)
	if err != nil {
		switch {
		case errors.Is(err, ErrSameStorage), errors.Is(err, ErrSourceNotFound):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, ErrTargetExists), errors.Is(err, ErrMigrationInProgress):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(job)
}

func (s *Server) listMigrations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.migrations.ListMigrations())
}

func (s *Server) getMigration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, ok := s.migrations.GetMigration(id)
	if !ok {
		http.Error(w, "migration not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}

func (s *Server) cancelMigration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.migrations.CancelMigration(id); err != nil {
		if errors.Is(err, ErrMigrationNotFound) {
			http.Error(w, "migration not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) commitMigration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.migrations.CommitDeleteSource(r.Context(), id); err != nil {
		if errors.Is(err, ErrMigrationNotFound) {
			http.Error(w, "migration not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
