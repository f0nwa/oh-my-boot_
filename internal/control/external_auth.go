package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const externalAuthCacheTTL = 30 * time.Second

// LDAPConfig holds non-secret configuration for direct Active Directory / LDAP login.
type LDAPConfig struct {
	DirectoryID     string `json:"directory_id"`
	ServerURL       string `json:"server_url"`
	TLSMode         string `json:"tls_mode"` // "ldaps" or "starttls"
	CAPEM           string `json:"ca_pem"`
	BindDN          string `json:"bind_dn"`
	BaseDN          string `json:"base_dn"`
	LoginAttr       string `json:"login_attr"`
	IDAttr          string `json:"id_attr"`
	GroupAdminDN    string `json:"group_admin_dn"`
	GroupOperatorDN string `json:"group_operator_dn"`
	GroupObserverDN string `json:"group_observer_dn"`
	NestedGroups    bool   `json:"nested_groups"`
}

// LDAPSecrets holds encrypted secrets for LDAP integration.
type LDAPSecrets struct {
	BindPassword string `json:"bind_password"`
}

// KeycloakConfig holds non-secret configuration for external Keycloak OIDC login.
type KeycloakConfig struct {
	PublicBaseURL   string `json:"public_base_url"`
	IssuerURL       string `json:"issuer_url"`
	ClientID        string `json:"client_id"`
	ServiceClientID string `json:"service_client_id"`
	CAPEM           string `json:"ca_pem"`
}

// KeycloakSecrets holds encrypted secrets for Keycloak integration.
type KeycloakSecrets struct {
	ClientSecret        string `json:"client_secret"`
	ServiceClientSecret string `json:"service_client_secret"`
}

// ExternalIdentity is returned after successful external authentication.
type ExternalIdentity struct {
	Source      string
	ExternalID  string
	Username    string
	DisplayName string
	Role        string
	ExternalSID string
}

// ExternalUserStatus is the live account status and role returned by an external provider.
type ExternalUserStatus struct {
	Active      bool
	Role        string
	Username    string
	DisplayName string
}

// ExternalAuthChecker verifies live external user status/role.
type ExternalAuthChecker interface {
	CheckExternalUser(ctx context.Context, source, externalID string) (ExternalUserStatus, error)
}

var (
	errProviderDisabled    = errors.New("external auth provider is disabled")
	errProviderUnavailable = errors.New("external auth provider is unavailable")
)

type externalAuthCacheEntry struct {
	status    ExternalUserStatus
	checkedAt time.Time
}

type externalAuthCache struct {
	mu      sync.Mutex
	entries map[string]externalAuthCacheEntry
}

func externalCacheKey(source, externalID string) string {
	return source + "\x00" + externalID
}

func (c *externalAuthCache) get(source, externalID string, now time.Time) (ExternalUserStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		return ExternalUserStatus{}, false
	}
	entry, ok := c.entries[externalCacheKey(source, externalID)]
	if !ok || now.Sub(entry.checkedAt) > externalAuthCacheTTL {
		return ExternalUserStatus{}, false
	}
	return entry.status, true
}

func (c *externalAuthCache) set(source, externalID string, st ExternalUserStatus, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]externalAuthCacheEntry{}
	}
	c.entries[externalCacheKey(source, externalID)] = externalAuthCacheEntry{
		status:    st,
		checkedAt: now,
	}
}

func (c *externalAuthCache) deleteUser(source, externalID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, externalCacheKey(source, externalID))
}

func (c *externalAuthCache) deleteProvider(source string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := source + "\x00"
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
}

// HighestRole selects the highest-privileged OMB role (admin > operator > observer).
func HighestRole(roles ...string) string {
	rank := map[string]int{"observer": 1, "operator": 2, "admin": 3}
	best := ""
	bestRank := 0
	for _, r := range roles {
		r = strings.ToLower(strings.TrimSpace(r))
		if v := rank[r]; v > bestRank {
			best = r
			bestRank = v
		}
	}
	return best
}

func (s *Server) invalidateExternalUserCache(source, externalID string) {
	if s == nil {
		return
	}
	s.extAuthCache.deleteUser(source, externalID)
}

func (s *Server) invalidateExternalProviderCache(source string) {
	if s == nil {
		return
	}
	s.extAuthCache.deleteProvider(source)
}

func (s *Server) checkExternalUserLive(ctx context.Context, source, externalID string) (ExternalUserStatus, error) {
	if s.ExternalChecker != nil {
		return s.ExternalChecker.CheckExternalUser(ctx, source, externalID)
	}
	switch source {
	case AuthSourceLDAP:
		return s.checkLDAPUserLive(ctx, externalID)
	case AuthSourceKeycloak:
		return s.checkKeycloakUserLive(ctx, externalID)
	default:
		return ExternalUserStatus{}, errProviderDisabled
	}
}

// verifyExternalSession validates an active external session against its source.
// GET/HEAD requests use a 30s in-memory cache; mutating requests always hit the source live.
func (s *Server) verifyExternalSession(ctx context.Context, user User, method string) (User, int, string) {
	if user.IsLocal() {
		return user, 0, ""
	}
	now := time.Now()
	allowCache := method == http.MethodGet || method == http.MethodHead

	var st ExternalUserStatus
	var ok bool
	if allowCache {
		st, ok = s.extAuthCache.get(user.AuthSource, user.ExternalID, now)
	}
	if !ok {
		var err error
		st, err = s.checkExternalUserLive(ctx, user.AuthSource, user.ExternalID)
		if err != nil {
			if errors.Is(err, errProviderDisabled) {
				s.invalidateExternalProviderCache(user.AuthSource)
				return user, http.StatusUnauthorized, translate(normalizeLanguage(user.Language), "Внешний вход отключён")
			}
			s.recordAuthProviderEvent(ctx, user.AuthSource, false, err.Error())
			return user, http.StatusServiceUnavailable, translate(normalizeLanguage(user.Language), "Источник авторизации временно недоступен")
		}
		s.recordAuthProviderEvent(ctx, user.AuthSource, true, "")
		s.extAuthCache.set(user.AuthSource, user.ExternalID, st, now)
	}

	if !st.Active || !Allowed(st.Role, "view") {
		s.invalidateExternalUserCache(user.AuthSource, user.ExternalID)
		s.revokeExternalUserAccess(ctx, user)
		return user, http.StatusForbidden, translate(normalizeLanguage(user.Language), "Доступ отозван источником авторизации")
	}

	if st.Role != user.Role {
		user.Role = st.Role
		if es := s.externalAuthStore(); es != nil {
			_ = es.UpdateExternalUserRole(ctx, user.ID, st.Role)
		}
	}
	return user, 0, ""
}

// revokeExternalUserAccess drops active sessions and revokes local SMB/SFTP access for a disabled external user.
func (s *Server) revokeExternalUserAccess(ctx context.Context, user User) {
	if es := s.externalAuthStore(); es != nil {
		_ = es.RevokeUserSessions(ctx, user.ID)
	}
	if user.FSAccessMode == FSAccessRO || user.FSAccessMode == FSAccessRW {
		if fsStore, ok := s.Store.(FileServerStore); ok && fsStore != nil {
			_, _ = fsStore.SetUserFileAccess(ctx, user.ID, FSAccessNone, "")
			_ = s.SyncLocalFileServer(ctx)
		}
	}
}

// RevalidateExternalFSUsers checks external users with active SMB/SFTP access against their source
// and revokes SMB/SFTP access if the user was disabled or lost all OMB roles.
func (s *Server) RevalidateExternalFSUsers(ctx context.Context) {
	es := s.externalAuthStore()
	if es == nil {
		return
	}
	users, err := es.ExternalFSUsers(ctx)
	if err != nil || len(users) == 0 {
		return
	}
	for _, u := range users {
		st, err := s.checkExternalUserLive(ctx, u.AuthSource, u.ExternalID)
		if err != nil {
			if errors.Is(err, errProviderDisabled) {
				s.revokeExternalUserAccess(ctx, u)
			} else {
				s.recordAuthProviderEvent(ctx, u.AuthSource, false, err.Error())
			}
			continue
		}
		s.recordAuthProviderEvent(ctx, u.AuthSource, true, "")
		if !st.Active || !Allowed(st.Role, "view") {
			s.invalidateExternalUserCache(u.AuthSource, u.ExternalID)
			s.revokeExternalUserAccess(ctx, u)
		}
	}
}

func (s *Server) recordAuthProviderEvent(ctx context.Context, provider string, healthy bool, detail string) {
	evStore, ok := s.Store.(SystemEventStore)
	if !ok || evStore == nil {
		return
	}
	sourceKey := "auth:" + provider
	if healthy {
		_ = evStore.ResolveSystemEvents(ctx, sourceKey, time.Now())
		return
	}
	title := "Недоступен сервер LDAP / Active Directory"
	if provider == AuthSourceKeycloak {
		title = "Недоступен сервер Keycloak (OIDC)"
	}
	_ = evStore.RecordSystemEvent(ctx, SystemEvent{
		Severity:  "error",
		Category:  "auth",
		SourceKey: sourceKey,
		Title:     title,
		Detail:    detail,
		Link:      "/integrations",
	})
}

func (s *Server) ensureSecretBox() (*SecretBox, error) {
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	if s.Secrets != nil {
		return s.Secrets, nil
	}
	if s.SecretsKeyPath == "" {
		return nil, ErrSecretsKeyMissing
	}
	box, err := LoadSecretBox(s.SecretsKeyPath)
	if errors.Is(err, ErrSecretsKeyMissing) {
		box, err = GenerateSecretKeyFile(s.SecretsKeyPath)
	}
	if err != nil {
		return nil, err
	}
	s.Secrets = box
	return box, nil
}

func (s *Server) loadLDAPConfigAndSecrets(ctx context.Context) (AuthIntegrationRecord, LDAPConfig, LDAPSecrets, error) {
	es := s.externalAuthStore()
	if es == nil {
		return AuthIntegrationRecord{}, LDAPConfig{}, LDAPSecrets{}, errProviderDisabled
	}
	rec, err := es.GetAuthIntegration(ctx, AuthSourceLDAP)
	if err != nil {
		return rec, LDAPConfig{}, LDAPSecrets{}, err
	}
	var cfg LDAPConfig
	if len(rec.ConfigJSON) > 0 {
		_ = json.Unmarshal(rec.ConfigJSON, &cfg)
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
	var sec LDAPSecrets
	if len(rec.SecretsEnc) > 0 {
		box, err := s.ensureSecretBox()
		if err != nil {
			return rec, cfg, sec, err
		}
		plain, err := box.Decrypt(AuthSourceLDAP, rec.SecretsEnc)
		if err != nil {
			return rec, cfg, sec, err
		}
		if err := json.Unmarshal(plain, &sec); err != nil {
			return rec, cfg, sec, fmt.Errorf("decode ldap secrets: %w", err)
		}
	}
	return rec, cfg, sec, nil
}

func (s *Server) loadKeycloakConfigAndSecrets(ctx context.Context) (AuthIntegrationRecord, KeycloakConfig, KeycloakSecrets, error) {
	es := s.externalAuthStore()
	if es == nil {
		return AuthIntegrationRecord{}, KeycloakConfig{}, KeycloakSecrets{}, errProviderDisabled
	}
	rec, err := es.GetAuthIntegration(ctx, AuthSourceKeycloak)
	if err != nil {
		return rec, KeycloakConfig{}, KeycloakSecrets{}, err
	}
	var cfg KeycloakConfig
	if len(rec.ConfigJSON) > 0 {
		_ = json.Unmarshal(rec.ConfigJSON, &cfg)
	}
	var sec KeycloakSecrets
	if len(rec.SecretsEnc) > 0 {
		box, err := s.ensureSecretBox()
		if err != nil {
			return rec, cfg, sec, err
		}
		plain, err := box.Decrypt(AuthSourceKeycloak, rec.SecretsEnc)
		if err != nil {
			return rec, cfg, sec, err
		}
		if err := json.Unmarshal(plain, &sec); err != nil {
			return rec, cfg, sec, fmt.Errorf("decode keycloak secrets: %w", err)
		}
	}
	return rec, cfg, sec, nil
}

// Placeholder hooks implemented in ldap_provider.go (Stage 3) and oidc_provider.go (Stage 4).
