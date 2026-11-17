package control

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"omb/internal/images"
	"omb/internal/nfsstorage"
	"omb/internal/smbstorage"
)

//go:embed iso_upload.js
var isoUploadJS []byte

func (s *Server) isoUploadScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(isoUploadJS)
}

// uploadStorage finds a storage that takes ISO uploads: a ready SMB storage,
// or a local one this server may write to.
func (s *Server) uploadStorage(ctx context.Context, idText string) (images.Storage, bool) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 || s.Images == nil {
		return images.Storage{}, false
	}
	catalog, err := s.Images.Catalog(ctx)
	if err != nil {
		return images.Storage{}, false
	}
	for _, storage := range catalog.Storages {
		if storage.ID == id && storage.Uploads && storage.State == "ready" {
			if storage.Kind == "smb" && smbstorage.Validate(storage.Key, storage.Server, storage.Share, storage.Subpath) != nil {
				return images.Storage{}, false
			}
			if storage.Kind == "nfs" && nfsstorage.Validate(storage.Key, storage.Server, storage.ExportPath, storage.NFSVersion) != nil {
				return images.Storage{}, false
			}
			return storage, true
		}
	}
	return images.Storage{}, false
}

func (s *Server) imagesFolders(w http.ResponseWriter, r *http.Request) {
	storage, ok := s.uploadStorage(r.Context(), r.PathValue("id"))
	if !ok {
		http.Error(w, "Хранилище недоступно", http.StatusNotFound)
		return
	}
	folders, err := s.Images.Folders(r.Context(), storage.ID)
	if err != nil {
		http.Error(w, "Не удалось прочитать папки", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(folders)
}

func (s *Server) imagesUpload(w http.ResponseWriter, r *http.Request) {
	if s.Images == nil {
		http.Error(w, "Служба загрузки недоступна", http.StatusServiceUnavailable)
		return
	}
	storage, ok := s.uploadStorage(r.Context(), r.PathValue("id"))
	if !ok || (storage.Kind == "smb" && s.SMB == nil) || (storage.Kind == "nfs" && s.NFS == nil) {
		http.Error(w, "Хранилище недоступно", http.StatusNotFound)
		return
	}
	name, folder := r.URL.Query().Get("name"), r.URL.Query().Get("folder")
	if !images.ValidISOName(name) {
		http.Error(w, "Неверное имя ISO", http.StatusBadRequest)
		return
	}
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(2 * time.Hour)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	// An SMB or NFS storage is written through a separate read-write mount that the
	// Agent connects on demand; a local storage is written directly.
	if storage.Kind == "smb" {
		if err := s.SMB.EnableUpload(r.Context(), storage.Key, storage.Server, storage.Share, storage.Subpath); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.upload", "mount-error", strconv.FormatInt(storage.ID, 10))
			http.Error(w, "Не удалось подключить хранилище для записи. Проверьте права SMB.", http.StatusServiceUnavailable)
			return
		}
	}
	if storage.Kind == "nfs" {
		if err := s.NFS.EnableNFSUpload(r.Context(), storage.Key, storage.Server, storage.ExportPath, storage.NFSVersion); err != nil {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.upload", "mount-error", strconv.FormatInt(storage.ID, 10))
			http.Error(w, "Не удалось подключить хранилище для записи. Проверьте права NFS.", http.StatusServiceUnavailable)
			return
		}
	}
	result, err := s.Images.UploadISO(r.Context(), storage.ID, folder, name, r.Body, r.ContentLength)
	if err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.upload", "error", strconv.FormatInt(storage.ID, 10))
		http.Error(w, "Не удалось загрузить ISO: "+uploadErrorMessage(err), http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.upload", "ok", strconv.FormatInt(storage.ID, 10)+" "+result.Path+" "+result.SHA256)
	s.InvalidateResourceAuthorsCache()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(result)
}

func uploadErrorMessage(err error) string {
	// Images returns a bounded status and text; never expose local paths or mount details.
	text := err.Error()
	switch {
	case containsUploadStatus(text, "409"):
		return "файл с таким именем уже существует"
	case containsUploadStatus(text, "400"):
		return "проверьте имя файла и выбранную папку"
	case containsUploadStatus(text, "507"):
		return "недостаточно свободного места"
	default:
		return "хранилище недоступно или не разрешает запись"
	}
}

func containsUploadStatus(text, status string) bool {
	return len(text) >= len("image upload: ")+len(status) && text[:len("image upload: ")+len(status)] == "image upload: "+status
}
