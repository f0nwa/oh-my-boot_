package control

import "net/http"

// Supported admin color themes. "system" follows the browser's
// prefers-color-scheme setting.
const (
	themeSystem = "system"
	themeLight  = "light"
	themeDark   = "dark"
)

func validTheme(theme string) bool {
	return theme == themeSystem || theme == themeLight || theme == themeDark
}

func normalizeTheme(theme string) string {
	if validTheme(theme) {
		return theme
	}
	return themeSystem
}

// setTheme stores the chosen color theme and returns to the page.
func (s *Server) setTheme(w http.ResponseWriter, r *http.Request) {
	theme := r.FormValue("theme")
	if !validTheme(theme) {
		http.Error(w, "Неверная форма", http.StatusBadRequest)
		return
	}
	info := requestInfo(r)
	if err := s.Store.SetUserTheme(r.Context(), info.User.ID, theme); err != nil {
		http.Error(w, "Не удалось сохранить тему", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, safeReturnPath(r.FormValue("return")), http.StatusSeeOther)
}
