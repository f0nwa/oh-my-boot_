package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Login2FAChallenge holds a pending second-factor login verification state.
type Login2FAChallenge struct {
	User      User
	SecretEnc []byte
	ExpiresAt time.Time
	Attempts  int
}

// TOTPStore defines persistence operations for local TOTP 2FA and recovery codes.
type TOTPStore interface {
	VerifyLocalCredentials(ctx context.Context, username, password string) (User, error)
	VerifyLocalPasswordByID(ctx context.Context, userID int64, password string) error
	CreateLocalSession(ctx context.Context, userID int64) (string, error)
	UserTOTPSecret(ctx context.Context, userID int64) (secretEnc []byte, enabled bool, lastStep int64, err error)
	EnableUserTOTP(ctx context.Context, userID int64, secretEnc []byte, lastStep int64, codeHashes []string) error
	DisableUserTOTP(ctx context.Context, userID int64) error
	ResetUserTOTP(ctx context.Context, userID int64) error
	ReplaceRecoveryCodes(ctx context.Context, userID int64, codeHashes []string) error
	CountUnusedRecoveryCodes(ctx context.Context, userID int64) (int, error)
	UpdateTOTPLastStep(ctx context.Context, userID int64, step int64) error
	ConsumeRecoveryCode(ctx context.Context, userID int64, codeHash string) (bool, error)
	CreateLogin2FAChallenge(ctx context.Context, userID int64, ttl time.Duration) (string, error)
	GetLogin2FAChallenge(ctx context.Context, token string) (Login2FAChallenge, error)
	IncrementLogin2FAAttempts(ctx context.Context, token string) (int, error)
	DeleteLogin2FAChallenge(ctx context.Context, token string) error
}

func (s Store) VerifyLocalCredentials(ctx context.Context, username, password string) (User, error) {
	var u User
	var hash string
	var fsHash string
	err := s.Pool.QueryRow(ctx, `SELECT id,username,auth_source,coalesce(external_id,''),display_name,role,enabled,language,theme,password_changed_at,must_change_password,password_policy_mode,coalesce(password_max_age_days,0),coalesce(password_hash,''),fs_access_mode,fs_password_hash,fs_password_changed_at,totp_enabled,totp_enabled_at,totp_last_step FROM users WHERE auth_source='local' AND lower(username)=lower($1)`, username).
		Scan(&u.ID, &u.Username, &u.AuthSource, &u.ExternalID, &u.DisplayName, &u.Role, &u.Enabled, &u.Language, &u.Theme, &u.PasswordChangedAt, &u.MustChangePassword, &u.PasswordPolicyMode, &u.PasswordMaxAgeDays, &hash, &u.FSAccessMode, &fsHash, &u.FSPasswordChangedAt, &u.TOTPEnabled, &u.TOTPEnabledAt, &u.TOTPLastStep)
	if err != nil || !u.Enabled || !VerifyPassword(hash, password) {
		return User{}, fmt.Errorf("invalid credentials")
	}
	u.HasFSPassword = fsHash != ""
	return u, nil
}

func (s Store) VerifyLocalPasswordByID(ctx context.Context, userID int64, password string) error {
	var hash string
	var enabled bool
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(password_hash,''), enabled FROM users WHERE id=$1 AND auth_source='local'`, userID).Scan(&hash, &enabled)
	if err != nil || !enabled || !VerifyPassword(hash, password) {
		return fmt.Errorf("invalid credentials")
	}
	return nil
}

func (s Store) CreateLocalSession(ctx context.Context, userID int64) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	_, err = s.Pool.Exec(ctx, "INSERT INTO sessions (token_hash,user_id,auth_source,expires_at) VALUES ($1,$2,'local',$3)", tokenHash(token), userID, time.Now().Add(sessionLifetime))
	return token, err
}

func (s Store) UserTOTPSecret(ctx context.Context, userID int64) ([]byte, bool, int64, error) {
	var secret []byte
	var enabled bool
	var lastStep int64
	err := s.Pool.QueryRow(ctx, `SELECT totp_secret_enc, totp_enabled, totp_last_step FROM users WHERE id=$1 AND auth_source='local'`, userID).
		Scan(&secret, &enabled, &lastStep)
	return secret, enabled, lastStep, err
}

func (s Store) EnableUserTOTP(ctx context.Context, userID int64, secretEnc []byte, lastStep int64, codeHashes []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE users SET totp_secret_enc=$1, totp_enabled=TRUE, totp_enabled_at=now(), totp_last_step=$2 WHERE id=$3 AND auth_source='local'`,
		secretEnc, lastStep, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("local user not found")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err := tx.Exec(ctx, `INSERT INTO user_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, h); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s Store) DisableUserTOTP(ctx context.Context, userID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE users SET totp_secret_enc=NULL, totp_enabled=FALSE, totp_enabled_at=NULL, totp_last_step=0 WHERE id=$1 AND auth_source='local'`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM login_2fa_challenges WHERE user_id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) ResetUserTOTP(ctx context.Context, userID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE users SET totp_secret_enc=NULL, totp_enabled=FALSE, totp_enabled_at=NULL, totp_last_step=0 WHERE id=$1 AND auth_source='local'`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("local user not found")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM login_2fa_challenges WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) ReplaceRecoveryCodes(ctx context.Context, userID int64, codeHashes []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT totp_enabled FROM users WHERE id=$1 AND auth_source='local'`, userID).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("2fa is not enabled")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err := tx.Exec(ctx, `INSERT INTO user_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, h); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s Store) CountUnusedRecoveryCodes(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM user_recovery_codes WHERE user_id=$1 AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

func (s Store) UpdateTOTPLastStep(ctx context.Context, userID int64, step int64) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET totp_last_step=$1 WHERE id=$2 AND totp_last_step < $1`, step, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("totp code already used")
	}
	return nil
}

func (s Store) ConsumeRecoveryCode(ctx context.Context, userID int64, codeHash string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE user_recovery_codes SET used_at=now() WHERE id = (
		SELECT id FROM user_recovery_codes WHERE user_id=$1 AND code_hash=$2 AND used_at IS NULL ORDER BY id LIMIT 1
	)`, userID, codeHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s Store) CreateLogin2FAChallenge(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	_, _ = s.Pool.Exec(ctx, `DELETE FROM login_2fa_challenges WHERE expires_at < now()`)
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO login_2fa_challenges (token_hash, user_id, expires_at, attempts) VALUES ($1, $2, $3, 0)`,
		tokenHash(token), userID, time.Now().Add(ttl))
	return token, err
}

func (s Store) GetLogin2FAChallenge(ctx context.Context, token string) (Login2FAChallenge, error) {
	if token == "" {
		return Login2FAChallenge{}, pgx.ErrNoRows
	}
	var ch Login2FAChallenge
	var fsHash string
	err := s.Pool.QueryRow(ctx, `
SELECT u.id, u.username, u.auth_source, coalesce(u.external_id,''), u.display_name, u.role, u.enabled,
       u.language, u.theme, u.password_changed_at, u.must_change_password, u.password_policy_mode,
       coalesce(u.password_max_age_days,0), u.fs_access_mode, u.fs_password_hash, u.fs_password_changed_at,
       u.totp_enabled, u.totp_enabled_at, u.totp_last_step, u.totp_secret_enc,
       c.expires_at, c.attempts
FROM login_2fa_challenges c JOIN users u ON u.id = c.user_id
WHERE c.token_hash = $1`, tokenHash(token)).
		Scan(&ch.User.ID, &ch.User.Username, &ch.User.AuthSource, &ch.User.ExternalID, &ch.User.DisplayName,
			&ch.User.Role, &ch.User.Enabled, &ch.User.Language, &ch.User.Theme, &ch.User.PasswordChangedAt,
			&ch.User.MustChangePassword, &ch.User.PasswordPolicyMode, &ch.User.PasswordMaxAgeDays,
			&ch.User.FSAccessMode, &fsHash, &ch.User.FSPasswordChangedAt,
			&ch.User.TOTPEnabled, &ch.User.TOTPEnabledAt, &ch.User.TOTPLastStep, &ch.SecretEnc,
			&ch.ExpiresAt, &ch.Attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Login2FAChallenge{}, err
		}
		return Login2FAChallenge{}, err
	}
	ch.User.HasFSPassword = fsHash != ""
	return ch, nil
}

func (s Store) IncrementLogin2FAAttempts(ctx context.Context, token string) (int, error) {
	var attempts int
	err := s.Pool.QueryRow(ctx, `UPDATE login_2fa_challenges SET attempts = attempts + 1 WHERE token_hash=$1 RETURNING attempts`, tokenHash(token)).Scan(&attempts)
	return attempts, err
}

func (s Store) DeleteLogin2FAChallenge(ctx context.Context, token string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM login_2fa_challenges WHERE token_hash=$1`, tokenHash(token))
	return err
}
