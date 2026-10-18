package control

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"pxe-platform/internal/images"
)

//go:embed iso_upload.js
var isoUploadJS []byte

func (s *Server) isoUploadScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(isoUploadJS)
}

func (s *Server) imagesFolders(w http.ResponseWriter, r *http.Request) {
	storage, ok := s.smbStorage(r.Context(), r.PathValue("id"))
	if !ok || storage.State != "ready" {
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
	if s.SMB == nil || s.Images == nil {
		http.Error(w, "Служба загрузки недоступна", http.StatusServiceUnavailable)
		return
	}
	storage, ok := s.smbStorage(r.Context(), r.PathValue("id"))
	if !ok || storage.State != "ready" {
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
	if err := s.SMB.EnableUpload(r.Context(), storage.Key, storage.Server, storage.Share, storage.Subpath); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.upload", "mount-error", strconv.FormatInt(storage.ID, 10))
		http.Error(w, "Не удалось подключить хранилище для записи. Проверьте права SMB.", http.StatusServiceUnavailable)
		return
	}
	result, err := s.Images.UploadISO(r.Context(), storage.ID, folder, name, r.Body, r.ContentLength)
	if err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.upload", "error", strconv.FormatInt(storage.ID, 10))
		http.Error(w, "Не удалось загрузить ISO: "+uploadErrorMessage(err), http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "iso.upload", "ok", strconv.FormatInt(storage.ID, 10)+" "+result.Path+" "+result.SHA256)
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
