package images

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"omb/internal/nfsstorage"
	"omb/internal/smbstorage"
)

func (s *Server) deleteISO(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid storage ID", http.StatusBadRequest)
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "invalid image request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid image request", http.StatusBadRequest)
		return
	}
	storage, err := s.repo.Storage(r.Context(), id)
	if err != nil || storage.ID != id {
		http.Error(w, "storage not found", http.StatusNotFound)
		return
	}
	if storage.State != "ready" {
		http.Error(w, "storage is not ready", http.StatusConflict)
		return
	}
	root := storage.Root
	switch storage.Kind {
	case "local", "mounted":
	case "smb":
		if smbstorage.Validate(storage.Key, storage.Server, storage.Share, storage.Subpath) != nil {
			http.Error(w, "storage not found", http.StatusNotFound)
			return
		}
		root = s.uploadRoot(storage.Key)
	case "nfs":
		if nfsstorage.Validate(storage.Key, storage.Server, storage.ExportPath, storage.NFSVersion) != nil {
			http.Error(w, "storage not found", http.StatusNotFound)
			return
		}
		if !storage.UploadsEnabled {
			http.Error(w, "storage does not allow deletions", http.StatusForbidden)
			return
		}
		root = s.nfsUploadRoot(storage.Key)
	default:
		http.Error(w, "storage does not support deletion", http.StatusNotFound)
		return
	}
	mountBefore := ""
	if storage.Kind != "local" {
		var ok bool
		mountBefore, ok = s.mountIdentity(root)
		if !ok {
			http.Error(w, "storage mount unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	catalog, err := s.repo.Catalog(r.Context())
	if err != nil {
		http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
		return
	}
	found := false
	imagePresent := false
	for _, image := range catalog.Images {
		if image.StorageID == id && image.Path == input.Path {
			found = true
			imagePresent = image.Present
			break
		}
	}
	if !found {
		http.Error(w, "image not found", http.StatusNotFound)
		return
	}
	if imagePresent {
		if err := DeleteISO(root, input.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			status := http.StatusServiceUnavailable
			switch {
			case errors.Is(err, ErrInvalidISO), errors.Is(err, ErrInvalidFolder):
				status = http.StatusBadRequest
			case errors.Is(err, os.ErrPermission):
				status = http.StatusForbidden
			}
			http.Error(w, "cannot delete ISO", status)
			return
		}
	}
	if storage.Kind != "local" {
		mountAfter, ok := s.mountIdentity(root)
		if !ok || mountAfter != mountBefore {
			http.Error(w, "storage mount changed", http.StatusServiceUnavailable)
			return
		}
	}
	if err := s.repo.DeleteImage(r.Context(), id, input.Path); err != nil {
		http.Error(w, "ISO deleted; catalog update failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
