package control

import (
	_ "embed"
	"errors"
	"net/http"
	"unicode/utf8"
)

var (
	errWrongPassword = errors.New("wrong current password")
	errShortPassword = errors.New("password must have at least 12 characters")
)

//go:embed password.js
var passwordJS []byte

func (s *Server) passwordScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(passwordJS)
}

// accountPage shows the signed-in user's own settings.
func (s *Server) accountPage(w http.ResponseWriter, r *http.Request) {
	render(w, "account", pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), Error: pageError(r)})
}

// accountPassword changes the signed-in user's own password. The current
// password is required, and the user's other sessions are ended.
func (s *Server) accountPassword(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	next := r.FormValue("new_password")
	if utf8.RuneCountInString(next) < 12 {
		http.Error(w, "Новый пароль должен содержать не меньше 12 символов", http.StatusBadRequest)
		return
	}
	err := s.Store.ChangeOwnPassword(r.Context(), info.User.ID, info.Token, r.FormValue("current_password"), next)
	switch {
	case errors.Is(err, errWrongPassword):
		_ = s.Store.Audit(r.Context(), info.User.ID, "account.password", "error", "wrong current password")
		http.Error(w, "Текущий пароль неверен", http.StatusBadRequest)
	case errors.Is(err, errShortPassword):
		http.Error(w, "Новый пароль должен содержать не меньше 12 символов", http.StatusBadRequest)
	case err != nil:
		_ = s.Store.Audit(r.Context(), info.User.ID, "account.password", "error", "")
		http.Error(w, "Не удалось сменить пароль", http.StatusServiceUnavailable)
	default:
		_ = s.Store.Audit(r.Context(), info.User.ID, "account.password", "ok", "")
		http.Redirect(w, r, "/account?message=Пароль+изменён", http.StatusSeeOther)
	}
}
