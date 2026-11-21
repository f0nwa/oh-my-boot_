package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ExternalAuthStore is the optional Store interface for external authentication,
// integrations configuration, and OIDC state persistence.
type ExternalAuthStore interface {
	UpsertExternalUser(ctx context.Context, source, externalID, username, displayName, role string) (User, error)
	UpdateExternalUserRole(ctx context.Context, id int64, role string) error
	CreateExternalSession(ctx context.Context, userID int64, source, externalSID string) (string, error)
	SessionWithMeta(ctx context.Context, token string) (SessionRecord, error)
	RevokeProviderSessions(ctx context.Context, source string) error
	RevokeUserSessions(ctx context.Context, userID int64) error
	RevokeSessionsByExternalSID(ctx context.Context, source, sid string) (int64, error)
	RevokeSessionsByExternalID(ctx context.Context, source, externalID string) (int64, error)
	CountActiveSessionsBySource(ctx context.Context, source string) (int, error)
	ExternalFSUsers(ctx context.Context) ([]User, error)
	GetAuthIntegration(ctx context.Context, provider string) (AuthIntegrationRecord, error)
	SaveAuthIntegration(ctx context.Context, rec AuthIntegrationRecord) error
	SetAuthIntegrationEnabled(ctx context.Context, provider string, enabled bool) error
	SetAuthIntegrationTestResult(ctx context.Context, provider string, ok bool, detail string) error
	HasEncryptedIntegrations(ctx context.Context) (bool, error)
	SaveOIDCLoginState(ctx context.Context, st OIDCLoginState) error
	ConsumeOIDCLoginState(ctx context.Context, state string) (OIDCLoginState, error)
	RecordOIDCLogoutJTI(ctx context.Context, jti string, expiresAt time.Time) (bool, error)
}

func (s *Server) externalAuthStore() ExternalAuthStore {
	if s == nil || s.Store == nil {
		return nil
	}
	es, _ := s.Store.(ExternalAuthStore)
	return es
}

// AuthIntegrationRecord is a stored configuration row from auth_integrations.
type AuthIntegrationRecord struct {
	Provider       string
	Enabled        bool
	ConfigJSON     json.RawMessage
	SecretsEnc     []byte
	LastTestAt     *time.Time
	LastTestOK     *bool
	LastTestDetail string
	UpdatedAt      time.Time
}

// SessionRecord bundles a validated session with its user and external metadata.
type SessionRecord struct {
	User        User
	AuthSource  string
	ExternalSID string
	ExpiresAt   time.Time
}

// OIDCLoginState is a single-use state+nonce+PKCE record for OIDC Authorization Code flow.
type OIDCLoginState struct {
	State        string
	Nonce        string
	CodeVerifier string
	ExpiresAt    time.Time
}

func validExternalSource(source string) bool {
	return source == AuthSourceLDAP || source == AuthSourceKeycloak
}

// UpsertExternalUser creates or updates an external account keyed strictly by (auth_source, external_id).
// Existing local disablement (enabled=false) is never cleared by a repeat login.
func (s Store) UpsertExternalUser(ctx context.Context, source, externalID, username, displayName, role string) (User, error) {
	if !validExternalSource(source) {
		return User{}, fmt.Errorf("invalid external auth source %q", source)
	}
	externalID = strings.TrimSpace(externalID)
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if externalID == "" || username == "" || !Allowed(role, "view") {
		return User{}, fmt.Errorf("invalid external user attributes")
	}
	var u User
	var fsHash string
	err := s.Pool.QueryRow(ctx, `
INSERT INTO users (username, auth_source, external_id, display_name, password_hash, role, enabled, password_policy_mode)
VALUES ($1, $2, $3, $4, NULL, $5, TRUE, 'never')
ON CONFLICT (auth_source, external_id) WHERE auth_source <> 'local'
DO UPDATE SET username = EXCLUDED.username, display_name = EXCLUDED.display_name, role = EXCLUDED.role
RETURNING id, username, auth_source, coalesce(external_id,''), display_name, role, enabled, language, theme,
          password_changed_at, must_change_password, password_policy_mode, coalesce(password_max_age_days,0),
          fs_access_mode, fs_password_hash, fs_password_changed_at`,
		username, source, externalID, displayName, role).
		Scan(&u.ID, &u.Username, &u.AuthSource, &u.ExternalID, &u.DisplayName, &u.Role, &u.Enabled, &u.Language, &u.Theme,
			&u.PasswordChangedAt, &u.MustChangePassword, &u.PasswordPolicyMode, &u.PasswordMaxAgeDays,
			&u.FSAccessMode, &fsHash, &u.FSPasswordChangedAt)
	if err != nil {
		return User{}, err
	}
	u.HasFSPassword = fsHash != ""
	return u, nil
}

// UpdateExternalUserRole updates the cached role of an external user in PostgreSQL.
func (s Store) UpdateExternalUserRole(ctx context.Context, id int64, role string) error {
	if !Allowed(role, "view") {
		return fmt.Errorf("invalid role")
	}
	_, err := s.Pool.Exec(ctx, "UPDATE users SET role=$2 WHERE id=$1 AND auth_source <> 'local'", id, role)
	return err
}

// CreateExternalSession creates a 30-minute session bound to an external auth provider.
func (s Store) CreateExternalSession(ctx context.Context, userID int64, source, externalSID string) (string, error) {
	if !validExternalSource(source) {
		return "", fmt.Errorf("invalid external auth source %q", source)
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	_, err = s.Pool.Exec(ctx,
		"INSERT INTO sessions (token_hash, user_id, auth_source, external_sid, expires_at) VALUES ($1, $2, $3, $4, $5)",
		tokenHash(token), userID, source, strings.TrimSpace(externalSID), time.Now().Add(externalSessionLifetime))
	if err != nil {
		return "", err
	}
	return token, nil
}

// SessionWithMeta loads a session together with its user and external metadata.
func (s Store) SessionWithMeta(ctx context.Context, token string) (SessionRecord, error) {
	if token == "" {
		return SessionRecord{}, fmt.Errorf("missing session")
	}
	var rec SessionRecord
	var fsHash string
	err := s.Pool.QueryRow(ctx, `
SELECT u.id, u.username, u.auth_source, coalesce(u.external_id,''), u.display_name, u.role, u.enabled,
       u.language, u.theme, u.password_changed_at, u.must_change_password, u.password_policy_mode,
       coalesce(u.password_max_age_days,0), u.fs_access_mode, u.fs_password_hash, u.fs_password_changed_at,
       s.auth_source, s.external_sid, s.expires_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1`, tokenHash(token)).
		Scan(&rec.User.ID, &rec.User.Username, &rec.User.AuthSource, &rec.User.ExternalID, &rec.User.DisplayName,
			&rec.User.Role, &rec.User.Enabled, &rec.User.Language, &rec.User.Theme, &rec.User.PasswordChangedAt,
			&rec.User.MustChangePassword, &rec.User.PasswordPolicyMode, &rec.User.PasswordMaxAgeDays,
			&rec.User.FSAccessMode, &fsHash, &rec.User.FSPasswordChangedAt,
			&rec.AuthSource, &rec.ExternalSID, &rec.ExpiresAt)
	if err != nil {
		return SessionRecord{}, err
	}
	if !SessionValid(rec.ExpiresAt, rec.User.Enabled) {
		return SessionRecord{}, fmt.Errorf("session expired or disabled")
	}
	rec.User.HasFSPassword = fsHash != ""
	return rec, nil
}

// RevokeProviderSessions deletes all active sessions issued by a given auth_source.
func (s Store) RevokeProviderSessions(ctx context.Context, source string) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM sessions WHERE auth_source=$1", source)
	return err
}

// RevokeUserSessions deletes all sessions for a specific user ID.
func (s Store) RevokeUserSessions(ctx context.Context, userID int64) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", userID)
	return err
}

// RevokeSessionsByExternalSID deletes sessions matching an OIDC provider sid.
func (s Store) RevokeSessionsByExternalSID(ctx context.Context, source, sid string) (int64, error) {
	if sid == "" {
		return 0, nil
	}
	res, err := s.Pool.Exec(ctx, "DELETE FROM sessions WHERE auth_source=$1 AND external_sid=$2", source, sid)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

// RevokeSessionsByExternalID deletes all sessions for an external user identified by (auth_source, external_id).
func (s Store) RevokeSessionsByExternalID(ctx context.Context, source, externalID string) (int64, error) {
	res, err := s.Pool.Exec(ctx, `
DELETE FROM sessions
WHERE user_id IN (SELECT id FROM users WHERE auth_source=$1 AND external_id=$2)`, source, externalID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

// CountActiveSessionsBySource returns the number of unexpired sessions for an auth_source.
func (s Store) CountActiveSessionsBySource(ctx context.Context, source string) (int, error) {
	var count int
	err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE auth_source=$1 AND expires_at > now()", source).Scan(&count)
	return count, err
}

// ExternalFSUsers returns enabled external users that currently have SMB/SFTP access enabled.
func (s Store) ExternalFSUsers(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT id, username, auth_source, coalesce(external_id,''), display_name, role, enabled, fs_access_mode
FROM users
WHERE auth_source <> 'local' AND enabled AND fs_access_mode IN ('ro', 'rw')
ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.AuthSource, &u.ExternalID, &u.DisplayName, &u.Role, &u.Enabled, &u.FSAccessMode); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetAuthIntegration returns the stored record for provider ('ldap' or 'keycloak').
// If not configured yet, returns a zero-value record with Provider set and nil error.
func (s Store) GetAuthIntegration(ctx context.Context, provider string) (AuthIntegrationRecord, error) {
	if !validExternalSource(provider) {
		return AuthIntegrationRecord{}, fmt.Errorf("invalid provider %q", provider)
	}
	var rec AuthIntegrationRecord
	err := s.Pool.QueryRow(ctx, `
SELECT provider, enabled, config_json, secrets_enc, last_test_at, last_test_ok, last_test_detail, updated_at
FROM auth_integrations WHERE provider=$1`, provider).
		Scan(&rec.Provider, &rec.Enabled, &rec.ConfigJSON, &rec.SecretsEnc, &rec.LastTestAt, &rec.LastTestOK, &rec.LastTestDetail, &rec.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthIntegrationRecord{Provider: provider, ConfigJSON: json.RawMessage("{}")}, nil
	}
	return rec, err
}

// SaveAuthIntegration upserts configuration and encrypted secrets for a provider.
func (s Store) SaveAuthIntegration(ctx context.Context, rec AuthIntegrationRecord) error {
	if !validExternalSource(rec.Provider) {
		return fmt.Errorf("invalid provider %q", rec.Provider)
	}
	cfg := rec.ConfigJSON
	if len(cfg) == 0 {
		cfg = json.RawMessage("{}")
	}
	_, err := s.Pool.Exec(ctx, `
INSERT INTO auth_integrations (provider, enabled, config_json, secrets_enc, last_test_at, last_test_ok, last_test_detail, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (provider) DO UPDATE SET
	enabled = EXCLUDED.enabled,
	config_json = EXCLUDED.config_json,
	secrets_enc = EXCLUDED.secrets_enc,
	last_test_at = EXCLUDED.last_test_at,
	last_test_ok = EXCLUDED.last_test_ok,
	last_test_detail = EXCLUDED.last_test_detail,
	updated_at = now()`,
		rec.Provider, rec.Enabled, cfg, rec.SecretsEnc, rec.LastTestAt, rec.LastTestOK, rec.LastTestDetail)
	return err
}

// SetAuthIntegrationEnabled toggles enabled state for a configured provider.
func (s Store) SetAuthIntegrationEnabled(ctx context.Context, provider string, enabled bool) error {
	if !validExternalSource(provider) {
		return fmt.Errorf("invalid provider %q", provider)
	}
	_, err := s.Pool.Exec(ctx, `
INSERT INTO auth_integrations (provider, enabled, updated_at) VALUES ($1, $2, now())
ON CONFLICT (provider) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()`, provider, enabled)
	return err
}

// SetAuthIntegrationTestResult records the outcome of the last connectivity test.
func (s Store) SetAuthIntegrationTestResult(ctx context.Context, provider string, ok bool, detail string) error {
	if !validExternalSource(provider) {
		return fmt.Errorf("invalid provider %q", provider)
	}
	_, err := s.Pool.Exec(ctx, `
INSERT INTO auth_integrations (provider, last_test_at, last_test_ok, last_test_detail, updated_at)
VALUES ($1, now(), $2, $3, now())
ON CONFLICT (provider) DO UPDATE SET
	last_test_at = now(),
	last_test_ok = EXCLUDED.last_test_ok,
	last_test_detail = EXCLUDED.last_test_detail,
	updated_at = now()`, provider, ok, detail)
	return err
}

// HasEncryptedIntegrations reports whether any integration row contains encrypted secrets.
func (s Store) HasEncryptedIntegrations(ctx context.Context) (bool, error) {
	var count int
	err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM auth_integrations WHERE secrets_enc IS NOT NULL AND octet_length(secrets_enc) > 0").Scan(&count)
	return count > 0, err
}

// SaveOIDCLoginState stores a single-use OIDC login state (TTL 5 minutes) and prunes expired rows.
func (s Store) SaveOIDCLoginState(ctx context.Context, st OIDCLoginState) error {
	_, _ = s.Pool.Exec(ctx, "DELETE FROM oidc_login_states WHERE expires_at <= now()")
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO oidc_login_states (state, nonce, code_verifier, expires_at) VALUES ($1, $2, $3, $4)",
		st.State, st.Nonce, st.CodeVerifier, st.ExpiresAt)
	return err
}

// ConsumeOIDCLoginState atomically deletes and returns an unexpired login state.
func (s Store) ConsumeOIDCLoginState(ctx context.Context, state string) (OIDCLoginState, error) {
	var st OIDCLoginState
	err := s.Pool.QueryRow(ctx, `
DELETE FROM oidc_login_states
WHERE state = $1 AND expires_at > now()
RETURNING state, nonce, code_verifier, expires_at`, state).
		Scan(&st.State, &st.Nonce, &st.CodeVerifier, &st.ExpiresAt)
	return st, err
}

// RecordOIDCLogoutJTI records a hash of the logout identifier and reports whether it is new.
func (s Store) RecordOIDCLogoutJTI(ctx context.Context, jti string, expiresAt time.Time) (bool, error) {
	_, _ = s.Pool.Exec(ctx, "DELETE FROM oidc_logout_jti WHERE expires_at <= now()")
	res, err := s.Pool.Exec(ctx, `
INSERT INTO oidc_logout_jti (jti_hash, expires_at) VALUES ($1, $2)
ON CONFLICT (jti_hash) DO NOTHING`, tokenHash(jti), expiresAt)
	if err != nil {
		return false, err
	}
	return res.RowsAffected() == 1, nil
}
