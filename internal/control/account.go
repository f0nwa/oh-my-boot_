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
	errSamePassword  = errors.New("new password must differ from current")
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
	info := requestInfo(r)
	unused := 0
	if info.User.IsLocal() && info.User.TOTPEnabled {
		if ts := s.totpStore(); ts != nil {
			if n, err := ts.CountUnusedRecoveryCodes(r.Context(), info.User.ID); err == nil {
				unused = n
			}
		}
	}
	render(w, "account", pageData{
		Info:            info,
		Message:         r.URL.Query().Get("message"),
		Error:           pageError(r),
		FileServer:      s.localFileServerState(r.Context(), r.Host),
		TOTPUnusedCodes: unused,
		TOTPRequired:    info.PasswordPolicy.RequiresTOTP(info.User),
		TOTPConfigured:  s.Secrets != nil,
	})
}

// changePasswordPage renders the isolated mandatory password change screen.
func (s *Server) changePasswordPage(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	if !info.PasswordState.RequiresChange() {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	render(w, "change_password", pageData{Info: info, Error: pageError(r)})
}

func (s *Server) changePasswordSubmit(w http.ResponseWriter, r *http.Request) {
	s.handleOwnPasswordUpdate(w, r, "/")
}

// accountPassword changes the signed-in user's own password. The current
// password is required, and the user's other sessions are ended.
func (s *Server) accountPassword(w http.ResponseWriter, r *http.Request) {
	s.handleOwnPasswordUpdate(w, r, "/account?message=Пароль+изменён")
}

func (s *Server) handleOwnPasswordUpdate(w http.ResponseWriter, r *http.Request, redirectTo string) {
	info := requestInfo(r)
	next := r.FormValue("new_password")
	if next != r.FormValue("confirm_password") {
		http.Error(w, "Пароли не совпадают", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(next) < 12 {
		http.Error(w, "Новый пароль должен содержать не меньше 12 символов", http.StatusBadRequest)
		return
	}
	err := s.Store.ChangeOwnPassword(r.Context(), info.User.ID, info.Token, r.FormValue("current_password"), next)
	switch {
	case errors.Is(err, errWrongPassword):
		_ = s.Store.Audit(r.Context(), info.User.ID, "account.password", "error", "wrong current password")
		http.Error(w, "Текущий пароль неверен", http.StatusBadRequest)
	case errors.Is(err, errSamePassword):
		_ = s.Store.Audit(r.Context(), info.User.ID, "account.password", "error", "same password")
		http.Error(w, "Новый пароль не должен совпадать с текущим", http.StatusBadRequest)
	case errors.Is(err, errShortPassword):
		http.Error(w, "Новый пароль должен содержать не меньше 12 символов", http.StatusBadRequest)
	case err != nil:
		_ = s.Store.Audit(r.Context(), info.User.ID, "account.password", "error", "")
		http.Error(w, "Не удалось сменить пароль", http.StatusServiceUnavailable)
	default:
		_ = s.Store.Audit(r.Context(), info.User.ID, "account.password", "ok", "")
		http.Redirect(w, r, redirectTo, http.StatusSeeOther)
	}
}
