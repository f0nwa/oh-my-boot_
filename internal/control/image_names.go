package control

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type imageNamesStore interface {
	ImageNames(context.Context) (map[string]string, error)
	SetImageName(context.Context, int64, string, string) error
}

func (s Store) ImageNames(ctx context.Context) (map[string]string, error) {
	rows, err := s.Pool.Query(ctx, "SELECT storage_id,path,name FROM image_names")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]string{}
	for rows.Next() {
		var id int64
		var path, name string
		if err := rows.Scan(&id, &path, &name); err != nil {
			return nil, err
		}
		names[imageAuthorKey(id, path)] = name
	}
	return names, rows.Err()
}

func (s Store) SetImageName(ctx context.Context, id int64, path, name string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO image_names(storage_id,path,name) VALUES($1,$2,$3) ON CONFLICT(storage_id,path) DO UPDATE SET name=EXCLUDED.name`, id, path, name)
	return err
}

func (s *Server) imageNames(ctx context.Context) (map[string]string, error) {
	if store, ok := s.Store.(imageNamesStore); ok {
		return store.ImageNames(ctx)
	}
	return map[string]string{}, nil
}

func (s *Server) imageNameSave(w http.ResponseWriter, r *http.Request) {
	store, ok := s.Store.(imageNamesStore)
	if !ok || s.Images == nil {
		http.Error(w, "Не удалось сохранить название образа", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("storage_id"), 10, 64)
	name := strings.TrimSpace(r.FormValue("name"))
	path := r.FormValue("path")
	if err != nil || id <= 0 || name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 120 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		http.Error(w, "Укажите название образа: до 120 символов без управляющих знаков", http.StatusBadRequest)
		return
	}
	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		http.Error(w, "Каталог ISO недоступен", http.StatusServiceUnavailable)
		return
	}
	found := false
	for _, image := range catalog.Images {
		if image.StorageID == id && image.Path == path {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Образ не найден в библиотеке", http.StatusBadRequest)
		return
	}
	if err := store.SetImageName(r.Context(), id, path, name); err != nil {
		http.Error(w, "Не удалось сохранить название образа", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "image.name", "ok", fmt.Sprintf("storage=%d path=%s", id, path))
	http.Redirect(w, r, "/images", http.StatusSeeOther)
}
