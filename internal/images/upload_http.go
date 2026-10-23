package images

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"pxe-platform/internal/smbstorage"
)

func uploadErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrInvalidISO), errors.Is(err, ErrInvalidFolder):
		return http.StatusBadRequest
	case errors.Is(err, ErrISOExists), errors.Is(err, os.ErrExist):
		return http.StatusConflict
	case errors.Is(err, ErrInsufficientSpace):
		return http.StatusInsufficientStorage
	default:
		return http.StatusServiceUnavailable
	}
}

func safeUploadError(err error) string {
	switch {
	case errors.Is(err, ErrInvalidISO):
		return "invalid ISO file"
	case errors.Is(err, ErrInvalidFolder):
		return "folder unavailable"
	case errors.Is(err, ErrISOExists), errors.Is(err, os.ErrExist):
		return "file already exists"
	case errors.Is(err, ErrInsufficientSpace):
		return "not enough free space"
	case errors.Is(err, ErrIncompleteUpload):
		return "transfer interrupted"
	default:
		return "storage is not writable or is unavailable"
	}
}

// uploadTarget finds a ready storage that accepts ISO uploads and the
// directory to write into: the separate read-write mount of an SMB storage,
// or the root of a local storage this service may write to.
func (s *Server) uploadTarget(r *http.Request) (Storage, string, int) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return Storage{}, "", http.StatusBadRequest
	}
	storage, err := s.repo.Storage(r.Context(), id)
	if err != nil || storage.ID != id {
		return Storage{}, "", http.StatusNotFound
	}
	switch storage.Kind {
	case "smb":
		if smbstorage.Validate(storage.Key, storage.Server, storage.Share, storage.Subpath) != nil {
			return Storage{}, "", http.StatusNotFound
		}
		if storage.State != "ready" {
			return Storage{}, "", http.StatusConflict
		}
		if _, ok := s.mountIdentity(storage.Root); !ok {
			return Storage{}, "", http.StatusServiceUnavailable
		}
		return storage, s.uploadRoot(storage.Key), 0
	case "local":
		if storage.State != "ready" {
			return Storage{}, "", http.StatusConflict
		}
		if validateRootPath(storage.Root) != nil || !s.writable(storage.Root) {
			return Storage{}, "", http.StatusForbidden
		}
		return storage, storage.Root, 0
	}
	return Storage{}, "", http.StatusNotFound
}

func (s *Server) folders(w http.ResponseWriter, r *http.Request) {
	storage, _, status := s.uploadTarget(r)
	if status != 0 {
		http.Error(w, "storage unavailable", status)
		return
	}
	folders, err := ListFolders(storage.Root)
	if err != nil {
		http.Error(w, "cannot list folders", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(folders)
}

func (s *Server) uploadISO(w http.ResponseWriter, r *http.Request) {
	storage, uploadRoot, status := s.uploadTarget(r)
	if status != 0 {
		http.Error(w, "storage unavailable", status)
		return
	}
	name, folder := r.URL.Query().Get("name"), r.URL.Query().Get("folder")
	if !ValidISOName(name) || r.ContentLength <= 0 || r.ContentLength > MaxISOBytes {
		http.Error(w, "invalid ISO name or size", http.StatusBadRequest)
		return
	}
	// An SMB upload goes through its own mount; it must not change under
	// the transfer.
	smb := storage.Kind == "smb"
	mountBefore, ok := "", true
	if smb {
		mountBefore, ok = s.mountIdentity(uploadRoot)
	}
	if !ok {
		http.Error(w, "upload mount unavailable", http.StatusServiceUnavailable)
		return
	}
	s.mu.Lock()
	if s.uploads[storage.ID] {
		s.mu.Unlock()
		http.Error(w, "upload already running", http.StatusConflict)
		return
	}
	s.uploads[storage.ID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.uploads, storage.ID)
		s.mu.Unlock()
	}()
	result, err := ReceiveISO(r.Context(), uploadRoot, folder, name, r.Body, r.ContentLength)
	if err != nil {
		http.Error(w, "ISO upload failed: "+safeUploadError(err), uploadErrorStatus(err))
		return
	}
	if mountAfter, ok := s.mountIdentity(uploadRoot); smb && (!ok || mountAfter != mountBefore) {
		http.Error(w, "upload mount changed", http.StatusServiceUnavailable)
		return
	}
	file, err := os.Stat(filepath.Join(uploadRoot, filepath.FromSlash(result.Path)))
	if err != nil {
		http.Error(w, "uploaded ISO unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.repo.RecordUpload(r.Context(), Image{StorageID: storage.ID, Path: result.Path, Size: file.Size(), Modified: file.ModTime(), Present: true}); err != nil {
		http.Error(w, "ISO saved; refresh storage catalog", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(result)
}
