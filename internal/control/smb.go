package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"pxe-platform/internal/images"
	"pxe-platform/internal/smbstorage"
)

func newSMBKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (s *Server) imagesSMBCreate(w http.ResponseWriter, r *http.Request) {
	s.smbMu.Lock()
	defer s.smbMu.Unlock()
	if s.Images == nil || s.SMB == nil {
		http.Error(w, "Служба хранилищ недоступна", http.StatusServiceUnavailable)
		return
	}
	key, err := newSMBKey()
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}
	server, share, subpath, err := smbstorage.ParsePath(r.FormValue("path"))
	if err != nil {
		http.Error(w, "Неверный путь SMB", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "//" + server + "/" + share
		if subpath != "" {
			name += "/" + subpath
		}
	}
	input := images.ManagedStorageInput{Name: name, Key: key, Server: server, Share: share, Subpath: subpath}
	username, password, domain := r.FormValue("username"), r.FormValue("password"), r.FormValue("domain")
	if input.Name == "" || len(input.Name) > 120 || smbstorage.Validate(key, input.Server, input.Share, input.Subpath) != nil || !validSMBFormCredential(username, false) || !validSMBFormCredential(password, false) || !validSMBFormCredential(domain, true) {
		http.Error(w, "Неверные параметры SMB", http.StatusBadRequest)
		return
	}
	storage, err := s.Images.CreateSMB(r.Context(), input)
	if err != nil {
		http.Error(w, "Не удалось добавить хранилище", http.StatusServiceUnavailable)
		return
	}
	if err := s.SMB.Mount(r.Context(), storage.Key, storage.Server, storage.Share, storage.Subpath, username, password, domain); err != nil {
		_ = s.Images.MarkSMBError(r.Context(), storage.ID)
		s.auditSMB(r, "smb.create", "error", storage.ID)
		http.Error(w, "Не удалось подключить SMB. Хранилище сохранено для повторной попытки.", http.StatusServiceUnavailable)
		return
	}
	if err := s.Images.Scan(r.Context(), storage.ID); err != nil {
		_ = s.Images.MarkSMBScanError(r.Context(), storage.ID)
		s.auditSMB(r, "smb.create", "scan-error", storage.ID)
		http.Error(w, "SMB подключено, но поиск ISO не запущен. Повторите обновление на странице образов.", http.StatusServiceUnavailable)
		return
	}
	s.auditSMB(r, "smb.create", "ok", storage.ID)
	http.Redirect(w, r, "/images?message="+url.QueryEscape("Хранилище добавлено. Сканирование запущено."), http.StatusSeeOther)
}

func (s *Server) smbStorage(ctx context.Context, idText string) (images.Storage, bool) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 || s.Images == nil {
		return images.Storage{}, false
	}
	catalog, err := s.Images.Catalog(ctx)
	if err != nil {
		return images.Storage{}, false
	}
	for _, storage := range catalog.Storages {
		if storage.ID == id && storage.Kind == "smb" && smbstorage.Validate(storage.Key, storage.Server, storage.Share, storage.Subpath) == nil {
			return storage, true
		}
	}
	return images.Storage{}, false
}

func (s *Server) imagesSMBRetry(w http.ResponseWriter, r *http.Request) {
	s.smbMu.Lock()
	defer s.smbMu.Unlock()
	if s.SMB == nil {
		http.Error(w, "Служба хранилищ недоступна", http.StatusServiceUnavailable)
		return
	}
	storage, ok := s.smbStorage(r.Context(), r.PathValue("id"))
	if !ok {
		http.Error(w, "Хранилище не найдено", http.StatusNotFound)
		return
	}
	if storage.State == "scanning" {
		http.Error(w, "Дождитесь завершения сканирования", http.StatusConflict)
		return
	}
	username, password, domain := r.FormValue("username"), r.FormValue("password"), r.FormValue("domain")
	if !validSMBFormCredential(username, false) || !validSMBFormCredential(password, false) || !validSMBFormCredential(domain, true) {
		http.Error(w, "Неверные учётные данные", http.StatusBadRequest)
		return
	}
	if err := s.SMB.Mount(r.Context(), storage.Key, storage.Server, storage.Share, storage.Subpath, username, password, domain); err != nil {
		_ = s.Images.MarkSMBError(r.Context(), storage.ID)
		s.auditSMB(r, "smb.retry", "error", storage.ID)
		http.Error(w, "Не удалось подключить SMB. Проверьте адрес и учётные данные.", http.StatusServiceUnavailable)
		return
	}
	if err := s.Images.Scan(r.Context(), storage.ID); err != nil {
		_ = s.Images.MarkSMBScanError(r.Context(), storage.ID)
		s.auditSMB(r, "smb.retry", "scan-error", storage.ID)
		http.Error(w, "SMB подключено, но поиск ISO не запущен. Повторите обновление на странице образов.", http.StatusServiceUnavailable)
		return
	}
	s.auditSMB(r, "smb.retry", "ok", storage.ID)
	http.Redirect(w, r, "/images?message="+url.QueryEscape("Хранилище подключено. Сканирование запущено."), http.StatusSeeOther)
}

func (s *Server) imagesSMBRemove(w http.ResponseWriter, r *http.Request) {
	s.smbMu.Lock()
	defer s.smbMu.Unlock()
	if s.SMB == nil {
		http.Error(w, "Служба хранилищ недоступна", http.StatusServiceUnavailable)
		return
	}
	storage, ok := s.smbStorage(r.Context(), r.PathValue("id"))
	if !ok {
		http.Error(w, "Хранилище не найдено", http.StatusNotFound)
		return
	}
	if storage.State == "scanning" {
		http.Error(w, "Дождитесь завершения сканирования", http.StatusConflict)
		return
	}
	if err := s.SMB.Remove(r.Context(), storage.Key); err != nil {
		s.auditSMB(r, "smb.remove", "error", storage.ID)
		http.Error(w, "Не удалось отключить SMB. Запись сохранена для повторной попытки.", http.StatusServiceUnavailable)
		return
	}
	if err := s.Images.DeleteSMB(r.Context(), storage.ID); err != nil {
		s.auditSMB(r, "smb.remove", "error", storage.ID)
		http.Error(w, "Подключение снято, но запись не удалена. Повторите удаление.", http.StatusServiceUnavailable)
		return
	}
	s.auditSMB(r, "smb.remove", "ok", storage.ID)
	http.Redirect(w, r, "/images?message="+url.QueryEscape("Хранилище удалено"), http.StatusSeeOther)
}

func (s *Server) auditSMB(r *http.Request, action, result string, id int64) {
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, action, result, strconv.FormatInt(id, 10))
}

func validSMBFormCredential(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	return len(value) <= 1024 && !strings.HasPrefix(value, " ") && !strings.ContainsAny(value, "\x00\r\n")
}
