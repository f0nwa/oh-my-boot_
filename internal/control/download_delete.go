package control

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5"
)

func (s *Server) installationDelete(w http.ResponseWriter, r *http.Request) {
	s.deleteInstallation(w, r, false)
}

func (s *Server) installationRemoveDelete(w http.ResponseWriter, r *http.Request) {
	s.deleteInstallation(w, r, true)
}

func (s *Server) deleteInstallation(w http.ResponseWriter, r *http.Request, remove bool) {
	id, ok := deletionID(w, r)
	if !ok {
		return
	}
	if r.FormValue("confirm") != "yes" {
		http.Error(w, "Подтвердите удаление загрузки", http.StatusBadRequest)
		return
	}
	// Match publication's lock order. Draft changes must not resurrect a
	// placement after the deletion; the live Boot check runs under menuMu.
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	active, err := s.activeMenuContains(r, installationComponent{installationID: id})
	if err != nil {
		http.Error(w, "Не удалось проверить меню", http.StatusServiceUnavailable)
		return
	}
	if active {
		if !remove {
			s.rejectDeletion(w, r, "installation.delete", id, "published", "Сначала уберите загрузку из меню и опубликуйте изменения")
			return
		}
		if !s.publishInstallationRemoval(w, r, id) {
			return
		}
	}
	var installTitle string
	if list, err := s.Store.Installations(r.Context()); err == nil {
		for _, it := range list {
			if it.ID == id {
				installTitle = it.Title
				break
			}
		}
	}
	err = s.Store.DeleteInstallation(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Загрузка не найдена", http.StatusNotFound)
		return
	}
	if errors.Is(err, errInstallationPublished) {
		s.rejectDeletion(w, r, "installation.delete", id, "published", "Сначала уберите загрузку из меню и опубликуйте изменения")
		return
	}
	if err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "installation.delete", "error", fmt.Sprintf("id=%d database", id))
		http.Error(w, "Не удалось удалить загрузку", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "installation.delete", "ok", fmt.Sprintf("id=%d", id))
	msg := "Загрузка удалена"
	if installTitle != "" {
		msg = fmt.Sprintf("Загрузка «%s» удалена", installTitle)
	}
	http.Redirect(w, r, "/installations?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

// publishInstallationRemoval builds from the active snapshot, never from the
// draft: other pending changes remain pending. draftMu and menuMu are held by
// the caller until deletion also removes the selected draft placement.
func (s *Server) publishInstallationRemoval(w http.ResponseWriter, r *http.Request, id int64) bool {
	_, active, err := s.currentDraft(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", http.StatusServiceUnavailable)
		return false
	}
	if err := active.RemoveInstallation(id); err != nil {
		http.Error(w, "Не удалось проверить меню", http.StatusServiceUnavailable)
		return false
	}
	profiles, answers, configs, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать источники меню", http.StatusServiceUnavailable)
		return false
	}
	sources, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать источники меню", http.StatusServiceUnavailable)
		return false
	}
	script, err := BuildMenuFromDraft(active, s.BootBaseURL, profiles, answers, configs, sources)
	if err != nil {
		http.Error(w, "Меню не собрано: "+err.Error(), http.StatusConflict)
		return false
	}
	version := MenuVersion{Title: active.Title, Label: active.Label, Message: active.Message, Script: string(script), NoDiagnostics: active.NoDiagnostics, LocalTimeout: active.LocalTimeout, NoLocalTimer: active.NoLocalTimer, DefaultInstallationID: active.DefaultInstallationID, Look: menuLook, Items: make([]MenuVersionItem, 0, len(active.Items))}
	for position, item := range active.Items {
		version.Items = append(version.Items, MenuVersionItem{InstallationID: item.InstallationID, Position: position, Title: item.Title, Folder: item.Folder, Components: item.Components})
	}
	return s.publishMenuLocked(w, r, version, "installation.remove-delete.publish")
}
