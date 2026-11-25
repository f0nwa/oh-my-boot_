package control

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"omb/internal/control/totp"
)

const (
	login2FACookieName       = "omb_2fa"
	login2FACSRFCookie       = "omb_2fa_csrf"
	trustedBrowserCookieName = "omb_2fa_trust"
	trustedBrowserLifetime   = 365 * 24 * time.Hour
	login2FAChallengeTTL     = 5 * time.Minute
	login2FAMaxAttempts      = 5
)

func makeTrustedBrowserCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     trustedBrowserCookieName,
		Value:    token,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(trustedBrowserLifetime.Seconds()),
	}
}

func clearTrustedBrowserCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     trustedBrowserCookieName,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) totpStore() TOTPStore {
	ts, _ := s.Store.(TOTPStore)
	return ts
}

func totpUserAAD(userID int64) string {
	return fmt.Sprintf("totp:user:%d", userID)
}

func totpPendingAAD(userID int64) string {
	return fmt.Sprintf("totp:pending:%d", userID)
}

func isForced2FAAllowed(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET /setup-2fa", "POST /account/2fa/begin", "POST /account/2fa/enable",
		"POST /logout", "POST /settings/language", "POST /settings/theme":
		return true
	default:
		return false
	}
}

func clear2FACookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: login2FACookieName, Path: "/login/2fa", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: login2FACSRFCookie, Path: "/login/2fa", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (s *Server) login2FAPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ts := s.totpStore()
	c, err := r.Cookie(login2FACookieName)
	if ts == nil || err != nil || c.Value == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	ch, err := ts.GetLogin2FAChallenge(r.Context(), c.Value)
	if err != nil || !ch.User.Enabled || time.Now().After(ch.ExpiresAt) || ch.Attempts >= login2FAMaxAttempts {
		if c.Value != "" {
			_ = ts.DeleteLogin2FAChallenge(r.Context(), c.Value)
		}
		clear2FACookies(w)
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Сессия подтверждения истекла. Войдите снова."), http.StatusSeeOther)
		return
	}
	var csrf string
	if existing, err := r.Cookie(login2FACSRFCookie); err == nil && isValidCSRFToken(existing.Value) {
		csrf = existing.Value
	} else {
		var err error
		csrf, err = randomToken()
		if err != nil {
			http.Error(w, "Ошибка", http.StatusInternalServerError)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: login2FACSRFCookie, Value: csrf, Path: "/login/2fa", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(login2FAChallengeTTL.Seconds())})
	lang := normalizeLanguage(ch.User.Language)
	if lang == "" {
		lang = languageFromRequest(r)
	}
	render(w, "login_2fa", pageData{
		Lang:     lang,
		CSRF:     csrf,
		Message:  r.URL.Query().Get("error"),
		Username: ch.User.Username,
	})
}

func (s *Server) login2FASubmit(w http.ResponseWriter, r *http.Request) {
	ts := s.totpStore()
	c, err := r.Cookie(login2FACookieName)
	if ts == nil || err != nil || c.Value == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Неверная форма", http.StatusBadRequest)
		return
	}
	csrfCookie, err := r.Cookie(login2FACSRFCookie)
	if err != nil || csrfCookie.Value == "" || !constantEqual(csrfCookie.Value, r.FormValue("csrf")) {
		http.Redirect(w, r, "/login/2fa?error="+url.QueryEscape(loginCSRFExpiredMsg), http.StatusSeeOther)
		return
	}
	ch, err := ts.GetLogin2FAChallenge(r.Context(), c.Value)
	if err != nil || !ch.User.Enabled || time.Now().After(ch.ExpiresAt) || ch.Attempts >= login2FAMaxAttempts {
		_ = ts.DeleteLogin2FAChallenge(r.Context(), c.Value)
		clear2FACookies(w)
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Сессия подтверждения истекла. Войдите снова."), http.StatusSeeOther)
		return
	}
	code := r.FormValue("code")
	mode := r.FormValue("mode")
	var verified bool
	var method string
	if mode == "recovery" {
		ok, err := ts.ConsumeRecoveryCode(r.Context(), ch.User.ID, totp.HashRecoveryCode(code))
		if err == nil && ok {
			verified = true
			method = "recovery"
		}
	} else if s.Secrets != nil && len(ch.SecretEnc) > 0 {
		secret, err := s.Secrets.Decrypt(totpUserAAD(ch.User.ID), ch.SecretEnc)
		if err == nil {
			if step, ok := totp.VerifyCode(secret, code, time.Now(), ch.User.TOTPLastStep); ok {
				if err := ts.UpdateTOTPLastStep(r.Context(), ch.User.ID, step); err == nil {
					verified = true
					method = "totp"
				}
			}
		}
	}
	if !verified {
		attempts, _ := ts.IncrementLogin2FAAttempts(r.Context(), c.Value)
		_ = s.Store.Audit(r.Context(), ch.User.ID, "login.2fa", "error", fmt.Sprintf("attempt=%d", attempts))
		if attempts >= login2FAMaxAttempts {
			_ = ts.DeleteLogin2FAChallenge(r.Context(), c.Value)
			clear2FACookies(w)
			http.Redirect(w, r, "/login?error="+url.QueryEscape("Превышено число попыток подтверждения 2FA. Войдите снова."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/login/2fa?error="+url.QueryEscape("Неверный код подтверждения"), http.StatusSeeOther)
		return
	}
	_ = ts.DeleteLogin2FAChallenge(r.Context(), c.Value)
	clear2FACookies(w)
	if r.FormValue("trust_browser") == "true" || r.FormValue("trust_browser") == "on" || r.FormValue("trust_browser") == "1" {
		trustToken, terr := ts.CreateTrustedBrowser(r.Context(), ch.User.ID, r.UserAgent(), trustedBrowserLifetime)
		if terr == nil {
			http.SetCookie(w, makeTrustedBrowserCookie(trustToken))
			method += "+trust"
		}
	}
	sessionToken, err := ts.CreateLocalSession(r.Context(), ch.User.ID)
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "omb_session", Value: sessionToken, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(sessionLifetime)})
	_ = s.Store.Audit(r.Context(), ch.User.ID, "login", "ok", "2fa="+method)
	if policy, err := s.Store.PasswordPolicy(r.Context()); err == nil {
		if EvaluatePasswordState(ch.User, policy, time.Now()).RequiresChange() {
			http.Redirect(w, r, "/change-password", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) setup2FAPage(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	if !info.User.IsLocal() || info.User.TOTPEnabled {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	render(w, "setup_2fa", pageData{Info: info, Error: pageError(r)})
}

func (s *Server) account2FABegin(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	lang := normalizeLanguage(info.User.Language)
	if !info.User.IsLocal() {
		writeJSONError(w, http.StatusBadRequest, translate(lang, "2FA доступна только для локальных учётных записей"))
		return
	}
	if s.Secrets == nil {
		writeJSONError(w, http.StatusServiceUnavailable, translate(lang, "Ключ шифрования секретов не настроен на сервере"))
		return
	}
	raw, b32Secret, err := totp.GenerateSecret()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, translate(lang, "Не удалось сгенерировать ключ 2FA"))
		return
	}
	enc, err := s.Secrets.Encrypt(totpPendingAAD(info.User.ID), raw)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, translate(lang, "Не удалось зашифровать ключ 2FA"))
		return
	}
	uri := totp.KeyURI(totp.DefaultIssuer, info.User.Username, b32Secret)
	svg, err := totp.RenderQRSVG(uri)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, translate(lang, "Не удалось сформировать QR-код"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"qr_svg":           svg,
		"secret":           b32Secret,
		"secret_formatted": totp.FormatSecret(b32Secret),
		"pending_token":    base64.RawURLEncoding.EncodeToString(enc),
	})
}

func (s *Server) account2FAEnable(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	lang := normalizeLanguage(info.User.Language)
	ts := s.totpStore()
	if !info.User.IsLocal() || ts == nil || s.Secrets == nil {
		writeJSONError(w, http.StatusServiceUnavailable, translate(lang, "Настройка 2FA недоступна"))
		return
	}
	pendingEnc, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(r.FormValue("pending_token")))
	if err != nil || len(pendingEnc) == 0 {
		writeJSONError(w, http.StatusBadRequest, translate(lang, "Сессия настройки 2FA недействительна. Начните заново."))
		return
	}
	rawSecret, err := s.Secrets.Decrypt(totpPendingAAD(info.User.ID), pendingEnc)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, translate(lang, "Сессия настройки 2FA недействительна. Начните заново."))
		return
	}
	step, ok := totp.VerifyCode(rawSecret, r.FormValue("code"), time.Now(), 0)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, translate(lang, "Неверный 6-значный код из приложения"))
		return
	}
	storedEnc, err := s.Secrets.Encrypt(totpUserAAD(info.User.ID), rawSecret)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, translate(lang, "Не удалось сохранить ключ 2FA"))
		return
	}
	plainCodes, codeHashes, err := totp.GenerateRecoveryCodes(totp.RecoveryCodeCount)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, translate(lang, "Не удалось сгенерировать резервные коды"))
		return
	}
	if err := ts.EnableUserTOTP(r.Context(), info.User.ID, storedEnc, step, codeHashes); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, translate(lang, "Не удалось включить 2FA"))
		return
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.enable_2fa", "ok", "")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":    true,
		"codes": plainCodes,
	})
}

func (s *Server) account2FADisable(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	ts := s.totpStore()
	if !info.User.IsLocal() || ts == nil {
		http.Error(w, "2FA доступна только для локальных учётных записей", http.StatusBadRequest)
		return
	}
	if info.PasswordPolicy.RequiresTOTP(info.User) {
		http.Error(w, "Нельзя отключить 2FA: она обязательна по политике безопасности", http.StatusForbidden)
		return
	}
	if err := ts.VerifyLocalPasswordByID(r.Context(), info.User.ID, r.FormValue("current_password")); err != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "user.disable_2fa", "error", "wrong password")
		http.Error(w, "Текущий пароль неверен", http.StatusBadRequest)
		return
	}
	if err := ts.DisableUserTOTP(r.Context(), info.User.ID); err != nil {
		http.Error(w, "Не удалось отключить 2FA", http.StatusServiceUnavailable)
		return
	}
	clearTrustedBrowserCookie(w)
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.disable_2fa", "ok", "")
	http.Redirect(w, r, "/account?message="+url.QueryEscape("Двухфакторная аутентификация отключена"), http.StatusSeeOther)
}

func (s *Server) account2FARevokeTrusted(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	ts := s.totpStore()
	if !info.User.IsLocal() || ts == nil {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	_ = ts.RevokeUserTrustedBrowsers(r.Context(), info.User.ID)
	clearTrustedBrowserCookie(w)
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.revoke_trusted_browsers", "ok", "")
	http.Redirect(w, r, "/account?message="+url.QueryEscape("Доверие браузерам сброшено"), http.StatusSeeOther)
}

func (s *Server) account2FARecoveryCodes(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	lang := normalizeLanguage(info.User.Language)
	ts := s.totpStore()
	if !info.User.IsLocal() || !info.User.TOTPEnabled || ts == nil {
		writeJSONError(w, http.StatusBadRequest, translate(lang, "2FA не включена"))
		return
	}
	if err := ts.VerifyLocalPasswordByID(r.Context(), info.User.ID, r.FormValue("current_password")); err != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "user.regenerate_recovery_codes", "error", "wrong password")
		writeJSONError(w, http.StatusBadRequest, translate(lang, "Текущий пароль неверен"))
		return
	}
	plainCodes, codeHashes, err := totp.GenerateRecoveryCodes(totp.RecoveryCodeCount)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, translate(lang, "Не удалось сгенерировать резервные коды"))
		return
	}
	if err := ts.ReplaceRecoveryCodes(r.Context(), info.User.ID, codeHashes); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, translate(lang, "Не удалось обновить резервные коды"))
		return
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.regenerate_recovery_codes", "ok", "")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":    true,
		"codes": plainCodes,
	})
}

func (s *Server) usersReset2FA(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	ts := s.totpStore()
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id <= 0 || ts == nil {
		http.Error(w, "Неверный пользователь", http.StatusBadRequest)
		return
	}
	if err := ts.ResetUserTOTP(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "user.reset_2fa", "error", fmt.Sprintf("id=%d", id))
		http.Error(w, "Не удалось сбросить 2FA", http.StatusBadRequest)
		return
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.reset_2fa", "ok", fmt.Sprintf("id=%d", id))
	http.Redirect(w, r, "/users?message="+url.QueryEscape("2FA пользователя сброшена; его сеансы завершены"), http.StatusSeeOther)
}

func totpPolicyLabel(policy string) string {
	switch NormalizeTOTPPolicy(policy) {
	case TOTPPolicyAdmins:
		return "обязательно для администраторов"
	case TOTPPolicyAll:
		return "обязательно для всех локальных"
	default:
		return "не требовать"
	}
}

// safeHTML marks trusted server-rendered SVG markup for templates when needed.
func safeSVG(s string) template.HTML { return template.HTML(s) }
