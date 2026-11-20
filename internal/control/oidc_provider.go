package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"omb/internal/control/oidcclient"
)

const (
	oidcStateCookieName = "omb_oidc_state"
	oidcStateTTL        = 5 * time.Minute
)

func newOIDCClientFromConfig(cfg KeycloakConfig, sec KeycloakSecrets) (*oidcclient.Client, error) {
	return oidcclient.New(oidcclient.Config{
		PublicBaseURL:       cfg.PublicBaseURL,
		IssuerURL:           cfg.IssuerURL,
		ClientID:            cfg.ClientID,
		ClientSecret:        sec.ClientSecret,
		ServiceClientID:     cfg.ServiceClientID,
		ServiceClientSecret: sec.ServiceClientSecret,
		CAPEM:               cfg.CAPEM,
		Timeout:             5 * time.Second,
	})
}

func validateKeycloakConfig(cfg KeycloakConfig, sec KeycloakSecrets) error {
	if strings.TrimSpace(cfg.PublicBaseURL) == "" {
		return errors.New("не указан публичный HTTPS-адрес OMB")
	}
	if strings.TrimSpace(cfg.IssuerURL) == "" {
		return errors.New("не указан Issuer URL Keycloak")
	}
	if strings.TrimSpace(cfg.ClientID) == "" || sec.ClientSecret == "" {
		return errors.New("не указаны Client ID и Client Secret")
	}
	if strings.TrimSpace(cfg.ServiceClientID) == "" || sec.ServiceClientSecret == "" {
		return errors.New("не указаны данные сервисного клиента для проверки ролей")
	}
	return nil
}

func (s *Server) checkKeycloakUserLive(ctx context.Context, externalID string) (ExternalUserStatus, error) {
	rec, cfg, sec, err := s.loadKeycloakConfigAndSecrets(ctx)
	if err != nil {
		return ExternalUserStatus{}, err
	}
	if !rec.Enabled {
		return ExternalUserStatus{}, errProviderDisabled
	}
	wantPrefix := strings.TrimRight(strings.TrimSpace(cfg.IssuerURL), "/") + "|"
	if !strings.HasPrefix(externalID, wantPrefix) {
		return ExternalUserStatus{Active: false}, nil
	}
	sub := strings.TrimPrefix(externalID, wantPrefix)
	if sub == "" {
		return ExternalUserStatus{Active: false}, nil
	}
	cl, err := newOIDCClientFromConfig(cfg, sec)
	if err != nil {
		return ExternalUserStatus{}, err
	}
	doc, err := cl.Discover(ctx)
	if err != nil {
		return ExternalUserStatus{}, err
	}
	st, err := cl.FetchUserStatus(ctx, doc, sub)
	if err != nil {
		return ExternalUserStatus{}, err
	}
	if !st.Enabled {
		return ExternalUserStatus{Active: false}, nil
	}
	role := HighestRole(st.Roles...)
	if !Allowed(role, "view") {
		return ExternalUserStatus{Active: false}, nil
	}
	return ExternalUserStatus{
		Active:      true,
		Role:        role,
		Username:    st.Username,
		DisplayName: st.DisplayName,
	}, nil
}

func testKeycloakConnection(ctx context.Context, cfg KeycloakConfig, sec KeycloakSecrets) (string, error) {
	if err := validateKeycloakConfig(cfg, sec); err != nil {
		return "", err
	}
	start := time.Now()
	cl, err := newOIDCClientFromConfig(cfg, sec)
	if err != nil {
		return "", err
	}
	doc, err := cl.Discover(ctx)
	if err != nil {
		return "", fmt.Errorf("OIDC Discovery: %w", err)
	}
	jwks, err := cl.FetchJWKS(ctx, doc.JWKSURI)
	if err != nil {
		return "", fmt.Errorf("JWKS: %w", err)
	}
	if err := cl.TestServiceAPI(ctx, doc); err != nil {
		return "", fmt.Errorf("Admin REST API: %w", err)
	}
	ms := time.Since(start).Milliseconds()
	return fmt.Sprintf("OK (%d мс): Discovery ok, JWKS (%d ключей), Admin API подтверждён", ms, len(jwks.Keys)), nil
}

func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	es := s.externalAuthStore()
	if es == nil {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Вход через Keycloak недоступен"), http.StatusSeeOther)
		return
	}
	rec, cfg, sec, err := s.loadKeycloakConfigAndSecrets(r.Context())
	if err != nil || !rec.Enabled {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Вход через Keycloak отключён"), http.StatusSeeOther)
		return
	}
	cl, err := newOIDCClientFromConfig(cfg, sec)
	if err != nil {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Ошибка конфигурации Keycloak"), http.StatusSeeOther)
		return
	}
	doc, err := cl.Discover(r.Context())
	if err != nil {
		s.recordAuthProviderEvent(r.Context(), AuthSourceKeycloak, false, err.Error())
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Сервер Keycloak временно недоступен"), http.StatusSeeOther)
		return
	}
	state, err := randomToken()
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}
	verifier, err := randomToken()
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}
	if err := es.SaveOIDCLoginState(r.Context(), OIDCLoginState{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: verifier,
		ExpiresAt:    time.Now().Add(oidcStateTTL),
	}); err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    state,
		Path:     "/auth/oidc",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oidcStateTTL.Seconds()),
	})
	http.Redirect(w, r, cl.AuthURL(doc, state, nonce, verifier), http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	es := s.externalAuthStore()
	if es == nil {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Вход через Keycloak недоступен"), http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Path:     "/auth/oidc",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	stateParam := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	cookie, err := r.Cookie(oidcStateCookieName)
	if err != nil || stateParam == "" || code == "" || !constantEqual(cookie.Value, stateParam) {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Недействительный запрос входа Keycloak"), http.StatusSeeOther)
		return
	}
	st, err := es.ConsumeOIDCLoginState(r.Context(), stateParam)
	if err != nil {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Сессия входа истекла, попробуйте снова"), http.StatusSeeOther)
		return
	}

	rec, cfg, sec, err := s.loadKeycloakConfigAndSecrets(r.Context())
	if err != nil || !rec.Enabled {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Вход через Keycloak отключён"), http.StatusSeeOther)
		return
	}
	cl, err := newOIDCClientFromConfig(cfg, sec)
	if err != nil {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Ошибка конфигурации Keycloak"), http.StatusSeeOther)
		return
	}
	doc, err := cl.Discover(r.Context())
	if err != nil {
		s.recordAuthProviderEvent(r.Context(), AuthSourceKeycloak, false, err.Error())
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Сервер Keycloak временно недоступен"), http.StatusSeeOther)
		return
	}
	jwks, err := cl.FetchJWKS(r.Context(), doc.JWKSURI)
	if err != nil {
		s.recordAuthProviderEvent(r.Context(), AuthSourceKeycloak, false, err.Error())
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Не удалось проверить ключи Keycloak"), http.StatusSeeOther)
		return
	}
	idToken, err := cl.ExchangeCode(r.Context(), doc, code, st.CodeVerifier)
	if err != nil {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Вход через Keycloak отклонён"), http.StatusSeeOther)
		return
	}
	claims, err := cl.ValidateIDToken(idToken, jwks, st.Nonce, time.Now())
	if err != nil {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Недействительный токен Keycloak"), http.StatusSeeOther)
		return
	}

	// Authoritative live status and client roles from Keycloak Admin REST API.
	live, err := cl.FetchUserStatus(r.Context(), doc, claims.Sub)
	if err != nil {
		s.recordAuthProviderEvent(r.Context(), AuthSourceKeycloak, false, err.Error())
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Не удалось проверить права в Keycloak"), http.StatusSeeOther)
		return
	}
	s.recordAuthProviderEvent(r.Context(), AuthSourceKeycloak, true, "")
	role := HighestRole(live.Roles...)
	if !live.Enabled || !Allowed(role, "view") {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Доступ запрещён: нет разрешённой роли OMB"), http.StatusSeeOther)
		return
	}

	issuer := strings.TrimRight(strings.TrimSpace(cfg.IssuerURL), "/")
	externalID := issuer + "|" + claims.Sub
	username := claims.PreferredUsername
	if username == "" {
		username = live.Username
	}
	if username == "" {
		username = "kc-" + claims.Sub[:min(12, len(claims.Sub))]
	}
	displayName := claims.Name
	if displayName == "" {
		displayName = live.DisplayName
	}

	user, err := es.UpsertExternalUser(r.Context(), AuthSourceKeycloak, externalID, username, displayName, role)
	if err != nil || !user.Enabled {
		http.Redirect(w, r, "/login?error="+url.QueryEscape("Учётная запись отключена"), http.StatusSeeOther)
		return
	}
	token, err := es.CreateExternalSession(r.Context(), user.ID, AuthSourceKeycloak, claims.SID)
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}
	s.extAuthCache.set(AuthSourceKeycloak, externalID, ExternalUserStatus{
		Active:      true,
		Role:        role,
		Username:    username,
		DisplayName: displayName,
	}, time.Now())
	http.SetCookie(w, &http.Cookie{
		Name:     "omb_session",
		Value:    token,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(externalSessionLifetime),
	})
	_ = s.Store.Audit(r.Context(), user.ID, "login.keycloak", "ok", "role="+role)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) oidcBackChannelLogout(w http.ResponseWriter, r *http.Request) {
	es := s.externalAuthStore()
	if es == nil {
		http.Error(w, "not configured", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	logoutToken := r.FormValue("logout_token")
	if logoutToken == "" {
		http.Error(w, "missing logout_token", http.StatusBadRequest)
		return
	}
	rec, cfg, sec, err := s.loadKeycloakConfigAndSecrets(r.Context())
	if err != nil || !rec.Enabled {
		http.Error(w, "disabled", http.StatusBadRequest)
		return
	}
	cl, err := newOIDCClientFromConfig(cfg, sec)
	if err != nil {
		http.Error(w, "invalid config", http.StatusBadRequest)
		return
	}
	doc, err := cl.Discover(r.Context())
	if err != nil {
		http.Error(w, "discovery failed", http.StatusBadRequest)
		return
	}
	jwks, err := cl.FetchJWKS(r.Context(), doc.JWKSURI)
	if err != nil {
		http.Error(w, "jwks failed", http.StatusBadRequest)
		return
	}
	claims, err := cl.ValidateLogoutToken(logoutToken, jwks, time.Now())
	if err != nil {
		http.Error(w, "invalid logout_token", http.StatusBadRequest)
		return
	}
	exp := time.Now().Add(15 * time.Minute)
	if claims.Exp > 0 {
		exp = time.Unix(claims.Exp, 0)
	}
	fresh, err := es.RecordOIDCLogoutJTI(r.Context(), claims.JTI, exp)
	if err != nil || !fresh {
		http.Error(w, "replayed logout_token", http.StatusBadRequest)
		return
	}
	issuer := strings.TrimRight(strings.TrimSpace(cfg.IssuerURL), "/")
	if claims.SID != "" {
		_, _ = es.RevokeSessionsByExternalSID(r.Context(), AuthSourceKeycloak, claims.SID)
	} else if claims.Sub != "" {
		_, _ = es.RevokeSessionsByExternalID(r.Context(), AuthSourceKeycloak, issuer+"|"+claims.Sub)
	}
	if claims.Sub != "" {
		s.invalidateExternalUserCache(AuthSourceKeycloak, issuer+"|"+claims.Sub)
	} else {
		s.invalidateExternalProviderCache(AuthSourceKeycloak)
	}
	if st, ok := s.Store.(interface {
		AuditSystem(context.Context, string, string, string) error
	}); ok {
		_ = st.AuditSystem(r.Context(), "auth.oidc.backchannel_logout", "ok", "sid="+claims.SID)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) oidcLogoutRedirectURL(ctx context.Context) string {
	rec, cfg, sec, err := s.loadKeycloakConfigAndSecrets(ctx)
	if err != nil || !rec.Enabled {
		return ""
	}
	cl, err := newOIDCClientFromConfig(cfg, sec)
	if err != nil {
		return ""
	}
	doc, err := cl.Discover(ctx)
	if err != nil {
		return ""
	}
	return cl.LogoutURL(doc)
}
