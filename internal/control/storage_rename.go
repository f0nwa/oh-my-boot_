package control

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"omb/internal/images"
)

func (s *Server) storageRename(w http.ResponseWriter, r *http.Request) {
	if s.Images == nil {
		http.Error(w, "Служба хранилищ недоступна", http.StatusServiceUnavailable)
		return
	}
	idStr := r.PathValue("id")
	if idStr == "" || idStr == "0" {
		idStr = r.FormValue("id")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Хранилище не найдено", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	count := utf8.RuneCountInString(name)
	if count == 0 || count > 120 {
		http.Error(w, "Название хранилища должно содержать от 1 до 120 символов", http.StatusBadRequest)
		return
	}

	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		http.Error(w, "Служба хранилищ недоступна", http.StatusServiceUnavailable)
		return
	}
	found := false
	for _, storage := range catalog.Storages {
		if storage.ID == id {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Хранилище не найдено", http.StatusNotFound)
		return
	}

	if err := s.Images.RenameStorage(r.Context(), id, name); err != nil {
		if errors.Is(err, images.ErrStorageNameTaken) {
			_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "storage.rename", "rejected", fmt.Sprintf("id=%d duplicate_name=%s", id, name))
			http.Error(w, "Хранилище с таким названием уже существует", http.StatusConflict)
			return
		}
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "storage.rename", "error", fmt.Sprintf("id=%d name=%s", id, name))
		http.Error(w, "Не удалось переименовать хранилище", http.StatusServiceUnavailable)
		return
	}

	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "storage.rename", "ok", fmt.Sprintf("id=%d name=%s", id, name))
	http.Redirect(w, r, "/storages?message="+url.QueryEscape(fmt.Sprintf("Хранилище «%s» переименовано", name)), http.StatusSeeOther)
}
