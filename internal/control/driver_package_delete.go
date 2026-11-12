package control

import (
	"fmt"
	"net/http"
)

func (s *Server) driverPackageDelete(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("sha")
	if !validAnswerHash(hash) {
		http.Error(w, "Неверный пакет драйверов", http.StatusBadRequest)
		return
	}
	// The creation form holds draftMu while recording a configuration; preparing
	// and importing hold windowsMu. Never remove a package between either step.
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	packages, err := s.Store.DriverPackages(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать каталог драйверов", http.StatusServiceUnavailable)
		return
	}
	found := false
	for _, pkg := range packages {
		if pkg.SHA256 == hash {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Пакет драйверов не найден", http.StatusNotFound)
		return
	}
	configs, err := s.Store.DriverConfigs(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить использование", http.StatusServiceUnavailable)
		return
	}
	for _, config := range configs {
		for _, inf := range config.PackageINFs {
			if inf.PackageSHA256 == hash {
				_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-package.delete", "rejected", fmt.Sprintf("sha256=%s config=%d", hash, config.ID))
				http.Error(w, "Пакет используется конфигурацией драйверов", http.StatusConflict)
				return
			}
		}
	}
	if s.Cleanup == nil {
		http.Error(w, "Очистка файлов недоступна", http.StatusServiceUnavailable)
		return
	}
	if err := s.Cleanup.RemoveDriverPackage(r.Context(), hash); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-package.delete", "error", "sha256="+hash+" cleanup")
		http.Error(w, "Не удалось удалить файлы пакета драйверов", http.StatusServiceUnavailable)
		return
	}
	if err := s.Store.DeleteDriverPackage(r.Context(), hash); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-package.delete", "error", "sha256="+hash+" database")
		http.Error(w, "Файлы пакета удалены, но запись осталась. Повторите удаление.", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-package.delete", "ok", "sha256="+hash)
	s.InvalidateResourceAuthorsCache()
	http.Redirect(w, r, "/drivers?message=Пакет+драйверов+удалён", http.StatusSeeOther)
}
