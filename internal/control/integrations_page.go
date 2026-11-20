package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LDAPIntegrationView is rendered on /integrations.
type LDAPIntegrationView struct {
	Enabled         bool
	Config          LDAPConfig
	HasBindPassword bool
	ActiveSessions  int
	LastTestAt      *time.Time
	LastTestOK      *bool
	LastTestDetail  string
}

// KeycloakIntegrationView is rendered on /integrations.
type KeycloakIntegrationView struct {
	Enabled          bool
	Config           KeycloakConfig
	HasClientSecret  bool
	HasServiceSecret bool
	CallbackURL      string
	BackChannelURL   string
	ActiveSessions   int
	LastTestAt       *time.Time
	LastTestOK       *bool
	LastTestDetail   string
}

// IntegrationsView holds both external auth providers for /integrations.
type IntegrationsView struct {
	LDAP     LDAPIntegrationView
	Keycloak KeycloakIntegrationView
}

func (s *Server) buildIntegrationsView(ctx context.Context) IntegrationsView {
	var v IntegrationsView
	es := s.externalAuthStore()
	if es == nil {
		return v
	}

	if rec, cfg, sec, err := s.loadLDAPConfigAndSecrets(ctx); err == nil {
		v.LDAP = LDAPIntegrationView{
			Enabled:         rec.Enabled,
			Config:          cfg,
			HasBindPassword: sec.BindPassword != "",
			LastTestAt:      rec.LastTestAt,
			LastTestOK:      rec.LastTestOK,
			LastTestDetail:  rec.LastTestDetail,
		}
		if n, err := es.CountActiveSessionsBySource(ctx, AuthSourceLDAP); err == nil {
			v.LDAP.ActiveSessions = n
		}
	}
	if v.LDAP.Config.TLSMode == "" {
		v.LDAP.Config.TLSMode = "ldaps"
	}
	if v.LDAP.Config.LoginAttr == "" {
		v.LDAP.Config.LoginAttr = "sAMAccountName"
	}
	if v.LDAP.Config.IDAttr == "" {
		v.LDAP.Config.IDAttr = "objectGUID"
	}

	if rec, cfg, sec, err := s.loadKeycloakConfigAndSecrets(ctx); err == nil {
		base := strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
		cb, bc := "", ""
		if base != "" {
			cb = base + "/auth/oidc/callback"
			bc = base + "/auth/oidc/backchannel-logout"
		}
		v.Keycloak = KeycloakIntegrationView{
			Enabled:          rec.Enabled,
			Config:           cfg,
			HasClientSecret:  sec.ClientSecret != "",
			HasServiceSecret: sec.ServiceClientSecret != "",
			CallbackURL:      cb,
			BackChannelURL:   bc,
			LastTestAt:       rec.LastTestAt,
			LastTestOK:       rec.LastTestOK,
			LastTestDetail:   rec.LastTestDetail,
		}
		if n, err := es.CountActiveSessionsBySource(ctx, AuthSourceKeycloak); err == nil {
			v.Keycloak.ActiveSessions = n
		}
	}
	return v
}

func (s *Server) integrationsPage(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	data := pageData{
		Info:         info,
		Message:      r.URL.Query().Get("message"),
		Error:        r.URL.Query().Get("error"),
		Integrations: s.buildIntegrationsView(r.Context()),
	}
	render(w, "integrations", data)
}

func parseLDAPForm(r *http.Request, prevCfg LDAPConfig, prevSec LDAPSecrets) (LDAPConfig, LDAPSecrets) {
	dirID := strings.TrimSpace(prevCfg.DirectoryID)
	if dirID == "" {
		if tok, err := randomToken(); err == nil && len(tok) >= 12 {
			dirID = "ad-" + strings.ToLower(tok[:12])
		} else {
			dirID = "ad-default"
		}
	}
	cfg := LDAPConfig{
		DirectoryID:     dirID,
		ServerURL:       strings.TrimSpace(r.FormValue("server_url")),
		TLSMode:         strings.ToLower(strings.TrimSpace(r.FormValue("tls_mode"))),
		CAPEM:           strings.TrimSpace(r.FormValue("ca_pem")),
		BindDN:          strings.TrimSpace(r.FormValue("bind_dn")),
		BaseDN:          strings.TrimSpace(r.FormValue("base_dn")),
		LoginAttr:       strings.TrimSpace(r.FormValue("login_attr")),
		IDAttr:          strings.TrimSpace(r.FormValue("id_attr")),
		GroupAdminDN:    strings.TrimSpace(r.FormValue("group_admin_dn")),
		GroupOperatorDN: strings.TrimSpace(r.FormValue("group_operator_dn")),
		GroupObserverDN: strings.TrimSpace(r.FormValue("group_observer_dn")),
		NestedGroups:    r.FormValue("nested_groups") == "on" || r.FormValue("nested_groups") == "1",
	}
	if cfg.TLSMode == "" {
		cfg.TLSMode = "ldaps"
	}
	if cfg.LoginAttr == "" {
		cfg.LoginAttr = "sAMAccountName"
	}
	if cfg.IDAttr == "" {
		cfg.IDAttr = "objectGUID"
	}
	sec := prevSec
	if pw := r.FormValue("bind_password"); pw != "" {
		sec.BindPassword = pw
	}
	return cfg, sec
}

func (s *Server) integrationLDAPSave(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	es := s.externalAuthStore()
	if es == nil {
		http.Error(w, "Хранилище интеграций недоступно", http.StatusInternalServerError)
		return
	}
	prevRec, prevCfg, prevSec, _ := s.loadLDAPConfigAndSecrets(r.Context())
	cfg, sec := parseLDAPForm(r, prevCfg, prevSec)
	if err := validateLDAPConfig(cfg, sec); err != nil {
		http.Redirect(w, r, "/integrations?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	box, err := s.ensureSecretBox()
	if err != nil {
		http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Ошибка ключа шифрования секретов: "+err.Error()), http.StatusSeeOther)
		return
	}
	secRaw, err := json.Marshal(sec)
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}
	secEnc, err := box.Encrypt(AuthSourceLDAP, secRaw)
	if err != nil {
		http.Error(w, "Ошибка шифрования", http.StatusInternalServerError)
		return
	}
	cfgRaw, err := json.Marshal(cfg)
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}

	enableNow := r.FormValue("enable_now") == "1"
	nextEnabled := prevRec.Enabled
	lastTestAt := prevRec.LastTestAt
	lastTestOK := prevRec.LastTestOK
	lastTestDetail := prevRec.LastTestDetail

	criticalChanged := prevCfg.ServerURL != cfg.ServerURL ||
		prevCfg.BaseDN != cfg.BaseDN ||
		prevCfg.GroupAdminDN != cfg.GroupAdminDN ||
		prevCfg.GroupOperatorDN != cfg.GroupOperatorDN ||
		prevCfg.GroupObserverDN != cfg.GroupObserverDN ||
		prevCfg.NestedGroups != cfg.NestedGroups

	if enableNow {
		detail, testErr := testLDAPConnection(r.Context(), cfg, sec, r.FormValue("test_username"))
		now := time.Now()
		lastTestAt = &now
		if testErr != nil {
			okFalse := false
			lastTestOK = &okFalse
			lastTestDetail = testErr.Error()
			_ = es.SaveAuthIntegration(r.Context(), AuthIntegrationRecord{
				Provider:       AuthSourceLDAP,
				Enabled:        false,
				ConfigJSON:     cfgRaw,
				SecretsEnc:     secEnc,
				LastTestAt:     lastTestAt,
				LastTestOK:     lastTestOK,
				LastTestDetail: lastTestDetail,
			})
			_ = s.Store.Audit(r.Context(), info.User.ID, "integrations.ldap.save", "error", "enable test failed")
			http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Настройки сохранены, но включение отклонено: "+testErr.Error()), http.StatusSeeOther)
			return
		}
		okTrue := true
		lastTestOK = &okTrue
		lastTestDetail = detail
		nextEnabled = true
	}

	if err := es.SaveAuthIntegration(r.Context(), AuthIntegrationRecord{
		Provider:       AuthSourceLDAP,
		Enabled:        nextEnabled,
		ConfigJSON:     cfgRaw,
		SecretsEnc:     secEnc,
		LastTestAt:     lastTestAt,
		LastTestOK:     lastTestOK,
		LastTestDetail: lastTestDetail,
	}); err != nil {
		http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Не удалось сохранить настройки LDAP"), http.StatusSeeOther)
		return
	}
	if criticalChanged || !nextEnabled {
		_ = es.RevokeProviderSessions(r.Context(), AuthSourceLDAP)
		s.invalidateExternalProviderCache(AuthSourceLDAP)
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "integrations.ldap.save", "ok", fmt.Sprintf("enabled=%v server=%s", nextEnabled, cfg.ServerURL))
	msg := "Настройки Active Directory / LDAP сохранены"
	if enableNow && nextEnabled {
		msg = "Active Directory / LDAP настроен и включён"
	}
	http.Redirect(w, r, "/integrations?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) integrationLDAPTest(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	es := s.externalAuthStore()
	if es == nil {
		writeJSONError(w, http.StatusInternalServerError, "Хранилище интеграций недоступно")
		return
	}
	_, prevCfg, prevSec, _ := s.loadLDAPConfigAndSecrets(r.Context())
	cfg, sec := parseLDAPForm(r, prevCfg, prevSec)
	detail, err := testLDAPConnection(r.Context(), cfg, sec, r.FormValue("test_username"))
	if err != nil {
		_ = es.SetAuthIntegrationTestResult(r.Context(), AuthSourceLDAP, false, err.Error())
		_ = s.Store.Audit(r.Context(), info.User.ID, "integrations.ldap.test", "error", cfg.ServerURL)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	_ = es.SetAuthIntegrationTestResult(r.Context(), AuthSourceLDAP, true, detail)
	s.recordAuthProviderEvent(r.Context(), AuthSourceLDAP, true, "")
	_ = s.Store.Audit(r.Context(), info.User.ID, "integrations.ldap.test", "ok", cfg.ServerURL)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": detail})
}

func (s *Server) integrationLDAPToggle(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	es := s.externalAuthStore()
	if es == nil {
		http.Error(w, "Хранилище интеграций недоступно", http.StatusInternalServerError)
		return
	}
	enable := r.FormValue("enabled") == "1"
	if enable {
		_, cfg, sec, err := s.loadLDAPConfigAndSecrets(r.Context())
		if err != nil {
			http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Сначала настройте LDAP"), http.StatusSeeOther)
			return
		}
		detail, err := testLDAPConnection(r.Context(), cfg, sec, "")
		if err != nil {
			_ = es.SetAuthIntegrationTestResult(r.Context(), AuthSourceLDAP, false, err.Error())
			http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Проверка LDAP не пройдена: "+err.Error()), http.StatusSeeOther)
			return
		}
		_ = es.SetAuthIntegrationTestResult(r.Context(), AuthSourceLDAP, true, detail)
	}
	if err := es.SetAuthIntegrationEnabled(r.Context(), AuthSourceLDAP, enable); err != nil {
		http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Не удалось изменить состояние LDAP"), http.StatusSeeOther)
		return
	}
	if !enable {
		_ = es.RevokeProviderSessions(r.Context(), AuthSourceLDAP)
		s.invalidateExternalProviderCache(AuthSourceLDAP)
		s.recordAuthProviderEvent(r.Context(), AuthSourceLDAP, true, "")
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "integrations.ldap.toggle", "ok", fmt.Sprintf("enabled=%v", enable))
	msg := "Интеграция LDAP отключена, активные сессии завершены"
	if enable {
		msg = "Интеграция LDAP включена"
	}
	http.Redirect(w, r, "/integrations?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func parseKeycloakForm(r *http.Request, prevSec KeycloakSecrets) (KeycloakConfig, KeycloakSecrets) {
	cfg := KeycloakConfig{
		PublicBaseURL:   strings.TrimRight(strings.TrimSpace(r.FormValue("public_base_url")), "/"),
		IssuerURL:       strings.TrimRight(strings.TrimSpace(r.FormValue("issuer_url")), "/"),
		ClientID:        strings.TrimSpace(r.FormValue("client_id")),
		ServiceClientID: strings.TrimSpace(r.FormValue("service_client_id")),
		CAPEM:           strings.TrimSpace(r.FormValue("ca_pem")),
	}
	sec := prevSec
	if v := r.FormValue("client_secret"); v != "" {
		sec.ClientSecret = v
	}
	if v := r.FormValue("service_client_secret"); v != "" {
		sec.ServiceClientSecret = v
	}
	return cfg, sec
}

func (s *Server) integrationKeycloakSave(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	es := s.externalAuthStore()
	if es == nil {
		http.Error(w, "Хранилище интеграций недоступно", http.StatusInternalServerError)
		return
	}
	prevRec, prevCfg, prevSec, _ := s.loadKeycloakConfigAndSecrets(r.Context())
	cfg, sec := parseKeycloakForm(r, prevSec)
	if err := validateKeycloakConfig(cfg, sec); err != nil {
		http.Redirect(w, r, "/integrations?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	box, err := s.ensureSecretBox()
	if err != nil {
		http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Ошибка ключа шифрования секретов: "+err.Error()), http.StatusSeeOther)
		return
	}
	secRaw, err := json.Marshal(sec)
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}
	secEnc, err := box.Encrypt(AuthSourceKeycloak, secRaw)
	if err != nil {
		http.Error(w, "Ошибка шифрования", http.StatusInternalServerError)
		return
	}
	cfgRaw, err := json.Marshal(cfg)
	if err != nil {
		http.Error(w, "Ошибка", http.StatusInternalServerError)
		return
	}

	enableNow := r.FormValue("enable_now") == "1"
	nextEnabled := prevRec.Enabled
	lastTestAt := prevRec.LastTestAt
	lastTestOK := prevRec.LastTestOK
	lastTestDetail := prevRec.LastTestDetail

	criticalChanged := prevCfg.IssuerURL != cfg.IssuerURL || prevCfg.ClientID != cfg.ClientID

	if enableNow {
		detail, testErr := testKeycloakConnection(r.Context(), cfg, sec)
		now := time.Now()
		lastTestAt = &now
		if testErr != nil {
			okFalse := false
			lastTestOK = &okFalse
			lastTestDetail = testErr.Error()
			_ = es.SaveAuthIntegration(r.Context(), AuthIntegrationRecord{
				Provider:       AuthSourceKeycloak,
				Enabled:        false,
				ConfigJSON:     cfgRaw,
				SecretsEnc:     secEnc,
				LastTestAt:     lastTestAt,
				LastTestOK:     lastTestOK,
				LastTestDetail: lastTestDetail,
			})
			_ = s.Store.Audit(r.Context(), info.User.ID, "integrations.keycloak.save", "error", "enable test failed")
			http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Настройки сохранены, но включение отклонено: "+testErr.Error()), http.StatusSeeOther)
			return
		}
		okTrue := true
		lastTestOK = &okTrue
		lastTestDetail = detail
		nextEnabled = true
	}

	if err := es.SaveAuthIntegration(r.Context(), AuthIntegrationRecord{
		Provider:       AuthSourceKeycloak,
		Enabled:        nextEnabled,
		ConfigJSON:     cfgRaw,
		SecretsEnc:     secEnc,
		LastTestAt:     lastTestAt,
		LastTestOK:     lastTestOK,
		LastTestDetail: lastTestDetail,
	}); err != nil {
		http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Не удалось сохранить настройки Keycloak"), http.StatusSeeOther)
		return
	}
	if criticalChanged || !nextEnabled {
		_ = es.RevokeProviderSessions(r.Context(), AuthSourceKeycloak)
		s.invalidateExternalProviderCache(AuthSourceKeycloak)
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "integrations.keycloak.save", "ok", fmt.Sprintf("enabled=%v issuer=%s", nextEnabled, cfg.IssuerURL))
	msg := "Настройки Keycloak сохранены"
	if enableNow && nextEnabled {
		msg = "Keycloak настроен и включён"
	}
	http.Redirect(w, r, "/integrations?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) integrationKeycloakTest(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	es := s.externalAuthStore()
	if es == nil {
		writeJSONError(w, http.StatusInternalServerError, "Хранилище интеграций недоступно")
		return
	}
	_, _, prevSec, _ := s.loadKeycloakConfigAndSecrets(r.Context())
	cfg, sec := parseKeycloakForm(r, prevSec)
	detail, err := testKeycloakConnection(r.Context(), cfg, sec)
	if err != nil {
		_ = es.SetAuthIntegrationTestResult(r.Context(), AuthSourceKeycloak, false, err.Error())
		_ = s.Store.Audit(r.Context(), info.User.ID, "integrations.keycloak.test", "error", cfg.IssuerURL)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "detail": err.Error()})
		return
	}
	_ = es.SetAuthIntegrationTestResult(r.Context(), AuthSourceKeycloak, true, detail)
	s.recordAuthProviderEvent(r.Context(), AuthSourceKeycloak, true, "")
	_ = s.Store.Audit(r.Context(), info.User.ID, "integrations.keycloak.test", "ok", cfg.IssuerURL)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": detail})
}

func (s *Server) integrationKeycloakToggle(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	es := s.externalAuthStore()
	if es == nil {
		http.Error(w, "Хранилище интеграций недоступно", http.StatusInternalServerError)
		return
	}
	enable := r.FormValue("enabled") == "1"
	if enable {
		_, cfg, sec, err := s.loadKeycloakConfigAndSecrets(r.Context())
		if err != nil {
			http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Сначала настройте Keycloak"), http.StatusSeeOther)
			return
		}
		detail, err := testKeycloakConnection(r.Context(), cfg, sec)
		if err != nil {
			_ = es.SetAuthIntegrationTestResult(r.Context(), AuthSourceKeycloak, false, err.Error())
			http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Проверка Keycloak не пройдена: "+err.Error()), http.StatusSeeOther)
			return
		}
		_ = es.SetAuthIntegrationTestResult(r.Context(), AuthSourceKeycloak, true, detail)
	}
	if err := es.SetAuthIntegrationEnabled(r.Context(), AuthSourceKeycloak, enable); err != nil {
		http.Redirect(w, r, "/integrations?error="+url.QueryEscape("Не удалось изменить состояние Keycloak"), http.StatusSeeOther)
		return
	}
	if !enable {
		_ = es.RevokeProviderSessions(r.Context(), AuthSourceKeycloak)
		s.invalidateExternalProviderCache(AuthSourceKeycloak)
		s.recordAuthProviderEvent(r.Context(), AuthSourceKeycloak, true, "")
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "integrations.keycloak.toggle", "ok", fmt.Sprintf("enabled=%v", enable))
	msg := "Интеграция Keycloak отключена, активные сессии завершены"
	if enable {
		msg = "Интеграция Keycloak включена"
	}
	http.Redirect(w, r, "/integrations?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "detail": msg})
}
