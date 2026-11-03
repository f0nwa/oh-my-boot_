package control

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
)

func (s *Server) installationDelete(w http.ResponseWriter, r *http.Request) {
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
		s.rejectDeletion(w, r, "installation.delete", id, "published", "Сначала уберите загрузку из меню и опубликуйте изменения")
		return
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
	http.Redirect(w, r, "/installations?message=Загрузка+удалена", http.StatusSeeOther)
}
