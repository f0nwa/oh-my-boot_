package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"omb/internal/images"
	"omb/internal/nfsstorage"
)

func newNFSKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (s *Server) imagesNFSCreate(w http.ResponseWriter, r *http.Request) {
	s.nfsMu.Lock()
	defer s.nfsMu.Unlock()
	if s.Images == nil || s.NFS == nil {
		http.Error(w, "Служба хранилищ недоступна", http.StatusServiceUnavailable)
		return
	}
	key, err := newNFSKey()
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}
	server := strings.TrimSpace(r.FormValue("server"))
	rawPath := r.FormValue("export_path")
	exportPath, err := nfsstorage.NormalizeExportPath(rawPath)
	if err != nil {
		http.Error(w, "Неверный путь NFS", http.StatusBadRequest)
		return
	}
	version := strings.TrimSpace(r.FormValue("version"))
	if version == "" {
		version = "4.1"
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = nfsstorage.Source(server, exportPath)
	}
	if name == "" || len(name) > 120 || nfsstorage.Validate(key, server, exportPath, version) != nil {
		http.Error(w, "Неверные параметры NFS", http.StatusBadRequest)
		return
	}
	input := images.NFSStorageInput{
		Name:       name,
		Key:        key,
		Server:     server,
		ExportPath: exportPath,
		Version:    version,
	}
	storage, err := s.Images.CreateNFS(r.Context(), input)
	if err != nil {
		http.Error(w, "Не удалось добавить хранилище", http.StatusServiceUnavailable)
		return
	}
	writable, err := s.NFS.MountNFS(r.Context(), storage.Key, storage.Server, storage.ExportPath, storage.NFSVersion)
	if err != nil {
		_ = s.Images.MarkNFSError(r.Context(), storage.ID)
		s.auditNFS(r, "nfs.create", "error", storage.ID)
		http.Error(w, "Не удалось подключить NFS. Хранилище сохранено для повторной попытки.", http.StatusServiceUnavailable)
		return
	}
	if writable {
		_ = s.Images.SetNFSUploads(r.Context(), storage.ID, true)
	}
	if err := s.Images.Scan(r.Context(), storage.ID); err != nil {
		_ = s.Images.MarkNFSScanError(r.Context(), storage.ID)
		s.auditNFS(r, "nfs.create", "scan-error", storage.ID)
		http.Error(w, "NFS подключено, но поиск ISO не запущен. Повторите обновление на странице образов.", http.StatusServiceUnavailable)
		return
	}
	s.auditNFS(r, "nfs.create", "ok", storage.ID)
	http.Redirect(w, r, "/storages?message="+url.QueryEscape(fmt.Sprintf("Хранилище «%s» добавлено. Сканирование запущено.", storage.Name)), http.StatusSeeOther)
}

func (s *Server) nfsStorage(ctx context.Context, idText string) (images.Storage, bool) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 || s.Images == nil {
		return images.Storage{}, false
	}
	catalog, err := s.Images.Catalog(ctx)
	if err != nil {
		return images.Storage{}, false
	}
	for _, storage := range catalog.Storages {
		if storage.ID == id && storage.Kind == "nfs" && nfsstorage.Validate(storage.Key, storage.Server, storage.ExportPath, storage.NFSVersion) == nil {
			return storage, true
		}
	}
	return images.Storage{}, false
}

func (s *Server) imagesNFSRetry(w http.ResponseWriter, r *http.Request) {
	s.nfsMu.Lock()
	defer s.nfsMu.Unlock()
	if s.NFS == nil {
		http.Error(w, "Служба хранилищ недоступна", http.StatusServiceUnavailable)
		return
	}
	storage, ok := s.nfsStorage(r.Context(), r.PathValue("id"))
	if !ok {
		http.Error(w, "Хранилище не найдено", http.StatusNotFound)
		return
	}
	if storage.State == "scanning" {
		http.Error(w, "Дождитесь завершения сканирования", http.StatusConflict)
		return
	}
	version := strings.TrimSpace(r.FormValue("version"))
	if version != "" && version != storage.NFSVersion {
		if version != "4.1" && version != "3" {
			http.Error(w, "Неверная версия NFS", http.StatusBadRequest)
			return
		}
		if err := s.Images.UpdateNFSVersion(r.Context(), storage.ID, version); err != nil {
			http.Error(w, "Не удалось обновить версию NFS", http.StatusServiceUnavailable)
			return
		}
		storage.NFSVersion = version
	}
	writable, err := s.NFS.MountNFS(r.Context(), storage.Key, storage.Server, storage.ExportPath, storage.NFSVersion)
	if err != nil {
		_ = s.Images.MarkNFSError(r.Context(), storage.ID)
		s.auditNFS(r, "nfs.retry", "error", storage.ID)
		http.Error(w, "Не удалось подключить NFS. Проверьте адрес и параметры.", http.StatusServiceUnavailable)
		return
	}
	_ = s.Images.SetNFSUploads(r.Context(), storage.ID, writable)
	if err := s.Images.Scan(r.Context(), storage.ID); err != nil {
		_ = s.Images.MarkNFSScanError(r.Context(), storage.ID)
		s.auditNFS(r, "nfs.retry", "scan-error", storage.ID)
		http.Error(w, "NFS подключено, но поиск ISO не запущен. Повторите обновление на странице образов.", http.StatusServiceUnavailable)
		return
	}
	s.auditNFS(r, "nfs.retry", "ok", storage.ID)
	http.Redirect(w, r, "/storages?message="+url.QueryEscape(fmt.Sprintf("Хранилище «%s» подключено. Сканирование запущено.", storage.Name)), http.StatusSeeOther)
}

func (s *Server) imagesNFSRemove(w http.ResponseWriter, r *http.Request) {
	s.nfsMu.Lock()
	defer s.nfsMu.Unlock()
	if s.NFS == nil {
		http.Error(w, "Служба хранилищ недоступна", http.StatusServiceUnavailable)
		return
	}
	storage, ok := s.nfsStorage(r.Context(), r.PathValue("id"))
	if !ok {
		http.Error(w, "Хранилище не найдено", http.StatusNotFound)
		return
	}
	if storage.State == "scanning" {
		http.Error(w, "Дождитесь завершения сканирования", http.StatusConflict)
		return
	}
	profiles, err := s.Store.WindowsProfiles(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить профили ISO", http.StatusServiceUnavailable)
		return
	}
	for _, profile := range profiles {
		if profile.StorageID == storage.ID {
			http.Error(w, "Хранилище используется профилем Windows ISO", http.StatusConflict)
			return
		}
	}
	installations, err := s.Store.Installations(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить загрузки", http.StatusServiceUnavailable)
		return
	}
	activeProfiles := make(map[int64]bool)
	for _, inst := range installations {
		if inst.BootProfileID > 0 {
			activeProfiles[inst.BootProfileID] = true
		}
	}
	bootProfiles, err := s.Store.BootProfiles(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить профили загрузки", http.StatusServiceUnavailable)
		return
	}
	for _, bp := range bootProfiles {
		if (bp.Kind == BootProfileUbuntuISO || bp.Kind == BootProfileUbuntuBuiltin) && bp.UbuntuStorageID == storage.ID && activeProfiles[bp.ID] {
			http.Error(w, "Хранилище используется действующей загрузкой Ubuntu", http.StatusConflict)
			return
		}
	}
	if err := s.NFS.RemoveNFS(r.Context(), storage.Key); err != nil {
		s.auditNFS(r, "nfs.remove", "error", storage.ID)
		http.Error(w, "Не удалось отключить NFS. Запись сохранена для повторной попытки.", http.StatusServiceUnavailable)
		return
	}
	if err := s.Images.DeleteNFS(r.Context(), storage.ID); err != nil {
		s.auditNFS(r, "nfs.remove", "error", storage.ID)
		http.Error(w, "Подключение снято, но запись не удалена. Повторите удаление.", http.StatusServiceUnavailable)
		return
	}
	s.resolveStorageSystemEvents(r.Context(), storage.ID)
	s.auditNFS(r, "nfs.remove", "ok", storage.ID)
	http.Redirect(w, r, "/storages?message="+url.QueryEscape(fmt.Sprintf("Хранилище «%s» удалено", storage.Name)), http.StatusSeeOther)
}

func (s *Server) auditNFS(r *http.Request, action, result string, id int64) {
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, action, result, strconv.FormatInt(id, 10))
}
